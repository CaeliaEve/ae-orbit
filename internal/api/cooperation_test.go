package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

type createdDevice struct {
	Device protocol.DeviceView `json:"device"`
	Token  string              `json:"device_token"`
}

func TestHTTPDeviceLifecycleAndProtocolGate(t *testing.T) {
	f := setup(t)
	base := "/api/v1/networks/" + f.network.ID
	devices := decode[[]protocol.DeviceView](t, send(t, f, "GET", base+"/devices", adminToken, ""), 200)
	if len(devices) != 1 || devices[0].Role != "primary" {
		t.Fatal("network must expose its stable primary identity", devices)
	}
	worker := decode[createdDevice](t, send(t, f, "POST", base+"/devices", adminToken, `{"name":"Reader B"}`), 201)
	if worker.Device.ID == "" || worker.Device.Role != "quantity_worker" || len(worker.Token) < 32 {
		t.Fatal("worker identity or one-time credential missing", worker)
	}
	for _, path := range []string{"/bridge/v1/boot-id", "/bridge/v1/hello", "/bridge/v1/poll", "/bridge/v1/inventory", "/bridge/v2/boot-id"} {
		if response := send(t, f, "POST", path, worker.Token, `{}`); response.StatusCode != 409 {
			t.Fatalf("worker entered incompatible mode at %s: %d", path, response.StatusCode)
		}
	}
	decode[protocol.Network](t, send(t, f, "POST", base+"/cooperation", adminToken, `{}`), 200)
	decode[protocol.Network](t, send(t, f, "POST", base+"/cooperation", adminToken, `{}`), 200)
	for _, token := range []string{f.device, worker.Token} {
		if response := send(t, f, "POST", "/bridge/v1/boot-id", token, `{}`); response.StatusCode != 409 {
			t.Fatal("cooperative credential entered v1", response.StatusCode)
		}
		nonce := decode[map[string]string](t, send(t, f, "POST", "/bridge/v2/boot-id", token, `{}`), 200)
		if len(nonce["boot_id"]) != 32 {
			t.Fatal("missing v2 boot identity", nonce)
		}
	}
	hello := `{"protocol":2,"boot_id":"worker-boot","source":"simulator","max_response_bytes":16384,"capabilities":{"inventory":true,"quantity_refresh":true,"exact_quantities":true}}`
	welcome := decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v2/hello", worker.Token, hello), 200)
	if welcome.Protocol != 2 || welcome.Role != "quantity_worker" || welcome.DeviceID != worker.Device.ID {
		t.Fatal("server-assigned identity missing", welcome)
	}
	rotated := decode[map[string]string](t, send(t, f, "POST", base+"/devices/"+worker.Device.ID+"/token", adminToken, `{}`), 200)
	if response := send(t, f, "POST", "/bridge/v2/boot-id", worker.Token, `{}`); response.StatusCode != 401 {
		t.Fatal("old worker credential survived rotation", response.StatusCode)
	}
	if response := send(t, f, "DELETE", base+"/devices/"+worker.Device.ID, adminToken, ""); response.StatusCode != 204 {
		t.Fatal(response.StatusCode)
	}
	if response := send(t, f, "POST", "/bridge/v2/boot-id", rotated["device_token"], `{}`); response.StatusCode != 401 {
		t.Fatal("revoked worker credential was accepted", response.StatusCode)
	}
}

