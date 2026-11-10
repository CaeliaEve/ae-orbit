package store_test

import (
	"fmt"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func TestCooperativeRefreshLeasesRespectOutstandingLuaFastCapacity(t *testing.T) {
	for _, release := range []string{"receipt", "expired"} {
		t.Run(release, func(t *testing.T) {
			testCooperativeRefreshFastCapacity(t, release)
		})
	}
}

func testCooperativeRefreshFastCapacity(t *testing.T, release string) {
	t.Helper()
	f := newCooperativeFixture(t)
	updates := make([]protocol.Update, 9)
	for i := range updates {
		item := protocol.Definition{Kind: "item", Name: fmt.Sprintf("test:fast_%d", i), Label: "Fast", Exact: true}
		updates[i] = protocol.Update{Item: &item, Quantity: 1, Observation: 2}
	}
	seed, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2, Updates: updates})
	if err != nil || len(seed.Bindings) != len(updates) {
		t.Fatal(seed, err)
	}
	ids := make([]protocol.Quantity, len(seed.Bindings))
	for i, binding := range seed.Bindings {
		ids[i] = binding.ID
	}
	if _, err = f.s.Pool().Exec(ctx, `UPDATE inventory_schedule
		SET next_quantity_at=now()+interval '1 hour',next_verify_at=now()+interval '1 hour' WHERE network_id=$1`, f.network.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.RequestItemRefresh(ctx, f.admin.ID, f.network.ID, ids); err != nil {
		t.Fatal(err)
	}
	slots, queueBytes := 16, 16384
	p := protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &queueBytes}
	if _, err = f.s.PollV2(ctx, f.b, p); err != nil {
		t.Fatal(err)
	}
	accepted := 0
	var first protocol.ReadAssignment
	for round, want := range []int{4, 4, 0} {
		f.round(t)
		p.Sequence++
		// Match the ordinary capacity left by the real Lua queue; each
		// short fixture consumes less than the conservative 1024 bytes.
		slots, queueBytes = 16-accepted, 16384-1024*accepted
		ack, err := f.s.PollV2(ctx, f.b, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, read := range ack.Reads {
			if read.Purpose != "refresh" {
				t.Fatal("unexpected background work in fast-only fixture", read)
			}
			if first.ID == 0 {
				first = read
			}
		}
		var owned int
		if err = f.s.Pool().QueryRow(ctx, `SELECT count(*) FROM cooperative_read_fields
			WHERE network_id=$1 AND device_id=$2 AND field='quantity' AND purpose<>'background'
			AND completed_at IS NULL AND expires_at>now()`, f.network.ID, f.b.ID).Scan(&owned); err != nil {
			t.Fatal(err)
		}
		t.Logf("round=%d advertised_slots=%d advertised_bytes=%d assigned=%d outstanding_fast=%d",
			round+1, slots, queueBytes, len(ack.Reads), owned)
		if len(ack.Reads) != want || owned != accepted+want {
			t.Fatalf("Lua accepts at most eight outstanding fast tasks: round %d assigned=%d outstanding=%d; want assigned=%d outstanding=%d",
				round+1, len(ack.Reads), owned, want, accepted+want)
		}
		accepted += len(ack.Reads)
	}
	switch release {
	case "receipt":
		if _, err = f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: 1,
			Updates: []protocol.Update{{ID: first.ID, Quantity: 2, Observation: 1, AssignmentID: first.AssignmentID}}}); err != nil {
			t.Fatal(err)
		}
	case "expired":
		if _, err = f.s.Pool().Exec(ctx, `UPDATE cooperative_read_fields SET expires_at=now()-interval '1 second'
			WHERE network_id=$1 AND item_id=$2`, f.network.ID, int64(first.ID)); err != nil {
			t.Fatal(err)
		}
	}
	f.round(t)
	p.Sequence++
	ack, err := f.s.PollV2(ctx, f.b, p)
	if err != nil || len(ack.Reads) != 1 || ack.Reads[0].Purpose != "refresh" {
		t.Fatal("releasing one fast task did not restore exactly one fast slot", ack, err)
	}
	var owned int
	if err = f.s.Pool().QueryRow(ctx, `SELECT count(*) FROM cooperative_read_fields
		WHERE network_id=$1 AND device_id=$2 AND field='quantity' AND purpose<>'background'
		AND completed_at IS NULL AND expires_at>now()`, f.network.ID, f.b.ID).Scan(&owned); err != nil || owned != 8 {
		t.Fatal("fast capacity failed to recover to eight after release", owned, err)
	}
}

