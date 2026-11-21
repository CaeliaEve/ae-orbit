package store_test

import (
	"encoding/base64"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/nbt"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func TestGroupedPagingCountsAnchorsSearchAndSessionProvenance(t *testing.T) {
	f := plainFixture(t)
	enablePlain(t, f)
	caps := protocol.Capabilities{Inventory: true, Craft: true, MaxCraftQuantity: 10000, ExactQuantities: true, NativeScanDisabled: true, EventsDisabled: true, ExactNBT: true, MaxNBTBytes: 4096, Enumeration: "exact_only"}
	var err error
	f.wa, err = f.s.HelloV2(ctx, f.a, protocol.Hello{Protocol: 2, BootID: "paging-primary", Source: "simulator", MaxResponseBytes: 32768, Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	f.wb = safePlainHello(t, f, f.b, "paging-worker")
	raw, err := nbt.EncodeTypedJSON([]byte(`{"type":"compound","value":{"display":{"type":"compound","value":{"Name":{"type":"string","value":"custom stone"}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.s.RegisterVariantSample(ctx, f.a, f.wa.SessionID, protocol.Definition{Kind: "item", Name: "minecraft:stone", Label: "custom stone", NBT: base64.StdEncoding.EncodeToString(raw), Exact: true})
	if err != nil {
		t.Fatal(err)
	}
	all, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Grouped: true, Kind: "item", View: "all", Limit: 50})
	if err != nil || len(all.Items) != 3 {
		t.Fatal(all, err)
	}
	for _, base := range all.Items {
		if _, err = f.s.Pool().Exec(ctx, `INSERT INTO inventory(network_id,item_id,quantity,session_id,observation,observed_at,craftable,craftable_session_id,craftable_observation) VALUES($1,$2,10,$3,1,now(),true,$4,1) ON CONFLICT(network_id,item_id) DO UPDATE SET quantity=excluded.quantity,session_id=excluded.session_id,observation=excluded.observation,observed_at=excluded.observed_at,craftable=excluded.craftable,craftable_session_id=excluded.craftable_session_id,craftable_observation=excluded.craftable_observation`, f.network.ID, int64(base.ID), f.wb.SessionID, f.wa.SessionID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.s.Pool().Exec(ctx, `INSERT INTO inventory(network_id,item_id,quantity,session_id,observation,observed_at,craftable,craftable_observation) VALUES($1,$2,5,$3,2,now(),false,0) ON CONFLICT(network_id,item_id) DO UPDATE SET quantity=excluded.quantity,session_id=excluded.session_id,observation=excluded.observation,observed_at=excluded.observed_at`, f.network.ID, int64(id), f.wb.SessionID); err != nil {
		t.Fatal(err)
	}
	for _, sort := range []string{"name", "quantity"} {
		q := protocol.ItemQuery{Grouped: true, Kind: "item", View: "stock", Sort: sort, Limit: 1, Pagination: "pages", Consistency: "window", Page: 1}
		first, err := f.s.Items(ctx, f.admin.ID, f.network.ID, q)
		if err != nil || *first.Total != 3 || first.PageCount != 3 || len(first.Items) != 1 {
			t.Fatal(first, err)
		}
		q.LastPage = true
		q.Page = 0
		last, err := f.s.Items(ctx, f.admin.ID, f.network.ID, q)
		if err != nil || last.PageNumber != 3 || len(last.Items) != 1 {
			t.Fatal(last, err)
		}
		q.LastPage = false
		q.Page = 99999
		clamped, err := f.s.Items(ctx, f.admin.ID, f.network.ID, q)
		if err != nil || clamped.Items[0].ID != last.Items[0].ID {
			t.Fatal(clamped, err)
		}
		q.Page = 0
		q.Anchor = last.AnchorCursor
		anchored, err := f.s.Items(ctx, f.admin.ID, f.network.ID, q)
		if err != nil || anchored.Items[0].ID != last.Items[0].ID {
			t.Fatal(anchored, err)
		}
		q.Anchor = ""
		q.Page = 1
		q.Limit = 3
		q.PageSize = 1
		block, err := f.s.Items(ctx, f.admin.ID, f.network.ID, q)
		if err != nil || len(block.Items) != 3 || len(block.PageAnchors) != 3 || block.PageSize != 1 {
			t.Fatal(block, err)
		}
		q.Page = 0
		q.PageSize = 0
		q.Limit = 1
		q.Anchor = block.PageAnchors[1]
		middle, err := f.s.Items(ctx, f.admin.ID, f.network.ID, q)
		if err != nil || middle.PageNumber != 2 || middle.Items[0].ID != block.Items[1].ID {
			t.Fatal(middle, err)
		}
	}
	group, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Grouped: true, Kind: "item", View: "stock", Search: "custom stone", Limit: 50})
	if err != nil || len(group.Items) != 1 || group.Items[0].Group.Candidates != 2 || group.Items[0].Group.Stock != 2 || group.Items[0].Group.Quantity != "15" || !group.Items[0].CurrentSession || !group.Items[0].CraftableCurrentSession {
		t.Fatal("sibling quantities/provenance", group, err)
	}
	empty, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Grouped: true, Kind: "item", View: "stock", Search: "does-not-exist", Limit: 1, LastPage: true})
	if err != nil || *empty.Total != 0 || empty.PageCount != 1 || empty.PageNumber != 1 || len(empty.Items) != 0 {
		t.Fatal(empty, err)
	}
	mod, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Grouped: true, Kind: "item", View: "stock", Search: "@minecraft", Limit: 50})
	if err != nil || len(mod.Items) != 3 {
		t.Fatal(mod, err)
	}
	if _, err = f.s.Pool().Exec(ctx, `UPDATE inventory SET quantity=9223372036854775807 WHERE network_id=$1 AND item_id IN(SELECT id FROM item_definitions WHERE network_id=$1 AND name='minecraft:stone')`, f.network.ID); err != nil {
		t.Fatal(err)
	}
	group, err = f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Grouped: true, Kind: "item", View: "stock", Search: "custom stone", Limit: 50, Sort: "quantity"})
	if err != nil || group.Items[0].Group.Quantity != "18446744073709551614" {
		t.Fatal("group sum lost exact numeric precision", group, err)
	}
	// Retiring a quantity source leaves quantities visible but provenance stale.
	f.wb = safePlainHello(t, f, f.b, "paging-worker-restarted")
	group, err = f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Grouped: true, Kind: "item", View: "stock", Search: "custom stone", Limit: 50})
	if err != nil || group.Items[0].CurrentSession || !group.Items[0].CraftableCurrentSession {
		t.Fatal("stale quantity/source authority", group, err)
	}
}
