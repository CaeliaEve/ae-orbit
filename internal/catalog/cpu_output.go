package catalog

import (
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"strings"
)

type cpuOutputMatch struct {
	kind string
	item Item
}

// Older OC snapshots omitted metadata. Index their possible visual matches
// once at catalog load; never scan the entire catalog on each CPU refresh.
func (c *Catalog) indexCPUOutputs() {
	c.cpuOutputs = map[string][]cpuOutputMatch{}
	add := func(kind string, item Item) {
		if item.NBT == "" {
			c.cpuOutputs[item.Name] = append(c.cpuOutputs[item.Name], cpuOutputMatch{kind, item})
		}
	}
	if c.source {
		for _, item := range c.sourceItems {
			add("item", item)
		}
		for key, item := range c.sourceResources {
			kind, _, _ := strings.Cut(key, "\x00")
			add(kind, item)
		}
	} else {
		for _, items := range c.byKey {
			for _, item := range items {
				add("item", item)
			}
		}
	}
}

// LookupCPUOutput is presentation metadata only. New snapshots use exact
// type/metadata/untagged identity; legacy snapshots require one visual match.
func (c *Catalog) LookupCPUOutput(output protocol.Stack) (Item, string, string, bool) {
	if c == nil || output.HasTag != nil && *output.HasTag {
		return Item{}, "", "unmatched", false
	}
	if output.Kind != "" && output.HasTag != nil && (output.Kind != "item" || output.Damage != nil) {
		damage := 0
		if output.Damage != nil {
			damage = *output.Damage
		}
		item, state, ok := c.LookupResource(output.Kind, output.Name, damage, "")
		return item, output.Kind, state, ok
	}
	candidates := []cpuOutputMatch{}
	labelMatches := []cpuOutputMatch{}
	label := strings.ToLower(strings.TrimSpace(output.Label))
	for _, match := range c.cpuOutputs[output.Name] {
		if output.Kind != "" && output.Kind != match.kind || output.Damage != nil && *output.Damage != match.item.Damage {
			continue
		}
		candidates = append(candidates, match)
		if label != "" && label != strings.ToLower(output.Name) && label == strings.ToLower(strings.TrimSpace(match.item.LocalizedName)) {
			labelMatches = append(labelMatches, match)
		}
	}
	if len(labelMatches) > 0 {
		candidates = labelMatches
	}
	if len(candidates) != 1 {
		return Item{}, "", "unmatched", false
	}
	match := candidates[0]
	item, state, ok := c.LookupResource(match.kind, match.item.Name, match.item.Damage, "")
	return item, match.kind, state, ok
}
