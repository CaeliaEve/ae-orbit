package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestProductionJobAdmissionControls(t *testing.T) {
	s, admin, network, _, device, welcome, item := orderFixture(t)
	in := jobInput(item, "admission-job", "unused-source")
	in.Entries = nil
	for i := 0; i < 20; i++ {
		in.Entries = append(in.Entries, protocol.ProductionJobEntryInput{EntryKey: fmt.Sprintf("admit-source-%03d", i), ItemID: item, Quantity: 1})
	}
	a, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	p := protocol.Poll{SessionID: welcome.SessionID, Sequence: 1}
	for seq := 1; seq <= 8; seq++ {
		p.Sequence = protocol.Quantity(seq)
		if _, err = s.Poll(ctx, device, p); err != nil {
			t.Fatal(err)
		}
	}
	d, err := s.ProductionJob(ctx, admin.ID, network.ID, a.Receipt.JobID)
	if err != nil || d.Summary.Counts.Pending != 12 || d.Summary.Counts.Assigned != 1 || d.Summary.Counts.Queued != 7 {
		t.Fatal(d.Summary, err)
	}
	if _, err = s.Poll(ctx, device, p); err != nil {
		t.Fatal(err)
	}
	paused, err := s.ControlProductionJob(ctx, admin.ID, network.ID, a.Receipt.JobID, "pause", protocol.ProductionJobControlInput{OperationKey: "pause-operation", ExpectedControlVersion: 1})
	if err != nil || paused.Control != "paused" || paused.ControlVersion != 2 {
		t.Fatal(paused, err)
	}
	replay, err := s.ControlProductionJob(ctx, admin.ID, network.ID, a.Receipt.JobID, "pause", protocol.ProductionJobControlInput{OperationKey: "pause-operation", ExpectedControlVersion: 1})
	if err != nil || replay.Version != paused.Version {
		t.Fatal(replay, err)
	}
	_, err = s.ControlProductionJob(ctx, admin.ID, network.ID, a.Receipt.JobID, "resume", protocol.ProductionJobControlInput{OperationKey: "stale-operation", ExpectedControlVersion: 1})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatal(err)
	}
	stopped, err := s.ControlProductionJob(ctx, admin.ID, network.ID, a.Receipt.JobID, "stop", protocol.ProductionJobControlInput{OperationKey: "stop-operation", ExpectedControlVersion: 2})
	if err != nil || stopped.Counts.Canceled != 19 || stopped.Counts.Assigned != 1 || stopped.SettledAt != nil {
		t.Fatal(stopped, err)
	}
}

