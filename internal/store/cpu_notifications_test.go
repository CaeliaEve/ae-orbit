package store_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func cpuNotificationSnapshot() []protocol.CPU {
	return []protocol.CPU{
		{ID: "1:1", Name: "Worker", Storage: 4096, Coprocessors: 2},
		{ID: "1:2", Name: "Worker", Storage: 4096, Coprocessors: 2},
		{ID: "1:3", Name: "Assembler", Busy: true, Storage: 8192, Coprocessors: 4,
			Output: &protocol.Stack{Name: "test:plate", Label: "Plate", Quantity: 9007199254740992}},
	}
}

func TestCPUNotificationsIgnoreSnapshotIdentity(t *testing.T) {
	s, admin, network, token := fixture(t)
	device, welcome := hello(t, s, token, "cpu-identity")
	cpus := cpuNotificationSnapshot()
	if _, err := s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 1, CPUs: &cpus}); err != nil {
		t.Fatal(err)
	}
	previous, err := s.Events(ctx, admin.ID, network.ID, 0)
	if err != nil || len(previous.Events) != 2 || previous.Events[1].Kind != "cpus" {
		t.Fatal("first CPU snapshot did not publish an event", previous, err)
	}
	wake, stop := s.SubscribeEvents(network.ID)
	defer stop()
	oldTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err = s.Pool().Exec(ctx, "UPDATE cpu_snapshots SET observed_at=$2 WHERE network_id=$1", network.ID, oldTime); err != nil {
		t.Fatal(err)
	}
	// Preserve duplicate workers and the busy CPU while changing order and every ID.
	cpus = []protocol.CPU{cpus[2], cpus[1], cpus[0]}
	for index := range cpus {
		cpus[index].ID = []string{"2:1", "2:2", "2:3"}[index]
	}
	poll := protocol.Poll{SessionID: welcome.SessionID, Sequence: 2, CPUs: &cpus}
	if _, err = s.Poll(ctx, device, poll); err != nil {
		t.Fatal(err)
	}
	view, err := s.CPUs(ctx, admin.ID, network.ID)
	if err != nil || !reflect.DeepEqual(view.CPUs, cpus) || view.ObservedAt == nil || !view.ObservedAt.After(oldTime) {
		t.Fatal("unchanged CPU state did not retain the latest IDs and observation", view, err)
	}
	next, err := s.Events(ctx, admin.ID, network.ID, previous.LastID)
	if err != nil || next.Reset || len(next.Events) != 0 || next.LastID != previous.LastID {
		t.Fatal("snapshot-only changes produced a CPU event", next, err)
	}
	select {
	case <-wake:
		t.Fatal("unchanged CPU state woke subscribers")
	default:
	}
	if _, err = s.Poll(ctx, device, poll); err != nil {
		t.Fatal(err)
	}
	replay, err := s.CPUs(ctx, admin.ID, network.ID)
	if err != nil || !reflect.DeepEqual(replay, view) {
		t.Fatal("replayed poll changed CPU evidence", replay, err)
	}
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 3, CPUsTruncated: true}); err != nil {
		t.Fatal(err)
	}
	omitted, err := s.CPUs(ctx, admin.ID, network.ID)
	if err != nil || !reflect.DeepEqual(omitted, view) {
		t.Fatal("an omitted CPU list changed the snapshot", omitted, err)
	}
}

