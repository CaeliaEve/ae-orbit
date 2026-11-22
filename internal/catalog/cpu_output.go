package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
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
		c.cpuOutputs[item.Name] = append(c.cpuOutputs[item.Name], cpuOutputMatch{kind, item})
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
// type/metadata identity; legacy snapshots require one untagged visual match.
// CPU snapshots have no NBT payload. Tagged outputs may reuse base artwork,
// but this lookup never establishes an inventory or crafting identity.
func (c *Catalog) LookupCPUOutput(output protocol.Stack) (Item, string, string, bool) {
	if c == nil {
		return Item{}, "", "unmatched", false
	}
	if output.Kind != "" && output.HasTag != nil && (output.Kind != "item" || output.Damage != nil) {
		damage := 0
		if output.Damage != nil {
			damage = *output.Damage
		}
		item, state, ok := c.LookupResource(output.Kind, output.Name, damage, "")
		if ok && state == "available" {
			return item, output.Kind, state, true
		}
		if shared, found := c.sharedCPUArtwork(output.Kind, output.Name, damage); found {
			return shared, output.Kind, "available", true
		}
		return item, output.Kind, state, ok
	}
	if output.HasTag != nil && *output.HasTag {
		return Item{}, "", "unmatched", false
	}
	candidates := []cpuOutputMatch{}
	labelMatches := []cpuOutputMatch{}
	label := strings.ToLower(strings.TrimSpace(output.Label))
	for _, match := range c.cpuOutputs[output.Name] {
		if match.item.NBT != "" {
			continue
		}
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

// Without a base export, reuse artwork only if every exported variant has
// the same asset and animation. Names and NBT contents do not disambiguate it.
func (c *Catalog) sharedCPUArtwork(kind, name string, damage int) (Item, bool) {
	var chosen Item
	found := false
	for _, candidate := range c.cpuOutputs[name] {
		item := candidate.item
		if candidate.kind != kind || item.Damage != damage {
			continue
		}
		if item.ImagePath == "" {
			return Item{}, false
		}
		if found && (item.ImagePath != chosen.ImagePath || item.ImageWidth != chosen.ImageWidth || item.ImageHeight != chosen.ImageHeight || item.Interpolate != chosen.Interpolate || !reflect.DeepEqual(item.Frames, chosen.Frames)) {
			return Item{}, false
		}
		if !found || item.Key < chosen.Key {
			chosen = item
		}
		found = true
	}
	if !found {
		return Item{}, false
	}
	if c.source {
		_, registered := c.sourceAssets[chosen.ImagePath]
		if !registered {
			return Item{}, false
		}
		if _, err := os.Stat(filepath.Join(c.root, filepath.FromSlash(chosen.ImagePath))); err != nil {
			return Item{}, false
		}
	} else if _, present := c.archiveImages[chosen.ImagePath]; !present {
		return Item{}, false
	}
	return chosen, true
}
