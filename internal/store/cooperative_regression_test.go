package store_test

import (
	"fmt"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestCooperativePrimaryBackgroundProgressWithWorker(t *testing.T) {
	for _, order := range []string{"AB", "BA", "A"} {
		for _, lowBudget := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/low_budget=%t", order, lowBudget), func(t *testing.T) {
				f := newCooperativeFixture(t)
				if lowBudget {
					options := store.DefaultOptions()
					options.ScanItems = 8
					s, err := store.Open(ctx, f.s.Pool().Config().ConnString(), options)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(s.Close)
					f.s = s
				}
				slots, bytes := 16, 16384
				polls := map[byte]protocol.Poll{
					'A': {SessionID: f.wa.SessionID, ReadSlots: &slots, ReadBytes: &bytes},
					'B': {SessionID: f.wb.SessionID, ReadSlots: &slots, ReadBytes: &bytes},
				}
				devices := map[byte]store.Device{'A': f.a, 'B': f.b}
				poll := func(who byte) protocol.PollAck {
					t.Helper()
					p := polls[who]
					p.Sequence++
					polls[who] = p
					ack, err := f.s.PollV2(ctx, devices[who], p)
					if err != nil {
						t.Fatal(err)
					}
					return ack
				}
				for i := range len(order) {
					poll(order[i])
				}
				if _, err := f.s.Pool().Exec(ctx, `UPDATE inventory_schedule SET
					next_quantity_at=now()+interval '1 hour',next_verify_at=now()-interval '1 second'
					WHERE network_id=$1`, f.network.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := f.s.Pool().Exec(ctx, "UPDATE cooperative_rotation SET background_turn=0 WHERE network_id=$1", f.network.ID); err != nil {
					t.Fatal(err)
				}
				found, discovery := false, false
				for range 6 {
					f.round(t)
					for i := range len(order) {
						who := order[i]
						ack := poll(who)
						if who != 'A' {
							continue
						}
						spent := 0
						for _, read := range ack.Reads {
							if read.Kind == "quantity_recipe" {
								spent += 4
							} else {
								spent++
							}
							found = found || (read.ID == f.id && read.Kind == "quantity_recipe" && read.Purpose == "background")
						}
						discovery = discovery || ack.ScanBudget > spent
					}
				}
				if !found || !discovery {
					t.Fatal("primary recipe or discovery lane starved during steady polling", found, discovery)
				}
			})
		}
	}
}

