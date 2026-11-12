package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func waitJobBlocked(t *testing.T, s *store.Store, pid uint32, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := s.Pool().QueryRow(ctx, `WITH RECURSIVE blocked(pid) AS (
			SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid))
			UNION SELECT a.pid FROM pg_stat_activity a JOIN blocked b ON b.pid=ANY(pg_blocking_pids(a.pid))
			WHERE a.datname=current_database())
			SELECT count(*) FROM blocked`, int32(pid)).Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("concurrent operation did not reach network lock")
}

// The permission writer must win while admission is waiting, not just before it starts.
func TestProductionJobAuthorityWriterWinsConcurrentAdmission(t *testing.T) {
	for _, mode := range []string{"delete", "downgrade", "revoke"} {
		t.Run(mode, func(t *testing.T) {
			s, admin, n, _, device, w, item := orderFixture(t)
			owner, _, err := s.CreateMember(ctx, admin.ID, "racing-owner", []protocol.Grant{{NetworkID: n.ID, CanCraft: true}})
			if err != nil {
				t.Fatal(err)
			}
			a, err := s.CreateProductionJob(ctx, owner.ID, n.ID, jobInput(item, "racing-job-001", "racing-source-001"))
			if err != nil {
				t.Fatal(err)
			}
			blocker, err := s.Pool().Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(ctx)
			// NO KEY UPDATE still allows SetGrant's FK-compatible KEY SHARE.
			if _, err = blocker.Exec(ctx, "SELECT id FROM networks WHERE id=$1 FOR NO KEY UPDATE", n.ID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 1}); done <- err }()
			waitJobBlocked(t, s, blocker.Conn().PgConn().PID(), 1)
			switch mode {
			case "delete":
				err = s.DeleteGrant(ctx, admin.ID, owner.ID, n.ID)
			case "downgrade":
				err = s.SetGrant(ctx, admin.ID, owner.ID, protocol.Grant{NetworkID: n.ID})
			case "revoke":
				err = s.RevokeMember(ctx, admin.ID, owner.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			d, err := s.ProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID)
			if err != nil || d.Summary.Control != "paused" || d.Summary.Counts.Pending != 1 || d.Entries[0].OrderID != "" {
				t.Fatal("lost authority admitted", d, err)
			}
		})
	}
}

func TestProductionJobPauseWinsConcurrentPoll(t *testing.T) {
	s, admin, n, _, device, w, item := orderFixture(t)
	in := jobInput(item, "pause-race-job", "pause-race-source")
	in.Entries = append(in.Entries, protocol.ProductionJobEntryInput{EntryKey: "pause-race-source-2", ItemID: item, Quantity: 1}, protocol.ProductionJobEntryInput{EntryKey: "pause-race-source-3", ItemID: item, Quantity: 1})
	a, err := s.CreateProductionJob(ctx, admin.ID, n.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := ack.Commands[0]
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 2}); err != nil {
		t.Fatal(err)
	}
	blocker, err := s.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, "SELECT id FROM networks WHERE id=$1 FOR UPDATE", n.ID); err != nil {
		t.Fatal(err)
	}
	pauseDone := make(chan error, 1)
	go func() {
		_, err := s.ControlProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID, "pause", protocol.ProductionJobControlInput{OperationKey: "pause-race-operation", ExpectedControlVersion: 1})
		pauseDone <- err
	}()
	waitJobBlocked(t, s, blocker.Conn().PgConn().PID(), 1)
	pollDone := make(chan error, 1)
	go func() {
		_, err := s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 3, Reports: []protocol.Report{{OrderID: c.OrderID, AttemptID: c.AttemptID, State: "done"}}})
		pollDone <- err
	}()
	waitJobBlocked(t, s, blocker.Conn().PgConn().PID(), 2)
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-pauseDone; err != nil {
		t.Fatal(err)
	}
	if err = <-pollDone; err != nil {
		t.Fatal(err)
	}
	d, err := s.ProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID)
	if err != nil || d.Summary.Control != "paused" || d.Summary.Counts.Pending != 1 || d.Summary.Counts.Done != 1 || d.Summary.Counts.Assigned != 1 {
		t.Fatal("pause boundary", d, err)
	}
}

