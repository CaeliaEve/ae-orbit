package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCPUWatchV2RoundTripAndLimits(t *testing.T) {
	target := strings.Repeat("a", 32)
	ack := PollAck{Ack: 1, Reads: []ReadAssignment{}, CPUWatches: []CPUWatchAssignment{{TargetID: target, CPUID: "1:1", TTLMillis: 120000}}}
	raw, err := json.Marshal(ack)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PollAck
	if err = json.Unmarshal(raw, &decoded); err != nil || len(decoded.CPUWatches) != 1 {
		t.Fatal(string(raw), err)
	}
	p := Poll{SessionID: "boot", Sequence: 1, CPUWatches: []CPUWatchResult{{TargetID: target, Status: "busy"}}}
	if err = p.ValidateV2(64, 16); err != nil {
		t.Fatal(err)
	}
	if err = p.Validate(64, 16); err == nil {
		t.Fatal("legacy accepted watches")
	}
	p.CPUWatches = append(p.CPUWatches, p.CPUWatches[0])
	if err = p.ValidateV2(64, 16); err == nil {
		t.Fatal("duplicate target accepted")
	}
	p.CPUWatches = p.CPUWatches[:1]
	p.CPUWatches[0].Status = "done"
	if err = p.ValidateV2(64, 16); err == nil {
		t.Fatal("unsupported success claim accepted")
	}
}
