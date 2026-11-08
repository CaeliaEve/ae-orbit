package api_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func browsingFixture(t *testing.T) (fixture, protocol.Welcome, []protocol.Update, []protocol.Quantity) {
	t.Helper()
	f := setup(t)
	w := decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v1/hello", f.device, wire(t, protocol.Hello{
		Protocol: 1, BootID: "browse-1", Source: "simulator", MaxResponseBytes: 32768,
		Capabilities: protocol.Capabilities{Inventory: true, ExactQuantities: true, Enumeration: "simulated"},
	})), 200)
	yes := true
	updates := []protocol.Update{}
	for i, label := range []string{"Alpha", "Beta", "Gamma"} {
		updates = append(updates, protocol.Update{Item: &protocol.Definition{
			Kind: "item", Name: "test:" + label, Label: label, Exact: true, NBT: "H4sIAAAAAAAAA+NiYGAAAHg/+U4EAAAA",
		}, Quantity: protocol.Quantity((i + 1) * 10), Observation: 10, Craftable: &yes})
	}
	ack := uploadBrowse(t, f, w, 1, updates)
	ids := make([]protocol.Quantity, len(ack.Bindings))
	for i, binding := range ack.Bindings {
		ids[i] = binding.ID
	}
	return f, w, updates, ids
}

func uploadBrowse(t *testing.T, f fixture, w protocol.Welcome, sequence protocol.Quantity, updates []protocol.Update) protocol.InventoryAck {
	t.Helper()
	return decode[protocol.InventoryAck](t, send(t, f, "POST", "/bridge/v1/inventory", f.device,
		wire(t, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: sequence, Updates: updates})), 200)
}

func browsePath(f fixture) string { return "/api/v1/networks/" + f.network.ID + "/items" }

func TestHTTPLiveBrowseAcrossQuantityChanges(t *testing.T) {
	f, w, _, ids := browsingFixture(t)
	base := browsePath(f)
	live := decode[protocol.ItemPage](t, send(t, f, "GET", base+"?limit=1&consistency=live", adminToken, ""), 200)
	strict := decode[protocol.ItemPage](t, send(t, f, "GET", base+"?limit=1", adminToken, ""), 200)
	if live.Items[0].ID != ids[0] || live.NextCursor == "" || strict.NextCursor == "" {
		t.Fatal("unexpected first page", live, strict)
	}
	uploadBrowse(t, f, w, 2, []protocol.Update{{ID: ids[1], Quantity: 9007199254740993, Observation: 11}})
	next := decode[protocol.ItemPage](t, send(t, f, "GET", base+"?limit=1&consistency=live&cursor="+url.QueryEscape(live.NextCursor), adminToken, ""), 200)
	if len(next.Items) != 1 || next.Items[0].ID != ids[1] || next.Items[0].Quantity != 9007199254740993 || next.Revision <= live.Revision {
		t.Fatal("live page skipped an item or lost a newer exact quantity", next)
	}
	last := decode[protocol.ItemPage](t, send(t, f, "GET", base+"?consistency=live&cursor="+url.QueryEscape(next.NextCursor), adminToken, ""), 200)
	if len(last.Items) != 1 || last.Items[0].ID != ids[2] || last.NextCursor != "" {
		t.Fatal("live paging repeated or skipped entries", last)
	}
	if r := send(t, f, "GET", base+"?cursor="+url.QueryEscape(strict.NextCursor), adminToken, ""); r.StatusCode != 409 {
		t.Fatal("strict cursor accepted a changed inventory", r.StatusCode)
	}
}

