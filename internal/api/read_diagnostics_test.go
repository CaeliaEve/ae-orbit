package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func TestHTTPReadDiagnosticsSparseSnapshotsAndSessionReset(t *testing.T) {
	f := setup(t)
	hello := protocol.Hello{Protocol: 1, BootID: "diagnostics-boot", Source: "simulator", MaxResponseBytes: 4096, Capabilities: protocol.Capabilities{Inventory: true}}
	w := decode[map[string]any](t, send(t, f, "POST", "/bridge/v1/hello", f.device, wire(t, hello)), 200)
	if w["limits"].(map[string]any)["diagnostic_samples"] != float64(4) {
		t.Fatal("backend did not negotiate bounded diagnostic support")
	}
	session := w["session_id"].(string)
	sample := `[{"code":"exact_lookup_failed","source":"recheck","item_id":"9007199254740993","name":"test:iron_plate","damage":7,"nbt_bytes":1,"nbt_adler32":"00620062","message":"读取失败","occurrences":"3","last_seen_millis":"1000"}]`
	poll := func(id string, seq int, extra string) string {
		return fmt.Sprintf(`{"session_id":%q,"sequence":"%d","reports":[],"health":{"free_memory":"65536","diagnostics":[]%s}}`, id, seq, extra)
	}
	read := func() map[string]json.RawMessage {
		n := decode[protocol.Network](t, send(t, f, "GET", "/api/v1/networks/"+f.network.ID, adminToken, ""), 200)
		var h map[string]json.RawMessage
		if err := json.Unmarshal(n.Health, &h); err != nil {
			t.Fatal(err)
		}
		return h
	}
	check := func(want string) {
		t.Helper()
		var a, b any
		got := read()["read_failures"]
		if err := json.Unmarshal(got, &a); err != nil {
			t.Fatalf("missing stored snapshot: %s: %v", got, err)
		}
		if err := json.Unmarshal([]byte(want), &b); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("snapshot changed: %s", got)
		}
	}
	body := poll(session, 1, `,"read_failures":`+sample+`,"inventory":{"batches":"1","updates":"1","bytes":"256","credits_used":"1","collect_millis":"60"}`)
	decode[protocol.PollAck](t, send(t, f, "POST", "/bridge/v1/poll", f.device, body), 200)
	check(sample)
	// Omitting the snapshot updates normal health while retaining the last details.
	body = strings.Replace(poll(session, 2, ""), `"free_memory":"65536"`, `"free_memory":"32768"`, 1)
	decode[protocol.PollAck](t, send(t, f, "POST", "/bridge/v1/poll", f.device, body), 200)
	decode[protocol.PollAck](t, send(t, f, "POST", "/bridge/v1/poll", f.device, body), 200)
	check(sample)
	health := read()
	if string(health["free_memory"]) != `"32768"` || health["inventory"] != nil {
		t.Fatal("sparse diagnostics retained or replaced unrelated old health fields")
	}
	if response := send(t, f, "POST", "/bridge/v1/poll", f.device, poll(session, 2, `,"read_failures":[]`)); response.StatusCode != http.StatusConflict {
		t.Fatal("changed control retry was accepted")
	}
	check(sample)
	decode[protocol.PollAck](t, send(t, f, "POST", "/bridge/v1/poll", f.device, poll(session, 3, `,"read_failures":[]`)), 200)
	check("[]")
	decode[protocol.PollAck](t, send(t, f, "POST", "/bridge/v1/poll", f.device, poll(session, 4, `,"read_failures":`+sample)), 200)
	hello.BootID = "diagnostics-restart"
	restarted := decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v1/hello", f.device, wire(t, hello)), 200)
	if _, present := read()["read_failures"]; present {
		t.Fatal("new OC session inherited stale error details")
	}
	decode[protocol.PollAck](t, send(t, f, "POST", "/bridge/v1/poll", f.device, poll(restarted.SessionID, 1, `,"read_failures":`+sample)), 200)
	decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v1/hello", f.device, wire(t, hello)), 200)
	check(sample)
	if response := send(t, f, "POST", "/bridge/v1/poll", f.device, poll(session, 5, `,"read_failures":[]`)); response.StatusCode != http.StatusConflict {
		t.Fatal("old session overwrote the current diagnostics")
	}
	check(sample)
}
