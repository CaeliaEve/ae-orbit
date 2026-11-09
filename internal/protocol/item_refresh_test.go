package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestQuantityRefreshReceiptValidation(t *testing.T) {
	for _, token := range []string{"x", strings.Repeat("A", 32), strings.Repeat("0", 33)} {
		var batch InventoryBatch
		data := `{"session_id":"s","sequence":"1","updates":[{"id":"1","quantity":"0","observation":"1","craftable":null,"refresh":"` + token + `"}]}`
		if err := json.Unmarshal([]byte(data), &batch); err == nil && batch.Validate(128) == nil {
			t.Fatal("invalid refresh token accepted", token)
		}
	}
	var batch InventoryBatch
	if err := json.Unmarshal([]byte(`{"session_id":"s","sequence":"1","updates":[{"id":"1","quantity":"0","observation":"1","craftable":null,"refresh":"0123456789abcdef0123456789abcdef"}]}`), &batch); err != nil {
		t.Fatal(err)
	}
	if err := batch.Validate(128); err != nil {
		t.Fatal("valid quantity-only receipt was rejected", err)
	}
}

func TestQuantityRefreshPollCapacityValidation(t *testing.T) {
	for _, capacity := range []string{`"refresh_slots":-1`, `"refresh_slots":9`, `"refresh_bytes":-1`, `"refresh_bytes":16385`} {
		var poll Poll
		if err := json.Unmarshal([]byte(`{"session_id":"s","sequence":"1",`+capacity+`}`), &poll); err != nil {
			t.Fatal(err)
		}
		if poll.Validate(64, 16) == nil {
			t.Fatal("invalid refresh capacity accepted", capacity)
		}
	}
	for _, capacity := range []string{`"refresh_slots":0,"refresh_bytes":0`, `"refresh_slots":8,"refresh_bytes":16384`} {
		var poll Poll
		if err := json.Unmarshal([]byte(`{"session_id":"s","sequence":"1",`+capacity+`}`), &poll); err != nil || poll.Validate(64, 16) != nil {
			t.Fatal("valid refresh capacity rejected", capacity, err)
		}
	}
}
