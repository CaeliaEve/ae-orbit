package api_test

import (
	"context"
	"github.com/CaeliaEve/ae-orbit/internal/api"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCPUOutputCatalogImageWithoutInventoryMutation(t *testing.T) {
	f := setup(t)
	value := catalogFixture(t)
	t.Cleanup(func() { value.Close() })
	options := api.DefaultOptions()
	options.Origin = "http://localhost"
	options.SecureCookies = false
	server := httptest.NewServer(api.NewWithCatalog(f.db, options, value))
	t.Cleanup(server.Close)
	welcome := decode[protocol.Welcome](t, sendServer(t, server, "POST", "/bridge/v1/hello", f.device, wire(t, protocol.Hello{Protocol: 1, BootID: "cpu-texture", Source: "simulator", MaxResponseBytes: 32768, Capabilities: protocol.Capabilities{Inventory: true, CPUOutput: true, ExactQuantities: true}})), 200)
	damage := 0
	untagged := false
	tagged := true
	cpus := []protocol.CPU{
		{ID: "1:1", Name: "Main", Busy: true, Storage: 4096, Output: &protocol.Stack{Name: "minecraft:stone", Label: "Stone", Quantity: 64, Kind: "item", Damage: &damage, HasTag: &untagged}},
		{ID: "1:2", Name: "Tagged", Busy: true, Storage: 4096, Output: &protocol.Stack{Name: "minecraft:stone", Label: "Custom Stone", Quantity: 1, Kind: "item", Damage: &damage, HasTag: &tagged}},
		{ID: "1:3", Name: "Legacy", Busy: true, Storage: 4096, Output: &protocol.Stack{Name: "minecraft:stone", Label: "Stone", Quantity: 2}},
	}
	decode[protocol.PollAck](t, sendServer(t, server, "POST", "/bridge/v1/poll", f.device, wire(t, protocol.Poll{SessionID: welcome.SessionID, Sequence: 1, CPUs: &cpus})), 200)
	base := "/api/v1/networks/" + f.network.ID
	view := decode[store.CPUView](t, sendServer(t, server, "GET", base+"/cpus", adminToken, ""), 200)
	if len(view.CPUs) != 3 || view.CPUs[0].Output.Catalog == nil || view.CPUs[0].Output.Catalog.ImageURL == "" || view.CPUs[1].Output.Catalog != nil || view.CPUs[2].Output.Catalog == nil {
		t.Fatal("output catalog mismatch", view)
	}
	if view.CPUs[0].Output.Quantity != 64 || view.CPUs[0].Output.Catalog.LocalizedName != "石头" {
		t.Fatal(view)
	}
	imageURL := view.CPUs[0].Output.Catalog.ImageURL
	response := sendServer(t, server, "GET", imageURL, adminToken, "")
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || len(data) != 4 || response.Header.Get("Content-Type") != "image/png" {
		t.Fatal(response.StatusCode, data, err)
	}
	if sendServer(t, server, "GET", imageURL, "", "").StatusCode != http.StatusUnauthorized {
		t.Fatal("unauthenticated texture exposed")
	}
	if sendServer(t, server, "GET", imageURL+"-wrong", adminToken, "").StatusCode != http.StatusNotFound {
		t.Fatal("wrong asset version accepted")
	}
	stored, err := f.db.CPUs(context.Background(), f.admin.ID, f.network.ID)
	if err != nil || stored.CPUs[0].Output.Catalog != nil || stored.CPUs[2].Output.Kind != "" {
		t.Fatal("visual enrichment persisted", stored, err)
	}
	var definitions int
	if err = f.db.Pool().QueryRow(context.Background(), "SELECT count(*) FROM item_definitions WHERE network_id=$1", f.network.ID).Scan(&definitions); err != nil || definitions != 0 {
		t.Fatal("CPU texture created inventory identities", definitions, err)
	}
	// An authorized viewer of another network cannot use this output URL.
	other, _, err := f.db.CreateNetwork(context.Background(), f.admin.ID, "Other", "other-server", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	foreignURL := "/api/v1/networks/" + other.ID + "/catalog/cpus/minecraft:stone@0/image?v=item%2Fminecraft%2Fstone~0.png"
	if sendServer(t, server, "GET", foreignURL, adminToken, "").StatusCode != http.StatusNotFound {
		t.Fatal("foreign snapshot texture exposed")
	}
	// Once the output disappears an uncached request must not return stale art.
	empty := []protocol.CPU{{ID: "2:1", Name: "Main", Storage: 4096}}
	decode[protocol.PollAck](t, sendServer(t, server, "POST", "/bridge/v1/poll", f.device, wire(t, protocol.Poll{SessionID: welcome.SessionID, Sequence: 2, CPUs: &empty})), 200)
	if sendServer(t, server, "GET", imageURL, adminToken, "").StatusCode != http.StatusNotFound {
		t.Fatal("stale output texture accepted")
	}
}