func TestCooperativeLowBudgetPrimaryRefreshProgressWithWorker(t *testing.T) {
	f := newCooperativeFixture(t)
	options := store.DefaultOptions()
	options.ScanItems = 8
	s, err := store.Open(ctx, f.s.Pool().Config().ConnString(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	f.s = s
	f.request(t)
	slots, bytes := 16, 16384
	a := protocol.Poll{SessionID: f.wa.SessionID, ReadSlots: &slots, ReadBytes: &bytes}
	b := protocol.Poll{SessionID: f.wb.SessionID, ReadSlots: &slots, ReadBytes: &bytes}
	found := false
	for range 8 {
		a.Sequence++
		aa, err := f.s.PollV2(ctx, f.a, a)
		if err != nil {
			t.Fatal(err)
		}
		for _, read := range aa.Reads {
			found = found || (read.ID == f.id && read.Purpose == "refresh")
		}
		b.Sequence++
		if _, err := f.s.PollV2(ctx, f.b, b); err != nil {
			t.Fatal(err)
		}
		// Keep the request eligible without allowing B's live lease to hide
		// whether A ever receives its own task opportunity.
		if _, err := f.s.Pool().Exec(ctx, "UPDATE cooperative_read_fields SET expires_at=now()-interval '1 second' WHERE network_id=$1", f.network.ID); err != nil {
			t.Fatal(err)
		}
		f.round(t)
	}
	if !found {
		t.Fatal("four-credit primary never received a refresh opportunity")
	}
}

func TestCooperativeObservationFloorSurvivesSourceAlternation(t *testing.T) {
	for _, order := range []string{"ABA", "BAB"} {
		t.Run(order, func(t *testing.T) {
			f := newCooperativeFixture(t)
			devices := map[byte]store.Device{'A': f.a, 'B': f.b}
			welcomes := map[byte]protocol.Welcome{'A': f.wa, 'B': f.wb}
			polls := map[byte]protocol.Quantity{}
			sequences := map[byte]protocol.Quantity{'A': 1}
			observations := map[byte]protocol.Quantity{'A': 1}
			receipt := func(who byte, read protocol.ReadAssignment, observation, quantity protocol.Quantity) protocol.InventoryBatch {
				sequences[who]++
				return protocol.InventoryBatch{SessionID: welcomes[who].SessionID, Sequence: sequences[who],
					Updates: []protocol.Update{{ID: f.id, Quantity: quantity, Observation: observation, AssignmentID: read.AssignmentID}}}
			}
			// A's discovery supplies the first A in A/B/A. Seed B first for B/A/B.
			start := 1
			if order == "BAB" {
				start = 0
			}
			for i := start; i < 2; i++ {
				who := order[i]
				f.request(t)
				f.round(t)
				sequence := polls[who]
				read := f.read(t, devices[who], welcomes[who], &sequence)
				polls[who] = sequence
				observations[who]++
				if _, err := f.s.IngestV2(ctx, devices[who], receipt(who, read, observations[who], protocol.Quantity(12+i))); err != nil {
					t.Fatal(err)
				}
			}
			before := f.value(t)
			request := f.request(t)
			who := order[2]
			f.round(t)
			sequence := polls[who]
			read := f.read(t, devices[who], welcomes[who], &sequence)
			// A new Store instance must retain the issuing session's floor.
			s, err := store.Open(ctx, f.s.Pool().Config().ConnString(), store.DefaultOptions())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			f.s = s
			stale := receipt(who, read, observations[who], 2)
			ack, err := f.s.IngestV2(ctx, devices[who], stale)
			if err != nil || ack.Ack != stale.Sequence {
				t.Fatal("authentic stale receipt did not receive transport ACK", ack, err)
			}
			got, err := f.s.ItemsByID(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{f.id})
			if err != nil || got.Items[0].Quantity != before.Quantity || !got.Items[0].ObservedAt.Equal(before.ObservedAt) ||
				got.Refreshes[0].RequestID != request.RequestID || got.Refreshes[0].State != "pending" {
				t.Fatal("source alternation allowed an old observation to replace evidence or confirm refresh", got, err)
			}
			if replay, err := f.s.IngestV2(ctx, devices[who], stale); err != nil || replay.Ack != ack.Ack {
				t.Fatal("stale immutable batch did not replay", replay, err)
			}
			observations[who]++
			if _, err := f.s.IngestV2(ctx, devices[who], receipt(who, read, observations[who], 20)); err != nil {
				t.Fatal(err)
			}
			got, err = f.s.ItemsByID(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{f.id})
			if err != nil || got.Items[0].Quantity != 20 || got.Refreshes[0].RequestID != request.RequestID || got.Refreshes[0].State != "confirmed" {
				t.Fatal("new observation did not complete the same request", got, err)
			}
		})
	}
}

func TestCooperativeObservationFloorUsesSessionAtDispatch(t *testing.T) {
	f := newCooperativeFixture(t)
	other := protocol.Definition{Kind: "item", Name: "test:other", Exact: true}
	seed, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2,
		Updates: []protocol.Update{{Item: &other, Quantity: 1, Observation: 10}}})
	if err != nil {
		t.Fatal(err)
	}
	f.request(t)
	var poll protocol.Quantity
	read := f.read(t, f.a, f.wa, &poll)
	stale := protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 3,
		Updates: []protocol.Update{{ID: f.id, Quantity: 2, Observation: 2, AssignmentID: read.AssignmentID}}}
	if ack, err := f.s.IngestV2(ctx, f.a, stale); err != nil || ack.Ack != 3 {
		t.Fatal(ack, err)
	}
	if f.value(t).Quantity != 10 {
		t.Fatal("cross-item session observation did not raise the dispatch floor")
	}
	// This unrelated later observation must raise future dispatch floors, not
	// retroactively reject observation 11 from the already-dispatched read.
	if _, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 4,
		Updates: []protocol.Update{{ID: seed.Bindings[0].ID, Quantity: 1, Observation: 20}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 5,
		Updates: []protocol.Update{{ID: f.id, Quantity: 15, Observation: 11, AssignmentID: read.AssignmentID}}}); err != nil {
		t.Fatal(err)
	}
	if f.value(t).Quantity != 15 {
		t.Fatal("later unrelated observation retroactively invalidated a dispatched read")
	}
	f.request(t)
	f.round(t)
	next := f.read(t, f.a, f.wa, &poll)
	if _, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 6,
		Updates: []protocol.Update{{ID: f.id, Quantity: 2, Observation: 12, AssignmentID: next.AssignmentID}}}); err != nil {
		t.Fatal(err)
	}
	if f.value(t).Quantity != 15 {
		t.Fatal("unqualified cross-item input did not raise the next dispatch floor")
	}
}

