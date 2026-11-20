package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func resourceKey(kind, name string) string { return kind + "\x00" + name }

func (c *Catalog) readResources(files []string, kind string) error {
	return readSourceRows(c.root, files, func(row map[string]json.RawMessage) error {
		// OC cannot represent fluid tags. Never collapse a tagged fluid into an
		// untagged descriptor or treat exported aspect composition as inventory.
		if tag := bytes.TrimSpace(row["nbt"]); len(tag) > 0 && !bytes.Equal(tag, []byte("null")) {
			return nil
		}
		name, _ := rawString(row["registry"])
		mod := ""
		if kind == "essentia" {
			var source struct {
				Key   string `json:"key"`
				Owner string `json:"owner"`
			}
			if err := json.Unmarshal(row["source"], &source); err != nil {
				return fmt.Errorf("invalid NESQL aspect source")
			}
			name, mod = source.Key, source.Owner
		}
		id, _ := rawString(row["id"])
		nameID, _ := rawString(row["name"])
		label := c.resolveText(nameID)
		if label == "" {
			label = name
		}
		definition := protocol.Definition{Kind: kind, Name: name, Label: label, Exact: true}
		if id == "" || definition.Validate() != nil {
			return fmt.Errorf("invalid NESQL resource identity")
		}
		item := Item{Key: id, Name: name, LocalizedName: label, InternalName: name, UnlocalizedName: name, ModID: mod}
		if description, _ := rawString(row["description"]); description != "" {
			item.Tooltips = []string{c.resolveText(description)}
		}
		icon, _ := rawString(row["icon"])
		item.ImagePath = c.assetPath(icon)
		if meta, ok := c.sourceAssetMeta[icon]; ok {
			item.ImageWidth, item.ImageHeight = meta.Width, meta.Height
			item.Frames, item.Interpolate = cloneFrames(meta.Frames), meta.Interpolate
		}
		key := resourceKey(kind, name)
		if _, exists := c.sourceResources[key]; exists {
			return fmt.Errorf("ambiguous NESQL resource identity")
		}
		c.sourceResources[key] = item
		return nil
	})
}

// ResourceCandidates supplies exact fluid names and aspect tags from immutable
// metadata. Counts always come from authenticated game observations.
func (c *Catalog) ResourceCandidates() []protocol.Definition {
	if c == nil {
		return nil
	}
	result := []protocol.Definition{}
	for key, item := range c.sourceResources {
		kind, _, _ := strings.Cut(key, "\x00")
		result = append(result, protocol.Definition{Kind: kind, Name: item.Name, Label: item.LocalizedName, Exact: true})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func (c *Catalog) LookupResource(kind, name string, damage int, tag string) (Item, string, bool) {
	if kind == "item" || kind == "" {
		return c.Lookup(name, damage, tag)
	}
	if c == nil || damage != 0 || tag != "" {
		return Item{}, "unmatched", false
	}
	item, ok := c.sourceResources[resourceKey(kind, name)]
	if !ok {
		return Item{}, "unmatched", false
	}
	if item.ImagePath == "" {
		return item, "missing", true
	}
	if _, err := os.Stat(filepath.Join(c.root, filepath.FromSlash(item.ImagePath))); err != nil {
		return item, "missing", true
	}
	return item, "available", true
}
