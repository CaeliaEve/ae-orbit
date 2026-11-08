package protocol

import "fmt"

// SyncTelemetry uses the OC process's monotonic clock, not the backend clock.
// Omitted snapshots retain the previous value within the same bridge session.
type SyncTelemetry struct {
	CapturedMillis     Quantity                    `json:"captured_millis"`
	FirstScanAckMillis *Quantity                   `json:"first_scan_ack_millis,omitempty"`
	Stages             map[string]LatencyHistogram `json:"stages"`
}

// Buckets are disjoint: <=10, <=50, <=100, <=250, <=1000, <=5000,
// <=30000 milliseconds and infinity. They must sum exactly to Samples.
type LatencyHistogram struct {
	Samples     Quantity   `json:"samples"`
	Errors      Quantity   `json:"errors"`
	TotalMillis Quantity   `json:"total_millis"`
	MaxMillis   Quantity   `json:"max_millis"`
	Buckets     []Quantity `json:"buckets"`
}

func (s SyncTelemetry) Validate() error {
	if s.CapturedMillis < 0 || s.Stages == nil || len(s.Stages) > 8 ||
		(s.FirstScanAckMillis != nil && (*s.FirstScanAckMillis < 0 || *s.FirstScanAckMillis > s.CapturedMillis)) {
		return fmt.Errorf("invalid sync timing")
	}
	for phase, v := range s.Stages {
		switch phase {
		case "event_queue", "recheck_queue", "priority_queue", "collection", "inventory_http", "control_http", "batch_ack", "scan":
		default:
			return fmt.Errorf("invalid sync timing phase")
		}
		if v.Samples < 1 || v.Errors < 0 || v.Errors > v.Samples || v.TotalMillis < 0 || v.MaxMillis < 0 ||
			v.MaxMillis > v.TotalMillis || len(v.Buckets) != 8 {
			return fmt.Errorf("invalid sync histogram")
		}
		var count Quantity
		for _, bucket := range v.Buckets {
			if bucket < 0 || bucket > v.Samples-count {
				return fmt.Errorf("invalid sync histogram counts")
			}
			count += bucket
		}
		if count != v.Samples {
			return fmt.Errorf("invalid sync histogram counts")
		}
	}
	return nil
}
