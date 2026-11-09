package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestV2WireAcceptsAssignmentReceipt(t *testing.T) {
	var batch InventoryBatch
	err := json.Unmarshal([]byte(`{"session_id":"s","sequence":"1","updates":[{"id":"1","quantity":"9","observation":"1","craftable":null,"assignment_id":"0123456789abcdef0123456789abcdef"}]}`), &batch)
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Validate(16); err == nil {
		t.Fatal("v1 accepted a v2 assignment receipt")
	}
	validator, ok := any(batch).(interface{ ValidateV2(int) error })
	if !ok {
		t.Fatal("inventory has no v2 validator")
	}
	if err := validator.ValidateV2(16); err != nil {
		t.Fatal(err)
	}
}

func TestV2WireAcceptsBoundedReadCapacity(t *testing.T) {
	var poll Poll
	decoder := json.NewDecoder(strings.NewReader(`{"session_id":"s","sequence":"1","reports":[],"read_slots":16,"read_bytes":16384,"hints":[{"id":"1"}]}`))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&poll); err != nil {
		t.Fatal(err)
	}
	if err := poll.Validate(16, 16); err == nil {
		t.Fatal("v1 accepted v2 read capacity")
	}
	validator, ok := any(poll).(interface{ ValidateV2(int, int) error })
	if !ok {
		t.Fatal("poll has no v2 validator")
	}
	if err := validator.ValidateV2(16, 16); err != nil {
		t.Fatal(err)
	}
}

func TestV2HelloDoesNotNegotiateOnV1(t *testing.T) {
	hello := Hello{Protocol: 2, BootID: "boot", Source: "oc", MaxResponseBytes: 16384}
	if err := hello.Validate(); err == nil {
		t.Fatal("v1 accepted protocol 2")
	}
	validator, ok := any(hello).(interface{ ValidateV2() error })
	if !ok {
		t.Fatal("hello has no v2 validator")
	}
	if err := validator.ValidateV2(); err != nil {
		t.Fatal(err)
	}
}

func TestVersionBoundaryRejectsPresentEmptyFields(t *testing.T) {
	for _, field := range []string{`"read_slots":null`, `"read_bytes":null`, `"hints":null`, `"hints":[]`} {
		var poll Poll
		if err := json.Unmarshal([]byte(`{"session_id":"s","sequence":"1","reports":[],`+field+`}`), &poll); err != nil {
			t.Fatal(err)
		}
		if err := poll.Validate(16, 16); err == nil {
			t.Fatalf("v1 accepted v2 field: %s", field)
		}
	}
	for _, field := range []string{`"recheck_slots":null`, `"recheck_bytes":null`, `"refresh_slots":null`, `"refresh_bytes":null`} {
		var poll Poll
		if err := json.Unmarshal([]byte(`{"session_id":"s","sequence":"1","reports":[],`+field+`}`), &poll); err != nil {
			t.Fatal(err)
		}
		if err := poll.ValidateV2(16, 16); err == nil {
			t.Fatalf("v2 accepted legacy field: %s", field)
		}
	}
	for _, field := range []string{`"verification":""`, `"refresh":""`} {
		var batch InventoryBatch
		if err := json.Unmarshal([]byte(`{"session_id":"s","sequence":"1","updates":[{"id":"1","quantity":"1","observation":"1","craftable":null,`+field+`}]}`), &batch); err != nil {
			t.Fatal(err)
		}
		if err := batch.ValidateV2(16); err == nil {
			t.Fatalf("v2 accepted legacy receipt field: %s", field)
		}
	}
}

