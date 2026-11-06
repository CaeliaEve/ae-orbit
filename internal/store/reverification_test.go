package store_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func verificationToken(t *testing.T, ack protocol.PollAck, id protocol.Quantity) string {
	t.Helper()
	encoded, err := json.Marshal(ack)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Verify []struct {
			ID    protocol.Quantity
			Token string
		}
	}
	if err = json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for _, v := range wire.Verify {
		if v.ID == id && len(v.Token) == 32 {
			return v.Token
		}
	}
	t.Fatalf("completed output has no durable priority verification: %s", encoded)
	return ""
}

func verifiedUpdate(t *testing.T, id, observation, count protocol.Quantity, flag, token string) protocol.Update {
	t.Helper()
	var u protocol.Update
	data := fmt.Sprintf(`{"id":%q,"observation":%q,"quantity":%q,"craftable":%s,"verification":%q}`, id.String(), observation.String(), count.String(), flag, token)
	if err := json.Unmarshal([]byte(data), &u); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestPostCraftVerificationRequiresFreshMatchingObservation(t *testing.T) {
	s, admin, n, _, d, w, id := orderFixture(t)
	createOrder(t, s, admin, n, id, "postcraft-refresh-1")
	assigned, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil || len(assigned.Commands) != 1 {
		t.Fatal(assigned, err)
	}
	c := assigned.Commands[0]
	done := protocol.Poll{SessionID: w.SessionID, Sequence: 2, Reports: []protocol.Report{{OrderID: c.OrderID, AttemptID: c.AttemptID, State: "done"}}}
	ack, err := s.Poll(ctx, d, done)
	if err != nil {
		t.Fatal(err)
	}
	token := verificationToken(t, ack, id)
	replayed, err := s.Poll(ctx, d, done)
	if err != nil || verificationToken(t, replayed, id) != token {
		t.Fatal("poll replay changed the verification", err)
	}
	page, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{})
	if err != nil || page.Items[0].Quantity != 10 {
		t.Fatal("done report invented inventory", page, err)
	}
	seq := protocol.Quantity(1)
	ingest := func(updates ...protocol.Update) {
		t.Helper()
		seq++
		if _, err := s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: seq, Updates: updates}); err != nil {
			t.Fatal(err)
		}
	}
	pending := func(want int) {
		t.Helper()
		var count int
		if err := s.Pool().QueryRow(ctx, "SELECT count(*) FROM inventory_rechecks WHERE network_id=$1 AND item_id=$2", n.ID, int64(id)).Scan(&count); err != nil || count != want {
			t.Fatalf("pending = %d, want %d: %v", count, want, err)
		}
	}
	// Reading a poll, an older batch, or a quantity-only observation is not an ACK.
	pending(1)
	ingest(verifiedUpdate(t, id, 1, 10, "true", token))
	pending(1)
	ingest(verifiedUpdate(t, id, 2, 11, "null", token))
	pending(1)
	ingest(verifiedUpdate(t, id, 3, 11, "true", ""))
	pending(1)
	quiet, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 3})
	if err != nil || len(quiet.Verify) != 0 {
		t.Fatal("priority request retried without backoff", quiet, err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE inventory_rechecks SET next_attempt_at=now()-interval '1 second' WHERE network_id=$1", n.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 4})
	if err != nil || verificationToken(t, retry, id) != token {
		t.Fatal("unobserved request was lost", err)
	}
	// A stale confirmation must not be carried by a newer count in the same batch.
	ingest(verifiedUpdate(t, id, 2, 11, "true", token), verifiedUpdate(t, id, 4, 12, "null", ""))
	pending(1)
	// A fresh confirmation survives a later quantity-only observation.
	ingest(verifiedUpdate(t, id, 6, 14, "null", ""), verifiedUpdate(t, id, 5, 13, "true", token))
	pending(0)
	page, err = s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{})
	if err != nil || page.Items[0].Quantity != 14 || !page.Items[0].Craftable {
		t.Fatal("verified data was lost during batch merge", page, err)
	}
	// Repeating terminal evidence in another poll must not schedule a new read.
	done.Sequence = 5
	if _, err = s.Poll(ctx, d, done); err != nil {
		t.Fatal(err)
	}
	pending(0)
}

func TestPostCraftVerificationSurvivesReconnectAndNewCompletion(t *testing.T) {
	s, admin, n, deviceToken, d, w, id := orderFixture(t)
	createOrder(t, s, admin, n, id, "postcraft-generation-1")
	assigned, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := assigned.Commands[0]
	ack, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 2, Reports: []protocol.Report{{OrderID: c.OrderID, AttemptID: c.AttemptID, State: "done"}}})
	if err != nil {
		t.Fatal(err)
	}
	first := verificationToken(t, ack, id)
	_, next := hello(t, s, deviceToken, "postcraft-reconnect")
	if _, err = s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 2, Updates: []protocol.Update{verifiedUpdate(t, id, 2, 74, "true", first)}}); !errors.Is(err, store.ErrStaleSession) {
		t.Fatal("retired session acknowledged a pending read", err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE inventory_rechecks SET next_attempt_at=now()-interval '1 second' WHERE network_id=$1", n.ID); err != nil {
		t.Fatal(err)
	}
	ack, err = s.Poll(ctx, d, protocol.Poll{SessionID: next.SessionID, Sequence: 1})
	if err != nil || verificationToken(t, ack, id) != first || len(ack.Commands) != 0 {
		t.Fatal("reconnect lost the read or replayed a craft", ack, err)
	}
	createOrder(t, s, admin, n, id, "postcraft-generation-2")
	assigned, err = s.Poll(ctx, d, protocol.Poll{SessionID: next.SessionID, Sequence: 2})
	if err != nil || len(assigned.Commands) != 1 {
		t.Fatal(assigned, err)
	}
	c = assigned.Commands[0]
	ack, err = s.Poll(ctx, d, protocol.Poll{SessionID: next.SessionID, Sequence: 3, Reports: []protocol.Report{{OrderID: c.OrderID, AttemptID: c.AttemptID, State: "done"}}})
	if err != nil {
		t.Fatal(err)
	}
	second := verificationToken(t, ack, id)
	if first == second {
		t.Fatal("a new completion reused an old verification")
	}
	if _, err = s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: next.SessionID, Sequence: 1, Updates: []protocol.Update{verifiedUpdate(t, id, 1, 74, "true", first)}}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = s.Pool().QueryRow(ctx, "SELECT token FROM inventory_rechecks WHERE network_id=$1 AND item_id=$2", n.ID, int64(id)).Scan(&stored); err != nil || stored != second {
		t.Fatal("an earlier result cleared the newer completion", stored, err)
	}
	if _, err = s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: next.SessionID, Sequence: 2, Updates: []protocol.Update{verifiedUpdate(t, id, 2, 138, "true", second)}}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM inventory_rechecks WHERE network_id=$1", n.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("fresh new-session result did not clear the read", count, err)
	}
}

