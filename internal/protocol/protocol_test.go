package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// Replacing string decoding with float64, or accepting a noncanonical quantity,
// must fail these cases before a rounded value can reach a database or AE.
func TestQuantityPreservesInt64(t *testing.T) {
	for _, raw := range []string{`"0"`, `"9007199254740993"`, `"9223372036854775807"`} {
		var value Quantity
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != raw {
			t.Fatalf("round trip %s: %s, %v", raw, encoded, err)
		}
	}
	for _, raw := range []string{`1`, `9007199254740993`, `"9223372036854775808"`, `"-1"`, `"01"`, `"+1"`, `"1e3"`, `"1.0"`, `null`, `""`} {
		var value Quantity
		if err := json.Unmarshal([]byte(raw), &value); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

// Display names and counts are not identity; NBT and metadata are.
func TestItemIdentity(t *testing.T) {
	a := Definition{Kind: "item", Name: "minecraft:stone", Label: "Stone", Damage: 0, Exact: true}
	b := a
	b.Label = "石头"
	if a.Key() != b.Key() {
		t.Fatal("translated label changed identity")
	}
	b.Damage = 1
	if a.Key() == b.Key() {
		t.Fatal("metadata merged")
	}
	b = a
	b.NBT = "AQID"
	if a.Key() == b.Key() {
		t.Fatal("NBT merged")
	}
	b.NBT = "AQID\n"
	if err := b.Validate(); err == nil {
		t.Fatal("noncanonical NBT could split the same binary identity")
	}
	b.NBT = "AQID"
	b.Exact = false
	if err := b.Validate(); err == nil {
		t.Fatal("ambiguous NBT accepted")
	}
	a.Name = strings.Repeat("x", 257)
	if err := a.Validate(); err == nil {
		t.Fatal("oversize identity accepted")
	}
}

func TestBatchValidation(t *testing.T) {
	craftable := true
	valid := InventoryBatch{SessionID: "session", Sequence: 1, Updates: []Update{{Item: &Definition{Kind: "item", Name: "minecraft:stone", Label: "Stone", Exact: true}, Quantity: 0, Observation: 1, Craftable: &craftable}}}
	if err := valid.Validate(2); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Sequence = 0
	if err := invalid.Validate(2); err == nil {
		t.Fatal("sequence zero accepted")
	}
	invalid = valid
	invalid.Updates = append(append([]Update{}, valid.Updates...), valid.Updates[0], valid.Updates[0])
	if err := invalid.Validate(2); err == nil {
		t.Fatal("unbounded batch")
	}
	invalid = valid
	invalid.Updates = []Update{{ID: 1, Item: valid.Updates[0].Item, Observation: 1}}
	if err := invalid.Validate(2); err == nil {
		t.Fatal("ambiguous reference accepted")
	}
}

// A missing count or craftability flag must not silently overwrite stock with
// the Go zero value. A null flag is explicit uncertainty; zero/false are known.
func TestInventoryRequiresExplicitObservationFields(t *testing.T) {
	for _, raw := range []string{
		`{"id":"1","observation":"1","craftable":true}`,
		`{"id":"1","observation":"1","quantity":"0"}`,
		`{"id":"1","observation":"1","quantity":null,"craftable":false}`,
		`{"id":"1","observation":"1","quantity":"0","craftable":0}`,
		`{"id":"1","observation":"1","quantity":"0","craftable":"false"}`,
		`{"id":"1","observation":"1","quantity":"0","craftable":false,"unexpected":1}`,
	} {
		var update Update
		if err := json.Unmarshal([]byte(raw), &update); err == nil {
			t.Errorf("accepted incomplete or unknown fields: %s", raw)
		}
	}
	var batch InventoryBatch
	if err := json.Unmarshal([]byte(`{"session_id":"session","sequence":"1","updates":[{"id":"1","observation":"1","quantity":"0","craftable":false}]}`), &batch); err != nil {
		t.Fatal(err)
	}
	if err := batch.Validate(1); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"session_id":"session","sequence":"2","updates":[],"scan_completed":true}`), &batch); err != nil {
		t.Fatal(err)
	}
	if err := batch.Validate(1); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryAllowsExplicitUnknownCraftability(t *testing.T) {
	raw := `{"id":"1","observation":"1","quantity":"0","craftable":null}`
	var update Update
	if err := json.Unmarshal([]byte(raw), &update); err != nil {
		t.Fatal("quantity-only observation must preserve unknown craftability", err)
	}
	encoded, err := json.Marshal(update)
	if err != nil || !strings.Contains(string(encoded), `"craftable":null`) {
		t.Fatalf("unknown craftability became an authoritative false: %s, %v", encoded, err)
	}
}

func TestOrderTransitions(t *testing.T) {
	for _, pair := range [][2]string{{"assigned", "computing"}, {"computing", "running"}, {"running", "done"}, {"assigned", "done"}, {"computing", "failed"}, {"running", "needs_review"}} {
		if !CanTransition(pair[0], pair[1]) {
			t.Errorf("rejected %v", pair)
		}
	}
	for _, pair := range [][2]string{{"done", "running"}, {"running", "computing"}, {"needs_review", "assigned"}, {"queued", "done"}, {"failed", "done"}} {
		if CanTransition(pair[0], pair[1]) {
			t.Errorf("accepted %v", pair)
		}
	}
}

func TestVerificationTokenBudget(t *testing.T) {
	for _, token := range []string{"", strings.Repeat("a", 32), strings.Repeat("0", 32)} {
		batch := InventoryBatch{SessionID: "session", Sequence: 1, Updates: []Update{{ID: 1, Quantity: 1, Observation: 1, Verification: token}}}
		if err := batch.Validate(1); err != nil {
			t.Fatal("valid optional verification rejected", err)
		}
	}
	for _, token := range []string{strings.Repeat("a", 31), strings.Repeat("a", 33), strings.Repeat("A", 32), strings.Repeat("0", 31) + "!", strings.Repeat("a", 4096)} {
		batch := InventoryBatch{SessionID: "session", Sequence: 1, Updates: []Update{{ID: 1, Quantity: 1, Observation: 1, Verification: token}}}
		if err := batch.Validate(1); err == nil {
			t.Fatal("malformed or unbounded verification accepted")
		}
	}
}
