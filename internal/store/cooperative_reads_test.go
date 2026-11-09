package store_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

type cooperativeFixture struct {
	s       *store.Store
	admin   protocol.Member
	network protocol.Network
	a, b    store.Device
	wa, wb  protocol.Welcome
	item    protocol.Definition
	id      protocol.Quantity
}

func newCooperativeFixture(t *testing.T) cooperativeFixture {
	t.Helper()
	s, admin, n, token := fixture(t)
	_, workerToken, err := s.CreateDevice(ctx, admin.ID, n.ID, "quantity reader")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.EnableCooperation(ctx, admin.ID, n.ID); err != nil {
		t.Fatal(err)
	}
	connect := func(token, boot string) (store.Device, protocol.Welcome) {
		t.Helper()
		d, err := s.AuthDevice(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		caps := protocol.Capabilities{Inventory: true, ExactQuantities: true, QuantityRefresh: true}
		if d.Role == "primary" {
			caps.Craft, caps.CPUOutput = true, true
			caps.MaxCraftQuantity, caps.Enumeration = 9223372036854775807, "simulated"
		}
		w, err := s.HelloV2(ctx, d, protocol.Hello{Protocol: 2, BootID: boot, Source: "simulator", MaxResponseBytes: 32768, Capabilities: caps})
		if err != nil {
			t.Fatal(err)
		}
		return d, w
	}
	a, wa := connect(token, "cooperative-primary")
	b, wb := connect(workerToken, "cooperative-worker")
	item := protocol.Definition{Kind: "item", Name: "minecraft:iron_ingot", Label: "Iron Ingot", Exact: true}
	craftable := true
	seed, err := s.IngestV2(ctx, a, protocol.InventoryBatch{SessionID: wa.SessionID, Sequence: 1,
		Updates: []protocol.Update{{Item: &item, Quantity: 10, Observation: 1, Craftable: &craftable}}})
	if err != nil || len(seed.Bindings) != 1 {
		t.Fatal("primary discovery did not seed inventory", seed, err)
	}
	return cooperativeFixture{s: s, admin: admin, network: n, a: a, b: b, wa: wa, wb: wb, item: item, id: seed.Bindings[0].ID}
}

func (f cooperativeFixture) round(t *testing.T) {
	t.Helper()
	if _, err := f.s.Pool().Exec(ctx, "UPDATE server_groups SET next_scan_at=now()-interval '1 millisecond' WHERE name=$1", f.network.ServerGroup); err != nil {
		t.Fatal(err)
	}
}

func (f cooperativeFixture) request(t *testing.T) protocol.ItemRefresh {
	t.Helper()
	got, err := f.s.RequestItemRefresh(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{f.id})
	if err != nil || len(got.Refreshes) != 1 {
		t.Fatal("refresh admission failed", got, err)
	}
	return got.Refreshes[0]
}

func (f cooperativeFixture) read(t *testing.T, device store.Device, welcome protocol.Welcome, sequence *protocol.Quantity) protocol.ReadAssignment {
	t.Helper()
	slots, queueBytes := 16, 16384
	for range 3 {
		*sequence++
		ack, err := f.s.PollV2(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: *sequence, ReadSlots: &slots, ReadBytes: &queueBytes})
		if err != nil {
			t.Fatal(err)
		}
		for _, read := range ack.Reads {
			if read.ID == f.id {
				return read
			}
		}
		f.round(t)
	}
	t.Fatal("eligible device never received an exact read")
	return protocol.ReadAssignment{}
}

func (f cooperativeFixture) value(t *testing.T) protocol.Item {
	t.Helper()
	got, err := f.s.ItemsByID(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{f.id})
	if err != nil || len(got.Items) != 1 {
		t.Fatal(got, err)
	}
	return got.Items[0]
}

