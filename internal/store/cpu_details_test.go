package store_test

import (
	"encoding/json"
	"errors"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"strings"
	"testing"
)

func TestCPUDetailDemandLifecycle(t *testing.T) {
	f := newCooperativeFixture(t)
	// Upgrade only this disposable fixture's negotiated capability.
	if _, err := f.s.Pool().Exec(ctx, `UPDATE bridge_sessions SET capabilities=capabilities||'{"cpu_details":true}'::jsonb WHERE id=$1`, f.wa.SessionID); err != nil {
		t.Fatal(err)
	}
	cpus := []protocol.CPU{{ID: "1:1", Busy: true, Storage: 1024}, {ID: "1:2", Busy: true, Storage: 1024}}
	slots, bytes := 16, 16384
	var seq protocol.Quantity = 1
	poll := func(r *protocol.CPUDetailResult) protocol.PollAck {
		t.Helper()
		f.round(t)
		seq++
		ack, err := f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: seq, ReadSlots: &slots, ReadBytes: &bytes, CPUDetail: r})
		if err != nil {
			t.Fatal(err)
		}
		return ack
	}
	if _, err := f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1, CPUs: &cpus, ReadSlots: &slots, ReadBytes: &bytes}); err != nil {
		t.Fatal(err)
	}
	in := store.CPUDetailInput{SessionID: f.wa.SessionID, CPUID: "1:1", ViewerID: strings.Repeat("a", 32)}
	if _, err := f.s.RequestCPUDetails(ctx, f.admin.ID, f.network.ID, in); err != nil {
		t.Fatal(err)
	}
	other := in
	other.CPUID = "1:2"
	other.ViewerID = strings.Repeat("b", 32)
	if _, err := f.s.RequestCPUDetails(ctx, f.admin.ID, f.network.ID, other); !errors.Is(err, store.ErrCapacity) {
		t.Fatal("other CPU displaced active viewer", err)
	}
	same := in
	same.ViewerID = other.ViewerID
	if _, err := f.s.RequestCPUDetails(ctx, f.admin.ID, f.network.ID, same); err != nil {
		t.Fatal(err)
	}
	var a *protocol.CPUDetailAssignment
	for range 5 {
		ack := poll(nil)
		if ack.CPUDetail != nil {
			a = ack.CPUDetail
			break
		}
	}
	if a == nil || a.Kind != "active" || a.CPUID != "1:1" {
		t.Fatal("missing assignment", a)
	}
	replay, err := f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: seq, ReadSlots: &slots, ReadBytes: &bytes})
	if err != nil || replay.CPUDetail != nil {
		t.Fatal("replay repeated native task", replay, err)
	}
	result := &protocol.CPUDetailResult{AssignmentID: a.AssignmentID, CPUID: a.CPUID, Kind: a.Kind, Total: 1, Items: []protocol.CPUDetailItem{{Stack: protocol.Stack{Kind: "item", Name: "test:part", Label: "Part", Quantity: 64}, RowID: "row"}}}
	poll(result)
	view, err := f.s.CPUDetails(ctx, f.admin.ID, f.network.ID, in.SessionID, in.CPUID)
	if err != nil || len(view.Parts) != 1 || view.Parts[0].Items[0].Quantity != 64 {
		t.Fatal(view, err)
	}
	// Snapshot IDs changing does not replace an anchored target with an index.
	encoded, _ := json.Marshal([]protocol.CPU{{ID: "99:1", Busy: true}})
	f.s.Pool().Exec(ctx, `UPDATE cpu_snapshots SET cpus=$2 WHERE network_id=$1`, f.network.ID, encoded)
	if _, err = f.s.RequestCPUDetails(ctx, f.admin.ID, f.network.ID, in); err != nil {
		t.Fatal("anchored CPU renewal failed", err)
	}
	if err = f.s.CloseCPUDetails(ctx, f.admin.ID, f.network.ID, in.ViewerID); err != nil {
		t.Fatal(err)
	}
	if err = f.s.CloseCPUDetails(ctx, f.admin.ID, f.network.ID, same.ViewerID); err != nil {
		t.Fatal(err)
	}
	f.s.Pool().Exec(ctx, `UPDATE cpu_detail_demands SET next_read_at=now(),assignment_expires='-infinity' WHERE network_id=$1`, f.network.ID)
	if ack := poll(nil); ack.CPUDetail != nil {
		t.Fatal("closed demand kept reading", ack)
	}
	// Result after close cannot overwrite the last accepted data.
	result.Items[0].Quantity = 999
	poll(result)
	view, _ = f.s.CPUDetails(ctx, f.admin.ID, f.network.ID, in.SessionID, in.CPUID)
	if view.Parts[0].Items[0].Quantity != 64 {
		t.Fatal("late result accepted", view)
	}
	if _, err = f.s.PollV2(ctx, f.b, protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1, CPUDetail: result}); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("worker injected CPU detail", err)
	}
	stale := in
	stale.SessionID = "old-boot"
	if _, err = f.s.RequestCPUDetails(ctx, f.admin.ID, f.network.ID, stale); !errors.Is(err, store.ErrViewChanged) {
		t.Fatal(err)
	}
}

