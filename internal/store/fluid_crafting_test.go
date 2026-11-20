package store_test

import (
	"errors"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"testing"
)

func TestFluidCraftingPrimaryRecipeAndCommand(t *testing.T) {
	f := resourceFixture(t)
	page, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Kind: "fluid", View: "all"})
	if err != nil {
		t.Fatal(err)
	}
	id := page.Items[0].ID
	caps := protocol.Capabilities{Inventory: true, Craft: true, MaxCraftQuantity: 100000, ExactQuantities: true, NativeScanDisabled: true, EventsDisabled: true, Fluids: true, CraftFluids: true, Enumeration: "exact_only"}
	f.wa, err = f.s.HelloV2(ctx, f.a, protocol.Hello{Protocol: 2, BootID: "fluid-craft-primary", Source: "simulator", MaxResponseBytes: 32768, Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	f.wb = safePlainHello(t, f, f.b, "fluid-no-worker-craft")
	// Seed only authenticated quantity observations, including confirmed zero.
	slots, bytes := 16, 16384
	pollSeq := protocol.Quantity(0)
	uploadSeq := protocol.Quantity(0)
	var recipe protocol.ReadAssignment
	for attempt := 0; attempt < 12 && recipe.AssignmentID == ""; attempt++ {
		pollSeq++
		f.round(t)
		ack, e := f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: pollSeq, ReadSlots: &slots, ReadBytes: &bytes})
		if e != nil {
			t.Fatal(e)
		}
		updates := []protocol.Update{}
		for _, read := range ack.Reads {
			if read.Item.Kind != "fluid" {
				continue
			}
			if read.Kind == "quantity_recipe" && read.ID == id {
				recipe = read
				continue
			}
			if read.Kind == "quantity" {
				updates = append(updates, protocol.Update{ID: read.ID, Quantity: 0, Observation: pollSeq*10 + protocol.Quantity(len(updates)), AssignmentID: read.AssignmentID})
			}
		}
		if len(updates) > 0 {
			uploadSeq++
			if _, e = f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: uploadSeq, Updates: updates}); e != nil {
				t.Fatal(e)
			}
		}
	}
	if recipe.AssignmentID == "" {
		t.Fatal("zero stock fluid never received recipe verification")
	}
	craftable := true
	uploadSeq++
	if _, err = f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: uploadSeq, Updates: []protocol.Update{{ID: id, Quantity: 0, Observation: 1000, Craftable: &craftable, AssignmentID: recipe.AssignmentID}}}); err != nil {
		t.Fatal(err)
	}
	lookup, err := f.s.ItemsByID(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{id})
	if err != nil || len(lookup.Items) != 1 || !lookup.Items[0].Craftable {
		t.Fatal(lookup, err)
	}
	// Resources cannot be counted as item units or copied into item history.
	stats, err := f.s.InventoryStats(ctx, f.admin.ID, f.network.ID)
	if err != nil || stats.TotalQuantity != "0" {
		t.Fatal(stats, err)
	}
	var resourceHistory int
	if err = f.s.Pool().QueryRow(ctx, "SELECT count(*) FROM inventory_history_items WHERE item_id=$1", int64(id)).Scan(&resourceHistory); err != nil || resourceHistory != 0 {
		t.Fatal("fluid entered item history", err)
	}
	accepted, err := f.s.CreateProductionJob(ctx, f.admin.ID, f.network.ID, jobInput(id, "fluid-job-create", "fluid-source-entry"))
	if err != nil || accepted.Job == nil {
		t.Fatal(accepted, err)
	}
	// A boot downgrade retains saved intent, but cannot dispatch it.
	old := caps
	old.CraftFluids = false
	f.wa, err = f.s.HelloV2(ctx, f.a, protocol.Hello{Protocol: 2, BootID: "fluid-old-primary", Source: "simulator", MaxResponseBytes: 32768, Capabilities: old})
	if err != nil {
		t.Fatal(err)
	}
	f.round(t)
	ack, err := f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes})
	if err != nil || len(ack.Commands) != 0 {
		t.Fatal("old primary received fluid command", ack, err)
	}
	if _, err = f.s.CreateProductionJob(ctx, f.admin.ID, f.network.ID, jobInput(id, "fluid-job-unsupported", "fluid-source-unsupported")); !errors.Is(err, store.ErrCapability) {
		t.Fatal(err)
	}
	f.wa, err = f.s.HelloV2(ctx, f.a, protocol.Hello{Protocol: 2, BootID: "fluid-updated-primary", Source: "simulator", MaxResponseBytes: 32768, Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	f.round(t)
	ack, err = f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes})
	if err != nil || len(ack.Commands) != 1 {
		t.Fatal(ack, err)
	}
	command := ack.Commands[0]
	if command.Item.Kind != "fluid" || command.ItemID != id || command.Quantity != 64 {
		t.Fatal("fluid identity or units changed", command)
	}
	// A fresh terminal report queues a primary-only joint exact recheck.
	if _, err = f.s.Pool().Exec(ctx, "UPDATE cooperative_read_fields SET expires_at=now()-interval '1 second' WHERE network_id=$1 AND item_id=$2", f.network.ID, int64(id)); err != nil {
		t.Fatal(err)
	}
	f.round(t)
	completed, err := f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: 2, ReadSlots: &slots, ReadBytes: &bytes,
		Reports: []protocol.Report{{OrderID: command.OrderID, AttemptID: command.AttemptID, State: "done"}}})
	if err != nil {
		t.Fatal(err)
	}
	var recheck protocol.ReadAssignment
	for _, read := range completed.Reads {
		if read.ID == id && read.Purpose == "order" {
			recheck = read
		}
	}
	if recheck.Kind != "quantity_recipe" || recheck.Item.Kind != "fluid" {
		t.Fatal("fluid completion lost precise recheck", completed)
	}
	if _, err = f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 1, Updates: []protocol.Update{{ID: id, Quantity: 1000, Observation: 1, Craftable: &craftable, AssignmentID: recheck.AssignmentID}}}); err != nil {
		t.Fatal(err)
	}
	lookup, err = f.s.ItemsByID(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{id})
	if err != nil || lookup.Items[0].Quantity != 1000 || !lookup.Items[0].Craftable {
		t.Fatal(lookup, err)
	}
	var pendingRechecks int
	if err = f.s.Pool().QueryRow(ctx, "SELECT count(*) FROM inventory_rechecks WHERE network_id=$1 AND item_id=$2", f.network.ID, int64(id)).Scan(&pendingRechecks); err != nil || pendingRechecks != 0 {
		t.Fatal("completion recheck never settled", err)
	}
	f.round(t)
	worker, err := f.s.PollV2(ctx, f.b, protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes})
	if err != nil || len(worker.Commands) > 0 {
		t.Fatal("worker received crafting command", worker, err)
	}
	for _, read := range worker.Reads {
		if read.Kind != "quantity" {
			t.Fatal("worker received recipe", read)
		}
	}
	// Use 1000 mB for a separate order and assert retained ownership and replay.
	order, err := f.s.CreateOrder(ctx, f.admin.ID, f.network.ID, protocol.OrderInput{ItemID: id, Quantity: 1000, IdempotencyKey: "fluid-extra-order"})
	if err != nil || order.UserID != f.admin.ID || order.Quantity != 1000 {
		t.Fatal(order, err)
	}
	replay, err := f.s.CreateOrder(ctx, f.admin.ID, f.network.ID, protocol.OrderInput{ItemID: id, Quantity: 1000, IdempotencyKey: "fluid-extra-order"})
	if err != nil || replay.ID != order.ID {
		t.Fatal(replay, err)
	}
	// Source aspects are still view-only.
	aspects, err := f.s.Items(ctx, f.admin.ID, f.network.ID, protocol.ItemQuery{Kind: "essentia", View: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.CreateProductionJob(ctx, f.admin.ID, f.network.ID, jobInput(aspects.Items[0].ID, "aspect-job-rejected", "aspect-source-rejected")); !errors.Is(err, store.ErrCapability) {
		t.Fatal(err)
	}
}

func TestFluidCraftingRecipeRotationPreservesQuantityBudget(t *testing.T) {
	f := resourceFixture(t)
	workerCaps := protocol.Capabilities{Inventory: true, ExactQuantities: true, NativeScanDisabled: true, EventsDisabled: true, Fluids: true}
	var err error
	f.wb, err = f.s.HelloV2(ctx, f.b, protocol.Hello{Protocol: 2, BootID: "fluid-rotation-worker", Source: "simulator", MaxResponseBytes: 32768, Capabilities: workerCaps})
	if err != nil {
		t.Fatal(err)
	}
	// Establish both item stock and zero fluid stock through signed worker reads.
	seen := map[protocol.Quantity]bool{}
	var pollSeq, uploadSeq protocol.Quantity
	for attempt := 0; attempt < 8 && len(seen) < 5; attempt++ {
		ack := pollPlainReads(t, f, f.b, f.wb, &pollSeq, 16, 16384)
		updates := []protocol.Update{}
		for _, read := range ack.Reads {
			if read.Kind != "quantity" {
				t.Fatal("worker received recipe", read)
			}
			q := protocol.Quantity(0)
			if read.Item.Kind == "item" {
				q = 50
			}
			seen[read.ID] = true
			updates = append(updates, protocol.Update{ID: read.ID, Quantity: q, Observation: pollSeq*100 + protocol.Quantity(len(updates)+1), AssignmentID: read.AssignmentID})
		}
		uploadSeq++
		if _, err = f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: uploadSeq, Updates: updates}); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 5 {
		t.Fatal("resource observations never established", seen)
	}
	caps := workerCaps
	caps.Craft, caps.CraftFluids, caps.MaxCraftQuantity, caps.Enumeration = true, true, 100000, "exact_only"
	f.wa, err = f.s.HelloV2(ctx, f.a, protocol.Hello{Protocol: 2, BootID: "fluid-rotation-primary", Source: "simulator", MaxResponseBytes: 32768, Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	var primarySeq, primaryUploadSeq protocol.Quantity
	craftable := true
	for _, wantKind := range []string{"fluid", "item", "fluid", "item"} {
		// Resource turn 3 places quantity first unless the selected fluid joint
		// task removes its duplicate; this used to starve recipe verification.
		if _, err = f.s.Pool().Exec(ctx, "UPDATE cooperative_rotation SET resource_turn=3 WHERE network_id=$1", f.network.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = f.s.Pool().Exec(ctx, "UPDATE inventory_schedule SET next_quantity_at=now()-interval '1 second',next_verify_at=now()-interval '1 second' WHERE network_id=$1", f.network.ID); err != nil {
			t.Fatal(err)
		}
		ack := pollPlainReads(t, f, f.a, f.wa, &primarySeq, 16, 16384)
		updates := []protocol.Update{}
		identities := map[protocol.Quantity]bool{}
		recipes, quantityReads, cost := 0, 0, 0
		for _, read := range ack.Reads {
			if identities[read.ID] {
				t.Fatal("duplicate identity in packet", ack)
			}
			identities[read.ID] = true
			q := protocol.Quantity(0)
			if read.Item.Kind == "item" {
				q = 50
			}
			update := protocol.Update{ID: read.ID, Quantity: q, Observation: primarySeq*100 + protocol.Quantity(len(updates)+1), AssignmentID: read.AssignmentID}
			cost++
			if read.Kind == "quantity_recipe" {
				recipes++
				cost += 3
				if read.Item.Kind != wantKind {
					t.Fatal("recipe rotation lost", wantKind, ack)
				}
				update.Craftable = &craftable
			} else {
				quantityReads++
			}
			updates = append(updates, update)
		}
		if recipes != 1 || quantityReads == 0 || cost > ack.ScanBudget || len(ack.Reads) > 16 {
			t.Fatal("recipe consumed quantity budget or exceeded limits", ack)
		}
		primaryUploadSeq++
		if _, err = f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: primaryUploadSeq, Updates: updates}); err != nil {
			t.Fatal(err)
		}
	}
}
