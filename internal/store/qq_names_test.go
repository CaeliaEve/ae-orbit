package store_test

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/nbt"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestQQDispatchUsesInventoryChineseNamesWithoutChangingIdentity(t *testing.T) {
	root := writePlainCatalogFixture(t, 3)
	// This test uses production NESQL identity keys rather than the older
	// enumeration-only fixture's synthetic ids.
	file, err := os.Create(filepath.Join(root, "records/items/part.jsonl.gz"))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	for _, name := range []string{"minecraft:iron_ingot", "minecraft:stone", "minecraft:dye"} {
		meta := 0
		if name == "minecraft:dye" {
			meta = 4
		}
		id, err := nbt.NESQLItemID(name, meta, nil)
		if err != nil {
			t.Fatal(err)
		}
		row := map[string]any{"id": id, "registry": name, "meta": meta, "nbt": nil}
		if name == "minecraft:stone" {
			row["name"] = "text_stone"
		}
		data, _ := json.Marshal(row)
		if _, err = gz.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	f := openPlainCatalogFixture(t, newCooperativeFixture(t), root)
	if err := f.s.SetQQSettings(ctx, f.admin.ID, f.network.ServerGroup, store.QQSettings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	auth := store.QQAuth{Group: f.network.ServerGroup}
	if err := f.s.QQSyncChannels(ctx, auth, store.QQChannelSync{Groups: []store.QQChannel{{ID: "1081348823"}}}); err != nil {
		t.Fatal(err)
	}
	damage := 0
	tagged, untagged := true, false
	payloads := []map[string]any{
		{"item": "Stone", "registry": "minecraft:stone", "quantity": "64", "output": protocol.Stack{Name: "minecraft:stone", Kind: "item", Damage: &damage, HasTag: &untagged, Label: "Stone", Quantity: 64}},
		{"item": "My Stone", "registry": "minecraft:stone", "quantity": "1", "output": protocol.Stack{Name: "minecraft:stone", Kind: "item", Damage: &damage, HasTag: &tagged, Label: "My Stone", Quantity: 1}},
		{"item": "Custom Legacy Stone", "registry": "minecraft:stone", "quantity": "1"},
	}
	for i, p := range payloads {
		raw, _ := json.Marshal(p)
		if _, err := f.s.Pool().Exec(ctx, `SELECT qq_enqueue($1,$2,$3,$4,'cpu_end','1081348823','',false,$5)`, f.network.ServerGroup, f.network.ID, f.admin.ID, []string{"cpu-chinese", "cpu-nbt", "cpu-legacy"}[i], raw); err != nil {
			t.Fatal(err)
		}
	}
	// Exact inventory identity supports localization even if an item's OC label is English.
	known := true
	ack, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2, Updates: []protocol.Update{{Item: &protocol.Definition{Kind: "item", Name: "minecraft:stone", Label: "Stone", Exact: true}, Quantity: 2, Observation: 2, Craftable: &known}}})
	if err != nil || len(ack.Bindings) != 1 {
		t.Fatal(ack, err)
	}
	itemID := ack.Bindings[0].ID
	if _, err = f.s.Pool().Exec(ctx, `SELECT qq_enqueue($1,$2,$3,'web-chinese','craft','1081348823','',false,jsonb_build_object('item_id',$4::text,'item','Stone','registry','minecraft:stone','quantity','9007199254740993'))`, f.network.ServerGroup, f.network.ID, f.admin.ID, itemID.String()); err != nil {
		t.Fatal(err)
	}
	before, err := f.s.QQDeliveries(ctx, f.admin.ID, f.network.ServerGroup)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range before {
		if d.State != "queued" {
			t.Fatal(d)
		}
	}
	enableQQSending(t, f.s, f.admin, f.network)
	poll, err := f.s.QQBridgePoll(ctx, auth, store.QQBridgePoll{BotID: "12345678", Adapter: "snowluma"})
	if err != nil || len(poll.Deliveries) != 4 {
		t.Fatal(poll, err)
	}
	chinese, custom, legacy, exactQuantity := 0, 0, 0, false
	for _, d := range poll.Deliveries {
		var p map[string]any
		if err = json.Unmarshal(d.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p["item"] == "石头" {
			chinese++
		}
		if p["item"] == "My Stone" {
			custom++
		}
		if p["item"] == "Custom Legacy Stone" {
			legacy++
		}
		if p["quantity"] == "9007199254740993" {
			exactQuantity = true
		}
		if p["registry"] != "minecraft:stone" {
			t.Fatal("registry changed", p)
		}
	}
	if chinese != 2 || custom != 1 || legacy != 1 || !exactQuantity {
		t.Fatal(chinese, custom, legacy, exactQuantity)
	}
	var label string
	if err = f.s.Pool().QueryRow(ctx, `SELECT label FROM item_definitions WHERE network_id=$1 AND id=$2`, f.network.ID, int64(itemID)).Scan(&label); err != nil || label != "Stone" {
		t.Fatal("observation rewritten", label, err)
	}
	saved, err := f.s.QQDeliveries(ctx, f.admin.ID, f.network.ServerGroup)
	if err != nil || len(saved) != 4 {
		t.Fatal(saved, err)
	}
	for _, d := range saved {
		if d.State != "leased" {
			t.Fatal(d)
		}
	}
}