func TestProductionJobPermissionEpochRestoration(t *testing.T) {
	s, admin, network, _, device, welcome, item := orderFixture(t)
	owner, _, err := s.CreateMember(ctx, admin.ID, "job-owner", []protocol.Grant{{NetworkID: network.ID, CanCraft: true}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateProductionJob(ctx, owner.ID, network.ID, jobInput(item, "epoch-job-key", "epoch-source-key"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteGrant(ctx, admin.ID, owner.ID, network.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.SetGrant(ctx, admin.ID, owner.ID, protocol.Grant{NetworkID: network.ID, CanCraft: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	d, err := s.ProductionJob(ctx, admin.ID, network.ID, a.Receipt.JobID)
	if err != nil || d.Summary.Control != "paused" || d.Summary.Reason != "owner_permission_lost" || d.Summary.Counts.Pending != 1 {
		t.Fatal(d.Summary, err)
	}
	_, err = s.ControlProductionJob(ctx, owner.ID, network.ID, a.Receipt.JobID, "resume", protocol.ProductionJobControlInput{OperationKey: "resume-epoch-key", ExpectedControlVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 2}); err != nil {
		t.Fatal(err)
	}
	d, err = s.ProductionJob(ctx, admin.ID, network.ID, a.Receipt.JobID)
	if err != nil || d.Summary.Counts.Assigned != 1 {
		t.Fatal(d.Summary, err)
	}
}

func TestProductionJobAdmissionPublicKeyCollision(t *testing.T) {
	s, admin, network, _, device, welcome, item := orderFixture(t)
	a, err := s.CreateProductionJob(ctx, admin.ID, network.ID, jobInput(item, "collision-job-key", "collision-source-key"))
	if err != nil {
		t.Fatal(err)
	}
	createOrder(t, s, admin, network, item, "production-entry:"+a.Job.Entries[0].ID)
	ack, err := s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 1})
	if err != nil || len(ack.Commands) != 1 {
		t.Fatal("public key blocked primary poll", ack, err)
	}
	d, err := s.ProductionJob(ctx, admin.ID, network.ID, a.Receipt.JobID)
	if err != nil || d.Summary.Counts.Queued != 1 {
		t.Fatal(d.Summary, err)
	}
}

func TestProductionJobSmallQueueFairnessAndRestart(t *testing.T) {
	s, admin, network, _, device, welcome, item := orderFixture(t)
	options := store.DefaultOptions()
	options.MaxQueuedOrders = 3
	reopened, err := store.Open(ctx, s.Pool().Config().ConnString(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Close)
	s = reopened
	direct := createOrder(t, s, admin, network, item, "older-direct-order")
	jobs := make([]string, 2)
	for j := range jobs {
		in := jobInput(item, fmt.Sprintf("fair-job-%d", j), "unused-source")
		in.Entries = nil
		for i := 0; i < 500; i++ {
			in.Entries = append(in.Entries, protocol.ProductionJobEntryInput{EntryKey: fmt.Sprintf("fair-source-%d-%03d", j, i), ItemID: item, Quantity: 1})
		}
		a, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		jobs[j] = a.Receipt.JobID
	}
	p := protocol.Poll{SessionID: welcome.SessionID, Sequence: 1}
	ack, err := s.Poll(ctx, device, p)
	if err != nil || len(ack.Commands) != 1 || ack.Commands[0].OrderID != direct.ID {
		t.Fatal("FIFO changed", ack, err)
	}
	for _, job := range jobs {
		d, err := s.ProductionJob(ctx, admin.ID, network.ID, job)
		if err != nil || d.Summary.Counts.Queued != 1 || d.Summary.Counts.Pending != 499 {
			t.Fatal("quantum-one fairness", d.Summary, err)
		}
	}
	// A new Store instance must preserve admitted IDs and resume only pending intent.
	s2, err := store.Open(ctx, s.Pool().Config().ConnString(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s2.Close)
	if _, err = s2.Poll(ctx, device, p); err != nil {
		t.Fatal(err)
	}
	p.Sequence++
	if _, err = s2.Poll(ctx, device, p); err != nil {
		t.Fatal(err)
	}
	var total, queued, links int
	if err = s.Pool().QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE state='queued'),count(*) FILTER(WHERE production_job_entry_id IS NOT NULL) FROM orders`).Scan(&total, &queued, &links); err != nil {
		t.Fatal(err)
	}
	if total != 4 || queued != 3 || links != 3 {
		t.Fatal(total, queued, links)
	}
}

func TestProductionJobCapOneAlternatesAdmissionsAfterCompletion(t *testing.T) {
	s, admin, network, _, device, welcome, item := orderFixture(t)
	options := store.DefaultOptions()
	options.MaxQueuedOrders = 1
	reopened, err := store.Open(ctx, s.Pool().Config().ConnString(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Close)
	s = reopened
	jobs := make([]string, 2)
	for j := range jobs {
		in := jobInput(item, fmt.Sprintf("cap-one-job-%d", j), "unused-source")
		in.Entries = nil
		for i := 0; i < 4; i++ {
			in.Entries = append(in.Entries, protocol.ProductionJobEntryInput{
				EntryKey: fmt.Sprintf("cap-one-source-%d-%d", j, i), ItemID: item, Quantity: 1,
			})
		}
		accepted, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		jobs[j] = accepted.Receipt.JobID
	}
	var previous protocol.Command
	for sequence := 1; sequence <= 6; sequence++ {
		poll := protocol.Poll{SessionID: welcome.SessionID, Sequence: protocol.Quantity(sequence)}
		if sequence > 1 {
			poll.Reports = []protocol.Report{{OrderID: previous.OrderID, AttemptID: previous.AttemptID, State: "done"}}
		}
		ack, err := s.Poll(ctx, device, poll)
		if err != nil || len(ack.Commands) != 1 {
			t.Fatal("expected one fresh dispatch after completion", sequence, ack, err)
		}
		previous = ack.Commands[0]
		var jobID string
		if err = s.Pool().QueryRow(ctx, `SELECT e.job_id FROM orders o
			JOIN production_job_entries e ON e.id=o.production_job_entry_id WHERE o.id=$1`, previous.OrderID).Scan(&jobID); err != nil {
			t.Fatal(err)
		}
		if want := jobs[(sequence-1)%2]; jobID != want {
			t.Fatalf("poll %d admitted job %s; want alternating job %s", sequence, jobID, want)
		}
	}
}

func TestProductionJobWorkerCannotAdmit(t *testing.T) {
	f := newCooperativeFixture(t)
	a, err := f.s.CreateProductionJob(ctx, f.admin.ID, f.network.ID, jobInput(f.id, "worker-job-key", "worker-source-key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.PollV2(ctx, f.b, protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	d, err := f.s.ProductionJob(ctx, f.admin.ID, f.network.ID, a.Receipt.JobID)
	if err != nil || d.Summary.Counts.Pending != 1 || d.Summary.Version != 1 {
		t.Fatal(d.Summary, err)
	}
	if _, err = f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	d, err = f.s.ProductionJob(ctx, f.admin.ID, f.network.ID, a.Receipt.JobID)
	if err != nil || d.Summary.Counts.Assigned != 1 {
		t.Fatal(d.Summary, err)
	}
}

func TestProductionJobBulkTransitionsCoalesceAndReviewResolves(t *testing.T) {
	s, admin, network, _, device, welcome, item := orderFixture(t)
	jobs := make([]string, 2)
	for j := range jobs {
		in := jobInput(item, fmt.Sprintf("bulk-job-%d", j), "unused-source")
		in.Entries = nil
		for i := 0; i < 3; i++ {
			in.Entries = append(in.Entries, protocol.ProductionJobEntryInput{EntryKey: fmt.Sprintf("bulk-source-%d-%d", j, i), ItemID: item, Quantity: 1})
		}
		a, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		jobs[j] = a.Receipt.JobID
	}
	ack, err := s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 1})
	if err != nil || len(ack.Commands) != 1 {
		t.Fatal(ack, err)
	}
	c := ack.Commands[0]
	for seq := 2; seq <= 3; seq++ {
		if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: protocol.Quantity(seq)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE orders SET expires_at=now()-interval '1 second' WHERE state='queued'"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE networks SET last_seen=now()-interval '1 hour' WHERE id=$1", network.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	var review int
	for _, job := range jobs {
		d, err := s.ProductionJob(ctx, admin.ID, network.ID, job)
		if err != nil || d.Summary.Version != 5 || d.Summary.Counts.Expired+d.Summary.Counts.NeedsReview != 3 {
			t.Fatal("bulk projection not atomic/coalesced", d.Summary, err)
		}
		review += int(d.Summary.Counts.NeedsReview)
		var changes int
		if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM production_job_changes WHERE job_id=$1", job).Scan(&changes); err != nil || changes != 5 {
			t.Fatal(changes, err)
		}
	}
	if review != 1 {
		t.Fatal(review)
	}
	p := protocol.Poll{SessionID: welcome.SessionID, Sequence: 4, Reports: []protocol.Report{{OrderID: c.OrderID, AttemptID: c.AttemptID, State: "running"}}}
	if _, err = s.Poll(ctx, device, p); err != nil {
		t.Fatal(err)
	}
	o, err := s.Order(ctx, admin.ID, network.ID, c.OrderID)
	if err != nil || o.State != "needs_review" {
		t.Fatal(o, err)
	}
	p.Sequence++
	p.Reports[0].State = "done"
	if _, err = s.Poll(ctx, device, p); err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		d, err := s.ProductionJob(ctx, admin.ID, network.ID, job)
		if err != nil || d.Summary.SettledAt == nil || d.Summary.Result != "finished_with_issues" {
			t.Fatal(d.Summary, err)
		}
	}
	var orders int
	if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM orders").Scan(&orders); err != nil || orders != 6 {
		t.Fatal("terminal entry retried", orders, err)
	}
}

func TestProductionJobReportsReleaseOutstandingBeforeProjection(t *testing.T) {
	s, admin, network, _, device, welcome, item := orderFixture(t)
	in := jobInput(item, "release-job-key", "unused-source")
	in.Entries = nil
	for i := 0; i < 10; i++ {
		in.Entries = append(in.Entries, protocol.ProductionJobEntryInput{EntryKey: fmt.Sprintf("release-source-%d", i), ItemID: item, Quantity: 1})
	}
	a, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := ack.Commands[0]
	for seq := 2; seq <= 8; seq++ {
		if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: protocol.Quantity(seq)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 9, Reports: []protocol.Report{{OrderID: c.OrderID, AttemptID: c.AttemptID, State: "done"}}}); err != nil {
		t.Fatal(err)
	}
	d, err := s.ProductionJob(ctx, admin.ID, network.ID, a.Receipt.JobID)
	if err != nil || d.Summary.Counts.Pending != 1 || d.Summary.Counts.Done != 1 || d.Summary.Counts.Queued+d.Summary.Counts.Assigned != 8 {
		t.Fatal("stale entry projection blocked admission", d.Summary, err)
	}
}

func TestProductionJobAdminGrantChangeDoesNotLoseAuthority(t *testing.T) {
	s, admin, network, _, device, welcome, item := orderFixture(t)
	if err := s.SetGrant(ctx, admin.ID, admin.ID, protocol.Grant{NetworkID: network.ID, CanCraft: true}); err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateProductionJob(ctx, admin.ID, network.ID, jobInput(item, "admin-authority-job", "admin-authority-source"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteGrant(ctx, admin.ID, admin.ID, network.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	d, err := s.ProductionJob(ctx, admin.ID, network.ID, a.Receipt.JobID)
	if err != nil || d.Summary.Control != "active" || d.Summary.Counts.Assigned != 1 {
		t.Fatal("irrelevant admin grant fenced job", d.Summary, err)
	}
}

func TestProductionJobMaintenanceSkipsHealthyNetworks(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	_, err := s.Pool().Exec(ctx, `INSERT INTO networks(id,name,server_group,source,device_hash)
		SELECT 'healthy-'||i,'healthy','server-1','simulator',decode(md5('healthy-'||i),'hex') FROM generate_series(1,65) i`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Pool().Exec(ctx, `INSERT INTO production_job_authority_epochs(member_id,network_id) SELECT $1,id FROM networks WHERE id LIKE 'healthy-%'`, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Pool().Exec(ctx, `INSERT INTO production_jobs(id,network_id,member_id,manifest_digest,ttl_seconds,owner_authority_epoch,total)
		SELECT 'healthy-job-'||id,id,$1,'test',3600,0,1 FROM networks WHERE id LIKE 'healthy-%'`, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Insert the actionable network after the healthy rows.
	n, token, err := s.CreateNetwork(ctx, admin.ID, "Later network", network.ServerGroup, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	device, w := hello(t, s, token, "later-boot")
	ingested, err := s.Ingest(ctx, device, batch(w.SessionID, 1, 1, 10))
	if err != nil {
		t.Fatal(err)
	}
	item = ingested.Bindings[0].ID
	o := createOrder(t, s, admin, n, item, "later-expiring-order")
	if _, err = s.Pool().Exec(ctx, "UPDATE orders SET expires_at=now()-interval '1 second' WHERE id=$1", o.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	o, err = s.Order(ctx, admin.ID, n.ID, o.ID)
	if err != nil || o.State != "expired" {
		t.Fatal("healthy jobs starved maintenance", o, err)
	}
}

func TestProductionJobRejectedSelectionsAndNetworkQueueBound(t *testing.T) {
	t.Run("rejected selections", func(t *testing.T) {
		s, admin, n, _, device, w, item := orderFixture(t)
		in := jobInput(item, "reject-job-key", "unused-source")
		in.Entries = nil
		for i := 0; i < 20; i++ {
			in.Entries = append(in.Entries, protocol.ProductionJobEntryInput{EntryKey: fmt.Sprintf("reject-source-%d", i), ItemID: item, Quantity: 1})
		}
		a, err := s.CreateProductionJob(ctx, admin.ID, n.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Pool().Exec(ctx, "UPDATE inventory SET craftable=false WHERE network_id=$1", n.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 1}); err != nil {
			t.Fatal(err)
		}
		d, err := s.ProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID)
		if err != nil || d.Summary.Counts.Rejected != 1 || d.Summary.Counts.Pending != 19 {
			t.Fatal("rejected selections escaped budget", d.Summary, err)
		}
		if _, err = s.Pool().Exec(ctx, "UPDATE inventory SET craftable=true WHERE network_id=$1", n.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 2}); err != nil {
			t.Fatal(err)
		}
		d, err = s.ProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID)
		if err != nil || d.Summary.Counts.Rejected != 1 || d.Summary.Counts.Queued+d.Summary.Counts.Assigned != 1 {
			t.Fatal("rejected entries retried", d.Summary, err)
		}
	})
	t.Run("linked queue bound", func(t *testing.T) {
		s, admin, n, _, device, w, item := orderFixture(t)
		for j := 0; j < 5; j++ {
			in := jobInput(item, fmt.Sprintf("queue-job-%d", j), "unused-source")
			in.Entries = nil
			for i := 0; i < 20; i++ {
				in.Entries = append(in.Entries, protocol.ProductionJobEntryInput{EntryKey: fmt.Sprintf("queue-source-%d-%d", j, i), ItemID: item, Quantity: 1})
			}
			if _, err := s.CreateProductionJob(ctx, admin.ID, n.ID, in); err != nil {
				t.Fatal(err)
			}
		}
		for seq := 1; seq <= 8; seq++ {
			if _, err := s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: protocol.Quantity(seq)}); err != nil {
				t.Fatal(err)
			}
		}
		var queued, total int
		if err := s.Pool().QueryRow(ctx, "SELECT count(*) FILTER(WHERE state='queued'),count(*) FROM orders").Scan(&queued, &total); err != nil || queued != 32 || total != 33 {
			t.Fatal(queued, total, err)
		}
		var tooMany bool
		if err := s.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT e.job_id FROM production_job_entries e JOIN orders o ON o.production_job_entry_id=e.id GROUP BY e.job_id HAVING count(*)>8)`).Scan(&tooMany); err != nil || tooMany {
			t.Fatal(tooMany, err)
		}
	})
}

func TestProductionJobPauseStopReceiptCeilingAndSettledConflict(t *testing.T) {
	s, admin, n, _, device, w, item := orderFixture(t)
	in := jobInput(item, "pause-future-job", "unused-source")
	in.Entries = nil
	for i := 0; i < 10; i++ {
		in.Entries = append(in.Entries, protocol.ProductionJobEntryInput{EntryKey: fmt.Sprintf("pause-source-%d", i), ItemID: item, Quantity: 1})
	}
	a, err := s.CreateProductionJob(ctx, admin.ID, n.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := ack.Commands[0]
	for seq := 2; seq <= 8; seq++ {
		if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: protocol.Quantity(seq)}); err != nil {
			t.Fatal(err)
		}
	}
	paused, err := s.ControlProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID, "pause", protocol.ProductionJobControlInput{OperationKey: "pause-future-operation", ExpectedControlVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 9, Reports: []protocol.Report{{OrderID: c.OrderID, AttemptID: c.AttemptID, State: "done"}}}); err != nil {
		t.Fatal(err)
	}
	d, err := s.ProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID)
	if err != nil || d.Summary.Counts.Pending != 2 || d.Summary.Counts.Done != 1 || d.Summary.Counts.Assigned != 1 || d.Summary.ControlVersion != paused.ControlVersion {
		t.Fatal("pause blocked execution or admitted future work", d.Summary, err)
	}
	// Exhaust all non-stop receipt slots; stop must remain possible.
	if _, err = s.Pool().Exec(ctx, `INSERT INTO production_job_actions(job_id,operation_key,member_id,action,expected_control_version,result)
		SELECT $1,'seed-operation-'||i,$2,'pause',1,'{}' FROM generate_series(1,1022) i`, a.Receipt.JobID, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ControlProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID, "resume", protocol.ProductionJobControlInput{OperationKey: "capacity-resume-operation", ExpectedControlVersion: 2}); !errors.Is(err, store.ErrCapacity) {
		t.Fatal(err)
	}
	stopped, err := s.ControlProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID, "stop", protocol.ProductionJobControlInput{OperationKey: "reserved-stop-operation", ExpectedControlVersion: 2})
	if err != nil || stopped.Counts.Canceled != 8 || stopped.Counts.Assigned != 1 || stopped.Counts.Done != 1 {
		t.Fatal(stopped, err)
	}
	orders, err := s.Orders(ctx, admin.ID, n.ID, "assigned", 10)
	if err != nil || len(orders) != 1 {
		t.Fatal(orders, err)
	}
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 10, Reports: []protocol.Report{{OrderID: orders[0].ID, AttemptID: orders[0].AttemptID, State: "done"}}}); err != nil {
		t.Fatal(err)
	}
	replay, err := s.ControlProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID, "stop", protocol.ProductionJobControlInput{OperationKey: "reserved-stop-operation", ExpectedControlVersion: 2})
	if err != nil || replay.Version != stopped.Version || replay.SettledAt != nil {
		t.Fatal("receipt replay returned current result", replay, err)
	}
	if _, err = s.ControlProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID, "resume", protocol.ProductionJobControlInput{OperationKey: "settled-operation", ExpectedControlVersion: 3}); !errors.Is(err, store.ErrConflict) {
		t.Fatal(err)
	}
}

