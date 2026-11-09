package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

type refreshStatus struct {
	ID          protocol.Quantity `json:"id"`
	RequestID   string            `json:"request_id"`
	State       string            `json:"state"`
	RequestedAt time.Time         `json:"requested_at"`
	ExpiresAt   time.Time         `json:"expires_at"`
	ConfirmedAt *time.Time        `json:"confirmed_at"`
}

type refreshResult struct {
	Items      []protocol.Item     `json:"items"`
	MissingIDs []protocol.Quantity `json:"missing_ids"`
	Revision   protocol.Quantity   `json:"revision"`
	Refreshes  []refreshStatus     `json:"refreshes"`
}

type refreshPoll struct {
	Ack     protocol.Quantity `json:"ack"`
	Refresh []struct {
		ID        protocol.Quantity   `json:"id"`
		Item      protocol.Definition `json:"item"`
		Token     string              `json:"token"`
		TTLMillis int                 `json:"ttl_millis"`
	} `json:"refresh"`
}

func refreshFixture(t *testing.T, size int) (fixture, protocol.Welcome, []protocol.Quantity) {
	t.Helper()
	f := setup(t)
	w := decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v1/hello", f.device,
		`{"protocol":1,"boot_id":"refresh-boot","source":"simulator","max_response_bytes":32768,"capabilities":{"inventory":true,"exact_quantities":true,"quantity_refresh":true,"enumeration":"simulated"}}`), 200)
	var ids []protocol.Quantity
	for offset := 0; offset < size; offset += 128 {
		var updates []protocol.Update
		for i := offset; i < min(size, offset+128); i++ {
			yes := true
			updates = append(updates, protocol.Update{Item: &protocol.Definition{Kind: "item", Name: fmt.Sprintf("test:refresh_%03d", i), Label: "Refresh", Exact: true},
				Quantity: 9007199254740993, Observation: 10, Craftable: &yes})
		}
		ack := uploadBrowse(t, f, w, protocol.Quantity(offset/128+1), updates)
		for _, binding := range ack.Bindings {
			ids = append(ids, binding.ID)
		}
	}
	return f, w, ids
}

func requestRefresh(t *testing.T, f fixture, token string, ids []protocol.Quantity) refreshResult {
	t.Helper()
	return decode[refreshResult](t, send(t, f, "POST", browsePath(f)+"/refresh", token, wire(t, map[string]any{"ids": ids})), 202)
}

func lookupRefresh(t *testing.T, f fixture, id protocol.Quantity) refreshResult {
	t.Helper()
	return decode[refreshResult](t, send(t, f, "GET", browsePath(f)+"/lookup?ids="+id.String(), adminToken, ""), 200)
}

func pollRefresh(t *testing.T, f fixture, w protocol.Welcome, seq int) refreshPoll {
	t.Helper()
	body := fmt.Sprintf(`{"session_id":%q,"sequence":%q,"reports":[],"refresh_slots":8,"refresh_bytes":16384}`, w.SessionID, fmt.Sprint(seq))
	return decode[refreshPoll](t, send(t, f, "POST", "/bridge/v1/poll", f.device, body), 200)
}

func ingestRefresh(t *testing.T, f fixture, w protocol.Welcome, seq int, updates string) {
	t.Helper()
	body := fmt.Sprintf(`{"session_id":%q,"sequence":%q,"updates":[%s]}`, w.SessionID, fmt.Sprint(seq), updates)
	decode[protocol.InventoryAck](t, send(t, f, "POST", "/bridge/v1/inventory", f.device, body), 200)
}

func refreshUpdate(id protocol.Quantity, observation int, token string) string {
	return fmt.Sprintf(`{"id":%q,"quantity":"9007199254740993","observation":%q,"craftable":null,"refresh":%q}`, id.String(), fmt.Sprint(observation), token)
}

