package store_test

import (
	"encoding/base64"
	"github.com/CaeliaEve/ae-orbit/internal/nbt"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"os"
	"testing"
	"time"
)

func TestVariantSampleGroupAndExactCapability(t *testing.T) {
	f := plainFixture(t)
	enablePlain(t, f)
	f.wa = safePlainHello(t, f, f.a, "variant-old")
	raw, err := nbt.EncodeTypedJSON([]byte(`{"type":"compound","value":{"display":{"type":"compound","value":{"Name":{"type":"string","value":"custom stone"}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	d := protocol.Definition{Kind: "item", Name: "minecraft:stone", Label: "custom stone", NBT: base64.StdEncoding.EncodeToString(raw), Exact: true}
	if _, err = f.s.RegisterVariantSample(ctx, f.a, f.wa.SessionID, d); err == nil {
		t.Fatal("old OC registered variant")
	}
	caps := protocol.Capabilities{Inventory: true, Craft: true, MaxCraftQuantity: 10000, ExactQuantities: true, NativeScanDisabled: true, EventsDisabled: true, ExactNBT: true, MaxNBTBytes: 4096, Enumeration: "exact_only"}
	f.wa, err = f.s.HelloV2(ctx, f.a, protocol.Hello{Protocol: 2, BootID: "variant-new", Source: "simulator", MaxResponseBytes: 32768, Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.s.RegisterVariantSample(ctx, f.a, f.wa.SessionID, d)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.s.RegisterVariantSample(ctx, f.a, f.wa.SessionID, d)
	if err != nil || replay != id {
		t.Fatal(replay, err)
	}
	lookup, err := f.s.ItemsByID(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{id})
	if err != nil || len(lookup.Items) != 1 || *lookup.Items[0].QuantityKnown {
		t.Fatal("sample invented AE stock", lookup, err)
	}
	f.wb = safePlainHello(t, f, f.b, "variant-old-worker")
	var workerSeq, primarySeq protocol.Quantity
	worker := pollPlainReads(t, f, f.b, f.wb, &workerSeq, 16, 16384)
	for _, read := range worker.Reads {
		if read.ID == id {
			t.Fatal("old worker received NBT")
		}
	}
	primary := pollPlainReads(t, f, f.a, f.wa, &primarySeq, 16, 16384)
	var assigned protocol.ReadAssignment
	updates := []protocol.Update{}
	for _, read := range primary.Reads {
		if read.ID == id {
			assigned = read
		}
		updates = append(updates, protocol.Update{ID: read.ID, Quantity: 5, Observation: protocol.Quantity(len(updates) + 1), AssignmentID: read.AssignmentID})
	}
	if assigned.ID != id || assigned.Item.NBT == "" {
		t.Fatal("missing exact NBT task", primary)
	}
	if _, err = f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 1, Updates: updates}); err != nil {
		t.Fatal(err)
	}
	grouped, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Kind: "item", Grouped: true, View: "stock", Limit: 1, Pagination: "pages", Page: 1})
	if err != nil || len(grouped.Items) != 1 || grouped.Items[0].Group == nil {
		t.Fatal(grouped, err)
	}
	all, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Kind: "item", Grouped: true, View: "all", Search: "custom stone", Limit: 50})
	if err != nil || len(all.Items) != 1 || all.Items[0].Group.Candidates != 2 || all.Items[0].Group.Quantity != "5" {
		t.Fatal("variants not grouped", all, err)
	}
	variants, err := f.s.ItemVariants(ctx, f.admin.ID, f.network.ID, all.Items[0].ID, 0, true)
	if err != nil || len(variants.Items) != 2 {
		t.Fatal(variants, err)
	}
	anchored, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Kind: "item", Grouped: true, View: "all", Search: "custom stone", Limit: 50, Anchor: all.AnchorCursor})
	if err != nil || len(anchored.Items) != 1 {
		t.Fatal("group anchor failed", anchored, err)
	}
}

func TestVariantCatalogImportPreservesPlainProgress(t *testing.T) {
	root := os.Getenv("AE_TEST_PLAIN_CATALOG_ROOT")
	wantPlain, wantVariants, wantPage := "53746", "16988", 50
	if root == "" {
		root = writePlainCatalogFixture(t, 3)
		wantPlain, wantVariants, wantPage = "3", "1", 3
	}
	f := newCooperativeFixture(t)
	f = openPlainCatalogFixture(t, f, root)
	plain, err := f.s.EnablePlainCatalog(ctx, f.admin.ID, f.network.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Candidates != wantPlain {
		t.Fatal(plain)
	}
	var enabled time.Time
	if err = f.s.Pool().QueryRow(ctx, "SELECT enabled_at FROM network_inventory_profiles WHERE network_id=$1", f.network.ID).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	stats, err := f.s.EnableVariantCatalog(ctx, f.admin.ID, f.network.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Candidates != plain.Candidates || stats.Checked != plain.Checked || stats.Variants == nil || stats.Variants.Candidates != wantVariants || stats.Variants.Checked != "0" {
		t.Fatal(stats)
	}
	var after time.Time
	if err = f.s.Pool().QueryRow(ctx, "SELECT enabled_at FROM network_inventory_profiles WHERE network_id=$1", f.network.ID).Scan(&after); err != nil || !enabled.Equal(after) {
		t.Fatal("profile epoch changed", after, err)
	}
	page, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Kind: "item", View: "all", Grouped: true, Limit: 50, Page: 1})
	if err != nil || len(page.Items) != wantPage {
		t.Fatal(page, err)
	}
	t.Logf("Imported %s variants and read grouped catalog in %s", wantVariants, time.Since(started))
	// Supported quantities are never fabricated by metadata import.
	stock, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Kind: "item", View: "stock", Grouped: true, Limit: 50})
	if err != nil || len(stock.Items) != 0 {
		t.Fatal(stock, err)
	}
}
