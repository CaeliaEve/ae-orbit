package store_test

import (
	"errors"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func orderFixture(t *testing.T) (*store.Store, protocol.Member, protocol.Network, string, store.Device, protocol.Welcome, protocol.Quantity) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "boot-1")
	a, err := s.Ingest(ctx, d, batch(w.SessionID, 1, 1, 10))
	if err != nil {
		t.Fatal(err)
	}
	return s, admin, n, token, d, w, a.Bindings[0].ID
}
func createOrder(t *testing.T, s *store.Store, admin protocol.Member, n protocol.Network, id protocol.Quantity, key string) protocol.Order {
	t.Helper()
	o, err := s.CreateOrder(ctx, admin.ID, n.ID, protocol.OrderInput{ItemID: id, Quantity: 64, IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestOrderIdempotencyAndCancel(t *testing.T) {
	s, a, n, _, _, _, id := orderFixture(t)
	o := createOrder(t, s, a, n, id, "test-order-001")
	repeat := createOrder(t, s, a, n, id, "test-order-001")
	if repeat.ID != o.ID {
		t.Fatal("duplicate order")
	}
	_, err := s.CreateOrder(ctx, a.ID, n.ID, protocol.OrderInput{ItemID: id, Quantity: 65, IdempotencyKey: "test-order-001"})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatal("idempotency parameters ignored", err)
	}
	canceled, err := s.CancelOrder(ctx, a.ID, n.ID, o.ID)
	if err != nil || canceled.State != "canceled" {
		t.Fatal(canceled, err)
	}
}

func TestOrderRejectsUnsupportedCPUSelectionBeforeEnqueue(t *testing.T) {
	s, admin, network, _, _, _, id := orderFixture(t)
	for _, wait := range []bool{false, true} {
		_, err := s.CreateOrder(ctx, admin.ID, network.ID, protocol.OrderInput{
			ItemID: id, Quantity: 64, IdempotencyKey: "strict-cpu-001",
			CPUName: "Main CPU", WaitForCPU: wait,
		})
		if !errors.Is(err, store.ErrCapability) {
			t.Fatalf("unsupported CPU selection was not rejected: wait=%v err=%v", wait, err)
		}
	}
	orders, err := s.Orders(ctx, admin.ID, network.ID, "", 100)
	if err != nil || len(orders) != 0 {
		t.Fatal("unsupported selection enqueued an order", orders, err)
	}
	if _, err = s.CreateOrder(ctx, admin.ID, network.ID, protocol.OrderInput{
		ItemID: id, Quantity: 64, IdempotencyKey: "automatic-cpu-001",
	}); err != nil {
		t.Fatal("automatic order was rejected", err)
	}
}

func TestDeviceTokenRotationRetiresTracking(t *testing.T) {
	s, admin, network, oldToken, device, welcome, id := orderFixture(t)
	order := createOrder(t, s, admin, network, id, "rotate-order-001")
	if ack, err := s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 1}); err != nil || len(ack.Commands) != 1 {
		t.Fatal(ack, err)
	}
	viewer, _, err := s.CreateMember(ctx, admin.ID, "viewer", []protocol.Grant{{NetworkID: network.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RotateDeviceToken(ctx, viewer.ID, network.ID); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("viewer rotated device credential", err)
	}
	newToken, err := s.RotateDeviceToken(ctx, admin.ID, network.ID)
	if err != nil || newToken == oldToken {
		t.Fatal("credential was not replaced", err)
	}
	if _, err = s.AuthDevice(ctx, oldToken); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("old token still authorized", err)
	}
	if _, err = s.Ingest(ctx, device, batch(welcome.SessionID, 2, 2, 11)); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("in-flight old credential still mutated inventory", err)
	}
	got, err := s.Order(ctx, admin.ID, network.ID, order.ID)
	if err != nil || got.State != "needs_review" {
		t.Fatal("possible game effect lost on token rotation", got, err)
	}
	newDevice, next := hello(t, s, newToken, "after-rotation")
	if ack, err := s.Poll(ctx, newDevice, protocol.Poll{SessionID: next.SessionID, Sequence: 1}); err != nil || len(ack.Commands) != 0 {
		t.Fatal("uncertain work replayed after rotation", ack, err)
	}
	page, err := s.Items(ctx, admin.ID, network.ID, protocol.ItemQuery{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Quantity != 10 || page.Items[0].CurrentSession {
		t.Fatal("rotation changed or freshly marked old inventory", page, err)
	}
}

func TestReconnectKeepsReviewUntilLiveHandleCompletes(t *testing.T) {
	s, admin, network, _, device, welcome, id := orderFixture(t)
	order := createOrder(t, s, admin, network, id, "reconnect-order-001")
	ack, err := s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 1})
	if err != nil || len(ack.Commands) != 1 {
		t.Fatal(ack, err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE networks SET last_seen=now()-interval '5 minutes' WHERE id=$1", network.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	report := protocol.Report{OrderID: order.ID, AttemptID: ack.Commands[0].AttemptID, State: "running"}
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 2, Reports: []protocol.Report{report}}); err != nil {
		t.Fatal("live handle could not report after transport recovered", err)
	}
	got, err := s.Order(ctx, admin.ID, network.ID, order.ID)
	if err != nil || got.State != "needs_review" {
		t.Fatal("nonterminal observation cleared uncertainty", got, err)
	}
	report.State = "done"
	if _, err = s.Poll(ctx, device, protocol.Poll{SessionID: welcome.SessionID, Sequence: 3, Reports: []protocol.Report{report}}); err != nil {
		t.Fatal(err)
	}
	got, err = s.Order(ctx, admin.ID, network.ID, order.ID)
	if err != nil || got.State != "done" {
		t.Fatal("same-session final evidence was rejected", got, err)
	}
}

