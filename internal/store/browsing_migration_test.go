package store_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestBrowsingMigrationPreservesInventoryAndRetry(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "browsing-migration")
	b := batch(w.SessionID, 1, 10, 9007199254740993)
	ack, err := s.Ingest(ctx, d, b)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{Consistency: "live"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, `ALTER TABLE networks DROP COLUMN catalog_revision, DROP COLUMN craftable_revision;
        DELETE FROM schema_versions WHERE version='migrations/005_inventory_browsing.sql'`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	after, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{Consistency: "live"})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("migration changed cached inventory or its evidence", before, after, err)
	}
	retried, err := s.Ingest(ctx, d, b)
	if err != nil || !reflect.DeepEqual(ack, retried) {
		t.Fatal("migration changed the acknowledged batch", ack, retried, err)
	}
}

func TestBrowsingRevisionsRollBackWithInventory(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "browsing-rollback")
	yes := true
	b := protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 1, Updates: []protocol.Update{
		{Item: &protocol.Definition{Kind: "item", Name: "test:a", Label: "Alpha", Exact: true}, Quantity: 10, Observation: 1, Craftable: &yes},
		{Item: &protocol.Definition{Kind: "item", Name: "test:b", Label: "Beta", Exact: true}, Quantity: 20, Observation: 1, Craftable: &yes},
	}}
	if _, err := s.Ingest(ctx, d, b); err != nil {
		t.Fatal(err)
	}
	queries := []protocol.ItemQuery{{Consistency: "live", Limit: 1}, {Consistency: "live", Limit: 1, Craftable: &yes}}
	for i := range queries {
		first, err := s.Items(ctx, admin.ID, n.ID, queries[i])
		if err != nil || first.NextCursor == "" {
			t.Fatal(first, err)
		}
		queries[i].Cursor = first.NextCursor
	}
	if _, err := s.Pool().Exec(ctx, `CREATE FUNCTION reject_browse_ack() RETURNS trigger LANGUAGE plpgsql AS $$
        BEGIN RAISE EXCEPTION 'simulated acknowledgement persistence failure'; END $$;
        CREATE TRIGGER reject_browse_ack BEFORE UPDATE OF inventory_response ON bridge_sessions
        FOR EACH ROW EXECUTE FUNCTION reject_browse_ack()`); err != nil {
		t.Fatal(err)
	}
	no := false
	b.Sequence = 2
	b.Updates = b.Updates[1:]
	b.Updates[0].Item.Label, b.Updates[0].Craftable = "Before Alpha", &no
	b.Updates[0].Observation, b.Updates[0].Quantity = 2, 99
	if _, err := s.Ingest(ctx, d, b); err == nil {
		t.Fatal("failed acknowledgement unexpectedly committed")
	}
	for _, query := range queries {
		after, err := s.Items(ctx, admin.ID, n.ID, query)
		if err != nil || len(after.Items) != 1 || after.Items[0].Label != "Beta" || !after.Items[0].Craftable || after.Items[0].Quantity != 20 {
			t.Fatal("rolled-back data invalidated a cursor or leaked a change", after, err)
		}
	}
	if _, err := s.Pool().Exec(ctx, "DROP TRIGGER reject_browse_ack ON bridge_sessions"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest(ctx, d, b); err != nil {
		t.Fatal("rolled-back acknowledgement prevented retry", err)
	}
	for _, query := range queries {
		if _, err := s.Items(ctx, admin.ID, n.ID, query); !errors.Is(err, store.ErrViewChanged) {
			t.Fatal("committed membership change did not invalidate the cursor", err)
		}
	}
}

func TestItemLookupBoundsAndStaleEvidence(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "lookup-first")
	ack, err := s.Ingest(ctx, d, batch(w.SessionID, 1, 1, 9007199254740993))
	if err != nil {
		t.Fatal(err)
	}
	id := ack.Bindings[0].ID
	for _, ids := range [][]protocol.Quantity{nil, {0}, {-1}, make([]protocol.Quantity, 201)} {
		if _, err = s.ItemsByID(ctx, admin.ID, n.ID, ids); !errors.Is(err, store.ErrInvalid) {
			t.Fatal("invalid lookup bounds accepted", len(ids), err)
		}
	}
	before, err := s.ItemsByID(ctx, admin.ID, n.ID, []protocol.Quantity{id})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Items) != 1 || !before.Items[0].CurrentSession {
		t.Fatal(before)
	}
	hello(t, s, token, "lookup-second")
	after, err := s.ItemsByID(ctx, admin.ID, n.ID, []protocol.Quantity{id})
	if err != nil || len(after.Items) != 1 || after.Items[0].CurrentSession || after.Items[0].Quantity != 9007199254740993 {
		t.Fatal("session change discarded stale inventory or marked it current", after, err)
	}
	before.Items[0].CurrentSession = false
	if !reflect.DeepEqual(before.Items, after.Items) {
		t.Fatal("lookup refreshed evidence without a device observation", before, after)
	}
}
