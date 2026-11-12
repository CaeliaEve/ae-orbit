package store_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

// Removing historical payloads, cursor bounds, or keyset ordering breaks these assertions.
func TestProductionJobSyncPagesAndRetention(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	a, err := s.CreateProductionJob(ctx, admin.ID, network.ID, jobInput(item, "sync-job-001", "sync-source-001"))
	if err != nil {
		t.Fatal(err)
	}
	id := a.Receipt.JobID
	for i := 0; i < 104; i++ {
		action := "pause"
		if i%2 == 1 {
			action = "resume"
		}
		_, err = s.ControlProductionJob(ctx, admin.ID, network.ID, id, action, protocol.ProductionJobControlInput{
			OperationKey: fmt.Sprintf("sync-action-%03d", i), ExpectedControlVersion: protocol.Quantity(i + 1),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := s.ProductionJobs(ctx, admin.ID, network.ID)
	if err != nil || snapshot.Watermark != 105 || len(snapshot.Jobs) != 1 || snapshot.UnsettledCount != 1 {
		t.Fatal(snapshot, err)
	}
	page, err := s.ProductionJobChanges(ctx, admin.ID, network.ID, 0)
	if err != nil || len(page.Changes) != 100 || !page.HasMore || page.NextAfter != 100 || page.Watermark != 105 {
		t.Fatal(page, err)
	}
	if page.Changes[0].Summary == nil || page.Changes[0].Summary.Version != 1 || page.Changes[0].Summary.Control != "active" {
		t.Fatal("feed joined current state", page.Changes[0])
	}
	for i, change := range page.Changes {
		if change.Revision != protocol.Quantity(i+1) {
			t.Fatal("nonunique/out-of-order revision", change)
		}
	}
	page, err = s.ProductionJobChanges(ctx, admin.ID, network.ID, page.NextAfter)
	if err != nil || len(page.Changes) != 5 || page.HasMore || page.NextAfter != 105 {
		t.Fatal(page, err)
	}
	page, err = s.ProductionJobChanges(ctx, admin.ID, network.ID, 105)
	if err != nil || len(page.Changes) != 0 || page.NextAfter != 105 || page.Reset {
		t.Fatal(page, err)
	}
	page, err = s.ProductionJobChanges(ctx, admin.ID, network.ID, 106)
	if err != nil || !page.Reset {
		t.Fatal("ahead cursor did not reset", page, err)
	}
	if _, err = s.Pool().Exec(ctx, "DELETE FROM production_job_changes WHERE network_id=$1 AND revision<50", network.ID); err != nil {
		t.Fatal(err)
	}
	page, err = s.ProductionJobChanges(ctx, admin.ID, network.ID, 48)
	if err != nil || !page.Reset {
		t.Fatal("expired cursor did not reset", page, err)
	}
	page, err = s.ProductionJobChanges(ctx, admin.ID, network.ID, 49)
	if err != nil || page.Reset || len(page.Changes) == 0 {
		t.Fatal("floor boundary", page, err)
	}
	_, err = s.ControlProductionJob(ctx, admin.ID, network.ID, id, "stop", protocol.ProductionJobControlInput{OperationKey: "sync-stop-001", ExpectedControlVersion: 105})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateProductionJob(ctx, admin.ID, network.ID, jobInput(item, "sync-job-002", "sync-source-002"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ControlProductionJob(ctx, admin.ID, network.ID, b.Receipt.JobID, "stop", protocol.ProductionJobControlInput{OperationKey: "sync-stop-002", ExpectedControlVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	history, err := s.ProductionJobHistory(ctx, admin.ID, network.ID, "", 1)
	if err != nil || len(history.Jobs) != 1 || !history.HasMore || history.NextCursor == "" {
		t.Fatal(history, err)
	}
	next, err := s.ProductionJobHistory(ctx, admin.ID, network.ID, history.NextCursor, 1)
	if err != nil || len(next.Jobs) != 1 || next.HasMore || next.Jobs[0].ID == history.Jobs[0].ID {
		t.Fatal(next, err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE production_jobs SET settled_at=now()-interval '31 days' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err = s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ProductionJob(ctx, admin.ID, network.ID, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("detail survived archival", err)
	}
	replay, err := s.CreateProductionJob(ctx, admin.ID, network.ID, jobInput(item, "sync-job-001", "sync-source-001"))
	if err != nil || !replay.Replayed || !replay.Receipt.Archived || replay.Job != nil {
		t.Fatal("archive resurrected", replay, err)
	}
	if _, err = s.CreateProductionJob(ctx, admin.ID, network.ID, jobInput(item, "sync-new-key", "sync-source-001")); !errors.Is(err, store.ErrEntryAlreadyAccepted) {
		t.Fatal("source tombstone lost", err)
	}
	page, err = s.ProductionJobChanges(ctx, admin.ID, network.ID, 108)
	if err != nil || len(page.Changes) != 1 || !page.Changes[0].Removed || page.Changes[0].JobID != id || page.Changes[0].Summary != nil {
		t.Fatal("missing removal tombstone", page, err)
	}
	// Creation + 104 controls + stop is version 106; removal is the next entity change.
	if page.Changes[0].Version != 107 {
		t.Fatal("removal did not advance entity version", page.Changes[0])
	}
}

// Reading watermark and summaries in different snapshots permits mismatched versions.
func TestProductionJobSnapshotConcurrentWatermark(t *testing.T) {
	s, admin, n, _, _, _, item := orderFixture(t)
	a, err := s.CreateProductionJob(ctx, admin.ID, n.ID, jobInput(item, "snapshot-job-001", "snapshot-source-001"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			action := "pause"
			if i%2 == 1 {
				action = "resume"
			}
			if _, err := s.ControlProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID, action, protocol.ProductionJobControlInput{OperationKey: fmt.Sprintf("snapshot-action-%03d", i), ExpectedControlVersion: protocol.Quantity(i + 1)}); err != nil {
				errs <- err
				return
			}
		}
	}()
	for i := 0; i < 60; i++ {
		page, err := s.ProductionJobs(ctx, admin.ID, n.ID)
		if err != nil || len(page.Jobs) != 1 || page.UnsettledCount != 1 || page.Watermark != page.Jobs[0].Version {
			t.Errorf("inconsistent snapshot: %+v %v", page, err)
			break
		}
		changes, err := s.ProductionJobChanges(ctx, admin.ID, n.ID, page.Watermark)
		if err != nil || changes.Reset {
			t.Errorf("snapshot/feed catchup: %+v %v", changes, err)
			break
		}
		for _, change := range changes.Changes {
			if change.Revision <= page.Watermark {
				t.Error("catchup replayed earlier state")
			}
		}
	}
	wg.Wait()
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
}

// Keeping a terminal order by its own timestamp loses a live job's authoritative receipt.
func TestProductionJobUnsettledOrderRetentionAndOfflineAuthority(t *testing.T) {
	s, admin, n, _, device, w, item := orderFixture(t)
	in := jobInput(item, "retention-job-001", "retention-source-001")
	in.Entries = append(in.Entries, protocol.ProductionJobEntryInput{EntryKey: "retention-source-002", ItemID: item, Quantity: 1})
	a, err := s.CreateProductionJob(ctx, admin.ID, n.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := ack.Commands[0]
	if _, err = s.ControlProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID, "pause", protocol.ProductionJobControlInput{OperationKey: "retention-pause-001", ExpectedControlVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 2, Reports: []protocol.Report{{OrderID: c.OrderID, AttemptID: c.AttemptID, State: "done"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE orders SET updated_at=now()-interval '90 days' WHERE id=$1", c.OrderID); err != nil {
		t.Fatal(err)
	}
	if err = s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Order(ctx, admin.ID, n.ID, c.OrderID); err != nil {
		t.Fatal("unsettled job lost old order", err)
	}
	d, err := s.ProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID)
	if err != nil || d.Summary.Counts.Done != 1 || d.Summary.Counts.Pending != 1 || d.Entries[0].OrderID != c.OrderID {
		t.Fatal(d, err)
	}
	owner, _, err := s.CreateMember(ctx, admin.ID, "offline-owner", []protocol.Grant{{NetworkID: n.ID, CanCraft: true}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateProductionJob(ctx, owner.ID, n.ID, jobInput(item, "offline-job-001", "offline-source-001"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteGrant(ctx, admin.ID, owner.ID, n.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	d, err = s.ProductionJob(ctx, admin.ID, n.ID, b.Receipt.JobID)
	if err != nil || d.Summary.Control != "paused" || d.Summary.ControlVersion != 2 || d.Summary.Reason != "owner_permission_lost" || d.Summary.Counts.Pending != 1 {
		t.Fatal("orderless job not fenced", d, err)
	}
}

func TestProductionJobChangeRetentionFloorAndCoalescedHints(t *testing.T) {
	s, admin, n, _, device, w, item := orderFixture(t)
	wake, cancel := s.SubscribeEvents(n.ID)
	defer cancel()
	a, err := s.CreateProductionJob(ctx, admin.ID, n.ID, jobInput(item, "floor-job-001", "floor-source-001"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateProductionJob(ctx, admin.ID, n.ID, jobInput(item, "floor-job-002", "floor-source-002"))
	if err != nil {
		t.Fatal(err)
	}
	var before int
	if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM network_events WHERE network_id=$1 AND kind='jobs'", n.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Exclude creation hints so the poll must publish its own committed wake.
	select {
	case <-wake:
	default:
	}
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	var after int
	if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM network_events WHERE network_id=$1 AND kind='jobs'", n.ID).Scan(&after); err != nil || after != before+1 {
		t.Fatal("multi-job transaction not coalesced", before, after, err)
	}
	page, err := s.ProductionJobChanges(ctx, admin.ID, n.ID, 2)
	if err != nil || len(page.Changes) != 2 || page.Changes[0].Revision != 3 || page.Changes[1].Revision != 4 || page.Changes[0].JobID == page.Changes[1].JobID {
		t.Fatal(page, err)
	}
	select {
	case <-wake:
	default:
		t.Fatal("committed changes did not publish wake")
	}
	select {
	case <-wake:
		t.Fatal("wake hints did not coalesce")
	default:
	}
	beforeStats := s.NotificationStats()
	if _, err = s.ControlProductionJob(ctx, admin.ID, n.ID, b.Receipt.JobID, "pause", protocol.ProductionJobControlInput{OperationKey: "rejected-stale-pause", ExpectedControlVersion: 99}); !errors.Is(err, store.ErrConflict) {
		t.Fatal(err)
	}
	select {
	case <-wake:
		t.Fatal("failed transaction published a wake")
	default:
	}
	if s.NotificationStats().Published != beforeStats.Published {
		t.Fatal("failed transaction published")
	}
	// Seed an already-existing historical tail, then exercise production trimming.
	if _, err = s.Pool().Exec(ctx, `INSERT INTO production_job_changes(network_id,revision,job_id,version,summary)
		SELECT $1,i,$2,1,summary FROM generate_series(5,2050) i CROSS JOIN
		(SELECT summary FROM production_job_changes WHERE network_id=$1 AND revision=1) s`, n.ID, a.Receipt.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE networks SET jobs_revision=2050 WHERE id=$1", n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ControlProductionJob(ctx, admin.ID, n.ID, b.Receipt.JobID, "pause", protocol.ProductionJobControlInput{OperationKey: "floor-pause-001", ExpectedControlVersion: 1}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM production_job_changes WHERE network_id=$1", n.ID).Scan(&count); err != nil || count != 2048 {
		t.Fatal(count, err)
	}
	page, err = s.ProductionJobChanges(ctx, admin.ID, n.ID, 2)
	if err != nil || !page.Reset {
		t.Fatal(page, err)
	}
	page, err = s.ProductionJobChanges(ctx, admin.ID, n.ID, 3)
	if err != nil || page.Reset || page.Changes[0].Revision != 4 {
		t.Fatal(page, err)
	}
}

func TestProductionJobArchiveBoundedChunk(t *testing.T) {
	s, admin, n, _, _, _, item := orderFixture(t)
	a, err := s.CreateProductionJob(ctx, admin.ID, n.ID, jobInput(item, "bounded-job-001", "bounded-source-001"))
	if err != nil {
		t.Fatal(err)
	}
	// Seed independently old settled detail, with no execution orders.
	if _, err = s.Pool().Exec(ctx, `INSERT INTO production_jobs(id,network_id,member_id,manifest_digest,ttl_seconds,owner_authority_epoch,total,version,settled_at)
		SELECT 'archive-seed-'||i,$1,$2,'test',3600,0,1,1,now()-interval '31 days' FROM generate_series(1,65) i`, n.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	var old, removed int
	if err = s.Pool().QueryRow(ctx, `SELECT
		(SELECT count(*) FROM production_jobs WHERE network_id=$1 AND settled_at IS NOT NULL),
		(SELECT count(*) FROM production_job_changes WHERE network_id=$1 AND removed)`, n.ID).Scan(&old, &removed); err != nil || old != 1 || removed != 64 {
		t.Fatal(old, removed, err)
	}
	if _, err = s.ProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID); err != nil {
		t.Fatal("unsettled detail archived", err)
	}
	if err = s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM production_job_changes WHERE network_id=$1 AND removed", n.ID).Scan(&removed); err != nil || removed != 65 {
		t.Fatal(removed, err)
	}
}