func TestCooperativeReceiptPreservesIndependentRecipeEvidence(t *testing.T) {
	f := newCooperativeFixture(t)
	before := f.value(t)
	f.request(t)
	var poll protocol.Quantity
	read := f.read(t, f.b, f.wb, &poll)
	if read.Kind != "quantity" || read.Purpose != "refresh" || len(read.AssignmentID) != 32 {
		t.Fatal("worker received wrong task", read)
	}
	batch := protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: 1, Updates: []protocol.Update{{
		ID: f.id, Quantity: 12, Observation: 1, AssignmentID: read.AssignmentID,
	}}}
	if _, err := f.s.IngestV2(ctx, f.b, batch); err != nil {
		t.Fatal(err)
	}
	after := f.value(t)
	if after.Quantity != 12 || !after.Craftable || !after.CurrentSession || !after.CraftableCurrentSession ||
		after.CraftableObservedAt == nil || !after.CraftableObservedAt.Equal(*before.CraftableObservedAt) {
		t.Fatal("quantity worker replaced recipe provenance", before, after)
	}
	var recipeSession, quantitySession string
	if err := f.s.Pool().QueryRow(ctx, "SELECT session_id,craftable_session_id FROM inventory WHERE network_id=$1 AND item_id=$2", f.network.ID, int64(f.id)).Scan(&quantitySession, &recipeSession); err != nil ||
		quantitySession != f.wb.SessionID || recipeSession != f.wa.SessionID {
		t.Fatal("sources were not stored independently", quantitySession, recipeSession, err)
	}
}

func TestCooperativeReceiptRejectsSupersededAttemptBeforeReplacementArrives(t *testing.T) {
	f := newCooperativeFixture(t)
	request := f.request(t)
	var bPoll, aPoll protocol.Quantity
	old := f.read(t, f.b, f.wb, &bPoll)
	if _, err := f.s.Pool().Exec(ctx, "UPDATE cooperative_read_fields SET expires_at=now()-interval '1 second' WHERE network_id=$1", f.network.ID); err != nil {
		t.Fatal(err)
	}
	f.round(t)
	current := f.read(t, f.a, f.wa, &aPoll)
	if current.AssignmentID == old.AssignmentID {
		t.Fatal("reassignment reused the retired attempt")
	}
	if _, err := f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: 1,
		Updates: []protocol.Update{{ID: f.id, Quantity: 7, Observation: 999, AssignmentID: old.AssignmentID}}}); err != nil {
		t.Fatal("expired task blocked transport acknowledgement", err)
	}
	if f.value(t).Quantity != 10 {
		t.Fatal("superseded task wrote before the replacement arrived")
	}
	got, err := f.s.ItemsByID(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{f.id})
	if err != nil || got.Refreshes[0].RequestID != request.RequestID || got.Refreshes[0].State != "pending" {
		t.Fatal("reassignment changed or confirmed the logical request", got, err)
	}
	if _, err = f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2,
		Updates: []protocol.Update{{ID: f.id, Quantity: 12, Observation: 2, AssignmentID: current.AssignmentID}}}); err != nil {
		t.Fatal(err)
	}
	if f.value(t).Quantity != 12 {
		t.Fatal("current assignment was not accepted")
	}
}

func TestCooperativeDiscoveryCannotOverwriteKnownCanonicalEvidence(t *testing.T) {
	f := newCooperativeFixture(t)
	f.request(t)
	var poll protocol.Quantity
	read := f.read(t, f.b, f.wb, &poll)
	if _, err := f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: 1,
		Updates: []protocol.Update{{ID: f.id, Quantity: 12, Observation: 1, AssignmentID: read.AssignmentID}}}); err != nil {
		t.Fatal(err)
	}
	before := f.value(t)
	uncraftable := false
	alias := f.item
	alias.Label = "New metadata"
	if _, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2,
		Updates: []protocol.Update{{Item: &alias, Quantity: 3, Observation: 999, Craftable: &uncraftable}}}); err != nil {
		t.Fatal(err)
	}
	after := f.value(t)
	if after.Quantity != 12 || !after.Craftable || !after.ObservedAt.Equal(before.ObservedAt) ||
		!after.CraftableObservedAt.Equal(*before.CraftableObservedAt) || after.Label != "New metadata" {
		t.Fatal("delayed discovery overwrote qualified fields", before, after)
	}
}

