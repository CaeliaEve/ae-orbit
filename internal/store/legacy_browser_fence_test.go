package store_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

// Seed a pre-upgrade order without asking the current writer to accept a reserved key.
func seedLegacyDraftOrder(t *testing.T, s *store.Store, owner protocol.Member, network protocol.Network, item protocol.Quantity, key string) protocol.Order {
	t.Helper()
	order := createOrder(t, s, owner, network, item, "seed-"+key)
	if _, err := s.Pool().Exec(ctx, "UPDATE orders SET idempotency_key=$2 WHERE id=$1", order.ID, key); err != nil {
		t.Fatal(err)
	}
	return order
}

func TestLegacyBrowserFenceRejectsNewReservedOrder(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	_, err := s.CreateOrder(ctx, admin.ID, network.ID, protocol.OrderInput{ItemID: item, Quantity: 64, IdempotencyKey: "draft-new-row-001"})
	if !errors.Is(err, store.ErrClientUpgradeRequired) {
		t.Fatalf("new stale-browser intent accepted or wrongly classified: %v", err)
	}
	var orders int
	if err := s.Pool().QueryRow(ctx, "SELECT count(*) FROM orders").Scan(&orders); err != nil || orders != 0 {
		t.Fatal("rejected draft inserted an order", orders, err)
	}
	createOrder(t, s, admin, network, item, "normal-api-001")
}

func TestLegacyBrowserFenceRetainedOrderReplayPrecedesRejection(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	const key = "draft-retained-001"
	original := seedLegacyDraftOrder(t, s, admin, network, item, key)
	if _, err := s.Pool().Exec(ctx, "UPDATE networks SET capabilities='{}',current_session='' WHERE id=$1", network.ID); err != nil {
		t.Fatal(err)
	}
	input := protocol.OrderInput{ItemID: item, Quantity: 64, IdempotencyKey: key}
	replay, err := s.CreateOrder(ctx, admin.ID, network.ID, input)
	if err != nil || replay.ID != original.ID {
		t.Fatal("retained reserved order could not replay", replay, err)
	}
	for _, changed := range []protocol.OrderInput{
		{ItemID: item, Quantity: 65, IdempotencyKey: key},
		{ItemID: item, Quantity: 64, IdempotencyKey: key, TTLSeconds: 10},
		{ItemID: item, Quantity: 64, IdempotencyKey: key, BatchID: "changed-batch"},
	} {
		if _, err := s.CreateOrder(ctx, admin.ID, network.ID, changed); !errors.Is(err, store.ErrConflict) {
			t.Fatal("changed retained draft did not conflict", err)
		}
	}
	found, err := s.OrderByIdempotencyKey(ctx, admin.ID, network.ID, key)
	if err != nil || found.ID != original.ID {
		t.Fatal("legacy reconciliation changed", found, err)
	}
}

func TestLegacyBrowserFenceJobRejectsRetainedOrderWithoutReceipts(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	const key = "draft-retained-source-001"
	seedLegacyDraftOrder(t, s, admin, network, item, key)
	in := jobInput(item, "legacy-collision-job", "fresh-source-001")
	in.Entries = append(in.Entries, protocol.ProductionJobEntryInput{EntryKey: key, ItemID: item, Quantity: 64})
	result, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
	if !errors.Is(err, store.ErrConflict) || result.Receipt.JobID != "" || result.Job != nil {
		t.Fatal("legacy source was accepted or given a fake job identity", result, err)
	}
	var jobs, receipts, sources int
	if err := s.Pool().QueryRow(ctx, `SELECT (SELECT count(*) FROM production_jobs),
		(SELECT count(*) FROM production_job_receipts),(SELECT count(*) FROM production_job_entry_receipts)`).Scan(&jobs, &receipts, &sources); err != nil || jobs != 0 || receipts != 0 || sources != 0 {
		t.Fatal("legacy collision partially accepted manifest", jobs, receipts, sources, err)
	}
	if _, err := s.ProductionJobByKey(ctx, admin.ID, network.ID, "", in.RequestKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("legacy collision left creation receipt", err)
	}
}

func TestLegacyBrowserFenceJobScopeAndExactReplay(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	const key = "draft-scoped-source-001"
	other, _, err := s.CreateMember(ctx, admin.ID, "other crafter", []protocol.Grant{{NetworkID: network.ID, CanCraft: true}})
	if err != nil {
		t.Fatal(err)
	}
	seedLegacyDraftOrder(t, s, other, network, item, key)
	otherNetwork, token, err := s.CreateNetwork(ctx, admin.ID, "other network", "server-1", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	device, welcome := hello(t, s, token, "other-network-boot")
	ack, err := s.Ingest(ctx, device, batch(welcome.SessionID, 1, 1, 10))
	if err != nil {
		t.Fatal(err)
	}
	seedLegacyDraftOrder(t, s, admin, otherNetwork, ack.Bindings[0].ID, key)
	in := jobInput(item, "scoped-job-request", key)
	accepted, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
	if err != nil || accepted.Job == nil {
		t.Fatal("different owner/network blocked a fresh source", accepted, err)
	}
	seedLegacyDraftOrder(t, s, admin, network, item, key)
	replay, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in)
	if err != nil || !replay.Replayed || replay.Receipt.JobID != accepted.Receipt.JobID {
		t.Fatal("legacy collision invalidated an exact accepted job replay", replay, err)
	}
	in.Entries[0].Quantity++
	if _, err := s.CreateProductionJob(ctx, admin.ID, network.ID, in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("changed accepted manifest did not conflict", err)
	}
}

func TestLegacyBrowserFenceConcurrentOldAndNewIntent(t *testing.T) {
	s, admin, network, _, _, _, item := orderFixture(t)
	const key = "draft-concurrent-row-001"
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, old := range []bool{true, false} {
		wg.Add(1)
		go func(old bool) {
			defer wg.Done()
			<-start
			var err error
			if old {
				_, err = s.CreateOrder(ctx, admin.ID, network.ID, protocol.OrderInput{ItemID: item, Quantity: 64, IdempotencyKey: key})
			} else {
				_, err = s.CreateProductionJob(ctx, admin.ID, network.ID, jobInput(item, "concurrent-job-request", key))
			}
			results <- err
		}(old)
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, rejected := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if errors.Is(err, store.ErrClientUpgradeRequired) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	var orders, jobs int
	if err := s.Pool().QueryRow(ctx, "SELECT (SELECT count(*) FROM orders),(SELECT count(*) FROM production_jobs)").Scan(&orders, &jobs); err != nil || accepted != 1 || rejected != 1 || orders != 0 || jobs != 1 {
		t.Fatal("old and new clients double accepted intent", accepted, rejected, orders, jobs, err)
	}
}
