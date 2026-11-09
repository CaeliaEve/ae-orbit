package store_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

// Restore the published pre-004 layout only inside a disposable test schema.
// This lets migration tests exercise cached data, ACKs and pending work together.
func restoreLegacyInventory(t *testing.T, s *store.Store) {
	t.Helper()
	_, err := s.Pool().Exec(ctx, `DROP TABLE inventory_refreshes;
        DELETE FROM schema_versions WHERE version='migrations/008_item_refreshes.sql';
        ALTER TABLE inventory ADD COLUMN next_verify_at timestamptz;
        UPDATE inventory i SET next_verify_at=q.next_verify_at FROM inventory_schedule q
        WHERE i.network_id=q.network_id AND i.item_id=q.item_id;
        ALTER TABLE inventory ALTER COLUMN next_verify_at SET NOT NULL, ALTER COLUMN observed_at SET DEFAULT now(),
            DROP COLUMN craftable_observation, DROP COLUMN craftable_observed_at;
        CREATE INDEX inventory_verify ON inventory(network_id,next_verify_at,item_id);
        DROP TABLE inventory_schedule;
        DELETE FROM schema_versions WHERE version='migrations/004_inventory_scheduling.sql'`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestInventoryStorageMigrationPreservesCachedDataAndRetry(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "inventory-migration")
	b := batch(w.SessionID, 1, 50, 9007199254740993)
	ack, err := s.Ingest(ctx, d, b)
	if err != nil {
		t.Fatal(err)
	}
	id := ack.Bindings[0].ID
	order := createOrder(t, s, admin, n, id, "inventory-migration-order")
	const confirmation = "0123456789abcdef0123456789abcdef"
	if _, err = s.Pool().Exec(ctx, "INSERT INTO inventory_rechecks(network_id,item_id,token) VALUES($1,$2,$3)", n.ID, int64(id), confirmation); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	deadline := oldTime.Add(5 * time.Minute)
	if _, err = s.Pool().Exec(ctx, "UPDATE inventory SET observed_at=$2 WHERE network_id=$1", n.ID, oldTime); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE inventory_schedule SET next_verify_at=$2 WHERE network_id=$1", n.ID, deadline); err != nil {
		t.Fatal(err)
	}
	restoreLegacyInventory(t, s)
	for repeat := 0; repeat < 2; repeat++ {
		if err = s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Quantity != 9007199254740993 || !page.Items[0].Craftable || !page.Items[0].CurrentSession || !page.Items[0].ObservedAt.Equal(oldTime) || page.Revision != ack.Revision {
		t.Fatal("migration changed cached values or count evidence", page, err)
	}
	_, recipeTime := itemEvidence(t, page.Items[0])
	if recipeTime != nil {
		t.Fatal("migration invented a historical recipe observation")
	}
	var afterDeadline time.Time
	var afterToken string
	if err = s.Pool().QueryRow(ctx, `SELECT q.next_verify_at,r.token FROM inventory_schedule q
        JOIN inventory_rechecks r USING(network_id,item_id) WHERE q.network_id=$1`, n.ID).Scan(&afterDeadline, &afterToken); err != nil || !afterDeadline.Equal(deadline) || afterToken != confirmation {
		t.Fatal("migration changed pending work", err)
	}
	afterOrder, err := s.Order(ctx, admin.ID, n.ID, order.ID)
	if err != nil || !reflect.DeepEqual(afterOrder, order) {
		t.Fatal("migration changed an order", afterOrder, err)
	}
	replayed, err := s.Ingest(ctx, d, b)
	if err != nil || !reflect.DeepEqual(replayed, ack) {
		t.Fatal("migration lost the acknowledged batch", replayed, err)
	}
	// Legacy recipe history is unknown: never let a pre-migration older flag
	// resurrect a state that the published implementation would have rejected.
	no := false
	if _, err = s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 2, Updates: []protocol.Update{{ID: id, Observation: 40, Quantity: 1, Craftable: &no}}}); err != nil {
		t.Fatal(err)
	}
	page, err = s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{})
	if err != nil || !page.Items[0].Craftable || !page.Items[0].ObservedAt.Equal(oldTime) {
		t.Fatal("legacy ordering floor was lost", page, err)
	}
	_, recipeTime = itemEvidence(t, page.Items[0])
	if recipeTime != nil {
		t.Fatal("replay or stale reading manufactured recipe evidence")
	}
}

func TestInventoryEvidenceFailureRollsBackValuesAndACK(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "evidence-rollback")
	ack, err := s.Ingest(ctx, d, batch(w.SessionID, 1, 1, 10))
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, `CREATE FUNCTION reject_inventory_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
        BEGIN RAISE EXCEPTION 'test evidence failure'; END $$;
        CREATE TRIGGER reject_inventory_evidence BEFORE UPDATE ON inventory
        FOR EACH ROW EXECUTE FUNCTION reject_inventory_evidence()`); err != nil {
		t.Fatal(err)
	}
	b := protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 2, Updates: []protocol.Update{{ID: ack.Bindings[0].ID, Quantity: 0, Observation: 2}}}
	if _, err = s.Ingest(ctx, d, b); err == nil {
		t.Fatal("injected evidence write failure was ignored")
	}
	after, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{})
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("failed evidence write partially changed inventory", after, err)
	}
	if _, err = s.Pool().Exec(ctx, "DROP TRIGGER reject_inventory_evidence ON inventory"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Ingest(ctx, d, b); err != nil {
		t.Fatal("failed evidence write advanced the batch ACK", err)
	}
}

func TestInventoryMigrationFailureKeepsLegacyDataAndDependencies(t *testing.T) {
	s, _, n, token := fixture(t)
	d, w := hello(t, s, token, "migration-failure")
	ack, err := s.Ingest(ctx, d, batch(w.SessionID, 1, 7, 9007199254740993))
	if err != nil {
		t.Fatal(err)
	}
	restoreLegacyInventory(t, s)
	// An unexpected external dependency must stop migration rather than be
	// cascaded away; the old schema must remain usable after that failure.
	if _, err = s.Pool().Exec(ctx, `CREATE TABLE migration_dependent (
        network_id text,item_id bigint,FOREIGN KEY(network_id,item_id) REFERENCES inventory(network_id,item_id))`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "INSERT INTO migration_dependent VALUES($1,$2)", n.ID, int64(ack.Bindings[0].ID)); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err == nil {
		t.Fatal("migration removed an unexpected dependent object")
	}
	var quantity protocol.Quantity
	var noPartialSchema bool
	if err = s.Pool().QueryRow(ctx, `SELECT i.quantity,
        to_regclass('inventory_schedule') IS NULL AND to_regclass('inventory_current') IS NULL
        AND NOT EXISTS(SELECT 1 FROM schema_versions WHERE version='migrations/004_inventory_scheduling.sql')
        FROM inventory i JOIN migration_dependent d USING(network_id,item_id) WHERE i.network_id=$1`, n.ID).Scan(&quantity, &noPartialSchema); err != nil || quantity != 9007199254740993 || !noPartialSchema {
		t.Fatal("failed migration partially changed the old database", quantity, err)
	}
	var deadline time.Time
	if err = s.Pool().QueryRow(ctx, "SELECT next_verify_at FROM inventory WHERE network_id=$1", n.ID).Scan(&deadline); err != nil || deadline.IsZero() {
		t.Fatal("failed migration lost legacy scheduling", err)
	}
	if _, err = s.Pool().Exec(ctx, "DROP TABLE migration_dependent"); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("migration could not resume after resolving its dependency", err)
	}
}