func TestHTTPDeviceAuthorizationAndBoundedInput(t *testing.T) {
	f := setup(t)
	base := "/api/v1/networks/" + f.network.ID
	_, viewer, err := f.db.CreateMember(context.Background(), f.admin.ID, "viewer", []protocol.Grant{{NetworkID: f.network.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if response := send(t, f, "GET", base+"/devices", viewer, ""); response.StatusCode != 200 {
		t.Fatal("viewer cannot read devices", response.StatusCode)
	}
	for _, path := range []string{base + "/devices", base + "/cooperation"} {
		if response := send(t, f, "POST", path, viewer, `{}`); response.StatusCode != 403 {
			t.Fatal("viewer changed device configuration", path, response.StatusCode)
		}
	}
	for _, body := range []string{`{}`, `{"name":""}`, `{"name":"B","role":"primary"}`, `{"name":"B"} {}`} {
		if response := send(t, f, "POST", base+"/devices", adminToken, body); response.StatusCode != 400 {
			t.Fatal("invalid device body accepted", body, response.StatusCode)
		}
	}
	if response := send(t, f, "POST", base+"/devices", adminToken, strings.Repeat(" ", 4096)); response.StatusCode != 413 {
		t.Fatal("device body exceeded byte budget", response.StatusCode)
	}
	login := send(t, f, "POST", "/api/v1/session", "", `{"token":"`+adminToken+`"}`)
	cookie := login.Cookies()[0]
	session := decode[map[string]any](t, login, 201)
	for _, browser := range []struct {
		csrf   string
		origin string
		status int
	}{
		{"", "http://localhost", 403},
		{session["csrf"].(string), "https://unrelated.example", 403},
		{session["csrf"].(string), "http://localhost", 200},
	} {
		request, _ := http.NewRequest("POST", f.server.URL+base+"/cooperation", strings.NewReader(`{}`))
		request.AddCookie(cookie)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", browser.origin)
		request.Header.Set("X-CSRF-Token", browser.csrf)
		response, err := f.server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != browser.status {
			t.Fatal("browser Origin/CSRF result", response.StatusCode, "want", browser.status)
		}
	}
}

func cooperativeHTTPFixture(t *testing.T) (fixture, createdDevice, protocol.Welcome, protocol.Welcome) {
	t.Helper()
	f := setup(t)
	base := "/api/v1/networks/" + f.network.ID
	worker := decode[createdDevice](t, send(t, f, "POST", base+"/devices", adminToken, `{"name":"Reader B"}`), 201)
	decode[protocol.Network](t, send(t, f, "POST", base+"/cooperation", adminToken, `{}`), 200)
	hello := protocol.Hello{Protocol: 2, BootID: "primary-v2", Source: "simulator", MaxResponseBytes: 16384,
		Capabilities: protocol.Capabilities{Inventory: true, QuantityRefresh: true, ExactQuantities: true}}
	primaryWelcome := decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v2/hello", f.device, wire(t, hello)), 200)
	hello.BootID = "worker-v2"
	workerWelcome := decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v2/hello", worker.Token, wire(t, hello)), 200)
	return f, worker, primaryWelcome, workerWelcome
}

func TestHTTPV2RejectsWorkerPrivilegeEscalation(t *testing.T) {
	f, worker, primaryWelcome, workerWelcome := cooperativeHTTPFixture(t)
	definition := protocol.Definition{Kind: "item", Name: "test:v2_http", Label: "V2 HTTP", Exact: true}
	craftable := true
	discovery := protocol.InventoryBatch{SessionID: primaryWelcome.SessionID, Sequence: 1,
		Updates: []protocol.Update{{Item: &definition, Quantity: 9007199254740993, Observation: 1, Craftable: &craftable}}}
	ack := decode[protocol.InventoryAck](t, send(t, f, "POST", "/bridge/v2/inventory", f.device, wire(t, discovery)), 200)
	id := ack.Bindings[0].ID
	cpus := []protocol.CPU{}
	for _, poll := range []protocol.Poll{
		{CPUs: &cpus},
		{Reports: []protocol.Report{{OrderID: "o", AttemptID: "a", State: "done"}}},
		{Hints: []protocol.ReadHint{{ID: id}}},
	} {
		poll.SessionID, poll.Sequence = workerWelcome.SessionID, 1
		if response := send(t, f, "POST", "/bridge/v2/poll", worker.Token, wire(t, poll)); response.StatusCode != 403 {
			t.Fatal("worker submitted primary control data", poll, response.StatusCode)
		}
	}
	token := strings.Repeat("a", 32)
	for _, batch := range []protocol.InventoryBatch{
		{ScanCompleted: true},
		{Updates: []protocol.Update{{Item: &definition, Quantity: 1, Observation: 1}}},
		{Updates: []protocol.Update{{ID: id, Quantity: 1, Observation: 1, Craftable: &craftable, AssignmentID: token}}},
		{Updates: []protocol.Update{{ID: id, Quantity: 1, Observation: 1}}},
		{Updates: []protocol.Update{{ID: id, Quantity: 1, Observation: 1, AssignmentID: token}}},
	} {
		batch.SessionID, batch.Sequence = workerWelcome.SessionID, 1
		if response := send(t, f, "POST", "/bridge/v2/inventory", worker.Token, wire(t, batch)); response.StatusCode != 403 {
			t.Fatal("worker submitted unauthorized inventory", batch, response.StatusCode)
		}
	}
	// Rejected requests must not advance either independent checkpoint.
	poll := protocol.Poll{SessionID: workerWelcome.SessionID, Sequence: 1}
	decode[protocol.PollAck](t, send(t, f, "POST", "/bridge/v2/poll", worker.Token, wire(t, poll)), 200)
	batch := protocol.InventoryBatch{SessionID: workerWelcome.SessionID, Sequence: 1, Updates: []protocol.Update{}}
	decode[protocol.InventoryAck](t, send(t, f, "POST", "/bridge/v2/inventory", worker.Token, wire(t, batch)), 200)
}

func TestHTTPV2RejectsLegacyFieldsAndMalformedBodies(t *testing.T) {
	f, _, welcome, _ := cooperativeHTTPFixture(t)
	for _, field := range []string{`"refresh_slots":1`, `"recheck_slots":null`, `"read_slots":17`, `"read_bytes":16385`, `"hints":[{}]`} {
		body := `{"session_id":"` + welcome.SessionID + `","sequence":"1","reports":[],` + field + `}`
		if response := send(t, f, "POST", "/bridge/v2/poll", f.device, body); response.StatusCode != 400 {
			t.Fatal("invalid v2 poll accepted", field, response.StatusCode)
		}
	}
	for _, body := range []string{`{}`, `{"protocol":1}`, `{"protocol":2,"device_id":"forged"}`, `{} {}`} {
		if response := send(t, f, "POST", "/bridge/v2/hello", f.device, body); response.StatusCode != 400 {
			t.Fatal("invalid v2 hello accepted", body, response.StatusCode)
		}
	}
	if response := send(t, f, "POST", "/bridge/v2/inventory", f.device, strings.Repeat(" ", 1<<20)); response.StatusCode != 413 {
		t.Fatal("v2 inventory exceeded byte budget", response.StatusCode)
	}
}

func TestHTTPDeviceConcurrencyIsIndependentWithinNetwork(t *testing.T) {
	f := setup(t)
	base := "/api/v1/networks/" + f.network.ID
	worker := decode[createdDevice](t, send(t, f, "POST", base+"/devices", adminToken, `{"name":"Reader B"}`), 201)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ExpectContinueTimeout = 4 * time.Second
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	var writers []*io.PipeWriter
	finished := make(chan struct{}, 2)
	t.Cleanup(func() {
		for _, writer := range writers {
			writer.Close()
		}
		for range writers {
			<-finished
		}
	})
	for range 2 {
		reader, writer := io.Pipe()
		writers = append(writers, writer)
		reading := make(chan struct{}, 1)
		trace := &httptrace.ClientTrace{Got100Continue: func() { reading <- struct{}{} }}
		request, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), "POST", f.server.URL+"/bridge/v1/hello", reader)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+f.device)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Expect", "100-continue")
		go func() {
			defer func() { finished <- struct{}{} }()
			response, err := client.Do(request)
			if err == nil {
				response.Body.Close()
			}
		}()
		// 100 Continue is emitted only when readJSON starts consuming the
		// body, after authenticated per-device admission has completed.
		select {
		case <-reading:
		case <-ctx.Done():
			t.Fatal("request did not reach its admitted body read", ctx.Err())
		}
	}
	if response := send(t, f, "POST", "/bridge/v1/hello", f.device, `{}`); response.StatusCode != 429 {
		t.Fatal("third primary request escaped its concurrency bound", response.StatusCode)
	}
	if response := send(t, f, "POST", "/bridge/v2/hello", worker.Token, `{}`); response.StatusCode != 400 {
		t.Fatal("primary requests consumed worker admission capacity", response.StatusCode)
	}
}
