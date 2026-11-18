package store_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func scheduledPlainFixture(t *testing.T, count int) cooperativeFixture {
	t.Helper()
	f := plainFixtureCount(t, count)
	if _, err := f.s.EnablePlainCatalog(ctx, f.admin.ID, f.network.ID); err != nil {
		t.Fatal(err)
	}
	f.wb = safePlainHello(t, f, f.b, "scheduled-worker")
	return f
}

func pollPlainReads(t *testing.T, f cooperativeFixture, device store.Device, welcome protocol.Welcome, sequence *protocol.Quantity, slots, bytes int) protocol.PollAck {
	t.Helper()
	for range 3 {
		*sequence++
		f.round(t)
		ack, err := f.s.PollV2(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: *sequence, ReadSlots: &slots, ReadBytes: &bytes})
		if err != nil {
			t.Fatal(err)
		}
		if len(ack.Reads) > 0 {
			return ack
		}
	}
	t.Fatal("registered device received no reads")
	return protocol.PollAck{}
}

func seedPlainStock(t *testing.T, f cooperativeFixture, positive int) (map[protocol.Quantity]bool, protocol.Quantity) {
	t.Helper()
	slots, bytes := 16, 16384
	var sequence protocol.Quantity
	ack := pollPlainReads(t, f, f.b, f.wb, &sequence, slots, bytes)
	if len(ack.Reads) < positive {
		t.Fatal("not enough seed reads", ack)
	}
	updates := []protocol.Update{}
	stock := map[protocol.Quantity]bool{}
	for i, read := range ack.Reads {
		q := protocol.Quantity(0)
		if i < positive {
			q = 50
			stock[read.ID] = true
		}
		updates = append(updates, protocol.Update{ID: read.ID, Quantity: q, Observation: protocol.Quantity(i + 1), AssignmentID: read.AssignmentID})
	}
	if _, err := f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: 1, Updates: updates}); err != nil {
		t.Fatal(err)
	}
	return stock, sequence
}

func TestPlainSchedulePrimaryFillsEveryColdPollWithinSmallPacket(t *testing.T) {
	f := scheduledPlainFixture(t, 40)
	// Large display labels must stay on the backend, rather than occupying
	// the small client's identity queue on every exact read.
	if _, err := f.s.Pool().Exec(ctx, "UPDATE item_definitions SET label=repeat('物',160) WHERE network_id=$1", f.network.ID); err != nil {
		t.Fatal(err)
	}
	caps := protocol.Capabilities{Inventory: true, ExactQuantities: true, QuantityRefresh: true,
		NativeScanDisabled: true, EventsDisabled: true, Enumeration: "exact_only"}
	w, err := f.s.HelloV2(ctx, f.a, protocol.Hello{Protocol: 2, BootID: "small-packet-primary", Source: "simulator", MaxResponseBytes: 4096, Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	slots, bytes := 16, 4096
	var sequence protocol.Quantity
	ack := pollPlainReads(t, f, f.a, w, &sequence, slots, bytes)
	for poll := 0; poll < 2; poll++ {
		if poll > 0 {
			sequence++
			f.round(t)
			ack, err = f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: w.SessionID, Sequence: sequence, ReadSlots: &slots, ReadBytes: &bytes})
		}
		if err != nil || len(ack.Reads) < 5 {
			t.Fatal("cold discovery wasted a poll or retained display labels", ack, err)
		}
		encoded, _ := json.Marshal(ack)
		if len(encoded) > 4096 {
			t.Fatal("negotiated packet budget exceeded", len(encoded))
		}
		for _, read := range ack.Reads {
			if read.Kind != "quantity" || read.Item.Label != "" || read.Item.NBT != "" || !read.Item.Exact {
				t.Fatal("unexpected exact read", read)
			}
		}
	}
	page, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{View: "all", Search: "石"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Label != "石头" {
		t.Fatal("wire compaction changed the Chinese catalog label", page, err)
	}
}

func TestPlainScheduleRecipeDoesNotBlockQuantityOrQueueMoreRecipes(t *testing.T) {
	f := scheduledPlainFixture(t, 40)
	seedPlainStock(t, f, 3)
	f.wa = safePlainHello(t, f, f.a, "mixed-primary")
	slots, bytes := 16, 16384
	var sequence protocol.Quantity
	ack := pollPlainReads(t, f, f.a, f.wa, &sequence, slots, bytes)
	for poll := 0; poll < 2; poll++ {
		if poll > 0 {
			sequence++
			f.round(t)
			var err error
			ack, err = f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: sequence, ReadSlots: &slots, ReadBytes: &bytes})
			if err != nil {
				t.Fatal(err)
			}
		}
		quantity, recipe := 0, 0
		for _, read := range ack.Reads {
			if read.Kind == "quantity_recipe" {
				recipe++
			} else {
				quantity++
			}
		}
		wantRecipe := 0
		if poll == 0 {
			wantRecipe = 1
		}
		if quantity == 0 || recipe != wantRecipe {
			t.Fatal("recipe work blocked discovery or accumulated queued recipes", sequence, quantity, recipe)
		}
	}
}

