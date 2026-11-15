package store

import (
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func TestUniqueIdleCPURequiresCompleteSnapshot(t *testing.T) {
	cpus := []protocol.CPU{{ID: "1", Name: "Main CPU", Busy: false}}
	if uniqueIdleCPU(&cpus, true, "Main CPU") {
		t.Fatal("truncated CPU snapshots must not authorize a waiting order")
	}
	if !uniqueIdleCPU(&cpus, false, "Main CPU") {
		t.Fatal("a complete unique idle CPU snapshot should authorize a waiting order")
	}
	cpus = append(cpus, protocol.CPU{ID: "2", Name: "Main CPU", Busy: false})
	if uniqueIdleCPU(&cpus, false, "Main CPU") {
		t.Fatal("duplicate CPU names must remain ambiguous")
	}
}

func TestCanDispatchCPURequiresStrictCapabilityForNamedRequests(t *testing.T) {
	automatic := protocol.Command{}
	if !canDispatchCPU(automatic, false, nil, false) {
		t.Fatal("automatic commands should not require CPU selection")
	}
	named := protocol.Command{CPUName: "Main CPU"}
	if canDispatchCPU(named, false, nil, false) {
		t.Fatal("named commands must stay queued without strict CPU capability")
	}
	waiting := protocol.Command{CPUName: "Main CPU", WaitForCPU: true}
	cpus := []protocol.CPU{{ID: "1", Name: "Main CPU", Busy: false}}
	if !canDispatchCPU(waiting, true, &cpus, false) {
		t.Fatal("waiting command should dispatch for a complete unique idle snapshot")
	}
}