func TestHTTPItemRefreshRequiresCapabilityAndReadAccess(t *testing.T) {
	f, _, _, ids := browsingFixture(t)
	body := wire(t, map[string]any{"ids": ids[:1]})
	path := browsePath(f) + "/refresh"
	if got := send(t, f, "POST", path, adminToken, body); got.StatusCode != 409 {
		t.Fatalf("legacy bridge admission status = %d, want 409", got.StatusCode)
	}
	if got := send(t, f, "POST", path, "", body); got.StatusCode != 401 {
		t.Fatal("anonymous refresh was admitted", got.StatusCode)
	}
	_, outsider, err := f.db.CreateMember(context.Background(), f.admin.ID, "outsider", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := send(t, f, "POST", path, outsider, body); got.StatusCode != 403 {
		t.Fatal("ungranted member refreshed another network", got.StatusCode)
	}
}

func TestHTTPItemRefreshCoalescesAndConfirmsUnchangedQuantity(t *testing.T) {
	f, w, ids := refreshFixture(t, 1)
	_, viewer, err := f.db.CreateMember(context.Background(), f.admin.ID, "viewer", []protocol.Grant{{NetworkID: f.network.ID}})
	if err != nil {
		t.Fatal(err)
	}
	before := lookupRefresh(t, f, ids[0])
	if len(before.Refreshes) != 0 {
		t.Fatal("GET created a refresh", before.Refreshes)
	}
	first := requestRefresh(t, f, viewer, []protocol.Quantity{ids[0], 9223372036854775807, ids[0]})
	if len(first.Items) != 1 || first.Items[0].Quantity != 9007199254740993 || !reflect.DeepEqual(first.MissingIDs, []protocol.Quantity{9223372036854775807}) {
		t.Fatal("admission changed cached values or missing semantics", first)
	}
	if len(first.Refreshes) != 1 || first.Refreshes[0].State != "pending" || len(first.Refreshes[0].RequestID) != 32 ||
		first.Refreshes[0].ConfirmedAt != nil || first.Refreshes[0].ExpiresAt.Sub(first.Refreshes[0].RequestedAt) != time.Minute {
		t.Fatal("invalid pending generation", first.Refreshes)
	}
	second := requestRefresh(t, f, viewer, ids)
	if !reflect.DeepEqual(first.Refreshes, second.Refreshes) || !reflect.DeepEqual(before.Items, second.Items) || before.Revision != second.Revision {
		t.Fatal("duplicate admission changed deadline, generation or inventory evidence", first, second)
	}
	ack := pollRefresh(t, f, w, 1)
	if len(ack.Refresh) != 1 || ack.Refresh[0].ID != ids[0] || ack.Refresh[0].Token != first.Refreshes[0].RequestID || ack.Refresh[0].Item.Name != "test:refresh_000" ||
		ack.Refresh[0].TTLMillis < 1 || ack.Refresh[0].TTLMillis > 60000 {
		t.Fatal("refresh was not dispatched with its exact identity", ack)
	}
	if replay := pollRefresh(t, f, w, 1); !reflect.DeepEqual(ack, replay) {
		t.Fatal("replayed poll changed dispatch", ack, replay)
	}
	var cursor int64
	if err := f.db.Pool().QueryRow(context.Background(), "SELECT coalesce(max(id),0) FROM network_events WHERE network_id=$1", f.network.ID).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	update := refreshUpdate(ids[0], 11, ack.Refresh[0].Token)
	ingestRefresh(t, f, w, 2, update)
	after := lookupRefresh(t, f, ids[0])
	if after.Refreshes[0].State != "confirmed" || after.Refreshes[0].ConfirmedAt == nil || !after.Items[0].ObservedAt.After(before.Items[0].ObservedAt) {
		t.Fatal("fresh quantity receipt was not confirmed", after)
	}
	if !reflect.DeepEqual(before.Items[0].CraftableObservedAt, after.Items[0].CraftableObservedAt) || after.Revision != before.Revision {
		t.Fatal("quantity refresh changed recipe evidence or invalidated identical inventory", before, after)
	}
	ingestRefresh(t, f, w, 2, update)
	var events int
	if err := f.db.Pool().QueryRow(context.Background(), "SELECT count(*) FROM network_events WHERE network_id=$1 AND id>$2 AND kind='inventory'", f.network.ID, cursor).Scan(&events); err != nil || events != 1 {
		t.Fatal("unchanged confirmation must notify once after commit", events, err)
	}
	next := requestRefresh(t, f, viewer, ids)
	if next.Refreshes[0].RequestID == first.Refreshes[0].RequestID || next.Refreshes[0].State != "pending" {
		t.Fatal("new click reused a completed generation", next)
	}
	ingestRefresh(t, f, w, 3, refreshUpdate(ids[0], 12, first.Refreshes[0].RequestID))
	if got := lookupRefresh(t, f, ids[0]); got.Refreshes[0].State != "pending" {
		t.Fatal("old receipt confirmed a newer request", got)
	}
}

func TestHTTPItemRefreshRejectsUnsupportedCanonicalResourcesAtomically(t *testing.T) {
	f, w, ids := refreshFixture(t, 5)
	ctx := context.Background()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"UPDATE item_definitions SET kind='fluid' WHERE network_id=$1 AND id=$2", []any{f.network.ID, int64(ids[1])}},
		{"UPDATE item_definitions SET exact=false WHERE network_id=$1 AND id=ANY($2::bigint[])", []any{f.network.ID, []int64{int64(ids[2]), int64(ids[4])}}},
		{"DELETE FROM inventory WHERE network_id=$1 AND item_id=ANY($2::bigint[])", []any{f.network.ID, []int64{int64(ids[3]), int64(ids[4])}}},
		{"INSERT INTO item_aliases(network_id,alias_id,canonical_id) VALUES($1,$2,$3),($1,$4,$5)", []any{f.network.ID, int64(ids[3]), int64(ids[1]), int64(ids[4]), int64(ids[0])}},
	} {
		if _, err := f.db.Pool().Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		ids  []protocol.Quantity
	}{
		{"fluid", ids[1:2]},
		{"inexact", ids[2:3]},
		{"alias to fluid", ids[3:4]},
		{"mixed valid and fluid", ids[:2]},
		{"mixed valid and inexact", []protocol.Quantity{ids[0], ids[2]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := send(t, f, "POST", browsePath(f)+"/refresh", adminToken, wire(t, map[string]any{"ids": tc.ids}))
			body := decode[struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}](t, response, 400)
			if body.Error.Code != "invalid_request" {
				t.Fatal("unsupported resource did not return invalid_request", body)
			}
			var queued int
			if err := f.db.Pool().QueryRow(ctx, "SELECT count(*) FROM inventory_refreshes WHERE network_id=$1", f.network.ID).Scan(&queued); err != nil || queued != 0 {
				t.Fatal("rejected batch enqueued refresh work", queued, err)
			}
			if ack := pollRefresh(t, f, w, 1); len(ack.Refresh) != 0 {
				t.Fatal("rejected batch dispatched refresh work", ack)
			}
		})
	}
	accepted := requestRefresh(t, f, adminToken, []protocol.Quantity{ids[4], 9223372036854775807})
	if len(accepted.Refreshes) != 1 || accepted.Items[0].CanonicalID != ids[0] ||
		!reflect.DeepEqual(accepted.MissingIDs, []protocol.Quantity{9223372036854775807}) {
		t.Fatal("canonical eligibility or unknown-ID semantics changed", accepted)
	}
	other, device, err := f.db.CreateNetwork(ctx, f.admin.ID, "Other", "refresh-other", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	f.network, f.device = other, device
	decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v1/hello", device,
		`{"protocol":1,"boot_id":"refresh-other","source":"simulator","max_response_bytes":32768,"capabilities":{"inventory":true,"quantity_refresh":true}}`), 200)
	missing := requestRefresh(t, f, adminToken, ids[:3])
	if len(missing.Items) != 0 || len(missing.Refreshes) != 0 || !reflect.DeepEqual(missing.MissingIDs, ids[:3]) {
		t.Fatal("cross-network resources must remain missing regardless of kind or exactness", missing)
	}
}

