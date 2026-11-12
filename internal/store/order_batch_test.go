package store_test

import (
	"errors"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestOrderBatchAndReadOnlyIdempotencyLookup(t *testing.T) {
	s, admin, network, _, _, _, itemID := orderFixture(t)
	input := protocol.OrderInput{
		ItemID: itemID, Quantity: 12, IdempotencyKey: "batch-order-001",
		BatchID: "b343d6fc-c273-4ec4-ae5b-99fa57c97eb7",
	}
	first, err := s.CreateOrder(ctx, admin.ID, network.ID, input)
	if err != nil || first.BatchID != input.BatchID {
		t.Fatal("batch ID was not persisted", first, err)
	}
	replay, err := s.CreateOrder(ctx, admin.ID, network.ID, input)
	if err != nil || replay.ID != first.ID {
		t.Fatal("same batch replay created another order", replay, err)
	}
	input.BatchID = "a8151da8-049e-464c-aa69-ec9d64f6786d"
	if _, err = s.CreateOrder(ctx, admin.ID, network.ID, input); !errors.Is(err, store.ErrConflict) {
		t.Fatal("replay changed the original batch", err)
	}
	found, err := s.OrderByIdempotencyKey(ctx, admin.ID, network.ID, input.IdempotencyKey)
	if err != nil || found.ID != first.ID || found.BatchID != first.BatchID {
		t.Fatal("read-only lookup lost the submitted order", found, err)
	}
	if _, err = s.OrderByIdempotencyKey(ctx, admin.ID, network.ID, "unknown-order-001"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("missing intent was not reported as missing", err)
	}
	viewer, _, err := s.CreateMember(ctx, admin.ID, "batch viewer", []protocol.Grant{{NetworkID: network.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.OrderByIdempotencyKey(ctx, viewer.ID, network.ID, input.IdempotencyKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("idempotency lookup exposed another member's intent", err)
	}
	orders, err := s.Orders(ctx, admin.ID, network.ID, "", 100)
	if err != nil || len(orders) != 1 {
		t.Fatal("read-only lookup changed the queue", orders, err)
	}
}