func TestPostCraftVerificationBacklogRotatesWithinResponseBudget(t *testing.T) {
	s, _, n, _, d, w, _ := orderFixture(t)
	b := protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 2}
	yes := true
	for index := 0; index < 40; index++ {
		b.Updates = append(b.Updates, protocol.Update{Item: &protocol.Definition{Kind: "item", Name: fmt.Sprintf("test:recheck_%d", index), Label: "Recheck", Exact: true}, Quantity: 10, Observation: protocol.Quantity(index + 2), Craftable: &yes})
	}
	added, err := s.Ingest(ctx, d, b)
	if err != nil {
		t.Fatal(err)
	}
	for index, binding := range added.Bindings[:16] {
		if _, err = s.Pool().Exec(ctx, "INSERT INTO inventory_rechecks(network_id,item_id,token) VALUES($1,$2,$3)", n.ID, int64(binding.ID), fmt.Sprintf("%032x", index+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE inventory SET next_verify_at=now()-interval '1 hour' WHERE network_id=$1", n.ID); err != nil {
		t.Fatal(err)
	}
	seen := map[protocol.Quantity]bool{}
	for seq := protocol.Quantity(1); seq <= 4; seq++ {
		ack, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: seq})
		if err != nil {
			t.Fatal(err)
		}
		priority, ordinary := 0, 0
		for _, v := range ack.Verify {
			if v.Token == "" {
				ordinary++
				continue
			}
			priority++
			if seen[v.ID] {
				t.Fatal("an unconfirmed item starved the remaining backlog")
			}
			seen[v.ID] = true
		}
		if priority != 4 || len(ack.Verify) > 16 || (seq == 1 && ordinary != 12) {
			t.Fatal("priority flood escaped its reserved window", priority, ordinary)
		}
	}
	var pending int
	if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM inventory_rechecks WHERE network_id=$1", n.ID).Scan(&pending); err != nil || pending != 16 {
		t.Fatal("sending requests deleted unfinished reads", pending, err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE bridge_sessions SET response_bytes=2048 WHERE id=$1", w.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE inventory_rechecks SET next_attempt_at=now()-interval '1 second' WHERE network_id=$1", n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, "UPDATE item_definitions SET label=repeat('x',512) WHERE network_id=$1", n.ID); err != nil {
		t.Fatal(err)
	}
	ack, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 5})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(ack)
	if len(encoded) > 2048 || len(ack.Verify) == 0 || len(ack.Verify) >= 4 {
		t.Fatal("response byte limit was ignored", len(encoded), len(ack.Verify))
	}
	if err = s.Pool().QueryRow(ctx, "SELECT count(*) FROM inventory_rechecks WHERE network_id=$1", n.ID).Scan(&pending); err != nil || pending != 16 {
		t.Fatal("response truncation lost pending reads", pending, err)
	}
}

func TestPostCraftVerificationMigrationRecoversUnobservedOutput(t *testing.T) {
	s, admin, n, _, d, w, id := orderFixture(t)
	order := createOrder(t, s, admin, n, id, "postcraft-migration-1")
	assigned, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := assigned.Commands[0]
	if _, err = s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 2, Reports: []protocol.Report{{OrderID: c.OrderID, AttemptID: c.AttemptID, State: "done"}}}); err != nil {
		t.Fatal(err)
	}
	// Emulate the old schema in this isolated test database, then upgrade twice.
	if _, err = s.Pool().Exec(ctx, "DROP TABLE inventory_rechecks; DELETE FROM schema_versions WHERE version='migrations/003_inventory_rechecks.sql'"); err != nil {
		t.Fatal(err)
	}
	for repeat := 0; repeat < 2; repeat++ {
		if err = s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var token string
	if err = s.Pool().QueryRow(ctx, "SELECT token FROM inventory_rechecks WHERE network_id=$1 AND item_id=$2", n.ID, int64(id)).Scan(&token); err != nil || token != order.ID {
		t.Fatal("upgrade lost the unobserved output", token, err)
	}
	got, err := s.Order(ctx, admin.ID, n.ID, order.ID)
	if err != nil || got.State != "done" || got.AttemptID != c.AttemptID {
		t.Fatal("migration changed an order instead of scheduling a read", got, err)
	}
}
