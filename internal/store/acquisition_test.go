package store_test

import (
	"fmt"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func fullAcquisitionQueue(t *testing.T) (cooperativeFixture, protocol.Poll) {
	t.Helper()
	f := newCooperativeFixture(t)
	updates := []protocol.Update{}
	for i := range 15 {
		item := protocol.Definition{Kind: "item", Name: fmt.Sprintf("test:acquisition_%02d", i), Exact: true}
		updates = append(updates, protocol.Update{Item: &item, Quantity: 1, Observation: 2})
	}
	if _, err := f.s.IngestV2(ctx, f.a, protocol.InventoryBatch{SessionID: f.wa.SessionID, Sequence: 2, Updates: updates}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Pool().Exec(ctx, "UPDATE inventory_schedule SET next_quantity_at=now()-interval '1 second' WHERE network_id=$1", f.network.ID); err != nil {
		t.Fatal(err)
	}
	slots, bytes := 16, 16384
	p := protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes}
	if _, err := f.s.PollV2(ctx, f.b, p); err != nil {
		t.Fatal(err)
	}
	f.round(t)
	p.Sequence++
	ack, err := f.s.PollV2(ctx, f.b, p)
	if err != nil || len(ack.Reads) != 16 {
		t.Fatal("worker did not receive a full queue", ack, err)
	}
	if _, err := f.s.Pool().Exec(ctx, "UPDATE acquisition_intents SET grant_until=now()-interval '1 second' WHERE device_id=$1", f.b.ID); err != nil {
		t.Fatal(err)
	}
	return f, p
}

func TestAcquisitionExistingWorkReceivesCreditsWithoutAdmissionCapacity(t *testing.T) {
	for _, capacity := range []struct {
		name         string
		slots, bytes int
	}{
		{"no_slots", 0, 16384},
		{"no_bytes", 16, 0},
		{"no_slots_or_bytes", 0, 0},
	} {
		t.Run(capacity.name, func(t *testing.T) {
			f, p := fullAcquisitionQueue(t)
			p.ReadSlots, p.ReadBytes = &capacity.slots, &capacity.bytes
			primarySlots, primaryBytes := 16, 16384
			primary := protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1, ReadSlots: &primarySlots, ReadBytes: &primaryBytes}
			if _, err := f.s.PollV2(ctx, f.a, primary); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				f.round(t)
				p.Sequence++
				ack, err := f.s.PollV2(ctx, f.b, p)
				if err != nil || ack.ScanBudget <= 0 || ack.ScanBudget > 32 || ack.GrantID == "" || len(ack.Reads) != 0 {
					t.Fatal("queued work lost execution credit or admitted new work", ack, err)
				}
				var slots, bytes, active int
				if err := f.s.Pool().QueryRow(ctx, "SELECT slots,bytes FROM acquisition_intents WHERE device_id=$1", f.b.ID).Scan(&slots, &bytes); err != nil {
					t.Fatal(err)
				}
				if slots != capacity.slots || bytes != capacity.bytes {
					t.Fatal("execution demand fabricated admission capacity", slots, bytes)
				}
				if err := f.s.Pool().QueryRow(ctx, `SELECT count(*) FROM cooperative_read_fields WHERE device_id=$1
					AND session_id=$2 AND field='quantity' AND completed_at IS NULL AND expires_at>now()`, f.b.ID, p.SessionID).Scan(&active); err != nil || active != 16 {
					t.Fatal("execution credits changed outstanding leases", active, err)
				}
				primary.Sequence++
				other, err := f.s.PollV2(ctx, f.a, primary)
				if err != nil || ack.ScanBudget != 16 || other.ScanBudget != 16 {
					t.Fatal("execution demand bypassed fair shared quota", ack, other, err)
				}
				p.Sequence++
				repeated, err := f.s.PollV2(ctx, f.b, p)
				if err != nil || repeated.ScanBudget != 0 || len(repeated.Reads) != 0 {
					t.Fatal("queued execution demand minted credits twice in one round", repeated, err)
				}
			}
		})
	}
}

