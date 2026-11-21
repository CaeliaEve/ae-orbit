package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCPUDetailVersionAndLimits(t *testing.T) {
	r := CPUDetailResult{AssignmentID: strings.Repeat("a", 32), CPUID: "1:1", Kind: "active", Total: 1, Items: []CPUDetailItem{{Stack: Stack{Kind: "item", Name: "test:part", Label: "Part", Quantity: 9007199254740993}, RowID: "row"}}}
	p := Poll{SessionID: "session", Sequence: 1, CPUDetail: &r}
	if p.Validate(64, 16) == nil {
		t.Fatal("v1 accepted CPU details")
	}
	if err := p.ValidateV2(64, 16); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*CPUDetailResult){func(r *CPUDetailResult) { r.Error = "read_failed" }, func(r *CPUDetailResult) { r.Kind = "all" }, func(r *CPUDetailResult) { r.Total = 0 }, func(r *CPUDetailResult) { r.Items[0].Catalog = &CatalogItem{} }, func(r *CPUDetailResult) { r.Items[0].VariantHash = strings.Repeat("a", 64) }, func(r *CPUDetailResult) { r.Items = append(r.Items, r.Items[0]); r.Total = 2 }} {
		b := r
		b.Items = append([]CPUDetailItem{}, r.Items...)
		mutate(&b)
		if b.Validate() == nil {
			t.Fatal("invalid detail accepted", b)
		}
	}
	errResult := CPUDetailResult{AssignmentID: r.AssignmentID, CPUID: r.CPUID, Kind: "stored", Error: "memory_low"}
	if err := errResult.Validate(); err != nil {
		t.Fatal(err)
	}
	ack := PollAck{Ack: 1, Reads: []ReadAssignment{}, CPUDetail: &CPUDetailAssignment{AssignmentID: r.AssignmentID, CPUID: r.CPUID, Kind: "active", TTLMillis: 8000}}
	encoded, err := json.Marshal(ack)
	if err != nil || !strings.Contains(string(encoded), "cpu_detail") || strings.Contains(string(encoded), "verify") {
		t.Fatal(string(encoded), err)
	}
}
