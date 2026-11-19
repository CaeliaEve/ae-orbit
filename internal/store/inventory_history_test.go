package store_test

import (
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestInventoryHistoryChangesAreExactAndDoNotLogRepeatedEvidence(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "history")
	b := batch(w.SessionID, 1, 10, 9007199254740993)
	ack, err := s.Ingest(ctx, d, b)
	if err != nil {
		t.Fatal(err)
	}
	id := ack.Bindings[0].ID
	if err = s.RecordInventoryHistory(ctx); err != nil {
		t.Fatal(err)
	}
	q := store.HistoryQuery{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Item: int64(id), Limit: 1}
	page, err := s.InventoryHistoryRecords(ctx, admin.ID, n.ID, q, 0)
	if err != nil || len(page.Records) != 1 || page.Records[0].Kind != "first" || page.Records[0].Delta != nil {
		t.Fatal(page, err)
	}
	snapshot := page.Snapshot
	for seq := protocol.Quantity(2); seq <= 3; seq++ {
		if _, err = s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: seq, Updates: []protocol.Update{{ID: id, Quantity: 9007199254740993, Observation: 10 + seq}}}); err != nil {
			t.Fatal(err)
		}
	}
	page, err = s.InventoryHistoryRecords(ctx, admin.ID, n.ID, q, 0)
	if err != nil || page.HasMore {
		t.Fatal("repeated evidence was logged", page, err)
	}
	if _, err = s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 4, Updates: []protocol.Update{{ID: id, Quantity: 9007199254740994, Observation: 20}}}); err != nil {
		t.Fatal(err)
	}
	page, err = s.InventoryHistoryRecords(ctx, admin.ID, n.ID, q, 0)
	if err != nil || !page.HasMore || page.Records[0].Delta == nil || *page.Records[0].Delta != "1" || page.Records[0].Quantity != "9007199254740994" {
		t.Fatal(page, err)
	}
	q.Before = 0
	oldSnapshot := int64(0)
	if err = s.Pool().QueryRow(ctx, `SELECT $1::bigint`, snapshot).Scan(&oldSnapshot); err != nil {
		t.Fatal(err)
	}
	frozen, err := s.InventoryHistoryRecords(ctx, admin.ID, n.ID, q, oldSnapshot)
	if err != nil || len(frozen.Records) != 1 || frozen.Records[0].Kind != "first" {
		t.Fatal("snapshot admitted new rows", frozen, err)
	}
	series, err := s.InventoryHistorySeries(ctx, admin.ID, n.ID, q)
	if err != nil || series.Basis != "last_known_quantity" || series.Item == nil || series.Item.Quantity != "9007199254740994" {
		t.Fatal(series, err)
	}
	if _, err = s.InventoryHistoryRecords(ctx, "missing-member", n.ID, q, 0); err == nil {
		t.Fatal("history read bypassed network access")
	}
	var minute, hour, day int
	if err = s.Pool().QueryRow(ctx, `SELECT count(*) FILTER(WHERE resolution=60),count(*) FILTER(WHERE resolution=3600),count(*) FILTER(WHERE resolution=86400) FROM inventory_history_totals`).Scan(&minute, &hour, &day); err != nil || minute != 1 || hour != 1 || day != 1 {
		t.Fatal(minute, hour, day, err)
	}
	if err = s.RecordInventoryHistory(ctx); err != nil {
		t.Fatal(err)
	}
	var samples int
	if err = s.Pool().QueryRow(ctx, `SELECT samples FROM inventory_history_totals WHERE resolution=3600`).Scan(&samples); err != nil || samples != 1 {
		t.Fatal("recorder duplicated minute", samples, err)
	}
}

func TestInventoryHistoryBaselineAndZeroDoNotInventProduction(t *testing.T) {
	s, admin, n, token := fixture(t)
	if _, err := s.Pool().Exec(ctx, `ALTER TABLE inventory DISABLE TRIGGER inventory_history_capture`); err != nil {
		t.Fatal(err)
	}
	d, w := hello(t, s, token, "history-baseline")
	ack, err := s.Ingest(ctx, d, batch(w.SessionID, 1, 10, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, `ALTER TABLE inventory ENABLE TRIGGER inventory_history_capture`); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordInventoryHistory(ctx); err != nil {
		t.Fatal(err)
	}
	q := store.HistoryQuery{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)}
	page, err := s.InventoryHistoryRecords(ctx, admin.ID, n.ID, q, 0)
	if err != nil || len(page.Records) != 1 || page.Records[0].Kind != "baseline" || page.Records[0].Delta != nil {
		t.Fatal(page, err)
	}
	id := ack.Bindings[0].ID
	if _, err = s.Ingest(ctx, d, protocol.InventoryBatch{SessionID: w.SessionID, Sequence: 2, Updates: []protocol.Update{{ID: id, Quantity: 7, Observation: 20}}}); err != nil {
		t.Fatal(err)
	}
	q.Direction = "increase"
	page, err = s.InventoryHistoryRecords(ctx, admin.ID, n.ID, q, 0)
	if err != nil || len(page.Records) != 1 || *page.Records[0].Delta != "7" {
		t.Fatal(page, err)
	}
	q.Direction = "decrease"
	page, err = s.InventoryHistoryRecords(ctx, admin.ID, n.ID, q, 0)
	if err != nil || len(page.Records) != 0 {
		t.Fatal(page, err)
	}
	items, err := s.InventoryHistoryItems(ctx, admin.ID, n.ID, 0, "@minecraft")
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	if _, err = s.InventoryHistoryItems(ctx, admin.ID, n.ID, 0, "@"); err == nil {
		t.Fatal("invalid search accepted")
	}
}

