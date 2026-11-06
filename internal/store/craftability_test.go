package store_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestQuantityOnlyUpdatesPreserveRecipeObservations(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "craftability-1")
	a, err := s.Ingest(ctx, d, batch(w.SessionID, 1, 10, 30795))
	if err != nil {
		t.Fatal(err)
	}
	id := a.Bindings[0].ID.String()
	seq := 1
	ingest := func(updates string, wantQuantity protocol.Quantity, wantCraftable bool) {
		t.Helper()
		seq++
		body := fmt.Sprintf(`{"session_id":%q,"sequence":%q,"updates":[%s]}`, w.SessionID, fmt.Sprint(seq), updates)
		var b protocol.InventoryBatch
		if err := json.Unmarshal([]byte(body), &b); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Ingest(ctx, d, b); err != nil {
			t.Fatal(err)
		}
		page, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Items[0].Quantity != wantQuantity || page.Items[0].Craftable != wantCraftable {
			t.Fatalf("quantity/recipe observation was lost: %+v", page.Items)
		}
	}
	u := func(observation, quantity int, craftable string) string {
		return fmt.Sprintf(`{"id":%q,"observation":%q,"quantity":%q,"craftable":%s}`, id, fmt.Sprint(observation), fmt.Sprint(quantity), craftable)
	}
	ingest(u(11, 30794, "null"), 30794, true)
	ingest(u(12, 0, "null"), 0, true)
	ingest(u(13, 0, "false"), 0, false)
	// The last quantity in a batch must retain the newest known recipe flag.
	ingest(u(14, 1, "true")+","+u(15, 2, "null"), 2, true)
	// Input order is independent of observation order.
	ingest(u(18, 4, "null")+","+u(16, 3, "true")+","+u(17, 3, "false"), 4, false)
	// An old recipe flag alongside a fresh count must not resurrect a recipe.
	ingest(u(16, 3, "true")+","+u(19, 5, "null"), 5, false)
	ingest(u(20, 5, "true"), 5, true)
	// Quantity alone in a new boot must not revalidate the old boot's recipe.
	_, w = hello(t, s, token, "craftability-2")
	seq = 0
	ingest(u(1, 6, "null"), 6, false)
	ingest(u(2, 6, "true"), 6, true)
}

func TestSameObservationCraftabilityConflictRollsBack(t *testing.T) {
	s, _, _, token := fixture(t)
	d, w := hello(t, s, token, "craftability-conflict")
	b := batch(w.SessionID, 1, 1, 2)
	// Identical separately decoded booleans must compare by value, not address.
	var wire protocol.InventoryBatch
	encoded, _ := json.Marshal(b)
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	b.Updates = append(b.Updates, wire.Updates[0])
	if _, err := s.Ingest(ctx, d, b); err != nil {
		t.Fatal("equal recipe observations conflicted", err)
	}
	b.Sequence = 2
	for i := range b.Updates {
		b.Updates[i].Observation = 2
	}
	no := false
	b.Updates[1].Craftable = &no
	if _, err := s.Ingest(ctx, d, b); !errors.Is(err, store.ErrConflict) {
		t.Fatal("contradictory recipe observations were accepted", err)
	}
	b.Updates = b.Updates[:1]
	if _, err := s.Ingest(ctx, d, b); err != nil {
		t.Fatal("conflicting batch advanced the ACK", err)
	}
}
