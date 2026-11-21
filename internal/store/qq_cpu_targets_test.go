package store_test

import (
	"errors"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"testing"
)

func TestQQUnnamedCPUTargetSurvivesRosterChanges(t *testing.T) {
	f := newCooperativeFixture(t)
	if _, err := f.s.Pool().Exec(ctx, `UPDATE bridge_sessions SET capabilities=capabilities||'{"cpu_notifications":true}'::jsonb WHERE id=$1`, f.wa.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetQQSettings(ctx, f.admin.ID, f.network.ServerGroup, store.QQSettings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.QQSyncChannels(ctx, store.QQAuth{Group: f.network.ServerGroup}, store.QQChannelSync{Groups: []store.QQChannel{{ID: "1081348823"}}}); err != nil {
		t.Fatal(err)
	}
	cpus := []protocol.CPU{{ID: "1:1", Busy: true, Storage: 4096}, {ID: "1:2", Busy: true, Storage: 4096}}
	poll := protocol.Poll{SessionID: f.wa.SessionID, Sequence: 1, CPUs: &cpus}
	if _, err := f.s.PollV2(ctx, f.a, poll); err != nil {
		t.Fatal(err)
	}
	// The user clicked an old observation; current ordering has changed.
	cpus[0].ID, cpus[1].ID = "2:2", "2:1"
	poll.Sequence = 2
	if _, err := f.s.PollV2(ctx, f.a, poll); err != nil {
		t.Fatal(err)
	}
	in := store.QQCPUTargetInput{RequestKey: "target-request-001", SessionID: f.wa.SessionID, CPUID: "1:1"}
	target, err := f.s.PrepareQQCPUTarget(ctx, f.admin.ID, f.network.ID, in)
	if err != nil || target.Status != "pending" {
		t.Fatal(target, err)
	}
	replay, err := f.s.PrepareQQCPUTarget(ctx, f.admin.ID, f.network.ID, in)
	if err != nil || replay.ID != target.ID {
		t.Fatal(replay, err)
	}
	reminder := store.QQReminderInput{RequestKey: "reminder-request-001", TargetID: target.ID, SessionID: in.SessionID, CPUID: in.CPUID, Notification: protocol.QQNotification{GroupID: "1081348823"}}
	if _, err = f.s.AddQQCPUReminder(ctx, f.admin.ID, f.network.ID, reminder); !errors.Is(err, store.ErrViewChanged) {
		t.Fatal("unconfirmed target accepted", err)
	}
	poll = protocol.Poll{SessionID: f.wa.SessionID, Sequence: 3}
	ack, err := f.s.PollV2(ctx, f.a, poll)
	if err != nil || len(ack.CPUWatches) != 1 || ack.CPUWatches[0].CPUID != "1:1" {
		t.Fatal(ack, err)
	}
	repeated, err := f.s.PollV2(ctx, f.a, poll)
	if err != nil || len(repeated.CPUWatches) != 0 {
		t.Fatal("replay caused another read", repeated, err)
	}
	poll.Sequence = 4
	poll.CPUWatches = []protocol.CPUWatchResult{{TargetID: target.ID, Status: "busy"}}
	if _, err = f.s.PollV2(ctx, f.a, poll); err != nil {
		t.Fatal(err)
	}
	id, err := f.s.AddQQCPUReminder(ctx, f.admin.ID, f.network.ID, reminder)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.s.AddQQCPUReminder(ctx, f.admin.ID, f.network.ID, reminder)
	if err != nil || again != id {
		t.Fatal("reminder replay", again, err)
	}
	reminder.Notification.Recipient = "12345678"
	if _, err = f.s.AddQQCPUReminder(ctx, f.admin.ID, f.network.ID, reminder); !errors.Is(err, store.ErrConflict) {
		t.Fatal("changed request replay accepted", err)
	}
	// Worker cannot forge the end of another CPU's job.
	if _, err = f.s.PollV2(ctx, f.b, protocol.Poll{SessionID: f.wb.SessionID, Sequence: 1, CPUWatches: []protocol.CPUWatchResult{{TargetID: target.ID, Status: "idle"}}}); !errors.Is(err, store.ErrForbidden) {
		t.Fatal(err)
	}
	poll.Sequence = 5
	poll.CPUWatches[0].Status = "idle"
	if _, err = f.s.PollV2(ctx, f.a, poll); err != nil {
		t.Fatal(err)
	}
	reminders, err := f.s.QQReminders(ctx, f.admin.ID, f.network.ID)
	if err != nil || len(reminders) != 1 || reminders[0].State != "uncertain" {
		t.Fatal(reminders, err)
	}
	deliveries, err := f.s.QQDeliveries(ctx, f.admin.ID, f.network.ServerGroup)
	if err != nil || len(deliveries) != 1 || deliveries[0].Kind != "cpu_end" {
		t.Fatal(deliveries, err)
	}
	poll.Sequence = 6
	if _, err = f.s.PollV2(ctx, f.a, poll); err != nil {
		t.Fatal(err)
	}
	deliveries, err = f.s.QQDeliveries(ctx, f.admin.ID, f.network.ServerGroup)
	if err != nil || len(deliveries) != 1 {
		t.Fatal("duplicate end message", deliveries, err)
	}
}
