package catalog

import "testing"

func TestResourceCatalogExactNamesAndMetadata(t *testing.T) {
	root := t.TempDir()
	image := "assets/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png"
	writeElysiumSourceWithScope(t, root, map[string][]map[string]any{
		"items":   {{"id": "stone", "registry": "minecraft:stone", "meta": 0, "aspects": map[string]any{"aer": 100}}},
		"strings": {{"id": "water-text", "locale": "zh_CN", "text": "水"}, {"id": "air-text", "locale": "zh_CN", "text": "风"}},
		"assets":  {{"id": "icon", "path": image}},
		"fluids":  {{"id": "water", "registry": "water", "nbt": nil, "name": "water-text", "icon": "icon"}, {"id": "tagged", "registry": "tagged", "nbt": map[string]any{}}},
		"aspects": {{"id": "air", "source": map[string]any{"key": "aer", "owner": "Thaumcraft"}, "name": "air-text", "icon": "icon"}},
	}, map[string][]byte{image: {1, 2, 3}}, "complete", []string{"items", "strings", "assets", "fluids", "aspects"})
	c, err := OpenNESQLPP(root)
	if err != nil {
		t.Fatal(err)
	}
	candidates := c.ResourceCandidates()
	if len(candidates) != 2 {
		t.Fatal(candidates)
	}
	for _, d := range candidates {
		if !d.Exact || d.NBT != "" || d.Damage != 0 {
			t.Fatal(d)
		}
	}
	fluid, state, ok := c.LookupResource("fluid", "water", 0, "")
	if !ok || state != "available" || fluid.LocalizedName != "水" {
		t.Fatal(fluid, state, ok)
	}
	aspect, state, ok := c.LookupResource("essentia", "aer", 0, "")
	if !ok || state != "available" || aspect.ModID != "Thaumcraft" || aspect.LocalizedName != "风" {
		t.Fatal(aspect, state, ok)
	}
	if _, _, ok = c.LookupResource("fluid", "water", 1, ""); ok {
		t.Fatal("nonexact metadata accepted")
	}
	if _, _, ok = c.LookupResource("fluid", "tagged", 0, ""); ok {
		t.Fatal("tagged fluid admitted")
	}
	if _, _, ok = c.LookupResource("essentia", "water", 0, ""); ok {
		t.Fatal("type identity collapsed")
	}
}
