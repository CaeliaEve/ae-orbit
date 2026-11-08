package store_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

type scanTimes struct {
	First   *time.Time `json:"first_scan"`
	Last    *time.Time `json:"last_scan"`
	Started *time.Time `json:"session_started_at"`
}

func scanTiming(t *testing.T, s *store.Store, member, network string) scanTimes {
	t.Helper()
	n, err := s.Network(ctx, member, network)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	var got scanTimes
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestFirstScanPersistsAcrossScansAndRetries(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "scan-first")
	before := scanTiming(t, s, admin.ID, n.ID)
	if before.Started == nil || before.First != nil || before.Last != nil {
		t.Fatal("invalid new session timing", before)
	}
	b := protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 1, Updates: []protocol.Update{}, ScanCompleted: true}
	ack, err := s.Ingest(ctx, d, b)
	if err != nil {
		t.Fatal(err)
	}
	first := scanTiming(t, s, admin.ID, n.ID)
	if first.First == nil || first.Last == nil || !first.First.Equal(*first.Last) || first.First.Before(*before.Started) {
		t.Fatal("first accepted scan was not persisted", first)
	}
	retry, err := s.Ingest(ctx, d, b)
	if err != nil || !reflect.DeepEqual(ack, retry) || !reflect.DeepEqual(first, scanTiming(t, s, admin.ID, n.ID)) {
		t.Fatal("identical retry changed scan timing", err)
	}
	b.Sequence = 2
	if _, err := s.Ingest(ctx, d, b); err != nil {
		t.Fatal(err)
	}
	later := scanTiming(t, s, admin.ID, n.ID)
	if !later.First.Equal(*first.First) || later.Last.Before(*first.Last) {
		t.Fatal("later scan overwrote the first completion", later)
	}
	_, next := hello(t, s, token, "scan-second")
	reset := scanTiming(t, s, admin.ID, n.ID)
	if reset.First != nil || reset.Last != nil || reset.Started == nil {
		t.Fatal("new session kept old scan timing", reset)
	}
	if _, err := s.Ingest(ctx, d, b); !errors.Is(err, store.ErrStaleSession) {
		t.Fatal("old scan accepted", err)
	}
	b.SessionID = next.SessionID
	b.Sequence = 1
	if _, err := s.Ingest(ctx, d, b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RotateDeviceToken(ctx, admin.ID, n.ID); err != nil {
		t.Fatal(err)
	}
	reset = scanTiming(t, s, admin.ID, n.ID)
	if reset.First != nil || reset.Last != nil || reset.Started != nil {
		t.Fatal("token rotation kept scan timing", reset)
	}
}

func TestFirstScanMigrationDoesNotInventHistoricalTiming(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "scan-migration")
	b := batch(w.SessionID, 1, 1, 9007199254740993)
	b.ScanCompleted = true
	ack, err := s.Ingest(ctx, d, b)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, `ALTER TABLE networks DROP COLUMN first_scan;
		DELETE FROM schema_versions WHERE version='migrations/007_first_scan.sql'`); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	retry, err := s.Ingest(ctx, d, b)
	if err != nil || !reflect.DeepEqual(ack, retry) {
		t.Fatal("migration changed a frozen ACK", err)
	}
	b.Sequence = 2
	b.Updates = nil
	if _, err = s.Ingest(ctx, d, b); err != nil {
		t.Fatal(err)
	}
	if got := scanTiming(t, s, admin.ID, n.ID); got.First != nil || got.Last == nil {
		t.Fatal("migration fabricated the first historical scan", got)
	}
	after, err := s.Items(ctx, admin.ID, n.ID, protocol.ItemQuery{})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("scan migration changed inventory", err)
	}
}
