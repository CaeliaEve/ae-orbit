package protocol

import (
	"encoding/json"
	"os"
	"testing"
)

func TestNBTSemanticIdentity(t *testing.T) {
	data, err := os.ReadFile("../nbt/testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name, Gzip, SHA256 string
		Error              bool
	}
	if err = json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	for _, v := range vectors {
		d := Definition{Kind: "item", Name: "test:machine", Damage: 54, NBT: v.Gzip, Exact: true}
		if v.Error {
			if d.Validate() == nil {
				t.Errorf("accepted invalid NBT: %s", v.Name)
			}
			continue
		}
		if err := d.Validate(); err != nil {
			t.Errorf("%s: %v", v.Name, err)
			continue
		}
		keys[v.Name] = d.Key()
	}
	for _, name := range []string{"reordered", "stored", "fixed", "root_name", "optional_header"} {
		if keys[name] != keys["ordered"] {
			t.Errorf("equivalent NBT split: %s", name)
		}
	}
	for _, pair := range [][2]string{{"ordered", "different_value"}, {"ordered", "different_type"}, {"ordered", "different_list"}, {"positive_zero", "negative_zero"}, {"nan_bits_one", "nan_bits_two"}, {"empty_list_end", "empty_list_byte"}} {
		if keys[pair[0]] == keys[pair[1]] {
			t.Errorf("different typed NBT merged: %v", pair)
		}
	}
}
