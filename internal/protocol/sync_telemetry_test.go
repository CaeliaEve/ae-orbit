package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

const syncSample = `{"captured_millis":"9007199254740993","first_scan_ack_millis":"1000","stages":{"inventory_http":{"samples":"2","errors":"1","total_millis":"110","max_millis":"100","buckets":["1","0","1","0","0","0","0","0"]}}}`

func syncPoll(sample string) string {
	return `{"session_id":"s","sequence":"1","reports":[],"health":{"free_memory":"65536","diagnostics":[],"sync":` + sample + `}}`
}

func TestSyncTelemetryRoundTrip(t *testing.T) {
	var p Poll
	d := json.NewDecoder(strings.NewReader(syncPoll(syncSample)))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil || p.Validate(16, 16) != nil {
		t.Fatalf("valid bounded timing rejected: %v", err)
	}
	encoded, err := json.Marshal(p)
	if err != nil || !strings.Contains(string(encoded), `"captured_millis":"9007199254740993"`) {
		t.Fatal("timing did not preserve exact counters", string(encoded), err)
	}
}

func TestSyncTelemetryRejectsMalformedHistograms(t *testing.T) {
	for name, sample := range map[string]string{
		"too_many_buckets":  strings.Replace(syncSample, `"1","0","1","0","0","0","0","0"`, `"1","0","1","0","0","0","0","0","0"`, 1),
		"wrong_total":       strings.Replace(syncSample, `"samples":"2"`, `"samples":"3"`, 1),
		"excess_errors":     strings.Replace(syncSample, `"errors":"1"`, `"errors":"3"`, 1),
		"excess_maximum":    strings.Replace(syncSample, `"max_millis":"100"`, `"max_millis":"111"`, 1),
		"negative":          strings.Replace(syncSample, `"total_millis":"110"`, `"total_millis":"-1"`, 1),
		"numeric":           strings.Replace(syncSample, `"samples":"2"`, `"samples":2`, 1),
		"unknown_phase":     strings.Replace(syncSample, `"inventory_http"`, `"arbitrary_identity"`, 1),
		"future_first_scan": strings.Replace(syncSample, `"first_scan_ack_millis":"1000"`, `"first_scan_ack_millis":"9007199254740994"`, 1),
		"overflow_buckets":  strings.Replace(syncSample, `"1","0","1","0","0","0","0","0"`, `"9223372036854775807","1","0","0","0","0","0","0"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			var p Poll
			d := json.NewDecoder(strings.NewReader(syncPoll(sample)))
			d.DisallowUnknownFields()
			if err := d.Decode(&p); err == nil && p.Validate(16, 16) == nil {
				t.Fatal("invalid timing accepted")
			}
		})
	}
}
