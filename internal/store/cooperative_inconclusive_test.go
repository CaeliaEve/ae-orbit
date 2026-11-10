package store_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func inconclusiveUpdate(t *testing.T, id protocol.Quantity, assignment string, quantity, observation int64, recipe string) protocol.Update {
	t.Helper()
	extra := ""
	if recipe == "null" {
		extra = `,"recipe_inconclusive":true`
	}
	var update protocol.Update
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"id":%q,"quantity":%q,"observation":%q,"craftable":%s,"assignment_id":%q%s}`,
		id.String(), fmt.Sprint(quantity), fmt.Sprint(observation), recipe, assignment, extra)), &update); err != nil {
		t.Fatalf("valid inconclusive outcome rejected by decoder: %v", err)
	}
	return update
}

func backgroundJoint(t *testing.T, f cooperativeFixture) protocol.ReadAssignment {
	t.Helper()
	if _, err := f.s.Pool().Exec(ctx, `UPDATE inventory_schedule SET
		next_quantity_at=now()+interval '1 hour',next_verify_at=now()-interval '1 second' WHERE network_id=$1`, f.network.ID); err != nil {
		t.Fatal(err)
	}
	f.round(t)
	var poll protocol.Quantity
	read := f.read(t, f.a, f.wa, &poll)
	if read.Kind != "quantity_recipe" || read.Purpose != "background" {
		t.Fatal("expected background joint task", read)
	}
	return read
}

func recipeEvidence(t *testing.T, f cooperativeFixture) string {
	t.Helper()
	var evidence string
	if err := f.s.Pool().QueryRow(ctx, `SELECT jsonb_build_object(
		'craftable',craftable,'observation',craftable_observation,'session',craftable_session_id,
		'observed_at',craftable_observed_at,'generation',craftable_generation)::text
		FROM inventory WHERE network_id=$1 AND item_id=$2`, f.network.ID, int64(f.id)).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	return evidence
}

func inconclusiveState(t *testing.T, f cooperativeFixture) string {
	t.Helper()
	var state string
	if err := f.s.Pool().QueryRow(ctx, `SELECT jsonb_build_object(
		'inventory',(SELECT to_jsonb(i) FROM inventory i WHERE network_id=$1 AND item_id=$2),
		'schedule',(SELECT to_jsonb(s) FROM inventory_schedule s WHERE network_id=$1 AND item_id=$2),
		'fields',(SELECT jsonb_agg(to_jsonb(f) ORDER BY field) FROM cooperative_read_fields f WHERE network_id=$1 AND item_id=$2),
		'refreshes',(SELECT jsonb_agg(to_jsonb(r)) FROM inventory_refreshes r WHERE network_id=$1 AND item_id=$2),
		'rechecks',(SELECT jsonb_agg(to_jsonb(r)) FROM inventory_rechecks r WHERE network_id=$1 AND item_id=$2))::text`,
		f.network.ID, int64(f.id)).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestCooperativeBackgroundInconclusivePreservesRecipeAndReleasesRefresh(t *testing.T) {
	f := newCooperativeFixture(t)
	read := backgroundJoint(t, f)
	before := recipeEvidence(t, f)
	request := f.request(t)
	batch := protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2,
		Updates: []protocol.Update{inconclusiveUpdate(t, f.id, read.AssignmentID, 42, 2, "null")}}
	ack, err := f.s.IngestV2(ctx, f.a, batch)
	if err != nil || ack.Ack != 2 || f.value(t).Quantity != 42 {
		t.Fatal("inconclusive quantity was not accepted", ack, err)
	}
	if after := recipeEvidence(t, f); after != before {
		t.Fatal("unknown outcome replaced recipe evidence", before, after)
	}
	var completed int
	var quantityDelay, verifyDelay float64
	if err := f.s.Pool().QueryRow(ctx, `SELECT count(*) FROM cooperative_read_fields
		WHERE network_id=$1 AND item_id=$2 AND assignment_id=$3 AND completed_at IS NOT NULL`,
		f.network.ID, int64(f.id), read.AssignmentID).Scan(&completed); err != nil || completed != 2 {
		t.Fatal("joint ownership was not completed together", completed, err)
	}
	if err := f.s.Pool().QueryRow(ctx, `SELECT extract(epoch FROM next_quantity_at-now()),
		extract(epoch FROM next_verify_at-now()) FROM inventory_schedule WHERE network_id=$1 AND item_id=$2`,
		f.network.ID, int64(f.id)).Scan(&quantityDelay, &verifyDelay); err != nil ||
		quantityDelay < 290 || quantityDelay > 300 || verifyDelay < 290 || verifyDelay > 300 {
		t.Fatal("inconclusive attempt did not use default 300-second recheck", quantityDelay, verifyDelay, err)
	}
	got, err := f.s.ItemsByID(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{f.id})
	if err != nil || got.Refreshes[0].RequestID != request.RequestID || got.Refreshes[0].State != "pending" {
		t.Fatal("unknown outcome confirmed an unrelated refresh", got, err)
	}
	state := inconclusiveState(t, f)
	replay, err := f.s.IngestV2(ctx, f.a, batch)
	if err != nil || replay.Ack != ack.Ack || replay.Revision != ack.Revision || inconclusiveState(t, f) != state {
		t.Fatal("frozen sequence replay changed accepted state", replay, err)
	}
	batch.Sequence = 3
	if _, err := f.s.IngestV2(ctx, f.a, batch); err != nil || inconclusiveState(t, f) != state {
		t.Fatal("completed receipt was not a no-op", err)
	}
	f.round(t)
	var poll protocol.Quantity
	next := f.read(t, f.b, f.wb, &poll)
	if next.Kind != "quantity" || next.Purpose != "refresh" {
		t.Fatal("completed unknown attempt blocked worker refresh", next)
	}
	if _, err := f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: 1,
		Updates: []protocol.Update{{ID: f.id, Quantity: 43, Observation: 1, AssignmentID: next.AssignmentID}}}); err != nil {
		t.Fatal(err)
	}
	got, err = f.s.ItemsByID(ctx, f.admin.ID, f.network.ID, []protocol.Quantity{f.id})
	if err != nil || got.Items[0].Quantity != 43 || got.Refreshes[0].State != "confirmed" || recipeEvidence(t, f) != before {
		t.Fatal("worker refresh failed after joint completion", got, err)
	}
}

func TestCooperativeInconclusiveJointReceiptArbitration(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		first, second                       string
		firstObservation, secondObservation int64
		firstQuantity, secondQuantity       int64
		wantQuantity                        protocol.Quantity
		wantRecipe                          bool
		conflict                            bool
	}{
		{"newer unknown last", "false", "null", 2, 3, 20, 30, 30, true, false},
		{"newer unknown first", "null", "false", 3, 2, 30, 20, 30, true, false},
		{"newer conclusive last", "null", "false", 2, 3, 20, 30, 30, false, false},
		{"newer conclusive first", "false", "null", 3, 2, 30, 20, 30, false, false},
		{"identical unknown", "null", "null", 2, 2, 20, 20, 20, true, false},
		{"unknown quantity conflict", "null", "null", 2, 2, 20, 21, 0, true, true},
		{"unknown conclusive conflict", "null", "true", 2, 2, 20, 20, 0, true, true},
		{"conclusive unknown conflict", "true", "null", 2, 2, 20, 20, 0, true, true},
		{"conclusive boolean conflict", "true", "false", 2, 2, 20, 20, 0, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCooperativeFixture(t)
			read := backgroundJoint(t, f)
			before, state := recipeEvidence(t, f), inconclusiveState(t, f)
			batch := protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2, Updates: []protocol.Update{
				inconclusiveUpdate(t, f.id, read.AssignmentID, tc.firstQuantity, tc.firstObservation, tc.first),
				inconclusiveUpdate(t, f.id, read.AssignmentID, tc.secondQuantity, tc.secondObservation, tc.second),
			}}
			_, err := f.s.IngestV2(ctx, f.a, batch)
			if tc.conflict {
				if !errors.Is(err, store.ErrConflict) || inconclusiveState(t, f) != state {
					t.Fatal("contradictory joint outcomes did not roll back", err)
				}
				return
			}
			if err != nil || f.value(t).Quantity != tc.wantQuantity || f.value(t).Craftable != tc.wantRecipe {
				t.Fatal("joint winner was not selected atomically", f.value(t), err)
			}
			if tc.wantRecipe && recipeEvidence(t, f) != before {
				t.Fatal("losing success leaked recipe provenance into winning unknown")
			}
		})
	}
}

func TestCooperativeInconclusiveLosingEqualObservationConflicts(t *testing.T) {
	for _, recipes := range [][]string{{"true", "null", "null"}, {"null", "null", "true"}} {
		t.Run(fmt.Sprint(recipes), func(t *testing.T) {
			f := newCooperativeFixture(t)
			read := backgroundJoint(t, f)
			state := inconclusiveState(t, f)
			_, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2,
				Updates: []protocol.Update{
					inconclusiveUpdate(t, f.id, read.AssignmentID, 20, 2, recipes[0]),
					inconclusiveUpdate(t, f.id, read.AssignmentID, 30, 3, recipes[1]),
					inconclusiveUpdate(t, f.id, read.AssignmentID, 20, 2, recipes[2]),
				}})
			if !errors.Is(err, store.ErrConflict) || inconclusiveState(t, f) != state {
				t.Fatal("higher winner hid contradictory older joint outcomes", err)
			}
		})
	}
}

func TestCooperativeInconclusiveUsesConfiguredRecheckAfter(t *testing.T) {
	f := newCooperativeFixture(t)
	options := store.DefaultOptions()
	options.RecheckAfter = 37 * time.Second
	custom, err := store.Open(ctx, f.s.Pool().Config().ConnString(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer custom.Close()
	f.s = custom
	read := backgroundJoint(t, f)
	if _, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2,
		Updates: []protocol.Update{inconclusiveUpdate(t, f.id, read.AssignmentID, 42, 2, "null")}}); err != nil {
		t.Fatal(err)
	}
	var delay float64
	if err := f.s.Pool().QueryRow(ctx, `SELECT extract(epoch FROM next_verify_at-now())
		FROM inventory_schedule WHERE network_id=$1 AND item_id=$2`, f.network.ID, int64(f.id)).Scan(&delay); err != nil || delay < 32 || delay > 37 {
		t.Fatal("unknown attempt ignored configured recheck interval", delay, err)
	}
}

func TestCooperativeInconclusiveStaleReceiptsAreNoOps(t *testing.T) {
	for _, stale := range []string{"expired", "below floor", "one claim completed", "one claim superseded", "superseded order purpose"} {
		t.Run(stale, func(t *testing.T) {
			f := newCooperativeFixture(t)
			read := backgroundJoint(t, f)
			switch stale {
			case "expired":
				if _, err := f.s.Pool().Exec(ctx, "UPDATE cooperative_read_fields SET expires_at=now()-interval '1 second' WHERE network_id=$1", f.network.ID); err != nil {
					t.Fatal(err)
				}
			case "below floor":
				if _, err := f.s.Pool().Exec(ctx, "UPDATE cooperative_read_fields SET observation_floor=999 WHERE network_id=$1", f.network.ID); err != nil {
					t.Fatal(err)
				}
			case "one claim completed":
				if _, err := f.s.Pool().Exec(ctx, "UPDATE cooperative_read_fields SET completed_at=now() WHERE network_id=$1 AND field='recipe'", f.network.ID); err != nil {
					t.Fatal(err)
				}
			case "one claim superseded", "superseded order purpose":
				purpose := "background"
				if stale == "superseded order purpose" {
					purpose = "order"
				}
				if _, err := f.s.Pool().Exec(ctx, "UPDATE cooperative_read_fields SET assignment_id=repeat('b',32),purpose=$2 WHERE network_id=$1 AND field='recipe'", f.network.ID, purpose); err != nil {
					t.Fatal(err)
				}
			}
			f.request(t)
			state := inconclusiveState(t, f)
			if _, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2,
				Updates: []protocol.Update{inconclusiveUpdate(t, f.id, read.AssignmentID, 99, 999, "null")}}); err != nil {
				t.Fatal("authenticated obsolete outcome rejected instead of ACK", err)
			}
			if after := inconclusiveState(t, f); after != state {
				t.Fatal("obsolete receipt changed evidence, schedule, ownership or request", state, after)
			}
			var watermark int64
			if err := f.s.Pool().QueryRow(ctx, "SELECT observation_watermark FROM bridge_sessions WHERE id=$1", f.wa.SessionID).Scan(&watermark); err != nil || watermark != 999 {
				t.Fatal("transport no-op lost authenticated observation watermark", watermark, err)
			}
		})
	}
}

func TestCooperativeInconclusiveRejectsUnauthorizedOutcomes(t *testing.T) {
	for _, invalid := range []string{"worker", "quantity token", "live order", "live refresh", "mismatched purpose"} {
		t.Run(invalid, func(t *testing.T) {
			f := newCooperativeFixture(t)
			var read protocol.ReadAssignment
			device, welcome, sequence := f.a, f.wa, protocol.Quantity(2)
			if invalid == "worker" || invalid == "quantity token" {
				f.request(t)
				if invalid == "worker" {
					device, welcome, sequence = f.b, f.wb, 1
				}
				var poll protocol.Quantity
				read = f.read(t, device, welcome, &poll)
				if read.Kind != "quantity" {
					t.Fatal("expected quantity-token fixture", read)
				}
			} else {
				read = backgroundJoint(t, f)
				purpose, field := "order", ""
				if invalid == "live refresh" {
					purpose = "refresh"
				}
				if invalid == "mismatched purpose" {
					field = "recipe"
				}
				if _, err := f.s.Pool().Exec(ctx, `UPDATE cooperative_read_fields SET purpose=$2
					WHERE network_id=$1 AND ($3='' OR field=$3)`, f.network.ID, purpose, field); err != nil {
					t.Fatal(err)
				}
			}
			state := inconclusiveState(t, f)
			_, err := f.s.IngestV2(ctx, device, protocol.InventoryBatch{SessionID: welcome.SessionID, Sequence: sequence,
				Updates: []protocol.Update{inconclusiveUpdate(t, f.id, read.AssignmentID, 42, 2, "null")}})
			if !errors.Is(err, store.ErrForbidden) || inconclusiveState(t, f) != state {
				t.Fatal("unauthorized unknown outcome was accepted or changed state", err)
			}
		})
	}
}

func TestCooperativeInconclusiveWelcomeNegotiation(t *testing.T) {
	f := newCooperativeFixture(t)
	for _, tc := range []struct {
		welcome protocol.Welcome
		want    bool
	}{{f.wa, true}, {f.wb, false}} {
		encoded, err := json.Marshal(tc.welcome)
		if err != nil {
			t.Fatal(err)
		}
		var fields struct {
			Limits map[string]any `json:"limits"`
		}
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		value, present := fields.Limits["background_recipe_inconclusive"]
		if present != tc.want || (present && value != true) {
			t.Fatal("welcome advertised unknown outcomes to wrong role", string(encoded))
		}
	}
	resumed, err := f.s.HelloV2(ctx, f.a, protocol.Hello{Protocol: 2, BootID: "cooperative-primary", Source: "simulator",
		MaxResponseBytes: 32768, Capabilities: protocol.Capabilities{Inventory: true, ExactQuantities: true}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(resumed.Limits)
	if err != nil {
		t.Fatal(err)
	}
	var limits map[string]any
	if err := json.Unmarshal(encoded, &limits); err != nil || limits["background_recipe_inconclusive"] != true {
		t.Fatal("resumed primary omitted negotiated flag", string(encoded), err)
	}
	s, _, _, token := fixture(t)
	_, w := hello(t, s, token, "inconclusive-v1")
	encoded, err = json.Marshal(w.Limits)
	if err != nil {
		t.Fatal(err)
	}
	// Decode into a fresh map: absent fields must not retain prior values.
	limits = nil
	if err := json.Unmarshal(encoded, &limits); err != nil {
		t.Fatal(err)
	}
	if _, exists := limits["background_recipe_inconclusive"]; exists {
		t.Fatal("v1 advertised v2-only negotiation", string(encoded))
	}
}

func TestCooperativeInconclusiveACKFailureRollsBackBothClaims(t *testing.T) {
	f := newCooperativeFixture(t)
	read := backgroundJoint(t, f)
	batch := protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2,
		Updates: []protocol.Update{inconclusiveUpdate(t, f.id, read.AssignmentID, 42, 2, "null")}}
	state := inconclusiveState(t, f)
	if _, err := f.s.Pool().Exec(ctx, `CREATE FUNCTION reject_inconclusive_ack() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'test acknowledgement failure'; END $$;
		CREATE TRIGGER reject_inconclusive_ack BEFORE UPDATE OF inventory_response ON bridge_sessions
		FOR EACH ROW EXECUTE FUNCTION reject_inconclusive_ack()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.IngestV2(ctx, f.a, batch); err == nil || inconclusiveState(t, f) != state {
		t.Fatal("ACK failure leaked quantity, scheduling or either completion", err)
	}
	if _, err := f.s.Pool().Exec(ctx, "DROP TRIGGER reject_inconclusive_ack ON bridge_sessions"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.IngestV2(ctx, f.a, batch); err != nil || f.value(t).Quantity != 42 {
		t.Fatal("rolled-back claims prevented identical retry", err)
	}
}