func TestPollOrderLifecycleAndCPU(t *testing.T) {
	s, a, n, _, d, w, id := orderFixture(t)
	o := createOrder(t, s, a, n, id, "test-order-001")
	p := protocol.Poll{SessionID: w.SessionID, Sequence: 1, Reports: []protocol.Report{}}
	ack, err := s.Poll(ctx, d, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ack.Commands) != 1 || ack.Commands[0].OrderID != o.ID {
		t.Fatal(ack)
	}
	retry, err := s.Poll(ctx, d, p)
	if err != nil || retry.Commands[0].AttemptID != ack.Commands[0].AttemptID {
		t.Fatal("reassigned on retry", err)
	}
	if _, err = s.CancelOrder(ctx, a.ID, n.ID, o.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatal("active job canceled without AE evidence", err)
	}
	command := ack.Commands[0]
	cpus := []protocol.CPU{{ID: "1", Name: "Main CPU", Busy: true, Storage: 4096, Output: &protocol.Stack{Name: "minecraft:stone", Label: "Stone", Quantity: 64}}}
	for i, state := range []string{"computing", "running", "done"} {
		p = protocol.Poll{SessionID: w.SessionID, Sequence: protocol.Quantity(i + 2), Reports: []protocol.Report{{OrderID: o.ID, AttemptID: command.AttemptID, State: state}}, CPUs: &cpus}
		if _, err = s.Poll(ctx, d, p); err != nil {
			t.Fatal(state, err)
		}
	}
	got, err := s.Order(ctx, a.ID, n.ID, o.ID)
	if err != nil || got.State != "done" {
		t.Fatal(got, err)
	}
	view, err := s.CPUs(ctx, a.ID, n.ID)
	if err != nil || len(view.CPUs) != 1 || view.CPUs[0].Output.Quantity != 64 {
		t.Fatal(view, err)
	}
	p.Sequence = 5
	p.Reports[0].State = "running"
	if _, err = s.Poll(ctx, d, p); !errors.Is(err, store.ErrConflict) {
		t.Fatal("terminal status regressed", err)
	}
}

func TestRestartDoesNotReplayCraft(t *testing.T) {
	s, a, n, token, d, w, id := orderFixture(t)
	o := createOrder(t, s, a, n, id, "test-order-001")
	ack, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil || len(ack.Commands) != 1 {
		t.Fatal(ack, err)
	}
	_, next := hello(t, s, token, "boot-2")
	got, err := s.Order(ctx, a.ID, n.ID, o.ID)
	if err != nil || got.State != "needs_review" {
		t.Fatal(got, err)
	}
	ack, err = s.Poll(ctx, d, protocol.Poll{SessionID: next.SessionID, Sequence: 1})
	if err != nil || len(ack.Commands) != 0 {
		t.Fatal("uncertain craft replayed", ack, err)
	}
	got, err = s.ResolveOrder(ctx, a.ID, n.ID, o.ID, "done", "checked in game")
	if err != nil || got.State != "done" {
		t.Fatal(got, err)
	}
}