func TestHTTPLiveBrowseInvalidation(t *testing.T) {
	for _, test := range []struct {
		name, query string
		status      int
	}{
		{"new item", "", 409}, {"renamed item", "", 409}, {"old rename", "", 200},
		{"recipe change", "", 200}, {"recipe change", "&craftable=true", 409},
		{"quantity change", "&craftable=true", 200}, {"session change", "", 409},
		{"token rotation", "", 409}, {"replay", "", 200},
	} {
		t.Run(test.name+test.query, func(t *testing.T) {
			f, w, updates, ids := browsingFixture(t)
			base := browsePath(f) + "?consistency=live" + test.query
			first := decode[protocol.ItemPage](t, send(t, f, "GET", base+"&limit=1", adminToken, ""), 200)
			switch test.name {
			case "new item":
				uploadBrowse(t, f, w, 2, []protocol.Update{{Item: &protocol.Definition{Kind: "item", Name: "test:added", Label: "Before Alpha", Exact: true}, Quantity: 1, Observation: 11}})
			case "renamed item", "old rename":
				updates[1].Item.Label = "Before Alpha"
				updates[1].Observation = 11
				if test.name == "old rename" {
					updates[1].Observation = 9
				}
				uploadBrowse(t, f, w, 2, updates[1:2])
			case "recipe change":
				no := false
				// An older count can still carry a newer independent recipe observation.
				uploadBrowse(t, f, w, 2, []protocol.Update{{ID: ids[1], Quantity: 99, Observation: 30}})
				uploadBrowse(t, f, w, 3, []protocol.Update{{ID: ids[1], Quantity: 20, Observation: 20, Craftable: &no}})
			case "quantity change":
				uploadBrowse(t, f, w, 2, []protocol.Update{{ID: ids[1], Quantity: 99, Observation: 11}})
			case "session change":
				decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v1/hello", f.device, wire(t, protocol.Hello{Protocol: 1, BootID: "browse-2", Source: "simulator", MaxResponseBytes: 32768})), 200)
			case "token rotation":
				decode[map[string]string](t, send(t, f, "POST", "/api/v1/networks/"+f.network.ID+"/device-token", adminToken, "{}"), 200)
			case "replay":
				uploadBrowse(t, f, w, 1, updates)
			}
			response := send(t, f, "GET", base+"&cursor="+url.QueryEscape(first.NextCursor), adminToken, "")
			if response.StatusCode != test.status {
				t.Fatalf("cursor status %d, want %d", response.StatusCode, test.status)
			}
			if test.status == 200 {
				page := decode[protocol.ItemPage](t, response, 200)
				if len(page.Items) != 2 || page.Items[0].ID != ids[1] || page.Items[1].ID != ids[2] {
					t.Fatal("valid cursor changed page membership", page)
				}
			}
		})
	}
}

func TestHTTPLiveBrowseValidatesModeCursorAndAccess(t *testing.T) {
	f, _, _, _ := browsingFixture(t)
	base := browsePath(f)
	for _, query := range []string{"?consistency=anything", "?consistency=live&sort=quantity"} {
		if response := send(t, f, "GET", base+query, adminToken, ""); response.StatusCode != 400 {
			t.Fatal("invalid browsing mode accepted", query, response.StatusCode)
		}
	}
	first := decode[protocol.ItemPage](t, send(t, f, "GET", base+"?consistency=live&limit=1", adminToken, ""), 200)
	for _, query := range []string{"?consistency=strict", "?consistency=live&q=Alpha", "?consistency=live&craftable=true"} {
		if response := send(t, f, "GET", base+query+"&cursor="+url.QueryEscape(first.NextCursor), adminToken, ""); response.StatusCode != 400 {
			t.Fatal("cursor reused for a different query", query, response.StatusCode)
		}
	}
	if response := send(t, f, "GET", base+"?consistency=live&cursor="+url.QueryEscape(first.NextCursor+"x"), adminToken, ""); response.StatusCode != 400 {
		t.Fatal("tampered live cursor accepted", response.StatusCode)
	}
	viewer, token, err := f.db.CreateMember(context.Background(), f.admin.ID, "viewer", []protocol.Grant{{NetworkID: f.network.ID}})
	if err != nil {
		t.Fatal(err)
	}
	decode[protocol.ItemPage](t, send(t, f, "GET", base+"?consistency=live&cursor="+url.QueryEscape(first.NextCursor), token, ""), 200)
	if err = f.db.DeleteGrant(context.Background(), f.admin.ID, viewer.ID, f.network.ID); err != nil {
		t.Fatal(err)
	}
	if response := send(t, f, "GET", base+"?consistency=live&cursor="+url.QueryEscape(first.NextCursor), token, ""); response.StatusCode != 403 {
		t.Fatal("a signed cursor bypassed revoked access", response.StatusCode)
	}
}

type lookupResponse struct {
	Items      []protocol.Item     `json:"items"`
	MissingIDs []protocol.Quantity `json:"missing_ids"`
	Revision   protocol.Quantity   `json:"revision"`
}