func TestCPUDetailErrorsKeepPreviousAndStopChangedTarget(t *testing.T) {
	f := newCooperativeFixture(t)
	f.s.Pool().Exec(ctx, `UPDATE bridge_sessions SET capabilities=capabilities||'{"cpu_details":true}'::jsonb WHERE id=$1`, f.wa.SessionID)
	cpus := []protocol.CPU{{ID: "1:1", Busy: true}}
	slots, bytes := 16, 16384
	f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1, CPUs: &cpus, ReadSlots: &slots, ReadBytes: &bytes})
	in := store.CPUDetailInput{SessionID: f.wa.SessionID, CPUID: "1:1", ViewerID: strings.Repeat("a", 32)}
	if _, err := f.s.RequestCPUDetails(ctx, f.admin.ID, f.network.ID, in); err != nil {
		t.Fatal(err)
	}
	sequence := protocol.Quantity(1)
	round := func(errorCode string) {
		t.Helper()
		f.s.Pool().Exec(ctx, `UPDATE cpu_detail_demands SET category=0,next_read_at=now(),assignment_expires='-infinity' WHERE network_id=$1`, f.network.ID)
		var a *protocol.CPUDetailAssignment
		for range 6 {
			f.round(t)
			sequence++
			ack, err := f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: in.SessionID, Sequence: sequence, ReadSlots: &slots, ReadBytes: &bytes})
			if err != nil {
				t.Fatal(err)
			}
			if ack.CPUDetail != nil {
				a = ack.CPUDetail
				break
			}
		}
		if a == nil {
			t.Fatal("no assignment")
		}
		r := &protocol.CPUDetailResult{AssignmentID: a.AssignmentID, CPUID: a.CPUID, Kind: a.Kind, Error: errorCode}
		if errorCode == "" {
			r.Total = 1
			r.Items = []protocol.CPUDetailItem{{Stack: protocol.Stack{Kind: "item", Name: "test:part", Quantity: 7}, RowID: "row"}}
		}
		sequence++
		if _, err := f.s.PollV2(ctx, f.a, protocol.Poll{SessionID: in.SessionID, Sequence: sequence, CPUDetail: r}); err != nil {
			t.Fatal(err)
		}
	}
	round("")
	round("read_failed")
	view, err := f.s.CPUDetails(ctx, f.admin.ID, f.network.ID, in.SessionID, in.CPUID)
	if err != nil || view.Parts[0].Items[0].Quantity != 7 || view.Parts[0].Error != "read_failed" || view.Parts[0].ObservedAt == nil {
		t.Fatal(view, err)
	}
	round("target_changed")
	view, _ = f.s.CPUDetails(ctx, f.admin.ID, f.network.ID, in.SessionID, in.CPUID)
	if view.Status != "target_changed" {
		t.Fatal(view)
	}
	var stopped bool
	f.s.Pool().QueryRow(ctx, `SELECT next_read_at='infinity' FROM cpu_detail_demands WHERE network_id=$1`, f.network.ID).Scan(&stopped)
	if !stopped {
		t.Fatal("changed target kept scheduling")
	}
}