func TestHTTPItemRefreshRejectsStaleUndispatchedExpiredAndMergedReceipts(t *testing.T) {
	f, w, ids := refreshFixture(t, 1)
	first := requestRefresh(t, f, adminToken, ids).Refreshes[0]
	ingestRefresh(t, f, w, 2, refreshUpdate(ids[0], 11, first.RequestID))
	if lookupRefresh(t, f, ids[0]).Refreshes[0].State != "pending" {
		t.Fatal("undispatched request was confirmed")
	}
	pollRefresh(t, f, w, 1)
	// A receipt older than the stored quantity cannot hitchhike on a fresh count.
	ingestRefresh(t, f, w, 3, refreshUpdate(ids[0], 11, first.RequestID)+","+refreshUpdate(ids[0], 12, ""))
	if lookupRefresh(t, f, ids[0]).Refreshes[0].State != "pending" {
		t.Fatal("stale merged receipt confirmed a fresh tokenless count")
	}
	// A fresh receipt survives a later independently observed quantity.
	ingestRefresh(t, f, w, 4, refreshUpdate(ids[0], 14, "")+","+refreshUpdate(ids[0], 13, first.RequestID))
	if lookupRefresh(t, f, ids[0]).Refreshes[0].State != "confirmed" {
		t.Fatal("fresh receipt was lost during quantity merge")
	}
	next := requestRefresh(t, f, adminToken, ids).Refreshes[0]
	dispatched := pollRefresh(t, f, w, 2)
	if len(dispatched.Refresh) != 1 || dispatched.Refresh[0].Token != next.RequestID {
		t.Fatal("expiry test requires the new generation dispatched in the current session", dispatched)
	}
	if _, err := f.db.Pool().Exec(context.Background(), "UPDATE inventory_refreshes SET requested_at=now()-interval '2 minutes',expires_at=now()-interval '1 minute' WHERE network_id=$1", f.network.ID); err != nil {
		t.Fatal(err)
	}
	ingestRefresh(t, f, w, 5, refreshUpdate(ids[0], 15, next.RequestID))
	if got := lookupRefresh(t, f, ids[0]); got.Refreshes[0].State != "expired" || got.Refreshes[0].ConfirmedAt != nil {
		t.Fatal("expired receipt revived a timed-out request", got)
	}
}

