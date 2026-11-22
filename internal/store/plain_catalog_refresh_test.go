package store_test

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestPlainCatalogRefreshPreservesStockSchedulesAndSavedIntent(t *testing.T) {
	f := plainFixture(t)
	enablePlain(t, f)
	f.wa = safePlainHello(t, f, f.a, "catalog-refresh-primary")
	var ironID protocol.Quantity
	if err := f.s.Pool().QueryRow(ctx, "SELECT d.id FROM item_definitions d JOIN plain_catalog_candidates c ON c.network_id=d.network_id AND c.item_id=d.id WHERE d.network_id=$1 AND d.name='minecraft:iron_ingot'", f.network.ID).Scan(&ironID); err != nil {
		t.Fatal(err)
	}
	f.id = ironID
	// Seed a previously confirmed observation. Ingest authorization is tested separately.
	seeded, err := f.s.Pool().Exec(ctx, `UPDATE inventory SET quantity=42,craftable=true,session_id=$3,
		observation=1,observed_at=now(),craftable_observation=1,craftable_observed_at=now(),craftable_session_id=$3
		WHERE network_id=$1 AND item_id=$2`, f.network.ID, int64(f.id), f.wa.SessionID)
	if err != nil || seeded.RowsAffected() != 1 {
		t.Fatal("failed to confirm existing fixture stock", err)
	}
	job, err := f.s.CreateProductionJob(ctx, f.admin.ID, f.network.ID, jobInput(f.id, "catalog-refresh-job", "catalog-refresh-entry"))
	if err != nil {
		t.Fatal(err)
	}
	var enabledAt, observedAt time.Time
	if err = f.s.Pool().QueryRow(ctx, "SELECT enabled_at FROM network_inventory_profiles WHERE network_id=$1", f.network.ID).Scan(&enabledAt); err != nil {
		t.Fatal(err)
	}
	if err = f.s.Pool().QueryRow(ctx, "SELECT observed_at FROM inventory WHERE network_id=$1 AND item_id=$2", f.network.ID, int64(f.id)).Scan(&observedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Pool().Exec(ctx, "UPDATE inventory_schedule SET next_quantity_at='2099-01-01',next_verify_at='2099-02-01' WHERE network_id=$1", f.network.ID); err != nil {
		t.Fatal(err)
	}
	root := writeCatalogRefreshSource(t)
	// The replacement export omits dye; refreshing must retain its existing identity.
	file, err := os.Create(filepath.Join(root, "records", "items", "part.jsonl.gz"))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	for _, row := range []map[string]any{
		{"id": "iron", "registry": "minecraft:iron_ingot", "meta": 0, "nbt": nil, "name": "text_stone"},
		{"id": "stone", "registry": "minecraft:stone", "meta": 0, "nbt": nil, "name": "text_stone"},
		{"id": "new", "registry": "test:catalog", "meta": 3, "nbt": nil, "name": "text_stone"},
		{"id": "tagged", "registry": "test:tagged", "meta": 0, "nbt": map[string]any{}},
	} {
		data, _ := json.Marshal(row)
		if _, err = gz.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	options := store.DefaultOptions()
	options.CatalogRoot = root
	updated, err := store.Open(ctx, f.s.Pool().Config().ConnString(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(updated.Close)
	f.s = updated
	stats, err := f.s.EnablePlainCatalog(ctx, f.admin.ID, f.network.ID)
	if err != nil || stats.Candidates != "4" || stats.Checked != "1" || stats.Pending != "3" || stats.TotalQuantity != "42" {
		t.Fatal("catalog refresh changed observation progress", stats, err)
	}
	page, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Search: "@minecraft shitou"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != f.id || page.Items[0].Label != "石头" || page.Items[0].Quantity != 42 {
		t.Fatal("new label or pinyin index lost the existing stock identity", page, err)
	}
	var currentEnabledAt, currentObservedAt time.Time
	if err = f.s.Pool().QueryRow(ctx, "SELECT enabled_at FROM network_inventory_profiles WHERE network_id=$1", f.network.ID).Scan(&currentEnabledAt); err != nil {
		t.Fatal(err)
	}
	if err = f.s.Pool().QueryRow(ctx, "SELECT observed_at FROM inventory WHERE network_id=$1 AND item_id=$2", f.network.ID, int64(f.id)).Scan(&currentObservedAt); err != nil {
		t.Fatal(err)
	}
	if !enabledAt.Equal(currentEnabledAt) || !observedAt.Equal(currentObservedAt) {
		t.Fatal("refresh reset profile or observation time")
	}
	var untouched, tagged, pending int
	if err = f.s.Pool().QueryRow(ctx, `SELECT count(*) FROM inventory_schedule WHERE network_id=$1 AND next_quantity_at='2099-01-01' AND next_verify_at='2099-02-01'`, f.network.ID).Scan(&untouched); err != nil || untouched != 3 {
		t.Fatal("refresh rescheduled existing observations", untouched, err)
	}
	if err = f.s.Pool().QueryRow(ctx, "SELECT count(*) FROM plain_catalog_candidates c JOIN item_definitions d ON d.network_id=c.network_id AND d.id=c.item_id WHERE c.network_id=$1 AND d.nbt<>''", f.network.ID).Scan(&tagged); err != nil || tagged != 0 {
		t.Fatal("tagged records entered the plain allowlist", tagged, err)
	}
	if err = f.s.Pool().QueryRow(ctx, "SELECT count(*) FROM production_job_entries WHERE job_id=$1 AND state='pending'", job.Receipt.JobID).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("refresh changed saved production intent", pending, err)
	}
	var revision int64
	if err = f.s.Pool().QueryRow(ctx, "SELECT catalog_revision FROM networks WHERE id=$1", f.network.ID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.EnablePlainCatalog(ctx, f.admin.ID, f.network.ID); err != nil {
		t.Fatal(err)
	}
	var afterRevision int64
	if err = f.s.Pool().QueryRow(ctx, "SELECT catalog_revision FROM networks WHERE id=$1", f.network.ID).Scan(&afterRevision); err != nil || afterRevision != revision {
		t.Fatal("unchanged refresh was not idempotent", afterRevision, revision, err)
	}
}

func writeCatalogRefreshSource(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png"), []byte{1, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	for kind, record := range map[string]string{
		"items":   "",
		"assets":  `{"id":"asset_stone","path":"assets/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png"}`,
		"strings": `{"id":"text_stone","locale":"zh_CN","text":"石头"}`,
	} {
		if err := os.MkdirAll(filepath.Join(root, "records", kind), 0700); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(root, "records", kind, "part.jsonl.gz"))
		if err != nil {
			t.Fatal(err)
		}
		gz := gzip.NewWriter(file)
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
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}