func TestAcquisitionInactiveWorkDoesNotCreateExecutionDemand(t *testing.T) {
	for _, state := range []string{"expired", "completed", "other_session", "other_device", "retired_session"} {
		t.Run(state, func(t *testing.T) {
			f, p := fullAcquisitionQueue(t)
			switch state {
			case "expired":
				_, err := f.s.Pool().Exec(ctx, "UPDATE cooperative_read_fields SET expires_at=now()-interval '1 second' WHERE device_id=$1", f.b.ID)
				if err != nil {
					t.Fatal(err)
				}
			case "completed":
				_, err := f.s.Pool().Exec(ctx, "UPDATE cooperative_read_fields SET completed_at=now() WHERE device_id=$1", f.b.ID)
				if err != nil {
					t.Fatal(err)
				}
			case "other_session":
				_, err := f.s.Pool().Exec(ctx, "UPDATE cooperative_read_fields SET session_id=$2 WHERE device_id=$1", f.b.ID, f.wa.SessionID)
				if err != nil {
					t.Fatal(err)
				}
			case "other_device":
				_, err := f.s.Pool().Exec(ctx, "UPDATE cooperative_read_fields SET device_id=$2 WHERE device_id=$1", f.b.ID, f.a.ID)
				if err != nil {
					t.Fatal(err)
				}
			case "retired_session":
				w, err := f.s.HelloV2(ctx, f.b, protocol.Hello{Protocol: 2, BootID: "worker-next", Source: "simulator",
					MaxResponseBytes: 32768, Capabilities: protocol.Capabilities{Inventory: true, ExactQuantities: true}})
				if err != nil {
					t.Fatal(err)
				}
				p.SessionID, p.Sequence = w.SessionID, 0
			}
			zero := 0
			p.ReadSlots, p.ReadBytes = &zero, &zero
			for range 2 {
				f.round(t)
				p.Sequence++
				ack, err := f.s.PollV2(ctx, f.b, p)
				if err != nil || ack.ScanBudget != 0 || ack.GrantID != "" || len(ack.Reads) != 0 {
					t.Fatal("inactive ownership created execution demand", ack, err)
				}
			}
		})
	}
}

func TestAcquisitionFairDeviceSharesAndNoRepeatedCredits(t *testing.T) {
	f := newCooperativeFixture(t)
	slots, bytes := 16, 16384
	a := protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes}
	b := protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes}
	if _, err := f.s.PollV2(ctx, f.a, a); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.PollV2(ctx, f.b, b); err != nil {
		t.Fatal(err)
	}
	f.round(t)
	a.Sequence, b.Sequence = 2, 2
	first, err := f.s.PollV2(ctx, f.a, a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.s.PollV2(ctx, f.b, b)
	if err != nil {
		t.Fatal(err)
	}
	if first.ScanBudget != 16 || second.ScanBudget != 16 || first.GrantID == "" || second.GrantID == "" || first.GrantID == second.GrantID {
		t.Fatal("two active readers did not split the group budget", first, second)
	}
	a.Sequence = 3
	repeated, err := f.s.PollV2(ctx, f.a, a)
	if err != nil || repeated.ScanBudget != 0 || len(repeated.Reads) != 0 {
		t.Fatal("another poll minted extra credits in the same round", repeated, err)
	}
	if _, err = f.s.Pool().Exec(ctx, "UPDATE acquisition_intents SET grant_until=now()-interval '1 second' WHERE device_id=$1", f.a.ID); err != nil {
		t.Fatal(err)
	}
	a.Sequence = 2
	// Only the most recent immutable poll may replay.
	if _, err = f.s.PollV2(ctx, f.a, a); err == nil {
		t.Fatal("old control sequence was replayed")
	}
	a.Sequence = 3
	expired, err := f.s.PollV2(ctx, f.a, a)
	if err != nil || expired.ScanBudget != 0 || expired.GrantTTLMillis != 0 {
		t.Fatal("expired cached response recovered credits", expired, err)
	}
}