func TestProductionJobProgressStopAndRotation(t *testing.T) {
	for _, mode := range []string{"computing", "running", "needs_review", "failed", "rotation"} {
		t.Run(mode, func(t *testing.T) {
			s, admin, n, token, device, w, item := orderFixture(t)
			_ = token
			in := jobInput(item, "progress-job-001", "progress-source-001")
			in.Entries = append(in.Entries, protocol.ProductionJobEntryInput{EntryKey: "progress-source-002", ItemID: item, Quantity: 1})
			a, err := s.CreateProductionJob(ctx, admin.ID, n.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			ack, err := s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
			if err != nil {
				t.Fatal(err)
			}
			c := ack.Commands[0]
			expected := mode
			nextSequence := protocol.Quantity(3)
			if mode == "rotation" {
				if _, err = s.RotateDeviceToken(ctx, admin.ID, n.ID); err != nil {
					t.Fatal(err)
				}
				expected = "needs_review"
			} else if mode == "needs_review" {
				nextSequence = 2
				if _, err = s.Pool().Exec(ctx, "UPDATE networks SET last_seen=now()-interval '1 hour' WHERE id=$1", n.ID); err != nil {
					t.Fatal(err)
				}
				if err = s.Maintain(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				_, err = s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 2, Reports: []protocol.Report{{OrderID: c.OrderID, AttemptID: c.AttemptID, State: mode}}})
				if err != nil {
					t.Fatal(err)
				}
			}
			d, err := s.ProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID)
			if err != nil || d.Entries[0].State != expected || d.Entries[0].OrderID != c.OrderID || d.Entries[0].AttemptID != c.AttemptID || d.Summary.Version <= 2 {
				t.Fatal("progress projection", d, err)
			}
			page, err := s.ProductionJobChanges(ctx, admin.ID, n.ID, 2)
			if err != nil || len(page.Changes) != 1 || page.Changes[0].Summary == nil || page.Changes[0].Summary.Version != d.Summary.Version {
				t.Fatal("progress feed", page, err)
			}
			stopped, err := s.ControlProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID, "stop", protocol.ProductionJobControlInput{OperationKey: "progress-stop-001", ExpectedControlVersion: 1})
			if err != nil {
				t.Fatal(err)
			}
			d, err = s.ProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID)
			if err != nil || d.Entries[0].State != expected || d.Entries[0].OrderID != c.OrderID || d.Entries[0].AttemptID != c.AttemptID {
				t.Fatal("stop changed execution identity", d, err)
			}
			if mode != "rotation" {
				if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: nextSequence}); err != nil {
					t.Fatal(err)
				}
			}
			counts := stopped.Counts
			if counts.Pending+counts.Rejected+counts.Queued+counts.Assigned+counts.Computing+counts.Running+counts.NeedsReview+counts.Done+counts.Failed+counts.Canceled+counts.Expired != 2 {
				t.Fatal("counts do not partition", stopped)
			}
			var links int
			if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM orders WHERE production_job_entry_id=$1", d.Entries[0].ID).Scan(&links); err != nil || links != 1 {
				t.Fatal("readmission", links, err)
			}
		})
	}
}

func TestProductionJobDelayedAdmissionTTLAndCapabilityChanges(t *testing.T) {
	for _, mode := range []string{"ttl", "service", "quantity", "inexact"} {
		t.Run(mode, func(t *testing.T) {
			s, admin, n, _, device, w, item := orderFixture(t)
			a, err := s.CreateProductionJob(ctx, admin.ID, n.ID, jobInput(item, "delayed-job-001", "delayed-source-001"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Pool().Exec(ctx, "UPDATE production_jobs SET created_at=now()-interval '2 days' WHERE id=$1", a.Receipt.JobID); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "service":
				_, err = s.Pool().Exec(ctx, `UPDATE bridge_sessions SET capabilities=jsonb_set(capabilities,'{craft}','false') WHERE id=$1`, w.SessionID)
			case "quantity":
				_, err = s.Pool().Exec(ctx, `UPDATE bridge_sessions SET capabilities=jsonb_set(capabilities,'{max_craft_quantity}','"1"') WHERE id=$1`, w.SessionID)
			case "inexact":
				_, err = s.Pool().Exec(ctx, "UPDATE item_definitions SET exact=false WHERE network_id=$1 AND id=$2", n.ID, int64(item))
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 1}); err != nil {
				t.Fatal(err)
			}
			d, err := s.ProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "ttl":
				var seconds float64
				if err = s.Pool().QueryRow(ctx, "SELECT extract(epoch FROM expires_at-created_at) FROM orders WHERE id=$1", d.Entries[0].OrderID).Scan(&seconds); err != nil || seconds != 3600 {
					t.Fatal("TTL not based on admission", seconds, err)
				}
				o, err := s.Order(ctx, admin.ID, n.ID, d.Entries[0].OrderID)
				if err != nil || time.Since(o.CreatedAt) > time.Minute {
					t.Fatal(o, err)
				}
			case "service":
				if d.Summary.Counts.Pending != 1 || d.Entries[0].OrderID != "" {
					t.Fatal(d)
				}
			default:
				if d.Summary.Counts.Rejected != 1 || d.Entries[0].Reason == "" || d.Entries[0].OrderID != "" {
					t.Fatal(fmt.Sprintf("%s: %+v", mode, d))
				}
			}
		})
	}
}
