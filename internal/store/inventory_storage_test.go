package store_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

// Observe writes to value columns: repeated evidence should use the metadata
// update path while accepted readings remain durable and quantity changes work.
func watchInventoryWrites(t *testing.T, s *store.Store) func() int {
	t.Helper()
	_, err := s.Pool().Exec(ctx, `CREATE TABLE inventory_write_audit(item_id bigint);
        CREATE FUNCTION audit_inventory_write() RETURNS trigger LANGUAGE plpgsql AS $$
        BEGIN INSERT INTO inventory_write_audit VALUES(NEW.item_id); RETURN NEW; END $$;
        CREATE TRIGGER inventory_write_audit AFTER UPDATE OF quantity,craftable ON inventory
        FOR EACH ROW EXECUTE FUNCTION audit_inventory_write()`)
	if err != nil {
		t.Fatal(err)
	}
	return func() int {
		t.Helper()
		var count int
		if err := s.Pool().QueryRow(ctx, "SELECT count(*) FROM inventory_write_audit").Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
}

func itemEvidence(t *testing.T, item protocol.Item) (time.Time, *time.Time) {
	t.Helper()
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		Quantity time.Time  `json:"observed_at"`
		Recipe   *time.Time `json:"craftable_observed_at"`
	}
	if err = json.Unmarshal(data, &evidence); err != nil {
		t.Fatal(err)
	}
	return evidence.Quantity, evidence.Recipe
}

func TestRepeatedInventoryObservationsOnlyRefreshEvidence(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "repeat-evidence")
	b := batch(w.SessionID, 1, 10, 9007199254740993)
	b.Updates = append(b.Updates, protocol.Update{Item: &protocol.Definition{Kind: "item", Name: "test:z", Label: "Z", Exact: true}, Quantity: 7, Observation: 10})
	ack, err := s.Ingest(ctx, d, b)
	if err != nil {
		t.Fatal(err)
	}
	id := ack.Bindings[0].ID
	first, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{Limit: 1})
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatal("missing initial page", first, err)
	}
	countWrites := watchInventoryWrites(t, s)
	for seq := protocol.Quantity(2); seq <= 4; seq++ {
		b = protocol.InventoryBatch{SessionID: w.SessionID, Sequence: seq, Updates: []protocol.Update{{ID: id, Quantity: 9007199254740993, Observation: seq + 10}}}
		if _, err = s.Ingest(ctx, d, b); err != nil {
			t.Fatal(err)
		}
	}
	after, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{Limit: 1})
	if err != nil || after.Revision != first.Revision || !after.Items[0].ObservedAt.After(first.Items[0].ObservedAt) {
		t.Fatal("identical counts lost evidence or invalidated the page", after, err)
	}
	if _, err = s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{Limit: 1, Cursor: first.NextCursor}); err != nil {
		t.Fatal("evidence-only updates invalidated a cursor", err)
	}
	if writes := countWrites(); writes != 0 {
		t.Fatalf("identical observations rewrote inventory value columns %d times", writes)
	}
	if _, err = s.Ingest(ctx, d, b); err != nil {
		t.Fatal("retry rejected", err)
	}
	b.Sequence = 5
	b.Updates[0].Observation = 5
	b.Updates[0].Quantity = 99
	if _, err = s.Ingest(ctx, d, b); err != nil {
		t.Fatal(err)
	}
	stale, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{Limit: 1})
	if err != nil || !stale.Items[0].ObservedAt.Equal(after.Items[0].ObservedAt) || stale.Items[0].Quantity != 9007199254740993 || countWrites() != 0 {
		t.Fatal("retry or stale evidence changed durable state", stale, err)
	}
	b.Sequence = 6
	b.Updates[0].Observation = 20
	b.Updates[0].Quantity = 0
	if _, err = s.Ingest(ctx, d, b); err != nil {
		t.Fatal(err)
	}
	if writes := countWrites(); writes != 1 {
		t.Fatalf("real count change wrote %d rows, want 1", writes)
	}
}

func TestLateRecipeObservationKeepsNewerQuantity(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "late-recipe")
	b := batch(w.SessionID, 1, 10, 10)
	no, yes := false, true
	b.Updates[0].Craftable = &no
	ack, err := s.Ingest(ctx, d, b)
	if err != nil {
		t.Fatal(err)
	}
	id := ack.Bindings[0].ID
	for index, update := range []protocol.Update{
		{ID: id, Quantity: 30, Observation: 30},
		{ID: id, Quantity: 20, Observation: 20, Craftable: &yes},
	} {
		if _, err = s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: protocol.Quantity(index + 2), Updates: []protocol.Update{update}}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Quantity != 30 || !page.Items[0].Craftable {
		t.Fatal("late recipe was discarded or replaced a newer count", page, err)
	}
	countTime, recipeTime := itemEvidence(t, page.Items[0])
	if recipeTime == nil || !recipeTime.After(countTime) {
		t.Fatal("separate recipe evidence was not exposed", countTime, recipeTime)
	}
	for index, update := range []protocol.Update{
		{ID: id, Quantity: 15, Observation: 15, Craftable: &no},
		{ID: id, Quantity: 40, Observation: 40},
	} {
		if _, err = s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: protocol.Quantity(index + 4), Updates: []protocol.Update{update}}); err != nil {
			t.Fatal(err)
		}
	}
	page, err = s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{})
	if err != nil || page.Items[0].Quantity != 40 || !page.Items[0].Craftable {
		t.Fatal("stale recipe replaced a newer flag", page, err)
	}
	newCountTime, newRecipeTime := itemEvidence(t, page.Items[0])
	if !newCountTime.After(countTime) || newRecipeTime == nil || !newRecipeTime.Equal(*recipeTime) {
		t.Fatal("quantity-only reading refreshed recipe evidence")
	}
}

func TestQuantityAndRecipeFreshnessStayIndependent(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "freshness-1")
	ack, err := s.Ingest(ctx, d, batch(w.SessionID, 1, 10, 5))
	if err != nil {
		t.Fatal(err)
	}
	read := func() protocol.Item {
		t.Helper()
		page, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{})
		if err != nil || len(page.Items) != 1 {
			t.Fatal(page, err)
		}
		return page.Items[0]
	}
	before := read()
	countTime, recipeTime := itemEvidence(t, before)
	if recipeTime == nil || !countTime.Equal(*recipeTime) {
		t.Fatal("a complete reading must expose both observation times")
	}
	id := ack.Bindings[0].ID
	if _, err = s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 2, Updates: []protocol.Update{{ID: id, Quantity: 6, Observation: 11}}}); err != nil {
		t.Fatal(err)
	}
	after := read()
	countAfter, recipeAfter := itemEvidence(t, after)
	if !countAfter.After(countTime) || recipeAfter == nil || !recipeAfter.Equal(*recipeTime) || !after.Craftable {
		t.Fatal("count-only reading changed recipe evidence")
	}
	_, w = hello(t, s, token, "freshness-2")
	if read().CurrentSession {
		t.Fatal("old reading appears current after reconnect")
	}
	if _, err = s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 1, Updates: []protocol.Update{{ID: id, Quantity: 6, Observation: 1}}}); err != nil {
		t.Fatal(err)
	}
	after = read()
	_, recipeAfter = itemEvidence(t, after)
	if !after.CurrentSession || after.Craftable || recipeAfter != nil {
		t.Fatal("new session inherited unverified recipe evidence", after)
	}
}