func TestHTTPItemRefreshCapacityAndInputValidation(t *testing.T) {
	f, _, ids := refreshFixture(t, 160)
	for _, body := range []string{`{}`, `{"ids":[]}`, `{"ids":[1]}`, `{"ids":["01"]}`, `{"ids":["0"]}`, `{"ids":["-1"]}`, `{"ids":["9223372036854775808"]}`, `{"ids":["1"],"extra":true}`,
		`{"ids":[` + strings.TrimSuffix(strings.Repeat(`"1",`, 33), ",") + `]}`} {
		if r := send(t, f, "POST", browsePath(f)+"/refresh", adminToken, body); r.StatusCode != 400 {
			t.Fatalf("malformed admission status %d: %s", r.StatusCode, body)
		}
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 5)
	for offset := 0; offset < 160; offset += 32 {
		body := wire(t, map[string]any{"ids": ids[offset : offset+32]})
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", f.server.URL+browsePath(f)+"/refresh", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+adminToken)
			response, err := f.server.Client().Do(req)
			if err != nil {
				statuses <- 0
				return
			}
			defer response.Body.Close()
			statuses <- response.StatusCode
		}()
	}
	wg.Wait()
	close(statuses)
	accepted, rejected := 0, 0
	for status := range statuses {
		switch status {
		case 202:
			accepted++
		case 429:
			rejected++
		default:
			t.Fatal("unexpected concurrent admission status", status)
		}
	}
	var count int
	if err := f.db.Pool().QueryRow(context.Background(), "SELECT count(*) FROM inventory_refreshes WHERE network_id=$1", f.network.ID).Scan(&count); err != nil || count != 128 || accepted != 4 || rejected != 1 {
		t.Fatal("concurrent admission escaped its atomic queue budget", count, accepted, rejected, err)
	}
	// Saturation must still allow duplicate requests for already-pending work.
	var existing protocol.Quantity
	if err := f.db.Pool().QueryRow(context.Background(), "SELECT item_id FROM inventory_refreshes WHERE network_id=$1 LIMIT 1", f.network.ID).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	requestRefresh(t, f, adminToken, []protocol.Quantity{existing})
}