func TestInventoryHistoryPlainProfileStartsNewEpochAndPreservesOldHistory(t *testing.T) {
	f := plainFixture(t)
	if err := f.s.RecordInventoryHistory(ctx); err != nil {
		t.Fatal(err)
	}
	old, err := f.s.InventoryHistorySeries(ctx, f.admin.ID, f.network.ID, store.HistoryQuery{})
	if err != nil || old.Epoch == "0" {
		t.Fatal(old, err)
	}
	enablePlain(t, f)
	if err = f.s.RecordInventoryHistory(ctx); err != nil {
		t.Fatal(err)
	}
	f.wb = safePlainHello(t, f, f.b, "history-safe-worker")
	slots, bytes := 16, 16384
	var updates []protocol.Update
	for seq := protocol.Quantity(1); seq <= 4; seq++ {
		f.round(t)
		ack, e := f.s.PollV2(ctx, f.b, protocol.Poll{SessionID: f.wb.SessionID, Sequence: seq, ReadSlots: &slots, ReadBytes: &bytes})
		if e != nil {
			t.Fatal(e)
		}
		for _, read := range ack.Reads {
			updates = append(updates, protocol.Update{ID: read.ID, Quantity: 7, Observation: protocol.Quantity(len(updates) + 1), AssignmentID: read.AssignmentID})
		}
	}
	if len(updates) != 3 {
		t.Fatal(updates)
	}
	if _, err = f.s.IngestV2(ctx, f.b, protocol.InventoryBatch{SessionID: f.wb.SessionID, Sequence: 1, Updates: updates}); err != nil {
		t.Fatal(err)
	}
	series, err := f.s.InventoryHistorySeries(ctx, f.admin.ID, f.network.ID, store.HistoryQuery{})
	if err != nil || series.Epoch == old.Epoch || len(series.Epochs) != 2 {
		t.Fatal(series, err)
	}
	page, err := f.s.InventoryHistoryRecords(ctx, f.admin.ID, f.network.ID, store.HistoryQuery{}, 0)
	if err != nil || len(page.Records) != 3 {
		t.Fatal(page, err)
	}
	for _, r := range page.Records {
		if r.Kind != "first" || r.Delta != nil {
			t.Fatal("new catalog stock counted as production", r)
		}
	}
	if err = f.s.RecordInventoryHistory(ctx); err != nil {
		t.Fatal(err)
	}
	page, err = f.s.InventoryHistoryRecords(ctx, f.admin.ID, f.network.ID, store.HistoryQuery{}, 0)
	if err != nil || len(page.Records) != 3 {
		t.Fatal("baseline duplicated new observations", page, err)
	}
	var oldEpoch int64
	if err = f.s.Pool().QueryRow(ctx, `SELECT $1::bigint`, old.Epoch).Scan(&oldEpoch); err != nil {
		t.Fatal(err)
	}
	previous, err := f.s.InventoryHistoryRecords(ctx, f.admin.ID, f.network.ID, store.HistoryQuery{Epoch: oldEpoch}, 0)
	if err != nil || len(previous.Records) == 0 {
		t.Fatal("profile change lost original history", previous, err)
	}
}

func TestInventoryHistoryLongRetentionAndPartialRollupBoundaries(t *testing.T) {
	s, admin, n, token := fixture(t)
	d, w := hello(t, s, token, "history-boundaries")
	if _, err := s.Ingest(ctx, d, batch(w.SessionID, 1, 10, 7)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordInventoryHistory(ctx); err != nil {
		t.Fatal(err)
	}
	long := store.HistoryQuery{From: time.Now().AddDate(-10, 0, 0), To: time.Now().Add(time.Hour)}
	page, err := s.InventoryHistoryRecords(ctx, admin.ID, n.ID, long, 0)
	if err != nil || len(page.Records) != 1 {
		t.Fatal("long-retained detail became unreadable", page, err)
	}
	series, err := s.InventoryHistorySeries(ctx, admin.ID, n.ID, long)
	if err != nil || series.Resolution <= 86400 || len(series.Points) != 1 {
		t.Fatal("long range was not downsampled", series, err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err = s.Pool().Exec(ctx, `DELETE FROM inventory_history_totals WHERE epoch_id=$1::bigint`, series.Epoch); err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct{ resolution, minute, quantity int }{{60, 10, 10}, {60, 30, 30}, {3600, 30, 30}, {86400, 30, 30}} {
		if _, err = s.Pool().Exec(ctx, `INSERT INTO inventory_history_totals(epoch_id,resolution,bucket,sampled_at,total_quantity,minimum,maximum,entries,checked,candidates,online,all_online)
		VALUES($1::bigint,$2,$3,$4,$5,$5,$5,1,1,1,true,true)`, series.Epoch, sample.resolution, start.Add(time.Duration((sample.minute*60/sample.resolution)*sample.resolution)*time.Second), start.Add(time.Duration(sample.minute)*time.Minute), sample.quantity); err != nil {
			t.Fatal(err)
		}
	}
	series, err = s.InventoryHistorySeries(ctx, admin.ID, n.ID, store.HistoryQuery{From: start, To: start.Add(20 * time.Minute), Resolution: 86400})
	if err != nil || len(series.Points) != 1 || series.Points[0].Quantity != "10" || !series.Points[0].At.Equal(start.Add(10*time.Minute)) {
		t.Fatal("end boundary included future rollup or lost earlier sample", series, err)
	}
	series, err = s.InventoryHistorySeries(ctx, admin.ID, n.ID, store.HistoryQuery{From: start.Add(20 * time.Minute), To: start.Add(time.Hour), Resolution: 86400})
	if err != nil || len(series.Points) != 1 || series.Points[0].Minimum != "30" {
		t.Fatal("start boundary included older sample", series, err)
	}
}
