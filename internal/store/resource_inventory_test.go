package store_test

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func resourceFixture(t *testing.T) cooperativeFixture {
	t.Helper()
	root := writePlainCatalogFixture(t, 3)
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err = json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	files := manifest["files"].([]any)
	for kind, records := range map[string]string{
		"fluids":  "{\"id\":\"water\",\"registry\":\"water\",\"name\":\"text_water\",\"nbt\":null,\"icon\":\"asset_stone\"}\n{\"id\":\"lava\",\"registry\":\"lava\",\"nbt\":null}\n",
		"aspects": "{\"id\":\"air\",\"source\":{\"key\":\"aer\",\"owner\":\"Thaumcraft\"},\"name\":\"text_air\",\"icon\":\"asset_stone\"}\n",
		"strings": "{\"id\":\"text_water\",\"locale\":\"zh_CN\",\"text\":\"水\"}\n{\"id\":\"text_air\",\"locale\":\"zh_CN\",\"text\":\"风\"}\n",
	} {
		path := "records/" + kind + "/resources.jsonl.gz"
		if err = os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		gz := gzip.NewWriter(f)
		if _, err = gz.Write([]byte(records)); err != nil {
			t.Fatal(err)
		}
		if err = gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
		files = append(files, map[string]any{"path": path, "kind": kind, "encoding": "jsonl.gzip"})
	}
	manifest["files"] = files
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	f := openPlainCatalogFixture(t, newCooperativeFixture(t), root)
	enablePlain(t, f)
	if err = f.s.EnableResourceCatalog(ctx, f.admin.ID, f.network.ID); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestResourceInventoryRequiresCapabilityAndPreservesItems(t *testing.T) {
	f := resourceFixture(t)
	var profileAt time.Time
	if err := f.s.Pool().QueryRow(ctx, "SELECT enabled_at FROM network_inventory_profiles WHERE network_id=$1", f.network.ID).Scan(&profileAt); err != nil {
		t.Fatal(err)
	}
	all, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Kind: "fluid", View: "all", Limit: 1})
	if err != nil || len(all.Items) != 1 || all.NextCursor == "" || *all.Items[0].QuantityKnown {
		t.Fatal(all, err)
	}
	if _, err = f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Kind: "essentia", View: "all", Limit: 1, Cursor: all.NextCursor}); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("cross-type cursor accepted", err)
	}
	stock, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Kind: "fluid"})
	if err != nil || len(stock.Items) != 0 {
		t.Fatal(stock, err)
	}
	if _, err = f.s.RequestItemRefresh(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{all.Items[0].ID}); !errors.Is(err, store.ErrCapability) {
		t.Fatal("unsupported refresh admitted", err)
	}
	f.wb = safePlainHello(t, f, f.b, "old-resource-worker")
	slots, bytes := 16, 16384
	f.round(t)
	ack, err := f.s.PollV2(ctx, f.b, protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range ack.Reads {
		if r.Item.Kind != "item" {
			t.Fatal("old OC received resource", r)
		}
	}
	caps := protocol.Capabilities{Inventory: true, ExactQuantities: true, NativeScanDisabled: true, EventsDisabled: true, QuantityRefresh: true, Fluids: true, Essentia: true}
	f.wb, err = f.s.HelloV2(ctx, f.b, protocol.Hello{Protocol: 2, BootID: "resource-worker", Source: "simulator", MaxResponseBytes: 32768, Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	sequence := protocol.Quantity(0)
	for poll := protocol.Quantity(1); poll <= 8 && len(seen) < 2; poll++ {
		f.round(t)
		ack, err = f.s.PollV2(ctx, f.b, protocol.Poll{SessionID: f.wb.SessionID, Sequence: poll, ReadSlots: &slots, ReadBytes: &bytes})
		if err != nil {
			t.Fatal(err)
		}
		if len(ack.Reads) > 16 {
			t.Fatal("slot bound exceeded")
		}
		updates := []protocol.Update{}
		for _, r := range ack.Reads {
			if r.Item.Kind == "item" {
				continue
			}
			if r.Kind != "quantity" || !r.Item.Exact || r.Item.NBT != "" {
				t.Fatal("unsafe resource read", r)
			}
			seen[r.Item.Kind] = true
			updates = append(updates, protocol.Update{ID: r.ID, Quantity: 2500, Observation: protocol.Quantity(len(updates)) + poll*10, AssignmentID: r.AssignmentID})
		}
		if len(updates) > 0 {
			sequence++
			if _, err = f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: sequence, Updates: updates}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(seen) != 2 {
		t.Fatal("resources starved", seen)
	}
	stats, err := f.s.InventoryStats(ctx, f.admin.ID, f.network.ID)
	if err != nil || stats.Candidates != "3" || stats.TotalQuantity != "0" || len(stats.Resources) != 2 {
		t.Fatal(stats, err)
	}
	for _, r := range stats.Resources {
		if !r.Supported || r.Checked == "0" || r.TotalQuantity == "0" {
			t.Fatal(r)
		}
	}
	stock, err = f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Kind: "essentia"})
	if err != nil || len(stock.Items) != 1 || stock.Items[0].Name != "aer" || stock.Items[0].Quantity != 2500 {
		t.Fatal(stock, err)
	}
	if _, err = f.s.RequestItemRefresh(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{stock.Items[0].ID}); err != nil {
		t.Fatal(err)
	}
	// Re-enabling must not reset quantities, item epoch or scheduler progress.
	var nextBefore, nextAfter time.Time
	if err = f.s.Pool().QueryRow(ctx, "SELECT next_quantity_at FROM inventory_schedule WHERE network_id=$1 AND item_id=$2", f.network.ID, int64(stock.Items[0].ID)).Scan(&nextBefore); err != nil {
		t.Fatal(err)
	}
	if err = f.s.EnableResourceCatalog(ctx, f.admin.ID, f.network.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.s.Pool().QueryRow(ctx, "SELECT next_quantity_at FROM inventory_schedule WHERE network_id=$1 AND item_id=$2", f.network.ID, int64(stock.Items[0].ID)).Scan(&nextAfter); err != nil || !nextAfter.Equal(nextBefore) {
		t.Fatal("schedule reset", err)
	}
	var after time.Time
	if err = f.s.Pool().QueryRow(ctx, "SELECT enabled_at FROM network_inventory_profiles WHERE network_id=$1", f.network.ID).Scan(&after); err != nil || !after.Equal(profileAt) {
		t.Fatal("item epoch reset", err)
	}
}