func TestCooperativeNewSessionDoesNotInheritObservationFloor(t *testing.T) {
	f := newCooperativeFixture(t)
	f.request(t)
	var poll protocol.Quantity
	read := f.read(t, f.b, f.wb, &poll)
	if _, err := f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: 1,
		Updates: []protocol.Update{{ID: f.id, Quantity: 12, Observation: 100, AssignmentID: read.AssignmentID}}}); err != nil {
		t.Fatal(err)
	}
	f.request(t)
	w, err := f.s.HelloV2(ctx, f.b, protocol.Hello{Protocol: 2, BootID: "worker-floor-reboot", Source: "simulator",
		MaxResponseBytes: 32768, Capabilities: protocol.Capabilities{Inventory: true, ExactQuantities: true, QuantityRefresh: true}})
	if err != nil {
		t.Fatal(err)
	}
	poll = 0
	f.round(t)
	next := f.read(t, f.b, w, &poll)
	if _, err := f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 1,
		Updates: []protocol.Update{{ID: f.id, Quantity: 14, Observation: 1, AssignmentID: next.AssignmentID}}}); err != nil {
		t.Fatal(err)
	}
	if f.value(t).Quantity != 14 {
		t.Fatal("new boot inherited the retired session's observation counter")
	}
}

func TestCooperativeObservationFloorMigrationPreservesEvidenceAndACK(t *testing.T) {
	f := newCooperativeFixture(t)
	f.request(t)
	var poll protocol.Quantity
	read := f.read(t, f.b, f.wb, &poll)
	batch := protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: 1,
		Updates: []protocol.Update{{ID: f.id, Quantity: 12, Observation: 8, AssignmentID: read.AssignmentID}}}
	ack, err := f.s.IngestV2(ctx, f.b, batch)
	if err != nil {
		t.Fatal(err)
	}
	// Available historical provenance has different maxima for A's quantity,
	// recipe and discovery, and B's latest quantity source.
	for _, query := range []string{
		"UPDATE inventory SET craftable_observation=7",
		"UPDATE item_definitions SET discovery_observation=10",
		"ALTER TABLE bridge_sessions DROP COLUMN IF EXISTS observation_watermark",
		"DELETE FROM schema_versions WHERE version='migrations/011_observation_watermark.sql'",
	} {
		if _, err := f.s.Pool().Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	before := f.value(t)
	if err := f.s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Migrate(ctx); err != nil {
		t.Fatal("watermark migration was not repeatable", err)
	}
	after := f.value(t)
	if after.Quantity != before.Quantity || !after.ObservedAt.Equal(before.ObservedAt) ||
		!after.CraftableObservedAt.Equal(*before.CraftableObservedAt) {
		t.Fatal("migration changed cached evidence", before, after)
	}
	if replay, err := f.s.IngestV2(ctx, f.b, batch); err != nil || replay.Ack != ack.Ack || replay.Revision != ack.Revision {
		t.Fatal("migration lost immutable ACK", replay, err)
	}
	for _, source := range []struct {
		device                store.Device
		welcome               protocol.Welcome
		poll                  protocol.Quantity
		sequence, observation protocol.Quantity
	}{
		{f.a, f.wa, 0, 2, 10},
		{f.b, f.wb, poll, 2, 8},
	} {
		f.request(t)
		f.round(t)
		next := f.read(t, source.device, source.welcome, &source.poll)
		if _, err := f.s.IngestV2(ctx, source.device, protocol.InventoryBatch{SessionID: source.welcome.SessionID,
			Sequence: source.sequence, Updates: []protocol.Update{{ID: f.id, Quantity: 2,
				Observation: source.observation, AssignmentID: next.AssignmentID}}}); err != nil {
			t.Fatal(err)
		}
		if f.value(t).Quantity != 12 {
			t.Fatal("migration failed to preserve a session's available observation floor", source.device.Role)
		}
		if _, err := f.s.Pool().Exec(ctx, "UPDATE cooperative_read_fields SET expires_at=now()-interval '1 second' WHERE network_id=$1", f.network.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCooperativeUnsupportedCandidatesDoNotBlockExactItems(t *testing.T) {
	for _, prefix := range []string{"fluid", "inexact"} {
		for _, lane := range []string{"quantity", "recipe", "refresh", "order"} {
			t.Run(prefix+"/"+lane, func(t *testing.T) {
				f := newCooperativeFixture(t)
				updates := make([]protocol.Update, 0, 65)
				for i := range 65 {
					item := protocol.Definition{Kind: "item", Name: fmt.Sprintf("test:unsupported_%02d", i), Exact: true}
					if prefix == "fluid" {
						item.Kind = "fluid"
					}
					updates = append(updates, protocol.Update{Item: &item, Quantity: 1, Observation: protocol.Quantity(i + 2)})
				}
				if _, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2, Updates: updates}); err != nil {
					t.Fatal(err)
				}
				// Retained inexact identities are historical baseline data.
				if prefix == "inexact" {
					if _, err := f.s.Pool().Exec(ctx, "UPDATE item_definitions SET exact=false WHERE network_id=$1 AND id<>$2", f.network.ID, int64(f.id)); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := f.s.Pool().Exec(ctx, `UPDATE inventory_schedule SET
					next_quantity_at=CASE WHEN item_id=$2 THEN now()-interval '1 second' ELSE now()-interval '1 hour' END,
					next_verify_at=CASE WHEN item_id=$2 THEN now()-interval '1 second' ELSE now()-interval '1 hour' END
					WHERE network_id=$1`, f.network.ID, int64(f.id)); err != nil {
					t.Fatal(err)
				}
				switch lane {
				case "refresh":
					if _, err := f.s.Pool().Exec(ctx, `INSERT INTO inventory_refreshes(network_id,item_id,token,next_attempt_at)
						SELECT network_id,id,md5(id::text),CASE WHEN id=$2 THEN now()-interval '1 second' ELSE now()-interval '1 hour' END
						FROM item_definitions WHERE network_id=$1`, f.network.ID, int64(f.id)); err != nil {
						t.Fatal(err)
					}
				case "order":
					if _, err := f.s.Pool().Exec(ctx, `INSERT INTO inventory_rechecks(network_id,item_id,token,next_attempt_at)
						SELECT network_id,id,md5(id::text),CASE WHEN id=$2 THEN now()-interval '1 second' ELSE now()-interval '1 hour' END
						FROM item_definitions WHERE network_id=$1`, f.network.ID, int64(f.id)); err != nil {
						t.Fatal(err)
					}
				}
				device, welcome := f.b, f.wb
				if lane == "recipe" || lane == "order" {
					device, welcome = f.a, f.wa
				}
				if lane == "recipe" {
					if _, err := f.s.Pool().Exec(ctx, "UPDATE inventory_schedule SET next_quantity_at=now()+interval '1 hour' WHERE network_id=$1", f.network.ID); err != nil {
						t.Fatal(err)
					}
				}
				var sequence protocol.Quantity
				read := f.read(t, device, welcome, &sequence)
				wantKind, wantPurpose := "quantity", "background"
				if lane == "recipe" || lane == "order" {
					wantKind = "quantity_recipe"
				}
				if lane == "refresh" || lane == "order" {
					wantPurpose = lane
				}
				if read.ID != f.id || read.Kind != wantKind || read.Purpose != wantPurpose {
					t.Fatal("wrong eligible assignment behind unsupported prefix", read)
				}
			})
		}
	}
}
