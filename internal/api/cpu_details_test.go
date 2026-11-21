package api_test

import (
	"context"
	"encoding/json"
	"github.com/CaeliaEve/ae-orbit/internal/api"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCPUDetailAPICatalogAndAuthorization(t *testing.T) {
	f := setup(t)
	if err := f.db.EnableCooperation(context.Background(), f.admin.ID, f.network.ID); err != nil {
		t.Fatal(err)
	}
	catalog := catalogFixture(t)
	t.Cleanup(func() { _ = catalog.Close() })
	options := api.DefaultOptions()
	options.Origin = "http://localhost"
	options.SecureCookies = false
	server := httptest.NewServer(api.NewWithCatalog(f.db, options, catalog))
	t.Cleanup(server.Close)
	w := decode[protocol.Welcome](t, sendServer(t, server, "POST", "/bridge/v2/hello", f.device, wire(t, protocol.Hello{Protocol: 2, BootID: "details", Source: "simulator", MaxResponseBytes: 32768, Capabilities: protocol.Capabilities{Inventory: true, ExactQuantities: true, CPUOutput: true, CPUDetails: true}})), 200)
	cpus := []protocol.CPU{{ID: "1:1", Name: "", Busy: true}}
	decode[protocol.PollAck](t, sendServer(t, server, "POST", "/bridge/v2/poll", f.device, wire(t, protocol.Poll{SessionID: w.SessionID, Sequence: 1, CPUs: &cpus})), 200)
	base := "/api/v1/networks/" + f.network.ID
	in := store.CPUDetailInput{SessionID: w.SessionID, CPUID: "1:1", ViewerID: strings.Repeat("a", 32)}
	if r := sendServer(t, server, "POST", base+"/cpu-details", "", wire(t, in)); r.StatusCode != http.StatusUnauthorized {
		t.Fatal(r.StatusCode)
	}
	view := decode[store.CPUDetailView](t, sendServer(t, server, "POST", base+"/cpu-details", adminToken, wire(t, in)), 200)
	if view.Status != "collecting" {
		t.Fatal(view)
	}
	damage := 0
	tagged := true
	items := []protocol.CPUDetailItem{{Stack: protocol.Stack{Kind: "item", Name: "minecraft:stone", Label: "Custom Stone", Damage: &damage, HasTag: &tagged, Quantity: 64}, RowID: "row"}}
	encoded, _ := json.Marshal(items)
	_, err := f.db.Pool().Exec(context.Background(), `INSERT INTO cpu_detail_snapshots(network_id,session_id,cpu_id,kind,items,total,observed_at) VALUES($1,$2,$3,'active',$4,1,now())`, f.network.ID, w.SessionID, in.CPUID, encoded)
	if err != nil {
		t.Fatal(err)
	}
	view = decode[store.CPUDetailView](t, sendServer(t, server, "POST", base+"/cpu-details", adminToken, wire(t, in)), 200)
	item := view.Parts[0].Items[0]
	if item.Catalog == nil || !item.Catalog.BaseFallback || item.Catalog.LocalizedName != "Custom Stone" || item.Catalog.ImageURL == "" {
		t.Fatal(item)
	}
	r := sendServer(t, server, "GET", item.Catalog.ImageURL, adminToken, "")
	data, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil || r.StatusCode != 200 || len(data) != 4 {
		t.Fatal(r.StatusCode, data, err)
	}
	if r := sendServer(t, server, "GET", item.Catalog.ImageURL, "", ""); r.StatusCode != 401 {
		t.Fatal("private image exposed", r.StatusCode)
	}
	if r := sendServer(t, server, "GET", item.Catalog.ImageURL+"wrong", adminToken, ""); r.StatusCode != 404 {
		t.Fatal("wrong version served", r.StatusCode)
	}
	var count int
	f.db.Pool().QueryRow(context.Background(), `SELECT count(*) FROM item_definitions WHERE network_id=$1`, f.network.ID).Scan(&count)
	if count != 0 {
		t.Fatal("CPU detail mutated inventory", count)
	}
	if r := sendServer(t, server, "DELETE", base+"/cpu-details/"+in.ViewerID, adminToken, ""); r.StatusCode != 204 {
		t.Fatal(r.StatusCode)
	}
	f.db.Pool().Exec(context.Background(), `UPDATE cpu_detail_snapshots SET observed_at=now()-interval '3 minutes' WHERE network_id=$1`, f.network.ID)
	if r := sendServer(t, server, "GET", item.Catalog.ImageURL, adminToken, ""); r.StatusCode != 404 {
		t.Fatal("stale image served", r.StatusCode)
	}
}
