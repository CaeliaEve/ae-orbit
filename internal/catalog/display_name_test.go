package catalog

import (
	"encoding/base64"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/nbt"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func TestNotificationDisplayNameRequiresExactMetadata(t *testing.T) {
	c := &Catalog{byKey: map[string][]Item{
		baseKey("test:meta", 1): {{Key: "one", Name: "test:meta", Damage: 1, LocalizedName: "钢板"}},
		baseKey("test:meta", 2): {{Key: "two", Name: "test:meta", Damage: 2, LocalizedName: "电路"}},
	}, sourceResources: map[string]Item{resourceKey("fluid", "water"): {Name: "water", LocalizedName: "水"}}}
	c.indexCPUOutputs()
	damage := 2
	tagged, untagged := true, false
	if got := c.CPUOutputLabel(protocol.Stack{Name: "test:meta", Kind: "item", Damage: &damage, HasTag: &untagged, Label: "Circuit"}); got != "电路" {
		t.Fatal(got)
	}
	if got := c.CPUOutputLabel(protocol.Stack{Name: "test:meta", Label: "Circuit"}); got != "Circuit" {
		t.Fatal("ambiguous metadata guessed", got)
	}
	if got := c.CPUOutputLabel(protocol.Stack{Name: "test:meta", Kind: "item", Damage: &damage, HasTag: &tagged, Label: "Named Circuit"}); got != "Named Circuit" {
		t.Fatal("NBT name replaced", got)
	}
	if got := c.DefinitionLabel(protocol.Definition{Kind: "fluid", Name: "water", Label: "Water"}); got != "水" {
		t.Fatal(got)
	}
	if got := c.DefinitionLabel(protocol.Definition{Kind: "item", Name: "test:unknown", Label: "Unknown"}); got != "Unknown" {
		t.Fatal(got)
	}
}
func TestNotificationDefinitionKeepsNBTCustomName(t *testing.T) {
	c := &Catalog{byKey: map[string][]Item{baseKey("minecraft:paper", 0): {{Name: "minecraft:paper", LocalizedName: "纸"}}}}
	raw, err := nbt.EncodeTypedJSON([]byte(`{"type":"compound","value":{"display":{"type":"compound","value":{"Name":{"type":"string","value":"棕基板被动"}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	item := protocol.Definition{Kind: "item", Name: "minecraft:paper", Label: "Paper", NBT: base64.StdEncoding.EncodeToString(raw)}
	if got := c.DefinitionLabel(item); got != "棕基板被动" {
		t.Fatal(got)
	}
}