func TestGroupPlanningBudget(t *testing.T) {
	s, a, n, _, d, w, id := orderFixture(t)
	createOrder(t, s, a, n, id, "test-order-001")
	n2, token2, err := s.CreateNetwork(ctx, a.ID, "second", "server-1", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	d2, w2 := hello(t, s, token2, "boot-2")
	b, err := s.Ingest(ctx, d2, batch(w2.SessionID, 1, 1, 8))
	if err != nil {
		t.Fatal(err)
	}
	createOrder(t, s, a, n2, b.Bindings[0].ID, "test-order-002")
	first, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil || len(first.Commands) != 1 {
		t.Fatal(first, err)
	}
	second, err := s.Poll(ctx, d2, protocol.Poll{SessionID: w2.SessionID, Sequence: 1})
	if err != nil || len(second.Commands) != 0 {
		t.Fatal("group budget exceeded", second, err)
	}
	c := first.Commands[0]
	if _, err = s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 2, Reports: []protocol.Report{{OrderID: c.OrderID, AttemptID: c.AttemptID, State: "running"}}}); err != nil {
		t.Fatal(err)
	}
	second, err = s.Poll(ctx, d2, protocol.Poll{SessionID: w2.SessionID, Sequence: 2})
	if err != nil || len(second.Commands) != 1 {
		t.Fatal("planning slot not released", second, err)
	}
}

func TestPollWrongAttemptRollsBack(t *testing.T) {
	s, a, n, _, d, w, id := orderFixture(t)
	o := createOrder(t, s, a, n, id, "test-order-001")
	ack, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	p := protocol.Poll{SessionID: w.SessionID, Sequence: 2, Reports: []protocol.Report{{OrderID: o.ID, AttemptID: "wrong", State: "done"}}}
	if _, err = s.Poll(ctx, d, p); !errors.Is(err, store.ErrConflict) {
		t.Fatal(err)
	}
	p.Reports[0].AttemptID = ack.Commands[0].AttemptID
	if _, err = s.Poll(ctx, d, p); err != nil {
		t.Fatal("invalid report advanced checkpoint", err)
	}
}

func TestOrderExpiryAndOfflineUncertainty(t *testing.T) {
	s, a, n, _, d, w, id := orderFixture(t)
	o := createOrder(t, s, a, n, id, "test-order-001")
	if _, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	queued := createOrder(t, s, a, n, id, "test-order-002")
	if _, err := s.Pool().Exec(ctx, "UPDATE networks SET last_seen=now()-interval '1 hour' WHERE id=$1", n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, "UPDATE orders SET expires_at=now()-interval '1 hour' WHERE id=$1", queued.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.Order(ctx, a.ID, n.ID, o.ID)
	if err != nil || got.State != "needs_review" {
		t.Fatal(got, err)
	}
	got, err = s.Order(ctx, a.ID, n.ID, queued.ID)
	if err != nil || got.State != "expired" {
		t.Fatal(got, err)
	}
	ack, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil || len(ack.Commands) != 0 {
		t.Fatal("cached expired command sent", ack, err)
	}
}

func TestGroupSharesAcquisitionBudget(t *testing.T) {
	s, a, _, _, d, w, _ := orderFixture(t)
	_, token2, err := s.CreateNetwork(ctx, a.ID, "second", "server-1", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	d2, w2 := hello(t, s, token2, "second-boot")
	first, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil || first.ScanBudget < 1 {
		t.Fatal(first, err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE server_groups SET next_scan_at=now()+interval '1 hour' WHERE name='server-1'"); err != nil {
		t.Fatal(err)
	}
	second, err := s.Poll(ctx, d2, protocol.Poll{SessionID: w2.SessionID, Sequence: 1})
	if err != nil || second.ScanBudget != 0 {
		t.Fatal("independent bridges bypassed shared budget", second, err)
	}
}
