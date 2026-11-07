package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const readDiagnosticFixture = `{"code":"exact_lookup_failed","source":"recheck","item_id":"9007199254740993","name":"test:iron_plate","damage":7,"nbt_bytes":1,"nbt_adler32":"00620062","message":"读取失败","occurrences":"9007199254740993","last_seen_millis":"1000"}`

func diagnosticPoll(samples string) string {
	return `{"session_id":"s","sequence":"1","reports":[],"health":{"free_memory":"131072","diagnostics":[],"read_failures":` + samples + `}}`
}

func TestReadDiagnosticsWirePreservesSamplesAndEmptySnapshot(t *testing.T) {
	for _, samples := range []string{`[` + readDiagnosticFixture + `]`, `[]`} {
		var p Poll
		if err := json.Unmarshal([]byte(diagnosticPoll(samples)), &p); err != nil {
			t.Fatal(err)
		}
		if err := p.Validate(16, 16); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Health struct {
				ReadFailures json.RawMessage `json:"read_failures"`
			} `json:"health"`
		}
		var want, actual any
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatalf("diagnostic snapshot was lost or rounded: %s: %v", encoded, err)
		}
		if err := json.Unmarshal([]byte(samples), &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(got.Health.ReadFailures, &actual); err != nil || !reflect.DeepEqual(actual, want) {
			t.Fatalf("diagnostic snapshot was lost or rounded: %s: %v", encoded, err)
		}
	}
	var legacy Poll
	if err := json.Unmarshal([]byte(`{"session_id":"s","sequence":"1","health":{"free_memory":"1","diagnostics":[]}}`), &legacy); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(legacy)
	if err != nil || strings.Contains(string(encoded), `"read_failures"`) {
		t.Fatalf("legacy health cleared the previous diagnostic snapshot: %s: %v", encoded, err)
	}
}

func TestReadDiagnosticsRejectsInvalidSamples(t *testing.T) {
	for _, test := range []struct{ name, old, replacement string }{
		{"unknown code", `"exact_lookup_failed"`, `"other"`},
		{"unknown source", `"recheck"`, `"other"`},
		{"empty name", `"test:iron_plate"`, `""`},
		{"long name", `"test:iron_plate"`, `"` + strings.Repeat("a", 257) + `"`},
		{"name control", `"test:iron_plate"`, `"bad\u0000name"`},
		{"long reason", `"读取失败"`, `"` + strings.Repeat("a", 161) + `"`},
		{"empty reason", `"读取失败"`, `""`},
		{"reason control", `"读取失败"`, `"bad\nreason"`},
		{"negative metadata", `"damage":7`, `"damage":-1`},
		{"metadata overflow", `"damage":7`, `"damage":2147483648`},
		{"negative NBT length", `"nbt_bytes":1`, `"nbt_bytes":-1`},
		{"NBT length overflow", `"nbt_bytes":1`, `"nbt_bytes":16385`},
		{"invalid NBT checksum", `"00620062"`, `"BAD"`},
		{"zero item ID", `"item_id":"9007199254740993"`, `"item_id":"0"`},
		{"numeric item ID", `"item_id":"9007199254740993"`, `"item_id":1`},
		{"zero occurrences", `"occurrences":"9007199254740993"`, `"occurrences":"0"`},
		{"numeric occurrences", `"occurrences":"9007199254740993"`, `"occurrences":1`},
		{"counter overflow", `"occurrences":"9007199254740993"`, `"occurrences":"9223372036854775808"`},
		{"missing count", `,"occurrences":"9007199254740993"`, ``},
	} {
		t.Run(test.name, func(t *testing.T) {
			var p Poll
			input := diagnosticPoll(`[` + strings.Replace(readDiagnosticFixture, test.old, test.replacement, 1) + `]`)
			if err := json.Unmarshal([]byte(input), &p); err == nil && p.Validate(16, 16) == nil {
				t.Fatal("invalid diagnostic sample was accepted")
			}
		})
	}
	for _, samples := range []string{
		`[` + strings.TrimSuffix(strings.Repeat(readDiagnosticFixture+`,`, 5), `,`) + `]`,
		`[` + strings.TrimSuffix(strings.Repeat(strings.Replace(strings.Replace(readDiagnosticFixture, `test:iron_plate`, strings.Repeat("n", 256), 1), `读取失败`, strings.Repeat("m", 160), 1)+`,`, 4), `,`) + `]`,
	} {
		var p Poll
		if err := json.Unmarshal([]byte(diagnosticPoll(samples)), &p); err == nil && p.Validate(16, 16) == nil {
			t.Fatal("diagnostic sample count or encoded byte budget was ignored")
		}
	}
}

// Lua's JSON is not embedded in HTML. Literal angle brackets in an exception
// must not turn a small, valid OC snapshot into a fatal control rejection.
func TestReadDiagnosticsMarkupUsesTransportByteBudget(t *testing.T) {
	sample := strings.Replace(readDiagnosticFixture, "读取失败", strings.Repeat("<>&", 50), 1)
	samples := `[` + strings.TrimSuffix(strings.Repeat(sample+`,`, 4), `,`) + `]`
	if len(samples) > 2048 {
		t.Fatal("fixture exceeds the OC byte budget")
	}
	var p Poll
	if err := json.Unmarshal([]byte(diagnosticPoll(samples)), &p); err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(16, 16); err != nil {
		t.Fatalf("valid OC payload was rejected by HTML expansion: %v", err)
	}
}