func TestV2ReadCapacityAndHintBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"zero", `"read_slots":0,"read_bytes":0`, true},
		{"max", `"read_slots":16,"read_bytes":16384`, true},
		{"negative slots", `"read_slots":-1`, false},
		{"overflow slots", `"read_slots":17`, false},
		{"negative bytes", `"read_bytes":-1`, false},
		{"overflow bytes", `"read_bytes":16385`, false},
		{"empty identity", `"hints":[{}]`, false},
		{"zero ID", `"hints":[{"id":"0"}]`, false},
		{"invalid definition", `"hints":[{"item":{"kind":"item","name":"test:a","exact":false}}]`, false},
		{"both identities", `"hints":[{"id":"1","item":{"kind":"item","name":"test:a","exact":true}}]`, false},
		{"too many", `"hints":[` + strings.TrimSuffix(strings.Repeat(`{"id":"1"},`, 17), ",") + `]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var poll Poll
			if err := json.Unmarshal([]byte(`{"session_id":"s","sequence":"1","reports":[],`+tc.body+`}`), &poll); err != nil {
				t.Fatal(err)
			}
			if err := poll.ValidateV2(16, 16); (err == nil) != tc.ok {
				t.Fatalf("valid=%v want=%v: %v", err == nil, tc.ok, err)
			}
		})
	}
}

func TestV2AssignmentValidation(t *testing.T) {
	assignment := ReadAssignment{ID: 1, Item: Definition{Kind: "item", Name: "test:a", Exact: true},
		AssignmentID: "0123456789abcdef0123456789abcdef", Kind: "quantity", Purpose: "refresh", TTLMillis: 30000}
	if err := assignment.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ReadAssignment){
		func(a *ReadAssignment) { a.ID = 0 },
		func(a *ReadAssignment) { a.AssignmentID = strings.Repeat("A", 32) },
		func(a *ReadAssignment) { a.TTLMillis = 0 },
		func(a *ReadAssignment) { a.TTLMillis = 30001 },
		func(a *ReadAssignment) { a.Kind = "discovery" },
		func(a *ReadAssignment) { a.Purpose = "craft" },
	} {
		bad := assignment
		mutate(&bad)
		if err := bad.Validate(); err == nil {
			t.Fatalf("invalid assignment accepted: %+v", bad)
		}
	}
	for _, value := range []string{`""`, `"a"`, `"` + strings.Repeat("A", 32) + `"`} {
		var update Update
		if err := json.Unmarshal([]byte(`{"id":"1","quantity":"1","observation":"1","craftable":null,"assignment_id":`+value+`}`), &update); err == nil {
			t.Fatalf("invalid assignment ID accepted: %s", value)
		}
	}
}

func TestPollAckVersionedWireArrays(t *testing.T) {
	t.Run("legacy", func(t *testing.T) {
		ack := PollAck{Ack: 1, Commands: []Command{}, Verify: []Verification{}, PollMillis: 1000}
		body, err := json.Marshal(ack)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(body), `{"ack":"1","commands":[],"verify":[],"poll_millis":1000,"scan_budget":0}`; got != want {
			t.Fatalf("legacy poll changed: got %s want %s", got, want)
		}
	})
	for _, tc := range []struct {
		name  string
		reads []ReadAssignment
	}{
		{"empty", []ReadAssignment{}},
		{"assigned", []ReadAssignment{{ID: 1, Item: Definition{Kind: "item", Name: "test:a", Exact: true},
			AssignmentID: strings.Repeat("a", 32), Kind: "quantity", Purpose: "refresh", TTLMillis: 30000}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ack := PollAck{Ack: 1, Reads: tc.reads, Verify: []Verification{{ID: 2}},
				Refresh: []QuantityRefresh{{ID: 3}}, PollMillis: 1000}
			for round := 0; round < 2; round++ {
				body, err := json.Marshal(ack)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err = json.Unmarshal(body, &fields); err != nil {
					t.Fatal(err)
				}
				if string(fields["commands"]) != "[]" {
					t.Fatalf("v2 commands must be an array: %s", body)
				}
				if _, present := fields["reads"]; !present || string(fields["reads"]) == "null" {
					t.Fatalf("v2 reads must be an array: %s", body)
				}
				for _, legacy := range []string{"verify", "refresh"} {
					if _, present := fields[legacy]; present {
						t.Fatalf("v2 leaked legacy %s: %s", legacy, body)
					}
				}
				var cached PollAck
				if err = json.Unmarshal(body, &cached); err != nil {
					t.Fatal(err)
				}
				if cached.Reads == nil || len(cached.Reads) != len(tc.reads) {
					t.Fatalf("cached v2 ack lost its reads discriminator: %+v", cached)
				}
				ack = cached
			}
		})
	}
}
