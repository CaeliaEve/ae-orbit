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
	for _, output := range []protocol.Stack{{Name: "test:meta"}, {Name: "test:duplicate", Label: "Duplicate"}, {Name: "test:meta", Kind: "item", HasTag: &tagged}, {Name: "test:meta", Kind: "fluid", Label: "Plate"}} {
		if _, _, _, ok = c.LookupCPUOutput(output); ok {
			t.Fatal("ambiguous or incomplete identity matched", output)
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

func TestCPUOutputNBTBaseAndSharedArtwork(t *testing.T) {
	common := "item/test/common.png"
	different := "item/test/different.png"
	items := []Item{
		{Key: "paper", Name: "test:paper", Damage: 0, LocalizedName: "Paper", ImagePath: common},
		{Key: "renamed-paper", Name: "test:paper", Damage: 0, NBT: "custom", LocalizedName: "Exported Custom Paper", ImagePath: different},
		{Key: "tool-a", Name: "test:tool", Damage: 2, NBT: "energy:1", ImagePath: common},
		{Key: "tool-b", Name: "test:tool", Damage: 2, NBT: "energy:2", ImagePath: common},
		{Key: "different-a", Name: "test:different", Damage: 0, NBT: "color:1", ImagePath: common},
		{Key: "different-b", Name: "test:different", Damage: 0, NBT: "color:2", ImagePath: different},
		{Key: "missing-a", Name: "test:missing", Damage: 0, NBT: "tag:1", ImagePath: common},
		{Key: "missing-b", Name: "test:missing", Damage: 0, NBT: "tag:2"},
		{Key: "animated-a", Name: "test:animated", Damage: 0, NBT: "tag:1", ImagePath: common},
		{Key: "animated-b", Name: "test:animated", Damage: 0, NBT: "tag:2", ImagePath: common, Frames: []protocol.CatalogFrame{{Width: 16, Height: 16, Ticks: 2}}},
	}
	c := &Catalog{byKey: map[string][]Item{}, archiveImages: map[string]struct{}{common: {}, different: {}}}
	for _, item := range items {
		key := baseKey(item.Name, item.Damage)
		c.byKey[key] = append(c.byKey[key], item)
	}
	c.indexCPUOutputs()
	zero, two := 0, 2
	tagged, untagged := true, false
	item, kind, state, ok := c.LookupCPUOutput(protocol.Stack{Name: "test:paper", Kind: "item", Damage: &zero, HasTag: &tagged, Label: "User Paper"})
	if !ok || kind != "item" || state != "available" || item.Key != "paper" {
		t.Fatal(item, kind, state, ok)
	}
	for _, hasTag := range []*bool{&tagged, &untagged} {
		item, kind, state, ok = c.LookupCPUOutput(protocol.Stack{Name: "test:tool", Kind: "item", Damage: &two, HasTag: hasTag})
		if !ok || kind != "item" || state != "available" || item.Key != "tool-a" {
			t.Fatal(item, kind, state, ok)
		}
	}
	for _, name := range []string{"test:different", "test:missing", "test:animated"} {
		if _, _, _, ok = c.LookupCPUOutput(protocol.Stack{Name: name, Kind: "item", Damage: &zero, HasTag: &tagged}); ok {
			t.Fatal("conflicting artwork guessed", name)
		}
	}
	// Matching artwork does not create an exact untagged inventory identity.
	if _, _, ok = c.Lookup("test:tool", two, ""); ok {
		t.Fatal("presentation changed inventory lookup")
	}
	if _, _, _, ok = c.LookupCPUOutput(protocol.Stack{Name: "test:tool", Label: "Tool"}); ok {
		t.Fatal("legacy snapshot inferred tagged metadata")
	}
	if _, _, _, ok = c.LookupCPUOutput(protocol.Stack{Name: "test:tool", Kind: "fluid", HasTag: &untagged}); ok {
		t.Fatal("cross-kind visual match")
	}
}

func TestCPUOutputNESQLNBTOnlyArtwork(t *testing.T) {
	root := t.TempDir()
	image := "assets/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png"
	writeElysiumSource(t, root, map[string][]map[string]any{
		"assets": {{"id": "asset", "path": image}},
		"items":  {{"id": "charged-export", "registry": "test:charged", "meta": 54, "nbt": map[string]any{"type": "compound"}, "icon": "asset"}},
	}, map[string][]byte{image: {1, 2, 3}})
	c, err := OpenNESQLPP(root)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	damage := 54
	tagged := true
	item, kind, state, ok := c.LookupCPUOutput(protocol.Stack{Name: "test:charged", Kind: "item", Damage: &damage, HasTag: &tagged})
	if !ok || kind != "item" || state != "available" || item.ImagePath != image || item.NBT == "" {
		t.Fatal(item, kind, state, ok)
	}
}