func TestCooperativeReceiptRejectsForgedAndWorkerPrivilegedWrites(t *testing.T) {
	f := newCooperativeFixture(t)
	invalid := []protocol.InventoryBatch{
		{SessionID: f.wb.SessionID, Sequence: 1, Updates: []protocol.Update{{Item: &f.item, Quantity: 1, Observation: 1}}},
		{SessionID: f.wb.SessionID, Sequence: 1, ScanCompleted: true},
		{SessionID: f.wb.SessionID, Sequence: 1, Updates: []protocol.Update{{ID: f.id, Quantity: 1, Observation: 1, AssignmentID: strings.Repeat("f", 32)}}},
	}
	for _, batch := range invalid {
		if _, err := f.s.IngestV2(ctx, f.b, batch); err == nil {
			t.Fatal("worker privilege or forged assignment was accepted", batch)
		}
	}
	empty := []protocol.CPU{}
	if _, err := f.s.PollV2(ctx, f.b, protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1, CPUs: &empty}); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("worker CPU write was not forbidden", err)
	}
	if f.value(t).Quantity != 10 {
		t.Fatal("rejected worker write changed stock")
	}
}

func TestCooperativeRefreshCannotAdoptAlreadyDispatchedBackgroundRead(t *testing.T) {
	f := newCooperativeFixture(t)
	if _, err := f.s.Pool().Exec(ctx, "UPDATE inventory_schedule SET next_quantity_at=now()-interval '1 second' WHERE network_id=$1", f.network.ID); err != nil {
		t.Fatal(err)
	}
	var poll protocol.Quantity
	read := f.read(t, f.b, f.wb, &poll)
	if read.Purpose != "background" {
		t.Fatal("expected a background read", read)
	}
	request := f.request(t)
	if _, err := f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: 1,
		Updates: []protocol.Update{{ID: f.id, Quantity: 13, Observation: 1, AssignmentID: read.AssignmentID}}}); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.ItemsByID(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{f.id})
	if err != nil || got.Refreshes[0].RequestID != request.RequestID || got.Refreshes[0].State != "pending" {
		t.Fatal("refresh was retroactively confirmed by an in-flight read", got, err)
	}
	f.round(t)
	next := f.read(t, f.b, f.wb, &poll)
	if next.Purpose != "refresh" || next.AssignmentID == read.AssignmentID {
		t.Fatal("refresh did not get its own later attempt", next)
	}
}

