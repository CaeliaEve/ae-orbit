package store_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func TestCooperativeReadsDoNotLeaseTasksBeyondLuaRetainedBudget(t *testing.T) {
	// Charges were evaluated with the real aeorbit.json encoder and adapter:key:
	// 2*encoded_assignment_bytes + identity_key_bytes + 256.
	tests := []struct {
		name         string
		label        string
		damage       int
		registry     string
		nbt          bool
		maxID        bool
		luaCharge    int
		queueBytes   int
		htmlEscaping int
	}{
		{name: "empty label envelope", luaCharge: 670, queueBytes: 669},
		{name: "low memory ASCII label", label: strings.Repeat("A", 512), luaCharge: 1694, queueBytes: 1562},
		{name: "escaped controls", label: "\"\\\n\r\t\b\f\x1f", luaCharge: 710, queueBytes: 709},
		{name: "multilingual BMP", label: strings.Repeat("\u4e2d\u6587", 80), luaCharge: 2590, queueBytes: 2589},
		{name: "astral surrogate pairs", label: strings.Repeat("\U0001f680", 128), luaCharge: 3742, queueBytes: 3741},
		{name: "HTML characters", label: "<>&", luaCharge: 676, queueBytes: 675, htmlEscaping: 30},
		{name: "maximum decimal ID and metadata", label: "X", damage: 2147483647,
			registry: "test:\u4e2d\U0001f680", maxID: true, luaCharge: 760, queueBytes: 759},
		{name: "canonical NBT digest key", nbt: true, luaCharge: 1012, queueBytes: 1011},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newCooperativeFixture(t)
			item := protocol.Definition{Kind: "item", Name: "test:memory", Damage: tt.damage, Label: tt.label, Exact: true}
			if tt.registry != "" {
				item.Name = tt.registry
			}
			if tt.nbt {
				item = nbtDefinition(t, "ordered")
			}
			id := f.id
			if tt.maxID {
				id = 9223372036854775807
				if _, err := f.s.Pool().Exec(ctx, `INSERT INTO item_definitions(id,network_id,identity_key,kind,name,damage,nbt,label,exact)
					VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, int64(id), f.network.ID, item.Key(),
					item.Kind, item.Name, item.Damage, item.NBT, item.Label, item.Exact); err != nil {
					t.Fatal(err)
				}
				if _, err := f.s.Pool().Exec(ctx, `INSERT INTO inventory_schedule(network_id,item_id,next_quantity_at,next_verify_at)
					VALUES($1,$2,now()+interval '1 hour',now()+interval '1 hour')`, f.network.ID, int64(id)); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.s.Pool().Exec(ctx, `UPDATE item_definitions
				SET identity_key=$2,name=$3,damage=$4,nbt=$5,label=$6 WHERE id=$1`,
				int64(id), item.Key(), item.Name, item.Damage, item.NBT, item.Label); err != nil {
				t.Fatal(err)
			}
			// This independent, smaller candidate must remain reachable after
			// the oversized head is rejected, without leasing that head.
			small := protocol.Definition{Kind: "item", Name: "x", Exact: true}
			seed, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2,
				Updates: []protocol.Update{{Item: &small, Quantity: 1, Observation: 2}}})
			if err != nil || len(seed.Bindings) != 1 {
				t.Fatal(seed, err)
			}
			smallID := seed.Bindings[0].ID
			if _, err = f.s.Pool().Exec(ctx, "UPDATE inventory_schedule SET next_quantity_at=now()+interval '1 hour' WHERE network_id=$1", f.network.ID); err != nil {
				t.Fatal(err)
			}
			slots, queueBytes := 16, tt.queueBytes
			p := protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &queueBytes}
			if _, err = f.s.PollV2(ctx, f.b, p); err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.Pool().Exec(ctx, `UPDATE inventory_schedule
				SET next_quantity_at=now()-CASE WHEN item_id=$2 THEN interval '2 seconds' ELSE interval '1 second' END
				WHERE network_id=$1 AND item_id IN ($2,$3)`, f.network.ID, int64(id), int64(smallID)); err != nil {
				t.Fatal(err)
			}
			f.round(t)
			p.Sequence++
			ack, err := f.s.PollV2(ctx, f.b, p)
			if err != nil {
				t.Fatal(err)
			}
			if len(ack.Reads) != 1 || ack.Reads[0].ID != smallID {
				t.Fatalf("Lua charge %d exceeds advertised %d bytes; only the later small task may be leased, got %+v",
					tt.luaCharge, queueBytes, ack.Reads)
			}
			var owned int
			if err = f.s.Pool().QueryRow(ctx, `SELECT count(*) FROM cooperative_read_fields
				WHERE network_id=$1 AND item_id=$2`, f.network.ID, int64(id)).Scan(&owned); err != nil || owned != 0 {
				t.Fatal("oversized task acquired ghost ownership", owned, err)
			}
			// Go's standard JSON encoder conservatively escapes each of <>&
			// into six bytes; the other fixtures fit their exact Lua charge.
			queueBytes = tt.luaCharge + tt.htmlEscaping
			if _, err = f.s.Pool().Exec(ctx, `UPDATE inventory_schedule SET next_quantity_at=now()-interval '1 second'
				WHERE network_id=$1 AND item_id=$2`, f.network.ID, int64(id)); err != nil {
				t.Fatal(err)
			}
			f.round(t)
			p.Sequence++
			ack, err = f.s.PollV2(ctx, f.b, p)
			if err != nil || len(ack.Reads) != 1 || ack.Reads[0].ID != id || ack.Reads[0].TTLMillis != 30000 {
				t.Fatal("a previously oversized candidate did not fit the larger retained budget", ack, err)
			}
		})
	}
}

func TestCooperativeReadsBoundCumulativeLuaRetainedMemory(t *testing.T) {
	f := newCooperativeFixture(t)
	updates := make([]protocol.Update, 8)
	for i := range updates {
		item := protocol.Definition{Kind: "item", Name: fmt.Sprintf("test:memory_%d", i),
			Label: strings.Repeat("A", 200), Exact: true}
		updates[i] = protocol.Update{Item: &item, Quantity: 1, Observation: 2}
	}
	if _, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2, Updates: updates}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Pool().Exec(ctx, "UPDATE inventory_schedule SET next_quantity_at=now()+interval '1 hour' WHERE network_id=$1", f.network.ID); err != nil {
		t.Fatal(err)
	}
	slots, queueBytes := 16, 8192
	p := protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &queueBytes}
	if _, err := f.s.PollV2(ctx, f.b, p); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Pool().Exec(ctx, `UPDATE inventory_schedule SET next_quantity_at=now()-interval '1 second'
		WHERE network_id=$1 AND item_id<>$2`, f.network.ID, int64(f.id)); err != nil {
		t.Fatal(err)
	}
	f.round(t)
	p.Sequence++
	ack, err := f.s.PollV2(ctx, f.b, p)
	if err != nil {
		t.Fatal(err)
	}
	// Each real Lua charge is 1076 bytes; seven fit, eight consume 8608.
	if len(ack.Reads) != 7 {
		t.Fatalf("8192 retained bytes must lease seven 1076-byte tasks, got %d", len(ack.Reads))
	}
	var owned int
	if err = f.s.Pool().QueryRow(ctx, `SELECT count(*) FROM cooperative_read_fields
		WHERE network_id=$1 AND field='quantity' AND completed_at IS NULL`, f.network.ID).Scan(&owned); err != nil || owned != 7 {
		t.Fatal("read ownership exceeded the cumulative retained budget", owned, err)
	}
}
