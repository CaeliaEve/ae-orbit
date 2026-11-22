package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

type CPUDetailAssignment struct {
	AssignmentID string `json:"assignment_id"`
	CPUID        string `json:"cpu_id"`
	Kind         string `json:"kind"`
	TTLMillis    int    `json:"ttl_millis"`
}
type CPUDetailItem struct {
	Stack
	RowID       string `json:"row_id"`
	VariantHash string `json:"variant_hash,omitempty"`
}
type CPUDetailResult struct {
	AssignmentID string          `json:"assignment_id"`
	CPUID        string          `json:"cpu_id"`
	Kind         string          `json:"kind"`
	Items        []CPUDetailItem `json:"items,omitempty"`
	Total        int             `json:"total"`
	Truncated    bool            `json:"truncated"`
	Error        string          `json:"error,omitempty"`
}

func CPUDetailKind(kind string) bool {
	return kind == "active" || kind == "pending" || kind == "stored"
}
func (r CPUDetailResult) Validate() error {
	if !tokenOK(r.AssignmentID) || !textOK(r.CPUID, 1, 128) || !CPUDetailKind(r.Kind) || r.Total < 0 || r.Total > 1000000 || len(r.Items) > 64 {
		return fmt.Errorf("invalid CPU detail envelope")
	}
	errors := map[string]bool{"unsupported": true, "target_expired": true, "target_changed": true, "read_failed": true, "memory_low": true, "invalid_items": true, "packet_budget": true}
	if r.Error != "" {
		if !errors[r.Error] || len(r.Items) > 0 || r.Total != 0 || r.Truncated {
			return fmt.Errorf("invalid CPU detail error")
		}
		return nil
	}
	if r.Total < len(r.Items) || r.Truncated != (r.Total > len(r.Items)) {
		return fmt.Errorf("invalid CPU detail count")
	}
	seen := map[string]bool{}
	for _, v := range r.Items {
		if !textOK(v.RowID, 1, 128) || seen[v.RowID] || !textOK(v.Name, 1, 256) || !textOK(v.Label, 0, 512) || v.Quantity < 0 || v.Catalog != nil {
			return fmt.Errorf("invalid CPU detail item")
		}
		seen[v.RowID] = true
		if v.Kind != "item" && v.Kind != "fluid" && v.Kind != "essentia" || v.Damage != nil && (*v.Damage < 0 || *v.Damage > 2147483647) {
			return fmt.Errorf("invalid detail identity")
		}
		if v.VariantHash != "" && (len(v.VariantHash) != 64 || strings.Trim(v.VariantHash, "0123456789abcdef") != "" || v.HasTag == nil || !*v.HasTag) {
			return fmt.Errorf("invalid variant fingerprint")
		}
	}
	encoded, _ := json.Marshal(r)
	if len(encoded) > 12288 {
		return fmt.Errorf("CPU detail exceeds payload budget")
	}
	return nil
}
