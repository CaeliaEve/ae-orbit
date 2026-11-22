package catalog

import (
	"encoding/base64"
	"unicode/utf8"

	"github.com/CaeliaEve/ae-orbit/internal/nbt"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

// DefinitionLabel uses the inventory's exact catalog identity and NBT custom
// name precedence. Missing artwork never prevents a localized label.
func (c *Catalog) DefinitionLabel(item protocol.Definition) string {
	label := item.Label
	if label == "" {
		label = item.Name
	}
	match, _, ok := c.LookupResource(item.Kind, item.Name, item.Damage, item.NBT)
	if !ok && item.NBT != "" {
		match, _, ok = c.LookupResource(item.Kind, item.Name, item.Damage, "")
	}
	if ok && match.LocalizedName != "" && match.LocalizedName != item.Name {
		label = match.LocalizedName
	}
	if item.NBT != "" {
		raw, err := base64.StdEncoding.DecodeString(item.NBT)
		if err == nil {
			data, err := nbt.TypedJSON(raw)
			if err == nil {
				root, _ := data.(map[string]any)
				fields, _ := root["value"].(map[string]any)
				display, _ := fields["display"].(map[string]any)
				values, _ := display["value"].(map[string]any)
				name, _ := values["Name"].(map[string]any)
				custom, _ := name["value"].(string)
				if custom != "" {
					if len(custom) > 512 {
						custom = custom[:512]
						for !utf8.ValidString(custom) {
							custom = custom[:len(custom)-1]
						}
					}
					label = custom
				}
			}
		}
	}
	return label
}

// CPU outputs carry no complete NBT identity. Retain their observed names
// for tagged variants instead of replacing them with the base item's name.
func (c *Catalog) CPUOutputLabel(output protocol.Stack) string {
	label := output.Label
	if label == "" {
		label = output.Name
	}
	if output.HasTag != nil && *output.HasTag {
		return label
	}
	match, _, _, ok := c.LookupCPUOutput(output)
	if ok && match.NBT == "" && match.LocalizedName != "" && match.LocalizedName != output.Name {
		return match.LocalizedName
	}
	return label
}