func TestCooperativeOrderAndRefreshShareFastCapacityWithoutBlockingBackground(t *testing.T) {
	f := newCooperativeFixture(t)
	updates := make([]protocol.Update, 12)
	for i := range updates {
		item := protocol.Definition{Kind: "item", Name: fmt.Sprintf("test:mixed_fast_%02d", i), Label: "Mixed", Exact: true}
		updates[i] = protocol.Update{Item: &item, Quantity: 1, Observation: 2}
	}
	seed, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2, Updates: updates})
	if err != nil || len(seed.Bindings) != len(updates) {
		t.Fatal(seed, err)
	}
	if _, err = f.s.Pool().Exec(ctx, `UPDATE inventory_schedule
		SET next_quantity_at=now()+interval '1 hour',next_verify_at=now()+interval '1 hour' WHERE network_id=$1`, f.network.ID); err != nil {
		t.Fatal(err)
	}
	refreshIDs := make([]protocol.Quantity, 0, 5)
	for i, binding := range seed.Bindings {
		switch {
		case i < 5:
			if _, err = f.s.Pool().Exec(ctx, `INSERT INTO inventory_rechecks(network_id,item_id,token)
				VALUES($1,$2,$3)`, f.network.ID, int64(binding.ID), fmt.Sprintf("%032x", int64(binding.ID))); err != nil {
				t.Fatal(err)
			}
		case i < 10:
			refreshIDs = append(refreshIDs, binding.ID)
		default:
			if _, err = f.s.Pool().Exec(ctx, `UPDATE inventory_schedule SET next_quantity_at=now()-interval '1 second'
				WHERE network_id=$1 AND item_id=$2`, f.network.ID, int64(binding.ID)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = f.s.RequestItemRefresh(ctx, f.admin.ID, f.network.ID, refreshIDs); err != nil {
		t.Fatal(err)
	}
	slots, queueBytes := 16, 16384
	p := protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &queueBytes}
	if _, err = f.s.PollV2(ctx, f.a, p); err != nil {
		t.Fatal(err)
	}
	f.round(t)
	p.Sequence++
	ack, err := f.s.PollV2(ctx, f.a, p)
	if err != nil {
		t.Fatal(err)
	}
	fast, background := 0, 0
	var joint protocol.ReadAssignment
	for _, read := range ack.Reads {
		if read.Purpose == "background" {
			background++
		} else {
			fast++
		}
		if read.Purpose == "order" {
			joint = read
		}
	}
	if fast != 8 || background != 2 || joint.ID == 0 {
		t.Fatalf("order and refresh must share eight fast slots without blocking background: fast=%d background=%d joint=%t",
			fast, background, joint.ID != 0)
	}
	yes := true
	if _, err = f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 3,
		Updates: []protocol.Update{{ID: joint.ID, Quantity: 2, Craftable: &yes, Observation: 3,
			AssignmentID: joint.AssignmentID}}}); err != nil {
		t.Fatal(err)
	}
	// A joint task occupies one fast slot, not one slot per quantity/recipe
	// field. Completing one must admit one replacement from either fast lane.
	slots, queueBytes = 7, 7168
	f.round(t)
	p.Sequence++
	ack, err = f.s.PollV2(ctx, f.a, p)
	if err != nil || len(ack.Reads) != 1 || ack.Reads[0].Purpose == "background" {
		t.Fatal("joint completion failed to recover exactly one shared fast slot", ack, err)
	}
}
