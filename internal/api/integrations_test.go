package api_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestIntegrationHTTPReadOnlyCachedQueries(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	input := store.IntegrationKeyInput{Name: "Dashboard", Scopes: []string{"inventory.read", "cpu.read", "energy.read", "subnet.read"}, Networks: []string{f.network.ID}, Groups: []string{f.network.ServerGroup}, Enabled: true, ExpiresDays: 30}
	created := decode[struct {
		Key   store.IntegrationKey `json:"key"`
		Token string               `json:"token"`
	}](t, send(t, f, "POST", "/api/v1/integrations", adminToken, wire(t, input)), 201)
	check := decode[map[string]any](t, send(t, f, "GET", "/api/integration/v1/check", created.Token, ""), 200)
	if check["read_only"] != true || check["source"] != "cached_observations" {
		t.Fatal(check)
	}
	for _, path := range []string{"/api/v1/me", "/api/v1/integrations", "/bridge/qq/v1/inventory/keywords"} {
		r := send(t, f, "GET", path, created.Token, "")
		if r.StatusCode != 401 {
			t.Fatal("key entered privileged API", path, r.StatusCode)
		}
	}
	if r := send(t, f, "GET", "/api/integration/v1/check", adminToken, ""); r.StatusCode != 401 {
		t.Fatal("member token entered dedicated API")
	}
	_, cpuToken, err := f.db.CreateIntegrationKey(ctx, f.admin.ID, store.IntegrationKeyInput{Name: "CPU only 2", Scopes: []string{"cpu.read"}, Networks: []string{f.network.ID}, Enabled: true, ExpiresDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if r := send(t, f, "GET", "/api/integration/v1/networks/"+f.network.ID+"/items", cpuToken, ""); r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
	other, _, err := f.db.CreateNetwork(ctx, f.admin.ID, "other", "other", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if r := send(t, f, "GET", "/api/integration/v1/networks/"+other.ID+"/items", created.Token, ""); r.StatusCode != 403 {
		t.Fatal("network scope bypass", r.StatusCode)
	}
	if r := send(t, f, "GET", "/api/integration/v1/groups/other/energy", created.Token, ""); r.StatusCode != 403 {
		t.Fatal("group scope bypass")
	}
	d, err := f.db.AuthDevice(ctx, f.device)
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.db.Hello(ctx, d, protocol.Hello{Protocol: 1, BootID: "integration-test", Source: "simulator", MaxResponseBytes: 32768, Capabilities: protocol.Capabilities{Inventory: true, ExactQuantities: true, Enumeration: "simulated"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.db.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 1, Updates: []protocol.Update{{Item: &protocol.Definition{Kind: "item", Name: "minecraft:stone", Label: "石头", Exact: true}, Quantity: 9007199254740993, Observation: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/integration/v1/networks/" + f.network.ID
	current := decode[map[string]any](t, send(t, f, "GET", base+"/items", created.Token, ""), 200)
	items := current["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["quantity"] != "9007199254740993" || items[0].(map[string]any)["known"] != true {
		t.Fatal(current)
	}
	if _, err = f.db.Pool().Exec(ctx, `UPDATE inventory SET observed_at=now()-interval '1 hour' WHERE network_id=$1`, f.network.ID); err != nil {
		t.Fatal(err)
	}
	stale := decode[map[string]any](t, send(t, f, "GET", base+"/items", created.Token, ""), 200)
	item := stale["items"].([]any)[0].(map[string]any)
	if item["quantity"] != nil || item["last_quantity"] != "9007199254740993" || item["known"] != false {
		t.Fatal(item)
	}
	for _, path := range []string{base + "/cpus", "/api/integration/v1/groups/" + f.network.ServerGroup + "/energy", "/api/integration/v1/groups/" + f.network.ServerGroup + "/subnets"} {
		decode[any](t, send(t, f, "GET", path, created.Token, ""), 200)
	}
	if r := send(t, f, "POST", base+"/items", created.Token, "{}"); r.StatusCode != 405 {
		t.Fatal("write method exposed", r.StatusCode)
	}
	var count int
	if err = f.db.Pool().QueryRow(ctx, `SELECT (SELECT count(*) FROM orders)+(SELECT count(*) FROM inventory_refreshes)`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("API triggered game work")
	}
	rows := send(t, f, "GET", "/api/v1/integrations", adminToken, "")
	body, _ := io.ReadAll(rows.Body)
	if strings.Contains(string(body), created.Token) || strings.Contains(string(body), "token_hash") {
		t.Fatal("secret leaked in listing")
	}
}