func TestHTTPItemLookupPreservesOrderPrecisionAndMissing(t *testing.T) {
	f, w, _, ids := browsingFixture(t)
	uploadBrowse(t, f, w, 2, []protocol.Update{{ID: ids[0], Quantity: 0, Observation: 11}, {ID: ids[2], Quantity: 9007199254740993, Observation: 11}})
	path := browsePath(f) + "/lookup?ids=" + ids[2].String() + ",9223372036854775807," + ids[0].String() + "," + ids[2].String()
	result := decode[lookupResponse](t, send(t, f, "GET", path, adminToken, ""), 200)
	if len(result.Items) != 2 || result.Items[0].ID != ids[2] || result.Items[0].Quantity != 9007199254740993 || result.Items[1].ID != ids[0] || result.Items[1].Quantity != 0 {
		t.Fatal("lookup lost exact values or request order", result)
	}
	if !reflect.DeepEqual(result.MissingIDs, []protocol.Quantity{9223372036854775807}) {
		t.Fatal("unknown identity was not distinguished from zero stock", result)
	}
	page := decode[protocol.ItemPage](t, send(t, f, "GET", browsePath(f), adminToken, ""), 200)
	if result.Revision != page.Revision || !reflect.DeepEqual(result.Items, []protocol.Item{page.Items[2], page.Items[0]}) {
		t.Fatal("lookup and listing disagree on inventory evidence", result, page)
	}
	if strings.Contains(wire(t, result), `"nbt"`) {
		t.Fatal("lookup included an unnecessary identity blob")
	}
	missing := decode[lookupResponse](t, send(t, f, "GET", browsePath(f)+"/lookup?ids=9223372036854775807", adminToken, ""), 200)
	if missing.Items == nil || len(missing.Items) != 0 || len(missing.MissingIDs) != 1 {
		t.Fatal("missing-only result used null or invented an item", missing)
	}
	maximum := decode[lookupResponse](t, send(t, f, "GET", browsePath(f)+"/lookup?ids="+strings.TrimSuffix(strings.Repeat(ids[0].String()+",", 200), ","), adminToken, ""), 200)
	if len(maximum.Items) != 1 || maximum.Items[0].ID != ids[0] || len(maximum.MissingIDs) != 0 {
		t.Fatal("maximum valid input was rejected or duplicates were not merged", maximum)
	}
}

func TestHTTPItemLookupBoundsAndIsolation(t *testing.T) {
	f, _, _, ids := browsingFixture(t)
	base := browsePath(f) + "/lookup"
	for _, query := range []string{"", "?ids=", "?ids=0", "?ids=-1", "?ids=%2B1", "?ids=01", "?ids=1,", "?ids=1,,2", "?ids=9223372036854775808", "?ids=1.0", "?ids=1&ids=2", "?ids=" + strings.Repeat("1,", 200) + "1", "?ids=" + strings.Repeat("1", 4000)} {
		if r := send(t, f, "GET", base+query, adminToken, ""); r.StatusCode != 400 {
			t.Fatal("invalid lookup request accepted", len(query), r.StatusCode)
		}
	}
	valid := base + "?ids=" + ids[0].String()
	if r := send(t, f, "GET", valid, "", ""); r.StatusCode != 401 {
		t.Fatal("anonymous lookup accepted", r.StatusCode)
	}
	if r := send(t, f, "GET", valid, f.device, ""); r.StatusCode != 401 {
		t.Fatal("device token accepted as a member", r.StatusCode)
	}
	_, token, err := f.db.CreateMember(context.Background(), f.admin.ID, "ungranted", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := send(t, f, "GET", valid, token, ""); r.StatusCode != 403 {
		t.Fatal("ungranted lookup accepted", r.StatusCode)
	}
	other, _, err := f.db.CreateNetwork(context.Background(), f.admin.ID, "Other", "server-2", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	result := decode[lookupResponse](t, send(t, f, "GET", "/api/v1/networks/"+other.ID+"/items/lookup?ids="+ids[0].String(), adminToken, ""), 200)
	if len(result.Items) != 0 || !reflect.DeepEqual(result.MissingIDs, ids[:1]) {
		t.Fatal("cross-network ID disclosed an item", result)
	}
}

func TestHTTPItemLookupIsReadOnlyAndCompressible(t *testing.T) {
	f, _, _, ids := browsingFixture(t)
	snapshot := func() string {
		t.Helper()
		var data string
		if err := f.db.Pool().QueryRow(context.Background(), `SELECT row_to_json(n)::text ||
            (SELECT row_to_json(b)::text FROM bridge_sessions b WHERE b.id=n.current_session) ||
            (SELECT count(*)::text FROM network_events WHERE network_id=n.id) ||
            (SELECT count(*)::text FROM inventory_rechecks WHERE network_id=n.id)
            FROM networks n WHERE id=$1`, f.network.ID).Scan(&data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	before := snapshot()
	path := browsePath(f) + "/lookup?ids=" + ids[0].String() + "," + ids[1].String()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		request, err := http.NewRequest(method, f.server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+adminToken)
		request.Header.Set("Accept-Encoding", "gzip, identity;q=0")
		response, err := f.server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || response.Header.Get("Content-Encoding") != "gzip" || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("lookup response contract", response.StatusCode, response.Header, err)
		}
		if method == http.MethodHead {
			if len(body) != 0 {
				t.Fatal("HEAD returned a body")
			}
			continue
		}
		reader, err := gzip.NewReader(strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		var result lookupResponse
		if err = json.NewDecoder(reader).Decode(&result); err != nil || len(result.Items) != 2 || len(result.MissingIDs) != 0 {
			t.Fatal("compressed lookup lost its data", result, err)
		}
		reader.Close()
	}
	if after := snapshot(); after != before {
		t.Fatal("reading cached IDs scheduled work or altered device evidence")
	}
}
