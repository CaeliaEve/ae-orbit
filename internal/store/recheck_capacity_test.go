package store_test

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func capacityPoll(t *testing.T, session string, seq, slots, bytes int) protocol.Poll {
	t.Helper()
	var p protocol.Poll
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"session_id":%q,"sequence":%q,"reports":[],"recheck_slots":%d,"recheck_bytes":%d}`, session, fmt.Sprint(seq), slots, bytes)), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// Cumulative byte accounting must stop before overflowing the OC FIFO, and a
// large NBT entry must not monopolize every poll or be deferred for five minutes.
func TestRecheckReceiverByteBudgetRotatesLargeDescriptors(t *testing.T) {
	s, _, n, _, d, w, _ := orderFixture(t)
	flag := false
	updates := []protocol.Update{}
	for index := 0; index < 3; index++ {
		definition := &protocol.Definition{Kind: "item", Name: fmt.Sprintf("test:byte_%d", index), Label: "Cold", Exact: true}
		if index == 0 {
			var compressed bytes.Buffer
			writer, err := gzip.NewWriterLevel(&compressed, gzip.NoCompression)
			if err != nil {
				t.Fatal(err)
			}
			payload := append([]byte{10, 0, 0, 7, 0, 1, 'a', 0, 0, 1, 44}, make([]byte, 300)...)
			payload = append(payload, 0)
			if _, err = writer.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err = writer.Close(); err != nil {
				t.Fatal(err)
			}
			definition.NBT = base64.StdEncoding.EncodeToString(compressed.Bytes())
		}
		updates = append(updates, protocol.Update{Item: definition, Observation: protocol.Quantity(index + 2), Quantity: 42, Craftable: &flag})
	}
	bindings, err := s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 2, Updates: updates})
	if err != nil {
		t.Fatal(err)
	}
	bigID := bindings.Bindings[0].ID
	expireInventoryEvidence(t, s, n.ID)
	if _, err = s.Pool().Exec(ctx, `UPDATE inventory_schedule SET next_verify_at=CASE WHEN item_id=$2 THEN now()-interval '2 minutes' ELSE now()-interval '1 minute' END WHERE network_id=$1 AND item_id IN (SELECT id FROM item_definitions WHERE network_id=$1 AND name LIKE 'test:byte_%')`, n.ID, int64(bigID)); err != nil {
		t.Fatal(err)
	}
	// Each plain descriptor costs 288 bytes; the stored NBT exceeds 600.
	ack, err := s.Poll(ctx, d, capacityPoll(t, w.SessionID, 1, 16, 600))
	if err != nil || len(ack.Verify) != 2 {
		t.Fatal("byte budget or large-item rotation blocked fitting work", ack, err)
	}
	for _, v := range ack.Verify {
		if v.ID == bigID || v.Item.NBT != "" {
			t.Fatal("descriptor larger than receiver capacity was sent")
		}
	}
	var shortRetry bool
	if err = s.Pool().QueryRow(ctx, `SELECT next_verify_at>now() AND next_verify_at<=now()+interval '10 seconds' FROM inventory_schedule WHERE network_id=$1 AND item_id=$2`, n.ID, int64(bigID)).Scan(&shortRetry); err != nil || !shortRetry {
		t.Fatal("unadmitted item lost its short retry", err)
	}
	// A reduced budget can hold only one of the two plain descriptors.
	if _, err = s.Pool().Exec(ctx, `UPDATE inventory_schedule SET next_verify_at=now()-interval '1 minute' WHERE network_id=$1 AND item_id<>$2 AND item_id IN (SELECT id FROM item_definitions WHERE network_id=$1 AND name LIKE 'test:byte_%')`, n.ID, int64(bigID)); err != nil {
		t.Fatal(err)
	}
	ack, err = s.Poll(ctx, d, capacityPoll(t, w.SessionID, 2, 16, 300))
	if err != nil || len(ack.Verify) != 1 {
		t.Fatal("cumulative descriptor sizes overflowed the reported byte budget", ack, err)
	}
}

// A full ordinary queue must leave ordinary work due while priority reads pass.
func TestRecheckReceiverCapacityKeepsUnsentWorkDue(t *testing.T) {
	s, _, n, _, d, w, priorityID := orderFixture(t)
	flag := false
	b := protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 2}
	for index := 0; index < 3; index++ {
		b.Updates = append(b.Updates, protocol.Update{Item: &protocol.Definition{Kind: "item", Name: fmt.Sprintf("test:cold_%d", index), Label: "Cold", Exact: true}, Observation: protocol.Quantity(index + 2), Quantity: 10, Craftable: &flag})
	}
	if _, err := s.Ingest(ctx, d, b); err != nil {
		t.Fatal(err)
	}
	expireInventoryEvidence(t, s, n.ID)
	if _, err := s.Pool().Exec(ctx, `UPDATE inventory_schedule SET next_verify_at=now()-interval '1 minute' WHERE network_id=$1`, n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, `INSERT INTO inventory_rechecks(network_id,item_id,token) VALUES($1,$2,$3)`, n.ID, int64(priorityID), "0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	due := func(want int) {
		t.Helper()
		var count int
		if err := s.Pool().QueryRow(ctx, `SELECT count(*) FROM inventory_schedule WHERE network_id=$1 AND item_id<>$2 AND next_verify_at<=now()`, n.ID, int64(priorityID)).Scan(&count); err != nil || count != want {
			t.Fatalf("ordinary requests still due = %d, want %d: %v", count, want, err)
		}
	}
	zero := capacityPoll(t, w.SessionID, 1, 0, 0)
	ack, err := s.Poll(ctx, d, zero)
	if err != nil || len(ack.Verify) != 1 || ack.Verify[0].ID != priorityID || ack.Verify[0].Token == "" {
		t.Fatalf("zero ordinary capacity did not preserve priority: %+v, %v", ack, err)
	}
	due(3)
	repeat, err := s.Poll(ctx, d, zero)
	if err != nil || len(repeat.Verify) != 1 || repeat.Verify[0].Token != ack.Verify[0].Token {
		t.Fatal("capacity-aware poll replay changed", repeat, err)
	}
	ack, err = s.Poll(ctx, d, capacityPoll(t, w.SessionID, 2, 16, 0))
	if err != nil || len(ack.Verify) != 0 {
		t.Fatal("zero byte capacity still delivered ordinary reads", ack, err)
	}
	due(3)
	ack, err = s.Poll(ctx, d, capacityPoll(t, w.SessionID, 3, 2, 16384))
	if err != nil || len(ack.Verify) != 2 {
		t.Fatal("receiver slot capacity ignored", ack, err)
	}
	due(1)
	// Missing capacity fields retain compatibility with already installed OC.
	ack, err = s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 4})
	if err != nil || len(ack.Verify) != 1 {
		t.Fatal("legacy poll did not receive the remaining ordinary read", ack, err)
	}
	due(0)
}
