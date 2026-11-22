package catalog

import (
	"encoding/base64"
	"os"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/nbt"
)

func TestVariantCatalogLosslessRoundTrip(t *testing.T) {
	raw, err := nbt.EncodeTypedJSON([]byte(`{"type":"compound","value":{"name":{"type":"string","value":"custom"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	id, err := nbt.NESQLItemID("minecraft:stone", 0, raw)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := nbt.TypedJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeElysiumSource(t, root, map[string][]map[string]any{"items": {{"id": id, "registry": "minecraft:stone", "meta": 0, "nbt": tag}}, "assets": {{"id": "icon", "path": "assets/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png"}}}, map[string][]byte{"assets/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png": {1}})
	c, err := OpenNESQLPP(root)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := c.VariantCandidates()
	if err != nil || len(candidates) != 1 {
		t.Fatal(candidates, err)
	}
	actual, err := base64.StdEncoding.DecodeString(candidates[0].NBT)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := nbt.Digest(raw)
	b, _ := nbt.Digest(actual)
	if a != b {
		t.Fatal("catalog changed NBT")
	}
	if export := os.Getenv("AE_TEST_PLAIN_CATALOG_ROOT"); export != "" {
		t.Run("full NESQL export", func(t *testing.T) { verifyVariantCatalogExport(t, export) })
	}
}

func verifyVariantCatalogExport(t *testing.T, root string) {
	t.Helper()
	c, err := OpenNESQLPP(root)
	if err != nil {
		t.Fatal(err)
	}
	variants, err := c.VariantCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) < 10000 {
		t.Fatal("missing exported NBT variants", len(variants))
	}
	t.Logf("Validated %d typed NBT candidates", len(variants))
}