func TestCPUNotificationsPreserveVisibleChanges(t *testing.T) {
	s, admin, network, token := fixture(t)
	device, welcome := hello(t, s, token, "cpu-visible")
	sequence := protocol.Quantity(0)
	publish := func(t *testing.T, cpus []protocol.CPU, truncated bool) {
		t.Helper()
		sequence++
		if _, err := s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: sequence, CPUs: &cpus, CPUsTruncated: truncated}); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name      string
		change    func([]protocol.CPU) []protocol.CPU
		truncated bool
	}{
		{name: "name", change: func(c []protocol.CPU) []protocol.CPU { c[0].Name = "Renamed"; return c }},
		{name: "busy", change: func(c []protocol.CPU) []protocol.CPU { c[0].Busy = true; return c }},
		{name: "storage", change: func(c []protocol.CPU) []protocol.CPU { c[0].Storage++; return c }},
		{name: "coprocessors", change: func(c []protocol.CPU) []protocol.CPU { c[0].Coprocessors++; return c }},
		{name: "output name", change: func(c []protocol.CPU) []protocol.CPU { c[2].Output.Name = "test:wire"; return c }},
		{name: "output label", change: func(c []protocol.CPU) []protocol.CPU { c[2].Output.Label = "New label"; return c }},
		{name: "exact output quantity", change: func(c []protocol.CPU) []protocol.CPU { c[2].Output.Quantity++; return c }},
		{name: "output unavailable", change: func(c []protocol.CPU) []protocol.CPU {
			c[2].Output = nil
			c[2].OutputUnavailable = "monitor unavailable"
			return c
		}},
		{name: "output missing", change: func(c []protocol.CPU) []protocol.CPU { c[2].Output = nil; return c }},
		{name: "unavailable reason", change: func(c []protocol.CPU) []protocol.CPU { c[2].OutputUnavailable = "partial output"; return c }},
		{name: "added duplicate", change: func(c []protocol.CPU) []protocol.CPU {
			extra := c[0]
			extra.ID = "1:4"
			return append(c, extra)
		}},
		{name: "removed duplicate", change: func(c []protocol.CPU) []protocol.CPU { return c[1:] }},
		{name: "changed duplicate multiplicity", change: func(c []protocol.CPU) []protocol.CPU {
			c[0] = c[2]
			c[0].ID = "1:1"
			return c
		}},
		{name: "truncated", change: func(c []protocol.CPU) []protocol.CPU { return c }, truncated: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			publish(t, cpuNotificationSnapshot(), false)
			previous, err := s.Events(ctx, admin.ID, network.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			wake, stop := s.SubscribeEvents(network.ID)
			defer stop()
			cpus := test.change(cpuNotificationSnapshot())
			publish(t, cpus, test.truncated)
			next, err := s.Events(ctx, admin.ID, network.ID, previous.LastID)
			if err != nil || next.Reset || len(next.Events) != 1 || next.Events[0].Kind != "cpus" {
				t.Fatal("a visible CPU change did not publish exactly one event", next, err)
			}
			select {
			case <-wake:
			default:
				t.Fatal("a visible CPU change did not wake subscribers")
			}
			view, err := s.CPUs(ctx, admin.ID, network.ID)
			if err != nil || !reflect.DeepEqual(view.CPUs, cpus) || view.Truncated != test.truncated {
				t.Fatal("notified CPU snapshot differs from the observation", view, err)
			}
		})
	}
}

func TestCPUNotificationsPreserveEmptySnapshotsAndSessions(t *testing.T) {
	s, admin, network, token := fixture(t)
	device, welcome := hello(t, s, token, "cpu-empty-1")
	cpus := cpuNotificationSnapshot()
	if _, err := s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 1, CPUs: &cpus}); err != nil {
		t.Fatal(err)
	}
	previous, err := s.Events(ctx, admin.ID, network.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	cpus = []protocol.CPU{}
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 2, CPUs: &cpus}); err != nil {
		t.Fatal(err)
	}
	cleared, err := s.Events(ctx, admin.ID, network.ID, previous.LastID)
	if err != nil || len(cleared.Events) != 1 || cleared.Events[0].Kind != "cpus" {
		t.Fatal("removing all CPUs was not notified", cleared, err)
	}
	cpus = nil
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 3, CPUs: &cpus}); err != nil {
		t.Fatal(err)
	}
	repeated, err := s.Events(ctx, admin.ID, network.ID, cleared.LastID)
	if err != nil || len(repeated.Events) != 0 {
		t.Fatal("equivalent empty snapshots created an event", repeated, err)
	}
	device, welcome = hello(t, s, token, "cpu-empty-2")
	previous, err = s.Events(ctx, admin.ID, network.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 1, CPUs: &cpus}); err != nil {
		t.Fatal(err)
	}
	current, err := s.Events(ctx, admin.ID, network.ID, previous.LastID)
	if err != nil || len(current.Events) != 1 || current.Events[0].Kind != "cpus" {
		t.Fatal("first CPU evidence in a new session was not notified", current, err)
	}
	view, err := s.CPUs(ctx, admin.ID, network.ID)
	if err != nil || view.SessionID != welcome.SessionID || view.CPUs == nil || len(view.CPUs) != 0 {
		t.Fatal("empty snapshot was not persisted in the new session", view, err)
	}
}
