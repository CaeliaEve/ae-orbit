package protocol

import "fmt"

type CPUWatchAssignment struct {
	TargetID  string `json:"target_id"`
	CPUID     string `json:"cpu_id"`
	TTLMillis int    `json:"ttl_millis"`
}

type CPUWatchResult struct {
	TargetID string `json:"target_id"`
	Status   string `json:"status"`
}

func (r CPUWatchResult) Validate() error {
	if !tokenOK(r.TargetID) {
		return fmt.Errorf("invalid CPU watch target")
	}
	switch r.Status {
	case "busy", "idle", "changed", "missing", "inactive":
		return nil
	}
	return fmt.Errorf("invalid CPU watch status")
}