func TestAcquisitionSharesAcrossNetworksBeforeDevicesAndLegacyMode(t *testing.T) {
	f := newCooperativeFixture(t)
	n, token, err := f.s.CreateNetwork(ctx, f.admin.ID, "legacy neighbor", f.network.ServerGroup, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	legacy, welcome := hello(t, f.s, token, "mixed-mode")
	slots, bytes := 16, 16384
	a := protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes}
	b := protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes}
	v1 := protocol.Poll{SessionID: welcome.SessionID, Sequence: 1}
	if _, err = f.s.PollV2(ctx, f.a, a); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.PollV2(ctx, f.b, b); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Poll(ctx, legacy, v1); err != nil {
		t.Fatal(err)
	}
	f.round(t)
	a.Sequence, b.Sequence, v1.Sequence = 2, 2, 2
	aa, err := f.s.PollV2(ctx, f.a, a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := f.s.PollV2(ctx, f.b, b)
	if err != nil {
		t.Fatal(err)
	}
	cc, err := f.s.Poll(ctx, legacy, v1)
	if err != nil || aa.ScanBudget != 8 || bb.ScanBudget != 8 || cc.ScanBudget != 16 {
		t.Fatal("extra devices displaced a neighboring legacy network", n.ID, aa, bb, cc, err)
	}
}

func TestAcquisitionZeroCapacityDoesNotReserveFutureShare(t *testing.T) {
	f := newCooperativeFixture(t)
	slots, bytes, zero := 16, 16384, 0
	a := protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes}
	b := protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1, ReadSlots: &zero, ReadBytes: &zero}
	if _, err := f.s.PollV2(ctx, f.a, a); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.PollV2(ctx, f.b, b); err != nil {
		t.Fatal(err)
	}
	f.round(t)
	a.Sequence, b.Sequence = 2, 2
	aa, err := f.s.PollV2(ctx, f.a, a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := f.s.PollV2(ctx, f.b, b)
	if err != nil || aa.ScanBudget != 32 || bb.ScanBudget != 0 || len(bb.Reads) != 0 {
		t.Fatal("backpressured reader held a future share", aa, bb, err)
	}
}

func TestAcquisitionRequiresWholeMillisecondLease(t *testing.T) {
	for _, test := range []struct {
		duration string
		budget   int
		ttl      int
	}{
		{"0 microseconds", 0, 0},
		{"500 microseconds", 0, 0},
		{"1000 microseconds", 16, 1},
		{"1500 microseconds", 16, 1},
	} {
		t.Run(test.duration, func(t *testing.T) {
			f := newCooperativeFixture(t)
			slots, bytes := 16, 16384
			p := protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1, ReadSlots: &slots, ReadBytes: &bytes}
			if _, err := f.s.PollV2(ctx, f.b, p); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.Pool().Exec(ctx, `UPDATE server_groups SET next_scan_at=now()+interval '1 hour' WHERE name=$1`,
				f.network.ServerGroup); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.Pool().Exec(ctx, `UPDATE acquisition_intents SET
				grant_id='0123456789abcdef0123456789abcdef',grant_budget=16,claimed=false,
				grant_until=now()+interval '1 second' WHERE device_id=$1`, f.b.ID); err != nil {
				t.Fatal(err)
			}
			// Set the deadline inside the poll transaction, whose now() is fixed.
			// This reproduces the sub-millisecond boundary without timing sleeps.
			if _, err := f.s.Pool().Exec(ctx, `
				CREATE FUNCTION test_grant_deadline() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN
					NEW.grant_until := now()+TG_ARGV[0]::interval;
					RETURN NEW;
				END $$;
				CREATE TRIGGER test_grant_deadline BEFORE UPDATE OF slots ON acquisition_intents
				FOR EACH ROW EXECUTE FUNCTION test_grant_deadline('`+test.duration+`')`); err != nil {
				t.Fatal(err)
			}
			p.Sequence++
			ack, err := f.s.PollV2(ctx, f.b, p)
			if err != nil {
				t.Fatal(err)
			}
			if ack.ScanBudget != test.budget || ack.GrantTTLMillis != test.ttl {
				t.Fatalf("unusable or extended acquisition lease: %+v", ack)
			}
			if (ack.GrantID != "") != (test.budget > 0) {
				t.Fatalf("grant identity did not match usable credits: %+v", ack)
			}
		})
	}
}
