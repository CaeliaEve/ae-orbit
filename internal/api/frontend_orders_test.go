package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/api"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func TestHTTPFrontendMountAndAPIBoundaries(t *testing.T) {
	server := api.New(nil, api.DefaultOptions())
	for _, tc := range []struct {
		path string
		code int
	}{
		{"/", http.StatusOK},
		{"/app.js", http.StatusOK},
		{"/style.css", http.StatusOK},
		{"/api/v1/not-a-route", http.StatusNotFound},
		{"/bridge/v1/not-a-route", http.StatusNotFound},
		{"/oc/tests/test_runner.lua", http.StatusNotFound},
		{"/missing.js", http.StatusNotFound},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			server.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != tc.code {
				t.Fatalf("HTTP %d, want %d", w.Code, tc.code)
			}
			if tc.code == http.StatusOK {
				if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
					t.Fatal("frontend response lost its CSP")
				}
			} else if strings.Contains(w.Body.String(), "<html") {
				t.Fatal("unknown route returned frontend HTML")
			}
		})
	}
}

func TestHTTPOrderByKeyReconcilesOwnIntentWithoutCreatingOrders(t *testing.T) {
	f := setup(t)
	hello := protocol.Hello{
		Protocol: 1, BootID: "batch-http-boot", Source: "simulator", MaxResponseBytes: 32768,
		Capabilities: protocol.Capabilities{
			Inventory: true, Craft: true, ExactQuantities: true,
			MaxCraftQuantity: 9223372036854775807, Enumeration: "simulated",
		},
	}
	welcome := decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v1/hello", f.device, wire(t, hello)), 200)
	craftable := true
	batch := protocol.InventoryBatch{
		SessionID: welcome.SessionID, Sequence: 1,
		Updates: []protocol.Update{{
			Item: &protocol.Definition{
				Kind: "item", Name: "minecraft:stone", Label: "Stone", Exact: true,
			},
			Quantity: 100, Observation: 1, Craftable: &craftable,
		}},
	}
	ack := decode[protocol.InventoryAck](t, send(t, f, "POST", "/bridge/v1/inventory", f.device, wire(t, batch)), 200)
	input := protocol.OrderInput{
		ItemID: ack.Bindings[0].ID, Quantity: 12,
		IdempotencyKey: "http-batch-order-001", BatchID: "http-batch-001",
	}
	base := "/api/v1/networks/" + f.network.ID
	created := decode[protocol.Order](t, send(t, f, "POST", base+"/orders", adminToken, wire(t, input)), 201)
	path := base + "/orders/by-key/" + input.IdempotencyKey
	if r := send(t, f, "GET", path, "", ""); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated lookup returned %d", r.StatusCode)
	}
	found := decode[protocol.Order](t, send(t, f, "GET", path, adminToken, ""), 200)
	if found.ID != created.ID || found.BatchID != input.BatchID {
		t.Fatal("lookup did not reconcile the original batch", found)
	}
	history := decode[[]struct {
		State string `json:"state"`
	}](t, send(t, f, "GET", base+"/orders/"+created.ID+"/events", adminToken, ""), 200)
	if len(history) != 1 || history[0].State != "queued" {
		t.Fatal("order events route was shadowed by reconciliation", history)
	}
	for _, tc := range []struct {
		key  string
		code int
	}{
		{"unknown-order-001", http.StatusNotFound},
		{"short", http.StatusBadRequest},
	} {
		if r := send(t, f, "GET", base+"/orders/by-key/"+tc.key, adminToken, ""); r.StatusCode != tc.code {
			t.Fatalf("lookup %q returned %d, want %d", tc.key, r.StatusCode, tc.code)
		}
	}
	_, viewerToken, err := f.db.CreateMember(context.Background(), f.admin.ID, "batch viewer", []protocol.Grant{{NetworkID: f.network.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if r := send(t, f, "GET", path, viewerToken, ""); r.StatusCode != http.StatusNotFound {
		t.Fatalf("another member's intent returned %d", r.StatusCode)
	}
	login := send(t, f, "POST", "/api/v1/session", "", `{"token":"`+adminToken+`"}`)
	cookies := login.Cookies()
	decode[struct {
		CSRF string `json:"csrf"`
	}](t, login, http.StatusCreated)
	if len(cookies) != 1 {
		t.Fatal("missing browser session cookie")
	}
	req, err := http.NewRequest(http.MethodGet, f.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookies[0])
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	fromCookie := decode[protocol.Order](t, response, http.StatusOK)
	if fromCookie.ID != created.ID {
		t.Fatal("read-only browser lookup required mutation credentials")
	}
	orders := decode[[]protocol.Order](t, send(t, f, "GET", base+"/orders", adminToken, ""), 200)
	if len(orders) != 1 || orders[0].ID != created.ID {
		t.Fatal("lookup changed the submitted queue", orders)
	}
}
