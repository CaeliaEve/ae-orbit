package store_test

import (
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

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
