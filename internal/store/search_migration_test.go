package store_test

import (
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"reflect"
	"testing"
)

func TestInventorySearchMigrationPreservesEvidence(t *testing.T) {
	f := plainFixture(t)
	enablePlain(t, f)
	before, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{View: "all"})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := f.s.InventoryStats(ctx, f.admin.ID, f.network.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Pool().Exec(ctx, `ALTER TABLE item_definitions DROP COLUMN search_label;
		ALTER TABLE plain_catalog_candidates DROP COLUMN search_text;
		DELETE FROM schema_versions WHERE version='migrations/016_inventory_search.sql'`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = f.s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	after, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{View: "all"})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("search migration changed inventory", before, after, err)
	}
	afterStats, err := f.s.InventoryStats(ctx, f.admin.ID, f.network.ID)
	if err != nil || !reflect.DeepEqual(stats, afterStats) {
		t.Fatal("search migration reset scan", stats, afterStats, err)
	}
	page, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{View: "all", Search: "@minecraft st"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Label != "石头" {
		t.Fatal("existing labels were not backfilled", page, err)
	}
}

func TestPlainWindowMembershipAndAnchorRemoval(t *testing.T) {
	f := plainFixture(t)
	enablePlain(t, f)
	if _, err := f.s.Pool().Exec(ctx, `INSERT INTO inventory(network_id,item_id,quantity,craftable,craftable_observation,observation,session_id,observed_at)
		SELECT network_id,item_id,1,false,0,1,$2,now() FROM plain_catalog_candidates WHERE network_id=$1
		ON CONFLICT(network_id,item_id) DO UPDATE SET quantity=1,observed_at=now()`, f.network.ID, f.wa.SessionID); err != nil {
		t.Fatal(err)
	}
	q := protocol.ItemQuery{Consistency: "window", View: "stock", Limit: 1}
	first, err := f.s.Items(ctx, f.admin.ID, f.network.ID, q)
	if err != nil {
		t.Fatal(err)
	}
	q.Cursor = first.NextCursor
	second, err := f.s.Items(ctx, f.admin.ID, f.network.ID, q)
	if err != nil {
		t.Fatal(err)
	}
	q.Cursor = second.NextCursor
	third, err := f.s.Items(ctx, f.admin.ID, f.network.ID, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Pool().Exec(ctx, `UPDATE inventory SET quantity=0 WHERE network_id=$1 AND item_id=$2`, f.network.ID, int64(second.Items[0].ID)); err != nil {
		t.Fatal(err)
	}
	q.Cursor = ""
	q.Anchor = second.AnchorCursor
	refresh, err := f.s.Items(ctx, f.admin.ID, f.network.ID, q)
	if err != nil || len(refresh.Items) != 1 || refresh.Items[0].ID != third.Items[0].ID {
		t.Fatal("removed anchor reset window", refresh, third, err)
	}
	q.Anchor = ""
	q.Cursor = first.NextCursor
	next, err := f.s.Items(ctx, f.admin.ID, f.network.ID, q)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != third.Items[0].ID {
		t.Fatal("stock revision broke continuation", next, err)
	}
}