func TestCooperativeRecipeNeedsQualifiedJointObservation(t *testing.T) {
	f := newCooperativeFixture(t)
	createOrder(t, f.s, f.admin, f.network, f.id, "cooperative-joint")
	slots, bytes := 16, 16384
	p := protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes}
	command, err := f.s.PollV2(ctx, f.a, p)
	if err != nil || len(command.Commands) != 1 {
		t.Fatal("primary did not receive its craft command", command, err)
	}
	c := command.Commands[0]
	p.Sequence = 2
	p.Reports = []protocol.Report{{OrderID: c.OrderID, AttemptID: c.AttemptID, State: "done"}}
	if _, err = f.s.PollV2(ctx, f.a, p); err != nil {
		t.Fatal(err)
	}
	pending := func(want int) {
		t.Helper()
		var count int
		if err := f.s.Pool().QueryRow(ctx, "SELECT count(*) FROM inventory_rechecks WHERE network_id=$1", f.network.ID).Scan(&count); err != nil || count != want {
			t.Fatal("joint confirmation state changed incorrectly", count, want, err)
		}
	}
	f.request(t)
	var bPoll protocol.Quantity
	quantity := f.read(t, f.b, f.wb, &bPoll)
	if _, err = f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: 1,
		Updates: []protocol.Update{{ID: f.id, Quantity: 14, Observation: 1, AssignmentID: quantity.AssignmentID}}}); err != nil {
		t.Fatal(err)
	}
	pending(1)
	f.round(t)
	aPoll := protocol.Quantity(2)
	joint := f.read(t, f.a, f.wa, &aPoll)
	if joint.Kind != "quantity_recipe" || joint.Purpose != "order" {
		t.Fatal("order confirmation was not a primary joint task", joint)
	}
	no, yes := false, true
	// The old observation cannot borrow the later unqualified observation's
	// watermark to acknowledge this task or its logical order confirmation.
	if _, err = f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2,
		Updates: []protocol.Update{
			{ID: f.id, Quantity: 2, Observation: 1, Craftable: &no, AssignmentID: joint.AssignmentID},
			{ID: f.id, Quantity: 999, Observation: 999, Craftable: &yes},
		}}); err != nil {
		t.Fatal(err)
	}
	pending(1)
	if f.value(t).Quantity != 14 || !f.value(t).Craftable {
		t.Fatal("stale joint receipt borrowed newer discovery evidence")
	}
	if _, err = f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 3,
		Updates: []protocol.Update{{ID: f.id, Quantity: 15, Observation: 2, Craftable: &no, AssignmentID: joint.AssignmentID}}}); err != nil {
		t.Fatal(err)
	}
	pending(0)
	got := f.value(t)
	if got.Quantity != 15 || got.Craftable || !got.CraftableCurrentSession {
		t.Fatal("joint fields were not accepted together", got)
	}
}

