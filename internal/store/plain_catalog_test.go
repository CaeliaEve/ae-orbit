package store_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func plainFixture(t *testing.T) cooperativeFixture {
	return plainFixtureCount(t, 3)
}

func plainFixtureCount(t *testing.T, count int) cooperativeFixture {
	t.Helper()
	f := newCooperativeFixture(t)
	return openPlainCatalogFixture(t, f, writePlainCatalogFixture(t, count))
}

func writePlainCatalogFixture(t *testing.T, count int) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png"), []byte{1, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "records", "items"), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(root, "records", "items", "part.jsonl.gz"))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	rows := []map[string]any{
		{"id": "iron", "registry": "minecraft:iron_ingot", "meta": 0, "nbt": nil},
		{"id": "stone", "registry": "minecraft:stone", "meta": 0, "nbt": nil, "name": "text_stone"},
		{"id": "dye", "registry": "minecraft:dye", "meta": 4, "nbt": nil},
		{"id": "tagged", "registry": "minecraft:stone", "meta": 0, "nbt": map[string]any{}},
	}
	for i := 3; i < count; i++ {
		rows = append(rows, map[string]any{"id": fmt.Sprintf("bulk-%d", i), "registry": "test:catalog", "meta": i, "nbt": nil})
	}
	for _, row := range rows {
		b, _ := json.Marshal(row)
		if _, err = gz.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	for kind, record := range map[string]string{
		"assets":  `{"id":"asset_stone","path":"assets/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png"}`,
		"strings": `{"id":"text_stone","locale":"zh_CN","text":"石头"}`,
	} {
		if err = os.MkdirAll(filepath.Join(root, "records", kind), 0700); err != nil {
			t.Fatal(err)
		}
		file, err = os.Create(filepath.Join(root, "records", kind, "part.jsonl.gz"))
		if err != nil {
			t.Fatal(err)
		}
		gz = gzip.NewWriter(file)
		if _, err = gz.Write([]byte(record + "\n")); err != nil {
			t.Fatal(err)
		}
		if err = gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{"format":"elysium.source","revision":41,"scope":{"mode":"complete","collections":["items","assets","strings"]},"files":[{"path":"records/items/part.jsonl.gz","kind":"items","encoding":"jsonl.gzip"},{"path":"records/assets/part.jsonl.gz","kind":"assets","encoding":"jsonl.gzip"},{"path":"records/strings/part.jsonl.gz","kind":"strings","encoding":"jsonl.gzip"}]}`
	if err = os.WriteFile(filepath.Join(root, "manifest.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func openPlainCatalogFixture(t *testing.T, f cooperativeFixture, root string) cooperativeFixture {
	t.Helper()
	options := store.DefaultOptions()
	options.CatalogRoot = root
	s, err := store.Open(ctx, f.s.Pool().Config().ConnString(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	f.s = s
	return f
}

func enablePlain(t *testing.T, f cooperativeFixture) {
	t.Helper()
	stats, err := f.s.EnablePlainCatalog(ctx, f.admin.ID, f.network.ID)
	if err != nil || stats.Candidates != "3" || stats.Checked != "0" || stats.Pending != "3" || stats.Entries != "0" {
		t.Fatal(stats, err)
	}
}

func safePlainHello(t *testing.T, f cooperativeFixture, d store.Device, boot string) protocol.Welcome {
	t.Helper()
	caps := protocol.Capabilities{Inventory: true, ExactQuantities: true, QuantityRefresh: true, NativeScanDisabled: true, EventsDisabled: true}
	if d.Role == "primary" {
		caps.Craft = true
		caps.MaxCraftQuantity = 10000
		caps.Enumeration = "exact_only"
	}
	w, err := f.s.HelloV2(ctx, d, protocol.Hello{Protocol: 2, BootID: boot, Source: "simulator", MaxResponseBytes: 32768, Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestPlainCatalogCandidatesDoNotFabricateStock(t *testing.T) {
	f := plainFixture(t)
	extra := protocol.Definition{Kind: "item", Name: "minecraft:diamond", Label: "Diamond", Exact: true}
	tagged := nbtDefinition(t, "ordered")
	if _, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2, Updates: []protocol.Update{{Item: &extra, Quantity: 500, Observation: 2}, {Item: &tagged, Quantity: 999, Observation: 3}}}); err != nil {
		t.Fatal(err)
	}
	enablePlain(t, f)
	page, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{})
	if err != nil || page.Scope != "nesql_plain" || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
	all, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{View: "all"})
	if err != nil || len(all.Items) != 3 {
		t.Fatal(all, err)
	}
	for _, i := range all.Items {
		if i.QuantityKnown == nil || *i.QuantityKnown || i.NBT != "" || i.Name == extra.Name || i.Name == tagged.Name {
			t.Fatal("old or excluded stock was exposed", i)
		}
	}
	var inventoryRows int
	if err = f.s.Pool().QueryRow(ctx, "SELECT count(*) FROM inventory WHERE network_id=$1", f.network.ID).Scan(&inventoryRows); err != nil || inventoryRows != 3 {
		t.Fatal("metadata seeding mutated observations", inventoryRows, err)
	}
	stats, err := f.s.EnablePlainCatalog(ctx, f.admin.ID, f.network.ID)
	if err != nil || stats.Candidates != "3" || stats.Checked != "0" {
		t.Fatal("enable is not idempotent", stats, err)
	}
	lookup, err := f.s.ItemsByID(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{f.id})
	if err != nil || len(lookup.Items) != 1 || lookup.Items[0].QuantityKnown == nil || *lookup.Items[0].QuantityKnown {
		t.Fatal(lookup, err)
	}
}

func TestPlainCatalogRequiresSafeDevicesAndAuthenticatedFirstObservation(t *testing.T) {
	f := plainFixture(t)
	enablePlain(t, f)
	slots, bytes := 16, 16384
	f.round(t)
	ack, err := f.s.PollV2(ctx, f.b, protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes})
	if err != nil || len(ack.Reads) != 0 || ack.ScanBudget != 0 {
		t.Fatal("old worker received a grant", ack, err)
	}
	if _, err = f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2, Updates: []protocol.Update{{ID: f.id, Quantity: 999, Observation: 2}}}); !errors.Is(err, store.ErrClientUpgradeRequired) {
		t.Fatal(err)
	}
	f.wb = safePlainHello(t, f, f.b, "safe-worker")
	var reads []protocol.ReadAssignment
	for seq := protocol.Quantity(1); seq <= 4; seq++ {
		f.round(t)
		ack, err = f.s.PollV2(ctx, f.b, protocol.Poll{SessionID: f.wb.SessionID, Sequence: seq, ReadSlots: &slots, ReadBytes: &bytes})
		if err != nil {
			t.Fatal(err)
		}
		reads = append(reads, ack.Reads...)
	}
	if len(reads) != 3 {
		t.Fatal("candidate dispatch failed", reads)
	}
	updates := []protocol.Update{}
	for index, read := range reads {
		if read.Kind != "quantity" || read.Item.NBT != "" {
			t.Fatal("unsafe candidate dispatched", read)
		}
		q := protocol.Quantity(0)
		if read.Item.Name == "minecraft:stone" {
			q = 42
		}
		updates = append(updates, protocol.Update{ID: read.ID, Quantity: q, Observation: protocol.Quantity(index + 1), AssignmentID: read.AssignmentID})
	}
	if _, err = f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: 1, Updates: updates}); err != nil {
		t.Fatal(err)
	}
	stats, err := f.s.InventoryStats(ctx, f.admin.ID, f.network.ID)
	if err != nil || stats.Entries != "1" || stats.TotalQuantity != "42" || stats.Checked != "3" || stats.Zero != "2" || stats.Pending != "0" {
		t.Fatal(stats, err)
	}
	page, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Name != "minecraft:stone" || page.Items[0].QuantityKnown == nil || !*page.Items[0].QuantityKnown {
		t.Fatal(page, err)
	}
	if page.Items[0].CraftableObservedAt != nil {
		t.Fatal("quantity read invented recipe evidence")
	}
	chinese, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Search: "石"})
	if err != nil || len(chinese.Items) != 1 || chinese.Items[0].Label != "石头" {
		t.Fatal("catalog Chinese search failed", chinese, err)
	}
	for _, query := range []string{"shitou", "st", "@minecraft shitou", "@minecraft st"} {
		page, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Search: query})
		if err != nil || len(page.Items) != 1 || page.Items[0].Label != "石头" {
			t.Fatal("plain pinyin search", query, page, err)
		}
	}
}

func TestPlainCatalogPendingRefreshAndCursorIsolation(t *testing.T) {
	f := plainFixture(t)
	enablePlain(t, f)
	f.wb = safePlainHello(t, f, f.b, "refresh-worker")
	page, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{View: "all", Limit: 1})
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	if _, err = f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{View: "stock", Limit: 1, Cursor: page.NextCursor}); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("view cursor was reused", err)
	}
	lookup, err := f.s.RequestItemRefresh(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{page.Items[0].ID})
	if err != nil || len(lookup.Refreshes) != 1 || lookup.Items[0].QuantityKnown == nil || *lookup.Items[0].QuantityKnown {
		t.Fatal("pending candidate could not be refreshed", lookup, err)
	}
}

func TestPlainCatalogPreservesExistingPendingProductionIntent(t *testing.T) {
	f := plainFixture(t)
	extra := protocol.Definition{Kind: "item", Name: "minecraft:diamond", Label: "Diamond", Exact: true}
	craftable := true
	seed, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2, Updates: []protocol.Update{{Item: &extra, Quantity: 500, Observation: 2, Craftable: &craftable}}})
	if err != nil {
		t.Fatal(err)
	}
	input := jobInput(f.id, "plain-job-001", "plain-entry-001")
	input.Entries = append(input.Entries, protocol.ProductionJobEntryInput{EntryKey: "excluded-entry-001", ItemID: seed.Bindings[0].ID, Quantity: 1})
	accepted, err := f.s.CreateProductionJob(ctx, f.admin.ID, f.network.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	enablePlain(t, f)
	f.wa = safePlainHello(t, f, f.a, "safe-job-primary")
	if _, err = f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err = f.s.Pool().QueryRow(ctx, "SELECT count(*) FROM production_job_entries WHERE job_id=$1 AND state='pending'", accepted.Receipt.JobID).Scan(&pending); err != nil || pending != 2 {
		t.Fatal("profile discarded saved production intent", pending, err)
	}
}

func TestPlainCatalogFullExportFitsRequestBudget(t *testing.T) {
	f := plainFixtureCount(t, 53689)
	// The profile activation route has a one-minute request budget. Using the
	// ordinary 15-second budget here rejects valid imports under race overhead.
	budget, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	started := time.Now()
	stats, err := f.s.EnablePlainCatalog(budget, f.admin.ID, f.network.ID)
	t.Logf("full export activation took %s", time.Since(started))
	if err != nil || stats.Candidates != "53689" || stats.Checked != "0" || stats.Pending != "53689" {
		t.Fatal("full export could not activate within the request budget", stats, err)
	}
	var observed int
	if err = f.s.Pool().QueryRow(ctx, "SELECT count(*) FROM inventory WHERE network_id=$1", f.network.ID).Scan(&observed); err != nil || observed != 1 {
		t.Fatal("bulk registration fabricated stock", observed, err)
	}
	// Stock near the end of the schedule must not make ORDER BY/LIMIT scan
	// the entire 53k queue against every eligible positive identity.
	f.wb = safePlainHello(t, f, f.b, "late-stock-worker")
	if _, err := f.s.Pool().Exec(ctx, `UPDATE inventory_schedule SET next_quantity_at=now()-interval '1 hour'
		WHERE network_id=$1 AND item_id IN(SELECT item_id FROM plain_catalog_candidates WHERE network_id=$1 ORDER BY item_id DESC LIMIT 16)`, f.network.ID); err != nil {
		t.Fatal(err)
	}
	seedPlainStock(t, f, 16)
	f.wa = safePlainHello(t, f, f.a, "late-stock-primary")
	slots, bytes := 16, 16384
	if _, err := f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes}); err != nil {
		t.Fatal(err)
	}
	f.round(t)
	pollBudget, stop := context.WithTimeout(ctx, 500*time.Millisecond)
	defer stop()
	started = time.Now()
	ack, err := f.s.PollV2(pollBudget, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: 2, ReadSlots: &slots, ReadBytes: &bytes})
	t.Logf("large late-stock dispatch took %s", time.Since(started))
	quantity, recipe := 0, 0
	for _, read := range ack.Reads {
		if read.Kind == "quantity_recipe" {
			recipe++
		} else {
			quantity++
		}
	}
	if err != nil || recipe != 1 || quantity == 0 {
		t.Fatal("late-stock recipe selection stalled the bounded quantity poll", ack, err)
	}
}

// Deployment tests can mount an immutable NESQL export. Ordinary CI uses a
// self-contained Source fixture so activation and dispatch are never skipped.
func TestPlainCatalogSourceExportFitsRequestBudget(t *testing.T) {
	root := os.Getenv("AE_TEST_PLAIN_CATALOG_ROOT")
	if root == "" {
		root = writePlainCatalogFixture(t, 128)
		t.Log("using generated NESQL Source fixture; no external export configured")
	}
	f := openPlainCatalogFixture(t, newCooperativeFixture(t), root)
	s := f.s
	want := fmt.Sprint(len(s.Catalog().PlainCandidates()))
	budget, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	started := time.Now()
	stats, err := s.EnablePlainCatalog(budget, f.admin.ID, f.network.ID)
	t.Logf("source export activation (%s candidates) took %s", want, time.Since(started))
	if err != nil || stats.Candidates != want || stats.Pending != want || stats.Checked != "0" {
		t.Fatal("source export could not activate within the request budget", stats, err)
	}
	f.wa = safePlainHello(t, f, f.a, "source-export-primary")
	slots, bytes := 16, 16384
	if _, err := s.PollV2(ctx, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes}); err != nil {
		t.Fatal(err)
	}
	f.round(t)
	pollBudget, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	started = time.Now()
	ack, err := s.PollV2(pollBudget, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: 2, ReadSlots: &slots, ReadBytes: &bytes})
	t.Logf("full source dispatch took %s and leased %d reads", time.Since(started), len(ack.Reads))
	if err != nil || len(ack.Reads) == 0 {
		t.Fatal("full source could not dispatch within the poll budget", ack, err)
	}
}
