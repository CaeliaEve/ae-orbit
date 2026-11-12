package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCPUSelectionFieldsFailClosed(t *testing.T) {
	valid := OrderInput{ItemID: 1, Quantity: 64, IdempotencyKey: "cpu-selection-001"}
	for _, name := range []string{"", "Main CPU", "主 CPU", " CPU with spaces "} {
		input := valid
		input.CPUName = name
		input.WaitForCPU = name != ""
		if err := input.Validate(); err != nil {
			t.Fatalf("rejected valid CPU request %q: %v", name, err)
		}
	}
	for _, name := range []string{" ", "\t", "CPU\nname", "CPU\x00name", strings.Repeat("a", 257)} {
		input := valid
		input.CPUName = name
		if input.Validate() == nil {
			t.Fatalf("accepted invalid CPU name %q", name)
		}
	}
	invalid := valid
	invalid.WaitForCPU = true
	if invalid.Validate() == nil {
		t.Fatal("waiting without a named target was accepted")
	}

	raw, err := json.Marshal(Command{OrderID: "order", Quantity: 64})
	if err != nil || strings.Contains(string(raw), "cpu_name") || strings.Contains(string(raw), "wait_for_cpu") {
		t.Fatalf("automatic commands gained target fields: %s, %v", raw, err)
	}
}

func TestCPUSelectionCapabilityIsExplicitAndReserved(t *testing.T) {
	hello := Hello{
		Protocol: 1, BootID: "boot", Source: "oc", MaxResponseBytes: 4096,
		Capabilities: Capabilities{Craft: true, ExactQuantities: true, MaxCraftQuantity: 64},
	}
	if err := hello.Validate(); err != nil {
		t.Fatal("legacy automatic crafting hello was rejected", err)
	}
	raw, err := json.Marshal(hello.Capabilities)
	if err != nil || !strings.Contains(string(raw), `"cpu_selection":false`) {
		t.Fatalf("missing explicit unsupported capability: %s, %v", raw, err)
	}
	hello.Capabilities.CPUSelection = true
	if hello.Validate() == nil {
		t.Fatal("service accepted strict CPU selection without a strict execution contract")
	}
	hello.Protocol = 2
	if hello.ValidateV2() == nil {
		t.Fatal("v2 bypassed the strict CPU selection capability gate")
	}
}
