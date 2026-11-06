package protocol

import (
	"encoding/json"
	"fmt"
	"testing"
)

// Ignoring a receiver's capacity would turn a full OC queue into lost work.
func TestPollRecheckCapacityValidation(t *testing.T) {
	for _, fields := range []string{``, `,"recheck_slots":0,"recheck_bytes":0`, `,"recheck_slots":16,"recheck_bytes":16384`} {
		var p Poll
		if err := json.Unmarshal([]byte(`{"session_id":"s","sequence":"1","reports":[]`+fields+`}`), &p); err != nil || p.Validate(16, 16) != nil {
			t.Fatalf("valid capacity rejected: %s: %v", fields, err)
		}
	}
	for _, fields := range []string{`"recheck_slots":-1`, `"recheck_slots":17`, `"recheck_bytes":-1`, `"recheck_bytes":16385`} {
		t.Run(fields, func(t *testing.T) {
			var p Poll
			err := json.Unmarshal([]byte(`{"session_id":"s","sequence":"1","reports":[],`+fields+`}`), &p)
			if err == nil && p.Validate(16, 16) == nil {
				t.Fatal("invalid receiver capacity was accepted")
			}
		})
	}
}

func TestInventoryTelemetryPreservesExactCounters(t *testing.T) {
	input := `{"session_id":"s","sequence":"1","reports":[],"health":{"free_memory":"131072","diagnostics":[],"inventory":{"batches":"2","updates":"16","bytes":"9007199254740993","credits_used":"17","collect_millis":"1020"}}}`
	var p Poll
	if err := json.Unmarshal([]byte(input), &p); err != nil || p.Validate(16, 16) != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Health struct {
			Inventory map[string]string `json:"inventory"`
		} `json:"health"`
	}
	if err = json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"batches": "2", "updates": "16", "bytes": "9007199254740993", "credits_used": "17", "collect_millis": "1020"} {
		if got.Health.Inventory[key] != want {
			t.Errorf("counter %s = %q, want %q", key, got.Health.Inventory[key], want)
		}
	}
	for _, value := range []string{`1`, `"-1"`, `"1e3"`, `"9223372036854775808"`} {
		var invalid Poll
		data := fmt.Sprintf(`{"session_id":"s","sequence":"1","reports":[],"health":{"free_memory":"1","diagnostics":[],"inventory":{"batches":%s,"updates":"0","bytes":"0","credits_used":"0","collect_millis":"0"}}}`, value)
		if err := json.Unmarshal([]byte(data), &invalid); err == nil {
			t.Errorf("noncanonical telemetry accepted: %s", value)
		}
	}
}
