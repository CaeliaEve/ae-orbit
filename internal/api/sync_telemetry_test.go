package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func TestHTTPSyncTelemetrySparseSnapshotsAndReset(t *testing.T) {
	f := setup(t)
	h := protocol.Hello{Protocol: 1, BootID: "sync-first", Source: "simulator", MaxResponseBytes: 4096, Capabilities: protocol.Capabilities{Inventory: true}}
	w := decode[map[string]any](t, send(t, f, "POST", "/bridge/v1/hello", f.device, wire(t, h)), 200)
	if w["limits"].(map[string]any)["sync_telemetry"] != true {
		t.Fatal("backend did not advertise bounded timing support")
	}
	session := w["session_id"].(string)
	sample := `{"captured_millis":"31000","first_scan_ack_millis":"120","stages":{"scan":{"samples":"1","errors":"0","total_millis":"120","max_millis":"120","buckets":["0","0","0","1","0","0","0","0"]}}}`
	body := func(id string, seq int, extra string) string {
		return fmt.Sprintf(`{"session_id":%q,"sequence":"%d","reports":[],"health":{"free_memory":"65536","diagnostics":[]%s}}`, id, seq, extra)
	}
	read := func() map[string]json.RawMessage {
		n := decode[protocol.Network](t, send(t, f, "GET", "/api/v1/networks/"+f.network.ID, adminToken, ""), 200)
		var health map[string]json.RawMessage
		if err := json.Unmarshal(n.Health, &health); err != nil {
			t.Fatal(err)
		}
		return health
	}
	check := func() {
		t.Helper()
		var got struct {
			Captured string `json:"captured_millis"`
		}
		if err := json.Unmarshal(read()["sync"], &got); err != nil || got.Captured != "31000" {
			t.Fatal("sparse poll lost or replaced the timing snapshot", got, err)
		}
	}
	p := body(session, 1, `,"sync":`+sample)
	decode[protocol.PollAck](t, send(t, f, "POST", "/bridge/v1/poll", f.device, p), 200)
	check()
	p = body(session, 2, "")
	decode[protocol.PollAck](t, send(t, f, "POST", "/bridge/v1/poll", f.device, p), 200)
	decode[protocol.PollAck](t, send(t, f, "POST", "/bridge/v1/poll", f.device, p), 200)
	check()
	if r := send(t, f, "POST", "/bridge/v1/poll", f.device, body(session, 2, `,"sync":`+sample)); r.StatusCode != http.StatusConflict {
		t.Fatal("timing changed the meaning of an identical sequence")
	}
	h.BootID = "sync-second"
	decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v1/hello", f.device, wire(t, h)), 200)
	if read()["sync"] != nil {
		t.Fatal("new session inherited timing from the previous OC boot")
	}
	if r := send(t, f, "POST", "/bridge/v1/poll", f.device, body(session, 3, `,"sync":`+sample)); r.StatusCode != http.StatusConflict {
		t.Fatal("retired session overwrote new health")
	}
}