func TestProductionJobPrimaryRestartPreservesUncertainty(t *testing.T) {
	s, admin, n, token, device, w, item := orderFixture(t)
	a, err := s.CreateProductionJob(ctx, admin.ID, n.ID, jobInput(item, "restart-job-key", "restart-source-key"))
	if err != nil {
		t.Fatal(err)
	}
	ack, err := s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := ack.Commands[0]
	device, w = hello(t, s, token, "new-primary-boot")
	d, err := s.ProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID)
	if err != nil || d.Summary.Counts.NeedsReview != 1 || d.Entries[0].OrderID != c.OrderID || d.Entries[0].AttemptID != c.AttemptID {
		t.Fatal(d, err)
	}
	ack, err = s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil || len(ack.Commands) != 0 {
		t.Fatal("uncertain entry readmitted after primary restart", ack, err)
	}
}

func TestProductionJobGrantRestoreLocksNetworkBeforeMember(t *testing.T) {
	s, admin, n, _, _, _, _ := orderFixture(t)
	owner, _, err := s.CreateMember(ctx, admin.ID, "restore-owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.Pool().Begin(cctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(cctx, "SELECT id FROM networks WHERE id=$1 FOR UPDATE", n.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- s.SetGrant(cctx, admin.ID, owner.ID, protocol.Grant{NetworkID: n.ID, CanCraft: true})
	}()
	// Wait for the restoration transaction's explicit network-lock wait.
	for {
		var waiting bool
		err = s.Pool().QueryRow(cctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity a WHERE datname=current_database() AND wait_event_type='Lock'
			AND query LIKE 'SELECT id FROM networks WHERE id=$1 FOR KEY SHARE%'
			AND EXISTS(SELECT 1 FROM pg_locks l WHERE l.pid=a.pid AND l.relation='networks'::regclass))`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err = <-result:
			t.Fatal("restore did not wait for network", err)
		default:
		}
		if cctx.Err() != nil {
			t.Fatal(cctx.Err())
		}
		time.Sleep(time.Millisecond)
	}
	// A primary's subsequent member lock must not wait on restoration.
	if _, err = tx.Exec(cctx, "SELECT id FROM members WHERE id=$1 FOR SHARE NOWAIT", owner.ID); err != nil {
		t.Fatal("restoration inverted network/member locking", err)
	}
	if err = tx.Commit(cctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
}

func TestProductionJobAuthorityLossModesAndNetworkIsolation(t *testing.T) {
	for _, mode := range []string{"downgrade", "revoke", "other-network"} {
		t.Run(mode, func(t *testing.T) {
			s, admin, n, _, device, w, item := orderFixture(t)
			other, _, err := s.CreateNetwork(ctx, admin.ID, "Other", "server-1", "simulator")
			if err != nil {
				t.Fatal(err)
			}
			owner, _, err := s.CreateMember(ctx, admin.ID, "authority-owner", []protocol.Grant{{NetworkID: n.ID, CanCraft: true}, {NetworkID: other.ID, CanCraft: true}})
			if err != nil {
				t.Fatal(err)
			}
			a, err := s.CreateProductionJob(ctx, owner.ID, n.ID, jobInput(item, "authority-job-key", "authority-source-key"))
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "downgrade":
				err = s.SetGrant(ctx, admin.ID, owner.ID, protocol.Grant{NetworkID: n.ID})
				if err == nil {
					err = s.SetGrant(ctx, admin.ID, owner.ID, protocol.Grant{NetworkID: n.ID, CanCraft: true})
				}
			case "revoke":
				err = s.RevokeMember(ctx, admin.ID, owner.ID)
				// Test-only restoration: there is no public member-unrevoke API.
				if err == nil {
					_, err = s.Pool().Exec(ctx, "UPDATE members SET revoked=false WHERE id=$1", owner.ID)
				}
			case "other-network":
				err = s.DeleteGrant(ctx, admin.ID, owner.ID, other.ID)
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
			if mode == "other-network" {
				if d.Summary.Control != "active" || d.Summary.Counts.Assigned != 1 {
					t.Fatal("other network fenced authority", d.Summary)
				}
			} else if d.Summary.Control != "paused" || d.Summary.Counts.Pending != 1 {
				t.Fatal("lost epoch admitted", d.Summary)
			}
		})
	}
}

func TestProductionJobStopSerializesWithDispatch(t *testing.T) {
	s, admin, n, _, device, w, item := orderFixture(t)
	for seq := 1; seq <= 6; seq++ {
		in := jobInput(item, fmt.Sprintf("race-stop-job-%d", seq), fmt.Sprintf("race-stop-source-%d", seq))
		a, err := s.CreateProductionJob(ctx, admin.ID, n.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: protocol.Quantity(seq)})
			errs <- err
		}()
		go func() {
			defer wg.Done()
			<-start
			_, err := s.ControlProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID, "stop", protocol.ProductionJobControlInput{OperationKey: "racing-stop-operation", ExpectedControlVersion: 1})
			errs <- err
		}()
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		d, err := s.ProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Summary.Control != "stopped" || d.Summary.Counts.Pending != 0 || d.Summary.Counts.Queued != 0 || d.Summary.Counts.Canceled+d.Summary.Counts.Assigned != 1 {
			t.Fatal("stop crossed dispatch boundary", d.Summary)
		}
	}
}

func TestProductionJobSingleOrderTransitionsAndRetention(t *testing.T) {
	for _, mode := range []string{"cancel", "review", "command-failure"} {
		t.Run(mode, func(t *testing.T) {
			s, admin, n, _, device, w, item := orderFixture(t)
			in := jobInput(item, "transition-job-key", "transition-source-key")
			a, err := s.CreateProductionJob(ctx, admin.ID, n.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" {
				// Occupy planning with an older direct order, leaving the job queued.
				createOrder(t, s, admin, n, item, "transition-older-direct")
			}
			if mode == "command-failure" {
				if _, err = s.Pool().Exec(ctx, "UPDATE bridge_sessions SET response_bytes=256 WHERE id=$1", w.SessionID); err != nil {
					t.Fatal(err)
				}
				if _, err = s.Pool().Exec(ctx, "UPDATE item_definitions SET label=repeat('x',512) WHERE network_id=$1", n.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 1}); err != nil {
				t.Fatal(err)
			}
			d, err := s.ProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "cancel":
				_, err = s.CancelOrder(ctx, admin.ID, n.ID, d.Entries[0].OrderID)
			case "review":
				if _, err = s.Pool().Exec(ctx, "UPDATE networks SET last_seen=now()-interval '1 hour' WHERE id=$1", n.ID); err == nil {
					err = s.Maintain(ctx)
				}
				if err == nil {
					_, err = s.ResolveOrder(ctx, admin.ID, n.ID, d.Entries[0].OrderID, "done", "explicit review evidence")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			d, err = s.ProductionJob(ctx, admin.ID, n.ID, a.Receipt.JobID)
			if err != nil || d.Summary.SettledAt == nil || d.Entries[0].TerminalAt == nil {
				t.Fatal(d, err)
			}
			// An old terminal order stays while its job retention deadline is young.
			if _, err = s.Pool().Exec(ctx, "UPDATE orders SET updated_at=now()-interval '31 days' WHERE id=$1", d.Entries[0].OrderID); err != nil {
				t.Fatal(err)
			}
			if err = s.Maintain(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Order(ctx, admin.ID, n.ID, d.Entries[0].OrderID); err != nil {
				t.Fatal("linked history removed too early", err)
			}
		})
	}
}

func TestProductionJobSelectionBudgetAndScopedKeys(t *testing.T) {
	s, admin, n, _, device, w, item := orderFixture(t)
	owner, _, err := s.CreateMember(ctx, admin.ID, "budget-owner", []protocol.Grant{{NetworkID: n.ID, CanCraft: true}})
	if err != nil {
		t.Fatal(err)
	}
	owners := []string{admin.ID, owner.ID}
	for i := 0; i < 17; i++ {
		member := owners[i/16]
		if _, err = s.CreateProductionJob(ctx, member, n.ID, jobInput(item, fmt.Sprintf("budget-job-%02d", i), fmt.Sprintf("budget-source-%02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	ack, err := s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	var admitted int
	if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM orders WHERE production_job_entry_id IS NOT NULL").Scan(&admitted); err != nil || admitted != 16 {
		t.Fatal("selection budget", admitted, err)
	}
	// Invalid and exact replay polls cannot consume the remaining seventeenth entry.
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 3}); !errors.Is(err, store.ErrSequence) {
		t.Fatal(err)
	}
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: w.SessionID, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM orders WHERE production_job_entry_id IS NOT NULL").Scan(&admitted); err != nil || admitted != 16 {
		t.Fatal(admitted, err)
	}
	p := protocol.Poll{SessionID: w.SessionID, Sequence: 2, Reports: []protocol.Report{{OrderID: ack.Commands[0].OrderID, AttemptID: ack.Commands[0].AttemptID, State: "running"}}}
	if _, err = s.Poll(ctx, device, p); err != nil {
		t.Fatal(err)
	}
	if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM orders WHERE production_job_entry_id IS NOT NULL").Scan(&admitted); err != nil || admitted != 17 {
		t.Fatal(admitted, err)
	}

	// Equal creation/source keys across owners and networks remain independent.
	a, err := s.CreateProductionJob(ctx, owner.ID, n.ID, jobInput(item, "budget-job-00", "budget-source-00"))
	if err != nil {
		t.Fatal("owner scope collided", err)
	}
	other, token, err := s.CreateNetwork(ctx, admin.ID, "Scoped network", "server-1", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	od, ow := hello(t, s, token, "scoped-boot")
	ingested, err := s.Ingest(ctx, od, batch(ow.SessionID, 1, 1, 10))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateProductionJob(ctx, admin.ID, other.ID, jobInput(ingested.Bindings[0].ID, "budget-job-00", "budget-source-00"))
	if err != nil {
		t.Fatal("network scope collided", err)
	}
	if a.Receipt.JobID == b.Receipt.JobID {
		t.Fatal("scoped identities merged")
	}
}
