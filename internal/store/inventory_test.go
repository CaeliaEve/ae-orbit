package store_test

import (
	"errors"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func stone() *protocol.Definition {
	return &protocol.Definition{Kind: "item", Name: "minecraft:stone", Label: "Stone", Exact: true}
}
func batch(session string, seq, observation, quantity protocol.Quantity) protocol.InventoryBatch {
	craftable := true
	return protocol.InventoryBatch{SessionID: session, Sequence: seq, Updates: []protocol.Update{{Item: stone(), Quantity: quantity, Observation: observation, Craftable: &craftable}}}
}

func TestInventoryRetryAndPrecision(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "boot-1")
	b := batch(w.SessionID, 1, 1, 9007199254740993)
	a, err := s.Ingest(ctx, d, b)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := s.Ingest(ctx, d, b)
	if err != nil {
		t.Fatal(err)
	}
	if a.Revision != repeated.Revision || a.Bindings[0].ID != repeated.Bindings[0].ID {
		t.Fatal("retry changed result")
	}
	page, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Quantity.String() != "9007199254740993" {
		t.Fatalf("%+v", page)
	}
	b.Updates[0].Quantity = 3
	if _, err = s.Ingest(ctx, d, b); !errors.Is(err, store.ErrSequence) {
		t.Fatal("different body reused sequence", err)
	}
	b.Sequence = 3
	if _, err = s.Ingest(ctx, d, b); !errors.Is(err, store.ErrSequence) {
		t.Fatal("gap accepted", err)
	}
}

func TestInventoryOrderingAndRollback(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "boot-1")
	b := batch(w.SessionID, 1, 10, 45)
	b.Updates = append(b.Updates, protocol.Update{Item: stone(), Quantity: 50, Observation: 11, Craftable: b.Updates[0].Craftable})
	a, err := s.Ingest(ctx, d, b)
	if err != nil {
		t.Fatal(err)
	}
	if a.Bindings[0].ID != a.Bindings[1].ID {
		t.Fatal("duplicate definitions split")
	}
	if _, err = s.Ingest(ctx, d, batch(w.SessionID, 2, 9, 5)); err != nil {
		t.Fatal(err)
	}
	page, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].Quantity != 50 {
		t.Fatal("older observation replaced inventory")
	}
	bad := batch(w.SessionID, 3, 12, 99)
	bad.Updates = append(bad.Updates, protocol.Update{ID: 99999999, Quantity: 1, Observation: 13})
	if _, err = s.Ingest(ctx, d, bad); err == nil {
		t.Fatal("unknown short ID accepted")
	}
	if _, err = s.Ingest(ctx, d, batch(w.SessionID, 3, 14, 0)); err != nil {
		t.Fatal("failed batch advanced ack", err)
	}
	page, err = s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Quantity != 0 || !page.Items[0].Craftable {
		t.Fatal("zero-stock craftable vanished")
	}
	_, newSession := hello(t, s, token, "boot-2")
	if _, err = s.Ingest(ctx, d, batch(w.SessionID, 4, 15, 1)); !errors.Is(err, store.ErrStaleSession) {
		t.Fatal(err)
	}
	page, err = s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].CurrentSession {
		t.Fatal("old observation presented as current session")
	}
	if _, err = s.Ingest(ctx, d, batch(newSession.SessionID, 1, 1, 7)); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryRejectsOtherNetworkID(t *testing.T) {
	s, admin, _, token := fixture(t)
	d, w := hello(t, s, token, "boot-1")
	a, err := s.Ingest(ctx, d, batch(w.SessionID, 1, 1, 2))
	if err != nil {
		t.Fatal(err)
	}
	_, token2, err := s.CreateNetwork(ctx, admin.ID, "Other", "server-2", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	d2, w2 := hello(t, s, token2, "other-boot")
	b := protocol.InventoryBatch{SessionID: w2.SessionID, Sequence: 1, Updates: []protocol.Update{{ID: a.Bindings[0].ID, Quantity: 99, Observation: 1}}}
	if _, err = s.Ingest(ctx, d2, b); err == nil {
		t.Fatal("cross-network reference accepted")
	}
}

func TestPaginationAndExactTotals(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "boot-1")
	items := []protocol.Update{}
	for i, name := range []string{"A", "B", "C"} {
		items = append(items, protocol.Update{Item: &protocol.Definition{Kind: "item", Name: "test:" + name, Label: name, Exact: true}, Quantity: 9223372036854775807, Observation: protocol.Quantity(i + 1)})
	}
	if _, err := s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 1, Updates: items}); err != nil {
		t.Fatal(err)
	}
	first, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{Limit: 2, Sort: "quantity"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatal(first)
	}
	second, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{Limit: 2, Sort: "quantity", Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID || second.Items[0].ID == first.Items[1].ID {
		t.Fatal("pagination duplicate")
	}
	stats, err := s.InventoryStats(ctx, admin.ID, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalQuantity != "27670116110564327421" {
		t.Fatal("aggregate overflow", stats)
	}
	if _, err = s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{Limit: 2, Sort: "quantity", Cursor: first.NextCursor + "x"}); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("cursor tampering accepted", err)
	}
	if _, err = s.Ingest(ctx, d, batch(w.SessionID, 2, 5, 7)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{Limit: 2, Sort: "quantity", Cursor: first.NextCursor}); !errors.Is(err, store.ErrViewChanged) {
		t.Fatal("stale view accepted", err)
	}
}
