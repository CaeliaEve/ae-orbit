package catalog

import (
	"encoding/base64"
	"fmt"
	"github.com/CaeliaEve/ae-orbit/internal/nbt"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"sort"
)

// VariantCandidates does not discover inventory: it supplies exact identities
// that OC can query without creating a native network iterator.
func (c *Catalog) VariantCandidates() ([]protocol.Definition, error) {
	if c == nil || !c.source {
		return nil, nil
	}
	result := []protocol.Definition{}
	seen := map[string]bool{}
	for _, item := range c.sourceItems {
		if item.NBT == "" {
			continue
		}
		raw, err := nbt.EncodeTypedJSON([]byte(item.NBT))
		if err != nil {
			return nil, fmt.Errorf("catalog variant %s: %w", item.Key, err)
		}
		identity, err := nbt.NESQLItemID(item.Name, item.Damage, raw)
		if err != nil || identity != item.Key {
			return nil, fmt.Errorf("catalog variant identity differs: %s", item.Key)
		}
		d := protocol.Definition{Kind: "item", Name: item.Name, Damage: item.Damage, Label: item.LocalizedName, NBT: base64.StdEncoding.EncodeToString(raw), Exact: true}
		if err = d.Validate(); err != nil {
			return nil, err
		}
		if !seen[d.Key()] {
			seen[d.Key()] = true
			result = append(result, d)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		if result[i].Damage != result[j].Damage {
			return result[i].Damage < result[j].Damage
		}
		return result[i].NBT < result[j].NBT
	})
	return result, nil
}