func TestCooperativeReceiptCanonicalizesAliasesBeforeCheckingToken(t *testing.T) {
	f := newCooperativeFixture(t)
	ordered, reordered := nbtDefinition(t, "ordered"), nbtDefinition(t, "reordered")
	yes := true
	seed, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2,
		Updates: []protocol.Update{{Item: &ordered, Quantity: 5, Observation: 2, Craftable: &yes}}})
	if err != nil {
		t.Fatal(err)
	}
	f.id = seed.Bindings[0].ID
	var alias protocol.Quantity
	if err = f.s.Pool().QueryRow(ctx, `INSERT INTO item_definitions(network_id,identity_key,kind,name,damage,nbt,label,exact)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, f.network.ID, legacyKey(reordered),
		reordered.Kind, reordered.Name, reordered.Damage, reordered.NBT, reordered.Label, reordered.Exact).Scan(&alias); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Pool().Exec(ctx, "INSERT INTO item_aliases(network_id,alias_id,canonical_id) VALUES($1,$2,$3)", f.network.ID, int64(alias), int64(f.id)); err != nil {
		t.Fatal(err)
	}
	f.request(t)
	var poll protocol.Quantity
	read := f.read(t, f.b, f.wb, &poll)
	if _, err = f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: 1,
		Updates: []protocol.Update{{ID: alias, Quantity: 17, Observation: 1, AssignmentID: read.AssignmentID}}}); err != nil {
		t.Fatal("canonical token was rejected for a real retained alias", err)
	}
	before := f.value(t)
	no := false
	if _, err = f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 3,
		Updates: []protocol.Update{{Item: &reordered, Quantity: 1, Observation: 999, Craftable: &no}}}); err != nil {
		t.Fatal(err)
	}
	after := f.value(t)
	if after.Quantity != 17 || !after.Craftable || !after.ObservedAt.Equal(before.ObservedAt) ||
		!after.CraftableObservedAt.Equal(*before.CraftableObservedAt) {
		t.Fatal("an alternate encoding was mistaken for new field evidence", before, after)
	}
}

func TestCooperativeWorkerDoesNotHidePrimaryFailure(t *testing.T) {
	f := newCooperativeFixture(t)
	order := createOrder(t, f.s, f.admin, f.network, f.id, "primary-offline")
	if _, err := f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Pool().Exec(ctx, "UPDATE networks SET last_seen=now()-interval '2 minutes' WHERE id=$1", f.network.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Pool().Exec(ctx, "UPDATE devices SET last_seen=now()-interval '2 minutes' WHERE id=$1", f.a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.PollV2(ctx, f.b, protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.Network(ctx, f.admin.ID, f.network.ID)
	if err != nil || got.Online || got.Services == nil || got.Services.QuantityReaders != 1 || got.Services.Craft || got.Services.Discovery || got.Services.Recipes {
		t.Fatal("worker heartbeat restored primary services", got, err)
	}
	if err = f.s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := f.s.Order(ctx, f.admin.ID, f.network.ID, order.ID)
	if err != nil || current.State != "needs_review" {
		t.Fatal("worker hid an uncertain primary execution", current, err)
	}
}

func TestCooperativeReadRequiresNegotiatedExactQuantities(t *testing.T) {
	f := newCooperativeFixture(t)
	w, err := f.s.HelloV2(ctx, f.b, protocol.Hello{Protocol: 2, BootID: "inexact-worker", Source: "simulator",
		MaxResponseBytes: 32768, Capabilities: protocol.Capabilities{Inventory: true}})
	if err != nil {
		t.Fatal(err)
	}
	f.request(t)
	slots, bytes := 16, 16384
	for sequence := protocol.Quantity(1); sequence <= 2; sequence++ {
		f.round(t)
		ack, err := f.s.PollV2(ctx, f.b, protocol.Poll{SessionID: w.SessionID, Sequence: sequence, ReadSlots: &slots, ReadBytes: &bytes})
		if err != nil || len(ack.Reads) != 0 || ack.ScanBudget != 0 {
			t.Fatal("inexact device acquired an exact quantity task", ack, err)
		}
	}
	if _, err = f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 1,
		Updates: []protocol.Update{{ID: f.id, Quantity: 1, Observation: 1, AssignmentID: strings.Repeat("a", 32)}}}); !errors.Is(err, store.ErrCapability) {
		t.Fatal("inexact device could submit exact task evidence", err)
	}
}

func TestCooperativeReadsBoundCapacityAndRetainBackgroundOpportunity(t *testing.T) {
	f := newCooperativeFixture(t)
	updates := []protocol.Update{}
	for i := range 40 {
		item := protocol.Definition{Kind: "item", Name: fmt.Sprintf("test:cold_%02d", i), Label: fmt.Sprintf("Cold %02d", i), Exact: true}
		updates = append(updates, protocol.Update{Item: &item, Quantity: 1, Observation: 2})
	}
	seed, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2, Updates: updates})
	if err != nil {
		t.Fatal(err)
	}
	ids := []protocol.Quantity{}
	for _, binding := range seed.Bindings[:16] {
		ids = append(ids, binding.ID)
	}
	if _, err = f.s.RequestItemRefresh(ctx, f.admin.ID, f.network.ID, ids); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Pool().Exec(ctx, "UPDATE inventory_schedule SET next_quantity_at=now()-interval '1 second' WHERE network_id=$1", f.network.ID); err != nil {
		t.Fatal(err)
	}
	slots, bytes := 16, 16384
	p := protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes}
	if _, err = f.s.PollV2(ctx, f.b, p); err != nil {
		t.Fatal(err)
	}
	f.round(t)
	p.Sequence++
	ack, err := f.s.PollV2(ctx, f.b, p)
	if err != nil || len(ack.Reads) != 16 {
		t.Fatal("bounded read pool did not fill available capacity", ack, err)
	}
	refreshes, background := 0, 0
	seen := map[protocol.Quantity]bool{}
	requested := map[protocol.Quantity]bool{}
	for _, id := range ids {
		requested[id] = true
	}
	for _, read := range ack.Reads {
		if seen[read.ID] {
			t.Fatal("same field was assigned twice in one response", read)
		}
		seen[read.ID] = true
		switch read.Purpose {
		case "refresh":
			refreshes++
		case "background":
			background++
			if requested[read.ID] {
				t.Fatal("background opportunity was spent on unconfirmed fast-lane work", read)
			}
		}
	}
	if refreshes != 4 || background != 12 {
		t.Fatal("hot refresh work starved background or exceeded its fast lane", refreshes, background)
	}
	f.round(t)
	p.Sequence++
	next, err := f.s.PollV2(ctx, f.b, p)
	if err != nil || len(next.Reads) != 0 {
		t.Fatal("device exceeded sixteen outstanding tasks", next, err)
	}
}

func TestCooperativeAvailabilityFollowsEachFieldSource(t *testing.T) {
	f := newCooperativeFixture(t)
	f.request(t)
	var poll protocol.Quantity
	read := f.read(t, f.b, f.wb, &poll)
	if _, err := f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: 1,
		Updates: []protocol.Update{{ID: f.id, Quantity: 12, Observation: 1, AssignmentID: read.AssignmentID}}}); err != nil {
		t.Fatal(err)
	}
	before := f.value(t)
	caps := protocol.Capabilities{Inventory: true, ExactQuantities: true, QuantityRefresh: true}
	if _, err := f.s.HelloV2(ctx, f.a, protocol.Hello{Protocol: 2, BootID: "primary-next", Source: "simulator",
		MaxResponseBytes: 32768, Capabilities: caps}); err != nil {
		t.Fatal(err)
	}
	after := f.value(t)
	if !after.CurrentSession || after.CraftableCurrentSession || after.Quantity != 12 || !after.Craftable ||
		!after.ObservedAt.Equal(before.ObservedAt) || !after.CraftableObservedAt.Equal(*before.CraftableObservedAt) {
		t.Fatal("primary restart retired the worker quantity or refreshed historical evidence", before, after)
	}
	if _, err := f.s.HelloV2(ctx, f.b, protocol.Hello{Protocol: 2, BootID: "worker-next", Source: "simulator",
		MaxResponseBytes: 32768, Capabilities: caps}); err != nil {
		t.Fatal(err)
	}
	if got := f.value(t); got.CurrentSession || got.CraftableCurrentSession || got.Quantity != 12 {
		t.Fatal("retired worker source remained current", got)
	}
}

func TestCooperativeReceiptFailureRollsBackQualificationAndRefresh(t *testing.T) {
	f := newCooperativeFixture(t)
	f.request(t)
	var poll protocol.Quantity
	read := f.read(t, f.b, f.wb, &poll)
	batch := protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: 1,
		Updates: []protocol.Update{{ID: f.id, Quantity: 12, Observation: 1, AssignmentID: read.AssignmentID}}}
	if _, err := f.s.Pool().Exec(ctx, `CREATE FUNCTION reject_cooperative_ack() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'test acknowledgement failure'; END $$;
		CREATE TRIGGER reject_cooperative_ack BEFORE UPDATE OF inventory_response ON bridge_sessions
		FOR EACH ROW EXECUTE FUNCTION reject_cooperative_ack()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.IngestV2(ctx, f.b, batch); err == nil {
		t.Fatal("failed ACK persistence was ignored")
	}
	got, err := f.s.ItemsByID(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{f.id})
	if err != nil || got.Items[0].Quantity != 10 || got.Refreshes[0].State != "pending" {
		t.Fatal("failed ACK persistence leaked data or logical completion", got, err)
	}
	if _, err = f.s.Pool().Exec(ctx, "DROP TRIGGER reject_cooperative_ack ON bridge_sessions"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.IngestV2(ctx, f.b, batch); err != nil || f.value(t).Quantity != 12 {
		t.Fatal("rolled-back task qualification prevented exact retry", err)
	}
}
