package catalog

import (
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"testing"
)

func TestCPUOutputExactMetadataAndLegacyAmbiguity(t *testing.T) {
	c := &Catalog{byKey: map[string][]Item{
		baseKey("test:meta", 1):      {{Key: "one", Name: "test:meta", Damage: 1, LocalizedName: "Plate"}},
		baseKey("test:meta", 2):      {{Key: "two", Name: "test:meta", Damage: 2, LocalizedName: "Circuit"}},
		baseKey("test:duplicate", 0): {{Key: "duplicate-zero", Name: "test:duplicate", Damage: 0, LocalizedName: "Duplicate"}},
		baseKey("test:duplicate", 1): {{Key: "duplicate-one", Name: "test:duplicate", Damage: 1, LocalizedName: "Duplicate"}},
	}}
	c.indexCPUOutputs()
	damage := 2
	untagged := false
	tagged := true
	item, kind, _, ok := c.LookupCPUOutput(protocol.Stack{Name: "test:meta", Kind: "item", Damage: &damage, HasTag: &untagged})
	if !ok || kind != "item" || item.Damage != 2 {
		t.Fatal(item, kind, ok)
	}
	item, _, _, ok = c.LookupCPUOutput(protocol.Stack{Name: "test:meta", Label: "Plate"})
	if !ok || item.Damage != 1 {
		t.Fatal(item, ok)
	}
	for _, output := range []protocol.Stack{{Name: "test:meta"}, {Name: "test:duplicate", Label: "Duplicate"}, {Name: "test:meta", Kind: "item", Damage: &damage, HasTag: &tagged}, {Name: "test:meta", Kind: "fluid", Label: "Plate"}} {
		if _, _, _, ok = c.LookupCPUOutput(output); ok {
			t.Fatal("ambiguous or tagged identity matched", output)
		}
	}
}

func TestCPUOutputFluidCatalogIdentity(t *testing.T) {
	root := t.TempDir()
	image := "assets/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png"
	writeElysiumSourceWithScope(t, root, map[string][]map[string]any{
		"items":  {{"id": "stone", "registry": "minecraft:stone", "meta": 0}},
		"assets": {{"id": "asset_stone", "path": image}},
		"fluids": {{"id": "fluid_water", "registry": "water", "nbt": nil, "icon": "asset_stone"}},
	}, map[string][]byte{image: {1, 2, 3}}, "complete", []string{"items", "strings", "assets", "fluids"})
	c, err := OpenNESQLPP(root)
	if err != nil {
		t.Fatal(err)
	}
	untagged := false
	item, kind, state, ok := c.LookupCPUOutput(protocol.Stack{Name: "water", Kind: "fluid", HasTag: &untagged})
	if !ok || kind != "fluid" || state != "available" || item.Name != "water" {
		t.Fatal(item, kind, state, ok)
	}
	if _, kind, _, ok = c.LookupCPUOutput(protocol.Stack{Name: "water"}); !ok || kind != "fluid" {
		t.Fatal("legacy unique fluid failed", kind, ok)
	}
}
