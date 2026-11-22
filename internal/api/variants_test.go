package api_test

import (
	"context"
	"encoding/base64"
	"github.com/CaeliaEve/ae-orbit/internal/nbt"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"strings"
	"testing"
)

func TestVariantDetailExposesAuthorizedTypedDataOnly(t *testing.T) {
	f := setup(t)
	raw, err := nbt.EncodeTypedJSON([]byte(`{"type":"compound","value":{"display":{"type":"compound","value":{"Name":{"type":"string","value":"氯.zip"}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	hello := protocol.Hello{Protocol: 1, BootID: "variant-display", Source: "simulator", MaxResponseBytes: 32768, Capabilities: protocol.Capabilities{Inventory: true, ExactQuantities: true}}
	welcome := decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v1/hello", f.device, wire(t, hello)), 200)
	definition := protocol.Definition{Kind: "item", Name: "minecraft:paper", Label: "氯.zip", NBT: base64.StdEncoding.EncodeToString(raw), Exact: true}
	ack := decode[protocol.InventoryAck](t, send(t, f, "POST", "/bridge/v1/inventory", f.device, wire(t, protocol.InventoryBatch{SessionID: welcome.SessionID, Sequence: 1, Updates: []protocol.Update{{Item: &definition, Quantity: 2, Observation: 1}}})), 200)
	base := "/api/v1/networks/" + f.network.ID
	page := decode[protocol.ItemPage](t, send(t, f, "GET", base+"/items", adminToken, ""), 200)
	if len(page.Items) != 1 || !page.Items[0].HasTag || page.Items[0].Variant == nil || page.Items[0].Variant.CustomName != "氯.zip" || strings.Contains(wire(t, page), definition.NBT) {
		t.Fatal(page)
	}
	path := base + "/items/" + ack.Bindings[0].ID.String() + "/variant-data"
	if send(t, f, "GET", path, "", "").StatusCode != 401 {
		t.Fatal("anonymous NBT access")
	}
	value := decode[map[string]any](t, send(t, f, "GET", path, adminToken, ""), 200)
	if !strings.Contains(wire(t, value), "氯.zip") {
		t.Fatal(value)
	}
	other, _, err := f.db.CreateNetwork(context.Background(), f.admin.ID, "other", "other", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	wrong := "/api/v1/networks/" + other.ID + "/items/" + ack.Bindings[0].ID.String() + "/variant-data"
	if send(t, f, "GET", wrong, adminToken, "").StatusCode != 404 {
		t.Fatal("cross-network NBT access")
	}
}