func TestPlainScheduleHotPriorityRetainsColdProgressAndObservationTimers(t *testing.T) {
	f := scheduledPlainFixture(t, 40)
	stock, sequence := seedPlainStock(t, f, 8)
	var positive, zero float64
	if err := f.s.Pool().QueryRow(ctx, `SELECT min(extract(epoch FROM(q.next_quantity_at-i.observed_at))) FILTER(WHERE i.quantity>0),
		min(extract(epoch FROM(q.next_quantity_at-i.observed_at))) FILTER(WHERE i.quantity=0)
		FROM inventory_schedule q JOIN inventory i ON i.network_id=q.network_id AND i.item_id=q.item_id
		WHERE q.network_id=$1`, f.network.ID).Scan(&positive, &zero); err != nil || positive != 30 || zero != 3600 {
		t.Fatal("wrong observed-stock scheduling", positive, zero, err)
	}
	if _, err := f.s.Pool().Exec(ctx, "UPDATE cooperative_rotation SET plain_quantity_turn=0 WHERE network_id=$1", f.network.ID); err != nil {
		t.Fatal(err)
	}
	slots, bytes := 1, 16384
	hot, cold := 0, 0
	for poll := 0; poll < 4; poll++ {
		sequence++
		if _, err := f.s.Pool().Exec(ctx, `UPDATE inventory_schedule q SET next_quantity_at=now()-interval '1 second'
			FROM inventory i WHERE i.network_id=q.network_id AND i.item_id=q.item_id AND q.network_id=$1 AND i.quantity>0`, f.network.ID); err != nil {
			t.Fatal(err)
		}
		f.round(t)
		ack, err := f.s.PollV2(ctx, f.b, protocol.Poll{SessionID: f.wb.SessionID, Sequence: sequence, ReadSlots: &slots, ReadBytes: &bytes})
		if err != nil || len(ack.Reads) != 1 {
			t.Fatal(ack, err)
		}
		read := ack.Reads[0]
		q := protocol.Quantity(0)
		if stock[read.ID] {
			hot++
			q = 50
		} else {
			cold++
		}
		if _, err := f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: protocol.Quantity(poll + 2),
			Updates: []protocol.Update{{ID: read.ID, Quantity: q, Observation: sequence + 100, AssignmentID: read.AssignmentID}}}); err != nil {
			t.Fatal(err)
		}
	}
	if hot != 3 || cold != 1 {
		t.Fatal("one-slot hot clients starved the candidate sweep", hot, cold)
	}
}

func TestPlainScheduleUpgradePreservesInventoryAndDiscoveryProgress(t *testing.T) {
	f := scheduledPlainFixture(t, 3)
	seedPlainStock(t, f, 1)
	before, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{View: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Pool().Exec(ctx, `UPDATE inventory_schedule q SET next_quantity_at=i.observed_at+interval '5 minutes'
		FROM inventory i WHERE i.network_id=q.network_id AND i.item_id=q.item_id AND q.network_id=$1`, f.network.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Pool().Exec(ctx, `ALTER TABLE cooperative_rotation DROP COLUMN plain_quantity_turn;
		DELETE FROM schema_versions WHERE version='migrations/016_plain_stock_schedule.sql'`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := f.s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	after, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{View: "all"})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("upgrade altered quantities, observation evidence or candidate progress", before, after, err)
	}
}
