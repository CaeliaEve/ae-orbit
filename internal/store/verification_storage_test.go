package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func expireInventoryEvidence(t *testing.T, s *store.Store, networkID string) {
	t.Helper()
	if _, err := s.Pool().Exec(ctx, `UPDATE inventory SET observed_at=now()-interval '1 hour',
        craftable_observed_at=CASE WHEN craftable_observed_at IS NOT NULL THEN now()-interval '1 hour' END WHERE network_id=$1`, networkID); err != nil {
		t.Fatal(err)
	}
}

func TestPollingDoesNotRewriteInventoryEvidence(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "schedule-evidence")
	if _, err := s.Ingest(ctx, d, batch(w.SessionID, 1, 1, 10)); err != nil {
		t.Fatal(err)
	}
	expireInventoryEvidence(t, s, n.ID)
	if _, err := s.Pool().Exec(ctx, "UPDATE inventory_schedule SET next_verify_at=now()-interval '1 hour' WHERE network_id=$1", n.ID); err != nil {
		t.Fatal(err)
	}
	before, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{})
	if err != nil {
		t.Fatal(err)
	}
	countWrites := watchInventoryWrites(t, s)
	p := protocol.Poll{SessionID: w.SessionID, Sequence: 1}
	ack, err := s.Poll(ctx, d, p)
	if err != nil || len(ack.Verify) != 1 {
		t.Fatal("due work was not delivered", ack, err)
	}
	var deadline time.Time
	if err = s.Pool().QueryRow(ctx, "SELECT next_verify_at FROM inventory_schedule WHERE network_id=$1", n.ID).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Poll(ctx, d, p); err != nil {
		t.Fatal(err)
	}
	var replayDeadline time.Time
	if err = s.Pool().QueryRow(ctx, "SELECT next_verify_at FROM inventory_schedule WHERE network_id=$1", n.ID).Scan(&replayDeadline); err != nil || !replayDeadline.Equal(deadline) {
		t.Fatal("replayed poll rescheduled its work", err)
	}
	after, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{})
	if err != nil || countWrites() != 0 || after.Revision != before.Revision || !after.Items[0].ObservedAt.Equal(before.Items[0].ObservedAt) {
		t.Fatal("task delivery changed inventory evidence", after, err)
	}
}

func TestRechecksUseRecipeFreshnessAndCurrentSession(t *testing.T) {
	s, _, n, token := fixture(t)
	d, w := hello(t, s, token, "schedule-freshness-1")
	yes := true
	updates := []protocol.Update{}
	for _, name := range []string{"fresh", "old-recipe", "unknown-recipe"} {
		u := protocol.Update{Item: &protocol.Definition{Kind: "item", Name: "test:" + name, Label: name, Exact: true}, Quantity: 10, Observation: 1, Craftable: &yes}
		if name == "unknown-recipe" {
			u.Craftable = nil
		}
		updates = append(updates, u)
	}
	ack, err := s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 1, Updates: updates})
	if err != nil {
		t.Fatal(err)
	}
	freshID, recipeID := ack.Bindings[0].ID, ack.Bindings[1].ID
	if _, err = s.Pool().Exec(ctx, "UPDATE inventory SET craftable_observed_at=now()-interval '1 hour' WHERE network_id=$1 AND item_id=$2", n.ID, int64(recipeID)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE inventory_schedule SET next_verify_at=now()-interval '1 second' WHERE network_id=$1", n.ID); err != nil {
		t.Fatal(err)
	}
	// A hot count alone cannot postpone an old or unknown recipe.
	if _, err = s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 2, Updates: []protocol.Update{{ID: recipeID, Quantity: 11, Observation: 2}}}); err != nil {
		t.Fatal(err)
	}
	countWrites := watchInventoryWrites(t, s)
	poll, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil || len(poll.Verify) != 2 {
		t.Fatal("recipe freshness did not select the right work", poll, err)
	}
	for _, v := range poll.Verify {
		if v.ID == freshID {
			t.Fatal("recent complete evidence caused a redundant read")
		}
	}
	var freshDeferred bool
	if err = s.Pool().QueryRow(ctx, "SELECT next_verify_at>now() FROM inventory_schedule WHERE network_id=$1 AND item_id=$2", n.ID, int64(freshID)).Scan(&freshDeferred); err != nil || !freshDeferred || countWrites() != 0 {
		t.Fatal("fresh candidate was not advanced independently", err)
	}
	_, w = hello(t, s, token, "schedule-freshness-2")
	if _, err = s.Pool().Exec(ctx, "UPDATE inventory_schedule SET next_verify_at=now()-interval '1 second' WHERE network_id=$1", n.ID); err != nil {
		t.Fatal(err)
	}
	poll, err = s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil || len(poll.Verify) != 3 {
		t.Fatal("old session evidence suppressed revalidation", poll, err)
	}
}

func TestRecheckCandidateWindowAdvancesWithoutStarvingColdWork(t *testing.T) {
	s, _, n, token := fixture(t)
	d, w := hello(t, s, token, "bounded-candidates")
	yes := true
	updates := []protocol.Update{}
	for index := 0; index < 128; index++ {
		updates = append(updates, protocol.Update{Item: &protocol.Definition{Kind: "item", Name: fmt.Sprintf("test:fresh_%03d", index), Label: "Fresh", Exact: true}, Quantity: 1, Observation: 1, Craftable: &yes})
	}
	if _, err := s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 1, Updates: updates}); err != nil {
		t.Fatal(err)
	}
	ack, err := s.Ingest(ctx, d, batch(w.SessionID, 2, 2, 7))
	if err != nil {
		t.Fatal(err)
	}
	coldID := ack.Bindings[0].ID
	if _, err = s.Pool().Exec(ctx, `UPDATE inventory_schedule SET next_verify_at=CASE WHEN item_id=$2
        THEN now()-interval '1 hour' ELSE now()-interval '2 hours' END WHERE network_id=$1`, n.ID, int64(coldID)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE inventory SET observed_at=now()-interval '1 hour' WHERE network_id=$1 AND item_id=$2", n.ID, int64(coldID)); err != nil {
		t.Fatal(err)
	}
	first, err := s.Poll(ctx, d, capacityPoll(t, w.SessionID, 1, 1, 16384))
	if err != nil || len(first.Verify) != 0 {
		t.Fatal("bounded fresh candidates should be advanced without dispatch", first, err)
	}
	second, err := s.Poll(ctx, d, capacityPoll(t, w.SessionID, 2, 1, 16384))
	if err != nil || len(second.Verify) != 1 || second.Verify[0].ID != coldID {
		t.Fatal("fresh candidates starved the following cold item", second, err)
	}
}
