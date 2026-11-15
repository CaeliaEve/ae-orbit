package catalog

import (
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/nbt"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func writeCatalog(t *testing.T, root string, items []Item, image map[string][]byte) {
	t.Helper()
	manifest, err := json.Marshal(Manifest{Version: manifestVersion, Items: items})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(root, "image.zip"))
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, data := range image {
		writer, createErr := archive.Create(name)
		if createErr != nil {
			file.Close()
			t.Fatal(createErr)
		}
		if _, createErr = writer.Write(data); createErr != nil {
			file.Close()
			t.Fatal(createErr)
		}
	}
	if err = archive.Close(); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogLookupRequiresUnambiguousEmptyNBT(t *testing.T) {
	root := t.TempDir()
	writeCatalog(t, root, []Item{
		{Name: "minecraft:stone", Damage: 0, LocalizedName: "石头", ImagePath: "item/minecraft/stone~0.png"},
		{Name: "gregtech:gt.metaitem.01", Damage: 32000, NBT: "{energy:1}", ImagePath: "item/gregtech/gt.metaitem.01~32000~tag.png"},
	}, map[string][]byte{
		"item/minecraft/stone~0.png":                 {1, 2, 3},
		"item/gregtech/gt.metaitem.01~32000~tag.png": {4, 5, 6},
	})
	catalog, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	item, state, ok := catalog.Lookup("minecraft:stone", 0, "")
	if !ok || state != "available" || item.LocalizedName != "石头" {
		t.Fatalf("lookup = %#v, %s, %v", item, state, ok)
	}
	if _, state, ok = catalog.Lookup("gregtech:gt.metaitem.01", 32000, ""); ok || state != "unmatched" {
		t.Fatalf("NBT variant was guessed: %s, %v", state, ok)
	}
	if _, state, ok = catalog.Lookup("minecraft:stone", 0, "opaque"); ok || state != "unmatched" {
		t.Fatalf("NBT item was guessed: %s, %v", state, ok)
	}
}

func TestOpenNESQLPPRejectsLegacyCatalog(t *testing.T) {
	root := t.TempDir()
	writeCatalog(t, root, []Item{{Name: "minecraft:stone", Damage: 0}}, nil)
	if _, err := OpenNESQLPP(root); !errors.Is(err, ErrIncompleteSource) {
		t.Fatalf("legacy catalog error = %v", err)
	}
}

func TestCatalogImageIsBoundToManifestPath(t *testing.T) {
	root := t.TempDir()
	writeCatalog(t, root, []Item{{Name: "minecraft:stone", Damage: 0, ImagePath: "item/minecraft/stone~0.png"}}, map[string][]byte{
		"item/minecraft/stone~0.png":        {137, 80, 78, 71},
		"item/minecraft/not-registered.png": {1, 2, 3},
	})
	catalog, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	data, digest, err := catalog.Image(Item{ImagePath: "item/minecraft/stone~0.png"})
	if err != nil || len(data) != 4 || len(digest) != 64 {
		t.Fatalf("image = %x, %s, %v", data, digest, err)
	}
	if _, _, err = catalog.Image(Item{ImagePath: "../outside.png"}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("unsafe image path error = %v", err)
	}
	if _, _, err = catalog.Image(Item{ImagePath: "item/minecraft/not-registered.png"}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("unregistered image path error = %v", err)
	}
}

func TestCatalogReportsMissingArchiveMembers(t *testing.T) {
	root := t.TempDir()
	writeCatalog(t, root, []Item{{Name: "minecraft:stone", Damage: 0, ImagePath: "item/minecraft/missing.png"}}, nil)
	catalog, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	item, state, ok := catalog.Lookup("minecraft:stone", 0, "")
	if !ok || state != "missing" || item.ImagePath == "" {
		t.Fatalf("missing archive member = %#v, %s, %v", item, state, ok)
	}
}

func TestImageURLPathUsesExactQuantity(t *testing.T) {
	if got := ImageURLPath("net/one", protocol.Quantity(9007199254740993)); got != "/api/v1/networks/net%2Fone/catalog/items/9007199254740993/image" {
		t.Fatalf("image URL = %s", got)
	}
}

