package store

import (
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func TestProductionJobSettlementResult(t *testing.T) {
	for _, tt := range []struct {
		name    string
		control string
		counts  protocol.ProductionJobCounts
		settled bool
		result  string
	}{
		{"pending", "active", protocol.ProductionJobCounts{Pending: 1}, false, ""},
		{"uncertain", "stopped", protocol.ProductionJobCounts{NeedsReview: 1}, false, ""},
		{"all done", "active", protocol.ProductionJobCounts{Done: 2}, true, "completed"},
		{"stopped cancellation", "stopped", protocol.ProductionJobCounts{Done: 1, Canceled: 1}, true, "canceled"},
		{"individual cancellation", "active", protocol.ProductionJobCounts{Done: 1, Canceled: 1}, true, "finished_with_issues"},
		{"stopped failure", "stopped", protocol.ProductionJobCounts{Canceled: 1, Failed: 1}, true, "finished_with_issues"},
		{"rejection", "active", protocol.ProductionJobCounts{Rejected: 1}, true, "finished_with_issues"},
		{"expiry", "active", protocol.ProductionJobCounts{Expired: 1}, true, "finished_with_issues"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			settled, result := productionJobSettlement(tt.counts, tt.control)
			if settled != tt.settled || result != tt.result {
				t.Fatal(settled, result)
			}
		})
	}
}
