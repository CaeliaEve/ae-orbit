package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProductionJobNormalization(t *testing.T) {
	base := ProductionJobInput{RequestKey: "request-001", Entries: []ProductionJobEntryInput{{EntryKey: "source-001", ItemID: 1, Quantity: 64}}}
	tests := []struct {
		name string
		edit func(*ProductionJobInput)
	}{
		{"empty", func(in *ProductionJobInput) { in.Entries = nil }},
		{"too many", func(in *ProductionJobInput) { in.Entries = make([]ProductionJobEntryInput, 501) }},
		{"duplicate source", func(in *ProductionJobInput) { in.Entries = append(in.Entries, in.Entries[0]) }},
		{"short request", func(in *ProductionJobInput) { in.RequestKey = "short" }},
		{"long request", func(in *ProductionJobInput) { in.RequestKey = strings.Repeat("a", 129) }},
		{"unicode request", func(in *ProductionJobInput) { in.RequestKey = "request-\u00e9" }},
		{"space source", func(in *ProductionJobInput) { in.Entries[0].EntryKey = "source 001" }},
		{"control name", func(in *ProductionJobInput) { in.Name = "line\nbreak" }},
		{"invalid utf8", func(in *ProductionJobInput) { in.Name = string([]byte{0xff}) }},
		{"long name", func(in *ProductionJobInput) { in.Name = strings.Repeat("x", 129) }},
		{"negative ttl", func(in *ProductionJobInput) { in.TTLSeconds = -1 }},
		{"long ttl", func(in *ProductionJobInput) { in.TTLSeconds = 86401 }},
		{"zero item", func(in *ProductionJobInput) { in.Entries[0].ItemID = 0 }},
		{"zero quantity", func(in *ProductionJobInput) { in.Entries[0].Quantity = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			in.Entries = append([]ProductionJobEntryInput(nil), base.Entries...)
			tt.edit(&in)
			if _, err := in.Normalize(); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	normalized, err := base.Normalize()
	if err != nil || normalized.TTLSeconds != 3600 {
		t.Fatal(normalized, err)
	}
	explicit := base
	explicit.TTLSeconds = 3600
	explicit, err = explicit.Normalize()
	if err != nil || normalized.ManifestDigest() != explicit.ManifestDigest() {
		t.Fatal("default TTL changed replay identity", err)
	}
	normalized.Entries = append(normalized.Entries, ProductionJobEntryInput{EntryKey: "source-002", ItemID: 1, Quantity: 64})
	reversed := normalized
	reversed.Entries = []ProductionJobEntryInput{normalized.Entries[1], normalized.Entries[0]}
	if normalized.ManifestDigest() == reversed.ManifestDigest() {
		t.Fatal("entry order discarded")
	}
}

func TestProductionJobExactIntegers(t *testing.T) {
	for _, value := range []string{`64`, `"1.5"`, `"01"`, `"9223372036854775808"`} {
		var in ProductionJobInput
		err := json.Unmarshal([]byte(`{"request_key":"request-001","entries":[{"entry_key":"source-001","item_id":"1","quantity":`+value+`}]}`), &in)
		if err == nil {
			t.Fatalf("non-exact quantity accepted: %s", value)
		}
	}
}
