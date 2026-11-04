package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/api"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"github.com/CaeliaEve/ae-orbit/internal/testutil"
)

const adminToken = "test-admin-token-abcdefghijklmnopqrstuvwxyz0123456789"

type fixture struct {
	db      *store.Store
	server  *httptest.Server
	admin   protocol.Member
	network protocol.Network
	device  string
}

func setup(t *testing.T) fixture {
	t.Helper()
	db := testutil.Database(t)
	ctx := context.Background()
	admin, err := db.Bootstrap(ctx, adminToken)
	if err != nil {
		t.Fatal(err)
	}
	n, d, err := db.CreateNetwork(ctx, admin.ID, "Factory", "server", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	options := api.DefaultOptions()
	options.Origin = "http://localhost"
	options.SecureCookies = false
	options.StreamInterval = 20 * time.Millisecond
	server := httptest.NewServer(api.New(db, options))
	t.Cleanup(server.Close)
	return fixture{db: db, server: server, admin: admin, network: n, device: d}
}
func send(t *testing.T, f fixture, method, path, token, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, f.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := f.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}
func decode[T any](t *testing.T, r *http.Response, status int) T {
	t.Helper()
	defer r.Body.Close()
	if r.StatusCode != status {
		data, _ := io.ReadAll(r.Body)
		t.Fatalf("HTTP %d, expected %d: %s", r.StatusCode, status, data)
	}
	var result T
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
func wire(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHTTPInventoryAndOrderFlow(t *testing.T) {
	f := setup(t)
	if r := send(t, f, "GET", "/api/v1/networks", "", ""); r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	hello := protocol.Hello{Protocol: 1, BootID: "boot-1", Source: "simulator", MaxResponseBytes: 32768, Capabilities: protocol.Capabilities{Inventory: true, Craft: true, ExactQuantities: true, MaxCraftQuantity: 9223372036854775807, Enumeration: "simulated"}}
	w := decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v1/hello", f.device, wire(t, hello)), 200)
	b := protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 1, Updates: []protocol.Update{{Item: &protocol.Definition{Kind: "item", Name: "minecraft:stone", Label: "石头", NBT: "AQID", Exact: true}, Quantity: 9007199254740993, Observation: 1, Craftable: true}}}
	ack := decode[protocol.InventoryAck](t, send(t, f, "POST", "/bridge/v1/inventory", f.device, wire(t, b)), 200)
	base := "/api/v1/networks/" + f.network.ID
	page := decode[protocol.ItemPage](t, send(t, f, "GET", base+"/items?limit=1", adminToken, ""), 200)
	if len(page.Items) != 1 || page.Items[0].Quantity.String() != "9007199254740993" {
		t.Fatal(page)
	}
	if strings.Contains(wire(t, page), `"nbt"`) {
		t.Fatal("catalog page carried an unnecessary identity blob")
	}
	o := decode[protocol.Order](t, send(t, f, "POST", base+"/orders", adminToken, wire(t, protocol.OrderInput{ItemID: ack.Bindings[0].ID, Quantity: 64, IdempotencyKey: "http-order-001"})), 201)
	p := decode[protocol.PollAck](t, send(t, f, "POST", "/bridge/v1/poll", f.device, wire(t, protocol.Poll{SessionID: w.SessionID, Sequence: 1})), 200)
	if len(p.Commands) != 1 || p.Commands[0].OrderID != o.ID || p.Commands[0].Item.NBT != "AQID" {
		t.Fatal(p)
	}
	decode[protocol.PollAck](t, send(t, f, "POST", "/bridge/v1/poll", f.device, wire(t, protocol.Poll{SessionID: w.SessionID, Sequence: 2, Reports: []protocol.Report{{OrderID: o.ID, AttemptID: p.Commands[0].AttemptID, State: "done"}}})), 200)
	got := decode[protocol.Order](t, send(t, f, "GET", base+"/orders/"+o.ID, adminToken, ""), 200)
	if got.State != "done" {
		t.Fatal(got)
	}
}

func TestHTTPRejectsOversizeAndMalformedJSON(t *testing.T) {
	f := setup(t)
	for _, test := range []struct {
		body   string
		status int
	}{{strings.Repeat(" ", 1<<20), 413}, {`{"protocol":1,"surprise":true}`, 400}, {`{} {}`, 400}} {
		r := send(t, f, "POST", "/bridge/v1/hello", f.device, test.body)
		if r.StatusCode != test.status {
			t.Fatalf("status %d, want %d", r.StatusCode, test.status)
		}
	}
	if r := send(t, f, "POST", "/bridge/v1/hello", adminToken, `{}`); r.StatusCode != 401 {
		t.Fatal("member token authorized a device", r.StatusCode)
	}
}

func TestBrowserSessionRequiresCSRF(t *testing.T) {
	f := setup(t)
	r := send(t, f, "POST", "/api/v1/session", "", `{"token":"`+adminToken+`"}`)
	cookies := r.Cookies()
	session := decode[struct {
		CSRF string `json:"csrf"`
	}](t, r, 201)
	if len(cookies) != 1 || !cookies[0].HttpOnly || session.CSRF == "" {
		t.Fatal("missing session protection")
	}
	call := func(csrf, origin string) *http.Response {
		req, _ := http.NewRequest("POST", f.server.URL+"/api/v1/networks", strings.NewReader(`{"name":"new","server_group":"server","source":"oc"}`))
		req.AddCookie(cookies[0])
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", csrf)
		response, err := f.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	if r = call("", "http://localhost"); r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
	if r = call(session.CSRF, "https://unrelated.example"); r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
	if r = call(session.CSRF, "http://localhost"); r.StatusCode != 201 {
		data, _ := io.ReadAll(r.Body)
		t.Fatal(r.StatusCode, string(data))
	}
}

func TestHTTPMemberIsolationAndRevocation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	m, token, err := f.db.CreateMember(ctx, f.admin.ID, "viewer", nil)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/networks/" + f.network.ID + "/items"
	if r := send(t, f, "GET", path, token, ""); r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
	if err = f.db.SetGrant(ctx, f.admin.ID, m.ID, protocol.Grant{NetworkID: f.network.ID}); err != nil {
		t.Fatal(err)
	}
	if r := send(t, f, "GET", path, token, ""); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	if r := send(t, f, "DELETE", "/api/v1/members/"+m.ID, adminToken, ""); r.StatusCode != 204 {
		t.Fatal(r.StatusCode)
	}
	if r := send(t, f, "GET", path, token, ""); r.StatusCode != 401 {
		t.Fatal("revoked token accepted", r.StatusCode)
	}
}

func TestSSEInitialResetAndCommittedEvent(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", f.server.URL+"/api/v1/networks/"+f.network.ID+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	reader := bufio.NewReader(r.Body)
	readEvent := func() string {
		var lines []string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if line == "\n" {
				return strings.Join(lines, "")
			}
			lines = append(lines, line)
		}
	}
	if event := readEvent(); !strings.Contains(event, "event: reset") {
		t.Fatal(event)
	}
	d, err := f.db.AuthDevice(context.Background(), f.device)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Hello(context.Background(), d, protocol.Hello{Protocol: 1, BootID: "sse-boot", Source: "simulator", MaxResponseBytes: 4096}); err != nil {
		t.Fatal(err)
	}
	if event := readEvent(); !strings.Contains(event, "event: change") || !strings.Contains(event, `"kind":"session"`) {
		t.Fatal(event)
	}
}

func TestSSECapacityAppliesBeforeAuthentication(t *testing.T) {
	f := setup(t)
	options := api.DefaultOptions()
	options.MaxStreams = 1
	server := httptest.NewServer(api.New(f.db, options))
	defer server.Close()
	f.server = server
	path := "/api/v1/networks/" + f.network.ID + "/events"
	stream := send(t, f, "GET", path, adminToken, "")
	defer stream.Body.Close()
	if stream.StatusCode != 200 {
		t.Fatal(stream.StatusCode)
	}
	// An exhausted stream budget must reject even an unauthenticated request
	// before it can enqueue another database authentication query.
	if response := send(t, f, "GET", path, "invalid", ""); response.StatusCode != 429 {
		t.Fatalf("stream admission returned %d, want 429", response.StatusCode)
	}
	if response := send(t, f, "GET", "/api/v1/me", adminToken, ""); response.StatusCode != 200 {
		t.Fatal("streams consumed the ordinary request budget", response.StatusCode)
	}
}

func TestDeviceBootNonceDoesNotDependOnOCClock(t *testing.T) {
	f := setup(t)
	a := decode[struct {
		BootID string `json:"boot_id"`
	}](t, send(t, f, "POST", "/bridge/v1/boot-id", f.device, `{}`), 200)
	b := decode[struct {
		BootID string `json:"boot_id"`
	}](t, send(t, f, "POST", "/bridge/v1/boot-id", f.device, `{}`), 200)
	if len(a.BootID) < 32 || a.BootID == b.BootID {
		t.Fatal("boot identities can be reused after a disk/clock rollback")
	}
}

func TestDeviceTokenRotationHTTP(t *testing.T) {
	f := setup(t)
	response := decode[struct {
		Token string `json:"device_token"`
	}](t, send(t, f, "POST", "/api/v1/networks/"+f.network.ID+"/device-token", adminToken, `{}`), 200)
	if len(response.Token) < 32 || response.Token == f.device {
		t.Fatal("missing replacement credential")
	}
	if old := send(t, f, "POST", "/bridge/v1/boot-id", f.device, `{}`); old.StatusCode != 401 {
		t.Fatal("old credential accepted", old.StatusCode)
	}
	if next := send(t, f, "POST", "/bridge/v1/boot-id", response.Token, `{}`); next.StatusCode != 200 {
		t.Fatal("new credential rejected", next.StatusCode)
	}
}

func TestOCInstallationDownloadsExposeOnlyBundledFiles(t *testing.T) {
	f := setup(t)
	response := send(t, f, "GET", "/oc/lib/aeorbit/runner.lua", "", "")
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(data), "/bridge/v1/hello") {
		t.Fatal("OC module unavailable", response.StatusCode, err)
	}
	for _, path := range []string{"/oc/tests/test_runner.lua", "/oc/lib/aeorbit", "/oc/config.live.lua", "/oc/.env"} {
		if response := send(t, f, "GET", path, "", ""); response.StatusCode != 404 {
			t.Fatal("unbundled path was exposed", path, response.StatusCode)
		}
	}
}