func TestHTTPItemRefreshDispatchBudgetsAndOrderIsolation(t *testing.T) {
	f, w, ids := refreshFixture(t, 16)
	requestRefresh(t, f, adminToken, ids)
	orderToken := strings.Repeat("a", 32)
	if _, err := f.db.Pool().Exec(context.Background(), "INSERT INTO inventory_rechecks(network_id,item_id,token) VALUES($1,$2,$3)", f.network.ID, int64(ids[0]), orderToken); err != nil {
		t.Fatal(err)
	}
	missingCapacity := fmt.Sprintf(`{"session_id":%q,"sequence":"1","reports":[]}`, w.SessionID)
	if ack := decode[refreshPoll](t, send(t, f, "POST", "/bridge/v1/poll", f.device, missingCapacity), 200); len(ack.Refresh) != 0 {
		t.Fatal("unspecified device capacity admitted work", ack)
	}
	ack := pollRefresh(t, f, w, 2)
	if len(ack.Refresh) != 4 {
		t.Fatal("refresh dispatch did not respect its four-entry window", ack)
	}
	if _, err := f.db.Pool().Exec(context.Background(), "UPDATE bridge_sessions SET response_bytes=2048 WHERE id=$1", w.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool().Exec(context.Background(), "UPDATE item_definitions SET label=repeat('x',512) WHERE network_id=$1", f.network.ID); err != nil {
		t.Fatal(err)
	}
	limited := fmt.Sprintf(`{"session_id":%q,"sequence":"3","reports":[],"refresh_slots":1,"refresh_bytes":16384}`, w.SessionID)
	response := send(t, f, "POST", "/bridge/v1/poll", f.device, limited)
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 {
		t.Fatal(response.StatusCode, err, string(data))
	}
	var bounded protocol.PollAck
	if err := json.Unmarshal(data, &bounded); err != nil {
		t.Fatal(err)
	}
	if len(bounded.Refresh) > 1 || len(data) > 2048 {
		t.Fatal("refresh exceeded its feedback or response bound", bounded)
	}
	// A quantity-only lane must never clear the recipe-required order recheck.
	for _, command := range ack.Refresh {
		if command.ID == ids[0] {
			ingestRefresh(t, f, w, 2, refreshUpdate(ids[0], 11, command.Token))
		}
	}
	var retained string
	if err := f.db.Pool().QueryRow(context.Background(), "SELECT token FROM inventory_rechecks WHERE network_id=$1 AND item_id=$2", f.network.ID, int64(ids[0])).Scan(&retained); err != nil || retained != orderToken {
		t.Fatal("quantity refresh cleared order confirmation", retained, err)
	}
}

func TestHTTPItemRefreshRotatesOversizedCandidateWindow(t *testing.T) {
	f, w, ids := refreshFixture(t, 33)
	requestRefresh(t, f, adminToken, ids[:32])
	requestRefresh(t, f, adminToken, ids[32:])
	ctx := context.Background()
	if _, err := f.db.Pool().Exec(ctx, "UPDATE item_definitions SET label=repeat('x',512) WHERE network_id=$1 AND id<>$2", f.network.ID, int64(ids[32])); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool().Exec(ctx, `UPDATE inventory_refreshes SET next_attempt_at=
		CASE WHEN item_id=$2 THEN now()-interval '1 minute' ELSE now()-interval '2 minutes' END
		WHERE network_id=$1`, f.network.ID, int64(ids[32])); err != nil {
		t.Fatal(err)
	}
	idStrings := make([]string, len(ids))
	for i, id := range ids {
		idStrings[i] = id.String()
	}
	before := decode[refreshResult](t, send(t, f, "GET", browsePath(f)+"/lookup?ids="+strings.Join(idStrings, ","), adminToken, ""), 200)
	poll := func(sequence int) refreshPoll {
		t.Helper()
		body := fmt.Sprintf(`{"session_id":%q,"sequence":%q,"reports":[],"refresh_slots":8,"refresh_bytes":512}`, w.SessionID, fmt.Sprint(sequence))
		return decode[refreshPoll](t, send(t, f, "POST", "/bridge/v1/poll", f.device, body), 200)
	}
	if first := poll(1); len(first.Refresh) != 0 {
		t.Fatal("oversized prefix should fill the first bounded candidate window without dispatch", first)
	}
	second := poll(2)
	if len(second.Refresh) != 1 || second.Refresh[0].ID != ids[32] {
		t.Fatal("rotated candidate window did not reach the small request", second)
	}
	var undispatched int
	if err := f.db.Pool().QueryRow(ctx, `SELECT count(*) FROM inventory_refreshes
		WHERE network_id=$1 AND item_id<>$2 AND dispatched_session='' AND next_attempt_at>now()`,
		f.network.ID, int64(ids[32])).Scan(&undispatched); err != nil || undispatched != 32 {
		t.Fatal("nonfitting candidates did not rotate without authorizing receipts", undispatched, err)
	}
	for _, status := range before.Refreshes {
		after := lookupRefresh(t, f, status.ID)
		if len(after.Refreshes) != 1 || !reflect.DeepEqual(after.Refreshes[0], status) {
			t.Fatal("candidate rotation changed generation or deadline", status, after)
		}
	}
}

func TestHTTPItemRefreshAliasAndRetainedStatusesAreBounded(t *testing.T) {
	f, _, ids := refreshFixture(t, 258)
	ctx := context.Background()
	if _, err := f.db.Pool().Exec(ctx, "DELETE FROM inventory WHERE network_id=$1 AND item_id=$2", f.network.ID, int64(ids[1])); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool().Exec(ctx, "INSERT INTO item_aliases(network_id,alias_id,canonical_id) VALUES($1,$2,$3)", f.network.ID, int64(ids[1]), int64(ids[0])); err != nil {
		t.Fatal(err)
	}
	aliased := requestRefresh(t, f, adminToken, ids[:2])
	if len(aliased.Refreshes) != 2 || aliased.Refreshes[0].RequestID != aliased.Refreshes[1].RequestID || aliased.Items[1].CanonicalID != ids[0] {
		t.Fatal("aliases did not share the canonical generation", aliased)
	}
	if _, err := f.db.Pool().Exec(ctx, "DELETE FROM inventory_refreshes WHERE network_id=$1", f.network.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool().Exec(ctx, `INSERT INTO inventory_refreshes(network_id,item_id,token,requested_at,expires_at)
		SELECT network_id,item_id,md5(item_id::text),now()-interval '2 minutes',now()-interval '1 minute'
		FROM inventory WHERE network_id=$1 AND item_id<>$2 ORDER BY item_id LIMIT 256`, f.network.ID, int64(ids[257])); err != nil {
		t.Fatal(err)
	}
	var oldest protocol.Quantity
	if err := f.db.Pool().QueryRow(ctx, "SELECT item_id FROM inventory_refreshes WHERE network_id=$1 ORDER BY requested_at,item_id LIMIT 1", f.network.ID).Scan(&oldest); err != nil {
		t.Fatal(err)
	}
	requestRefresh(t, f, adminToken, []protocol.Quantity{ids[257]})
	var count int
	if err := f.db.Pool().QueryRow(ctx, "SELECT count(*) FROM inventory_refreshes WHERE network_id=$1", f.network.ID).Scan(&count); err != nil || count != 256 {
		t.Fatal("terminal status retention grew without bound", count, err)
	}
	if got := lookupRefresh(t, f, oldest); len(got.Refreshes) != 0 {
		t.Fatal("oldest terminal status was not evicted", got)
	}
	if err := f.db.Migrate(ctx); err != nil {
		t.Fatal("repeated migration failed", err)
	}
	if got := lookupRefresh(t, f, ids[257]); len(got.Refreshes) != 1 || got.Refreshes[0].State != "pending" {
		t.Fatal("repeated migration lost a pending request", got)
	}
}

func TestHTTPItemRefreshPrecedesOrdinaryWithoutDisplacingOrderVerification(t *testing.T) {
	f, w, ids := refreshFixture(t, 32)
	requestRefresh(t, f, adminToken, ids[1:2])
	ctx := context.Background()
	for _, query := range []string{`UPDATE item_definitions SET label=repeat('x',512) WHERE network_id=$1`,
		`UPDATE inventory SET observed_at=now()-interval '1 hour',craftable_observed_at=now()-interval '1 hour' WHERE network_id=$1`,
		`UPDATE inventory_schedule SET next_verify_at=now()-interval '1 hour' WHERE network_id=$1`} {
		if _, err := f.db.Pool().Exec(ctx, query, f.network.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Pool().Exec(ctx, "INSERT INTO inventory_rechecks(network_id,item_id,token) VALUES($1,$2,$3)", f.network.ID, int64(ids[0]), strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool().Exec(ctx, "UPDATE bridge_sessions SET response_bytes=2048 WHERE id=$1", w.SessionID); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"session_id":%q,"sequence":"1","reports":[],"refresh_slots":8,"refresh_bytes":16384}`, w.SessionID)
	ack := decode[protocol.PollAck](t, send(t, f, "POST", "/bridge/v1/poll", f.device, body), 200)
	if len(ack.Verify) == 0 || ack.Verify[0].ID != ids[0] || ack.Verify[0].Token == "" {
		t.Fatal("refresh displaced an order confirmation", ack)
	}
	if len(ack.Refresh) == 0 || ack.Refresh[0].ID != ids[1] {
		t.Fatal("ordinary verification consumed every refresh response opportunity", ack)
	}
}

func TestHTTPItemRefreshBrowserCSRFAndReadOnlyStatus(t *testing.T) {
	f, _, ids := refreshFixture(t, 1)
	login := send(t, f, "POST", "/api/v1/session", "", `{"token":"`+adminToken+`"}`)
	cookies := login.Cookies()
	session := decode[struct {
		CSRF string `json:"csrf"`
	}](t, login, 201)
	call := func(csrf, origin string) int {
		req, err := http.NewRequest("POST", f.server.URL+browsePath(f)+"/refresh", strings.NewReader(wire(t, map[string]any{"ids": ids})))
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(cookies[0])
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", csrf)
		res, err := f.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	if status := call("", "http://localhost"); status != 403 {
		t.Fatal("missing CSRF admitted refresh", status)
	}
	if status := call(session.CSRF, "https://unrelated.example"); status != 403 {
		t.Fatal("foreign Origin admitted refresh", status)
	}
	if status := call(session.CSRF, "http://localhost"); status != 202 {
		t.Fatal("valid browser refresh rejected", status)
	}
	first := lookupRefresh(t, f, ids[0])
	second := lookupRefresh(t, f, ids[0])
	if !reflect.DeepEqual(first, second) {
		t.Fatal("cached lookup changed refresh or inventory evidence", first, second)
	}
}

func TestHTTPItemRefreshNewSessionRejectsOldReceipt(t *testing.T) {
	f, w, ids := refreshFixture(t, 1)
	requestRefresh(t, f, adminToken, ids)
	command := pollRefresh(t, f, w, 1).Refresh[0]
	next := decode[protocol.Welcome](t, send(t, f, "POST", "/bridge/v1/hello", f.device,
		`{"protocol":1,"boot_id":"refresh-next","source":"simulator","max_response_bytes":32768,"capabilities":{"inventory":true,"quantity_refresh":true}}`), 200)
	body := fmt.Sprintf(`{"session_id":%q,"sequence":"2","updates":[%s]}`, w.SessionID, refreshUpdate(ids[0], 11, command.Token))
	if response := send(t, f, "POST", "/bridge/v1/inventory", f.device, body); response.StatusCode != 409 {
		t.Fatal("retired session confirmed a request", response.StatusCode)
	}
	// A token carried into the new session still needs dispatch in that session.
	ingestRefresh(t, f, next, 1, refreshUpdate(ids[0], 1, command.Token))
	if got := lookupRefresh(t, f, ids[0]); got.Refreshes[0].State != "pending" {
		t.Fatal("old dispatch authorized a new-session receipt", got)
	}
	if _, err := f.db.Pool().Exec(context.Background(), "UPDATE inventory_refreshes SET next_attempt_at=now()-interval '1 second' WHERE network_id=$1", f.network.ID); err != nil {
		t.Fatal(err)
	}
	retry := pollRefresh(t, f, next, 1)
	if len(retry.Refresh) != 1 || retry.Refresh[0].Token != command.Token {
		t.Fatal("new session lost pending work", retry)
	}
	ingestRefresh(t, f, next, 2, refreshUpdate(ids[0], 2, command.Token))
	if got := lookupRefresh(t, f, ids[0]); got.Refreshes[0].State != "confirmed" {
		t.Fatal("fresh new-session read did not confirm", got)
	}
}

func TestHTTPItemRefreshJSONReceiptIsExplicitQuantityOnly(t *testing.T) {
	var update protocol.Update
	data := `{"id":"9007199254740993","quantity":"9007199254740995","observation":"12","craftable":null,"refresh":"0123456789abcdef0123456789abcdef"}`
	if err := json.Unmarshal([]byte(data), &update); err != nil || update.Quantity != 9007199254740995 || update.Craftable != nil {
		t.Fatal("quantity-only receipt lost precision or recipe independence", update, err)
	}
}
