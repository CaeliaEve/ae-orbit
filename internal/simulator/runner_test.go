package simulator_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/api"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/simulator"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"github.com/CaeliaEve/ae-orbit/internal/testutil"
)

func TestSimulatorRetriesCommittedBatchAndBoundsCache(t *testing.T) {
	ctx := context.Background()
	options := store.DefaultOptions()
	options.Limits.PollMillis = 1
	db := testutil.DatabaseWithOptions(t, options)
	admin, err := db.Bootstrap(ctx, "test-admin-token-abcdefghijklmnopqrstuvwxyz0123456789")
	if err != nil {
		t.Fatal(err)
	}
	network, token, err := db.CreateNetwork(ctx, admin.ID, "simulator", "sim-server", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	backend := api.New(db, api.DefaultOptions())
	var lost atomic.Bool
	var maxBatch atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bridge/v1/inventory" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			r.Body.Close()
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			var b protocol.InventoryBatch
			if err = json.Unmarshal(body, &b); err != nil {
				t.Error(err)
				return
			}
			if int64(len(b.Updates)) > maxBatch.Load() {
				maxBatch.Store(int64(len(b.Updates)))
			}
			if lost.CompareAndSwap(false, true) {
				recorder := httptest.NewRecorder()
				backend.ServeHTTP(recorder, r)
				if recorder.Code != 200 {
					t.Error(recorder.Body.String())
				}
				w.WriteHeader(503)
				return
			}
		}
		backend.ServeHTTP(w, r)
	}))
	defer server.Close()
	runner, err := simulator.New(simulator.Config{URL: server.URL, Token: token, Items: 257, BatchItems: 17, CacheEntries: 8})
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40 && runner.Stats().CatalogSent < 257; i++ {
		if err = runner.Step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := db.InventoryStats(ctx, admin.ID, network.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Entries != "257" {
		t.Fatal("catalog incomplete", stats, runner.Stats())
	}
	if maxBatch.Load() > 17 || runner.Stats().CacheEntries > 8 {
		t.Fatal("device buffers unbounded")
	}
	page, err := db.Items(ctx, admin.ID, network.ID, protocol.ItemQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	order, err := db.CreateOrder(ctx, admin.ID, network.ID, protocol.OrderInput{ItemID: page.Items[0].ID, Quantity: 64, IdempotencyKey: "simulated-order-001"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if err = runner.Step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.Order(ctx, admin.ID, network.ID, order.ID)
	if err != nil || got.State != "done" {
		t.Fatal(got, err)
	}
	n, err := db.Network(ctx, admin.ID, network.ID)
	if err != nil || n.Source != "simulator" {
		t.Fatal("simulation not labeled", n, err)
	}
}

func TestSimulatorRejectsConfigurationAndOversizeResponse(t *testing.T) {
	if _, err := simulator.New(simulator.Config{URL: "https://example.com", Token: "short", Items: 10, BatchItems: 1, CacheEntries: 1}); err == nil {
		t.Fatal("weak token accepted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, strings.Repeat("x", 1<<20)) }))
	defer server.Close()
	runner, err := simulator.New(simulator.Config{URL: server.URL, Token: strings.Repeat("x", 32), Items: 10, BatchItems: 1, CacheEntries: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.Connect(context.Background()); err == nil {
		t.Fatal("unbounded response accepted")
	}
}
