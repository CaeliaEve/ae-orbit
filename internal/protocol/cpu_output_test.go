package protocol

import "testing"

func TestCPUOutputOptionalIdentityValidation(t *testing.T) {
	damage := 32001
	untagged := false
	valid := Stack{Name: "gregtech:gt.metaitem.01", Label: "Plate", Quantity: 64, Kind: "item", Damage: &damage, HasTag: &untagged}
	check := func(output Stack) error {
		cpus := []CPU{{ID: "1", Storage: 4096, Output: &output}}
		return (Poll{SessionID: "session", Sequence: 1, CPUs: &cpus}).Validate(64, 16)
	}
	if err := check(valid); err != nil {
		t.Fatal(err)
	}
	if err := check(Stack{Name: "legacy", Quantity: 1}); err != nil {
		t.Fatal(err)
	}
	negative := -1
	for _, mutate := range []func(*Stack){func(o *Stack) { o.Kind = "wrong" }, func(o *Stack) { o.Damage = &negative }, func(o *Stack) { o.Kind = "fluid" }, func(o *Stack) { o.Catalog = &CatalogItem{ImageURL: "https://untrusted.test/icon.png"} }} {
		bad := valid
		mutate(&bad)
		if check(bad) == nil {
			t.Fatal("untrusted output metadata accepted", bad)
		}
	}
}