func writeJSONLSource(t *testing.T, root, kind string, rows ...map[string]any) {
	t.Helper()
	records := filepath.Join(root, "records", kind)
	if err := os.MkdirAll(records, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(records, "part-000000.jsonl.gz"))
	if err != nil {
		t.Fatal(err)
	}
	writer := gzip.NewWriter(file)
	for _, row := range rows {
		data, marshalErr := json.Marshal(row)
		if marshalErr != nil {
			file.Close()
			t.Fatal(marshalErr)
		}
		if _, err = writer.Write(append(data, '\n')); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeElysiumSource(t *testing.T, root string, rows map[string][]map[string]any, assets map[string][]byte) {
	t.Helper()
	for kind, values := range rows {
		writeJSONLSource(t, root, kind, values...)
	}
	for path, data := range assets {
		target := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	files := make([]map[string]any, 0, 3)
	for _, kind := range []string{"assets", "items", "strings"} {
		if len(rows[kind]) == 0 {
			continue
		}
		files = append(files, map[string]any{
			"path": "records/" + kind + "/part-000000.jsonl.gz", "kind": kind, "encoding": "jsonl.gzip",
		})
	}
	manifest := map[string]any{
		"format":   "elysium.source",
		"revision": 41,
		"scope":    map[string]any{"mode": "complete", "collections": []string{"assets", "items", "strings"}},
		"files":    files,
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestElysiumSourceLookupResolvesNativeAssetAndStrings(t *testing.T) {
	root := t.TempDir()
	assetPath := "assets/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png"
	itemID, err := nbt.NESQLItemID("minecraft:stone", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeElysiumSource(t, root, map[string][]map[string]any{
		"assets": {{
			"id":     "asset_stone",
			"path":   assetPath,
			"width":  32,
			"height": 16,
			"frames": []map[string]any{{
				"x": 0, "y": 0, "width": 16, "height": 16, "ticks": 2,
			}, {
				"x": 16, "y": 0, "width": 16, "height": 16, "ticks": 3,
			}},
			"interpolate": true,
		}},
		"strings": {{
			"id":     "text_stone",
			"locale": "en_US",
			"text":   "Stone",
		}, {
			"id":     "text_tip",
			"locale": "en_US",
			"text":   "A native item",
		}},
		"items": {{
			"id":         itemID,
			"registry":   "minecraft:stone",
			"meta":       0,
			"nbt":        nil,
			"name":       "text_stone",
			"tooltip":    []string{"text_tip"},
			"stackLimit": 64,
			"durability": 0,
			"icon":       "asset_stone",
		}},
	}, map[string][]byte{assetPath: {137, 80, 78, 71}})

	catalog, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	item, state, ok := catalog.Lookup("minecraft:stone", 0, "")
	if !ok || state != "available" {
		t.Fatalf("lookup = %#v, %s, %v", item, state, ok)
	}
	if item.LocalizedName != "Stone" || len(item.Tooltips) != 1 || item.Tooltips[0] != "A native item" {
		t.Fatalf("localized metadata = %#v", item)
	}
	if item.ImagePath != assetPath {
		t.Fatalf("asset path = %s", item.ImagePath)
	}
	if item.ImageWidth != 32 || item.ImageHeight != 16 || len(item.Frames) != 2 || item.Frames[1].Ticks != 3 || !item.Interpolate {
		t.Fatalf("asset animation metadata = %#v %dx%d interpolate=%v", item.Frames, item.ImageWidth, item.ImageHeight, item.Interpolate)
	}
	public := item.Public("available")
	if public.Width != 32 || public.Height != 16 || len(public.Frames) != 2 || !public.Interpolate {
		t.Fatalf("public asset animation metadata = %#v interpolate=%v", public.Frames, public.Interpolate)
	}
	data, _, err := catalog.Image(item)
	if err != nil || string(data) != string([]byte{137, 80, 78, 71}) {
		t.Fatalf("image = %x, %v", data, err)
	}
}

func TestElysiumSourceLookupUsesExactNBTIdentity(t *testing.T) {
	root := t.TempDir()
	raw := mustCatalogNBT(t, "ordered")
	itemID, err := nbt.NESQLItemID("test:machine", 54, raw)
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "assets/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.png"
	writeElysiumSource(t, root, map[string][]map[string]any{
		"assets": {{"id": "asset_machine", "path": assetPath}},
		"items": {{
			"id":       itemID,
			"registry": "test:machine",
			"meta":     54,
			"nbt":      map[string]any{"type": "compound"},
			"icon":     "asset_machine",
		}},
	}, map[string][]byte{assetPath: {1, 2, 3}})
	catalog, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	if _, state, ok := catalog.Lookup("test:machine", 54, raw); !ok || state != "available" {
		t.Fatalf("exact NBT lookup = %s, %v", state, ok)
	}
	other := mustCatalogNBT(t, "different_value")
	if _, state, ok := catalog.Lookup("test:machine", 54, other); ok || state != "unmatched" {
		t.Fatalf("different NBT was accepted = %s, %v", state, ok)
	}
}

func mustCatalogNBT(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "nbt", "testdata", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name string `json:"name"`
		Gzip string `json:"gzip"`
	}
	if err = json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		if vector.Name == name {
			return vector.Gzip
		}
	}
	t.Fatalf("missing NBT vector %s", name)
	return ""
}
