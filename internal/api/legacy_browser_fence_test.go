package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

func TestHTTPLegacyBrowserFenceTypedErrorAndJobConflict(t *testing.T) {
	f, jobsPath, in := jobHTTPFixture(t)
	ordersPath := strings.TrimSuffix(jobsPath, "/jobs") + "/orders"
	const key = "draft-http-source-001"
	orderIn := protocol.OrderInput{ItemID: in.Entries[0].ItemID, Quantity: 1, IdempotencyKey: key}
	rejected := decode[struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}](t, send(t, f, "POST", ordersPath, adminToken, wire(t, orderIn)), http.StatusConflict)
	if rejected.Error.Code != "client_upgrade_required" {
		t.Fatal("stale browser received an error it might treat as a safe rejection", rejected)
	}
	orderIn.IdempotencyKey = "seed-http-legacy-001"
	retained := decode[protocol.Order](t, send(t, f, "POST", ordersPath, adminToken, wire(t, orderIn)), http.StatusCreated)
	if _, err := f.db.Pool().Exec(context.Background(), "UPDATE orders SET idempotency_key=$2 WHERE id=$1", retained.ID, key); err != nil {
		t.Fatal(err)
	}
	orderIn.IdempotencyKey = key
	replay := decode[protocol.Order](t, send(t, f, "POST", ordersPath, adminToken, wire(t, orderIn)), http.StatusCreated)
	if replay.ID != retained.ID {
		t.Fatal("legacy HTTP replay changed identity", replay)
	}
	in.Entries[0].EntryKey = key
	conflict := decode[struct {
		Error struct {
			Code   string   `json:"code"`
			JobIDs []string `json:"job_ids"`
		} `json:"error"`
	}](t, send(t, f, "POST", jobsPath, adminToken, wire(t, in)), http.StatusConflict)
	if conflict.Error.Code != "conflict" || len(conflict.Error.JobIDs) != 0 {
		t.Fatal("legacy source conflict was not ambiguous/locked", conflict)
	}
	if response := send(t, f, "GET", jobsPath+"/by-key/"+in.RequestKey, adminToken, ""); response.StatusCode != http.StatusNotFound {
		t.Fatal("legacy collision left a job receipt", response.StatusCode)
	}
}
