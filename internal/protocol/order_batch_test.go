package protocol

import (
	"strings"
	"testing"
)

func TestOrderBatchIDValidation(t *testing.T) {
	input := OrderInput{ItemID: 1, Quantity: 12, IdempotencyKey: "batch-order-001"}
	for _, batch := range []string{"", "b343d6fc-c273-4ec4-ae5b-99fa57c97eb7"} {
		input.BatchID = batch
		if err := input.Validate(); err != nil {
			t.Fatal("valid batch rejected", err)
		}
	}
	for _, batch := range []string{" ", "batch\none", "batch\x00one", strings.Repeat("a", 129)} {
		input.BatchID = batch
		if input.Validate() == nil {
			t.Fatalf("invalid batch accepted: %q", batch)
		}
	}
}
