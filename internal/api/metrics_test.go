package api_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/api"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"github.com/CaeliaEve/ae-orbit/internal/testutil"
)

type durationMetric struct {
	Count   string  `json:"count"`
	Errors  string  `json:"errors"`
	TotalMS float64 `json:"total_ms"`
	MaxMS   float64 `json:"max_ms"`
}

type performanceResponse struct {
	HTTP []struct {
		Route             string         `json:"route"`
		Duration          durationMetric `json:"duration"`
		Query             durationMetric `json:"database_query"`
		Acquire           durationMetric `json:"database_acquire"`
		AcquireCalls      string         `json:"database_acquires"`
		ResponseBytes     string         `json:"response_bytes"`
		UncompressedBytes string         `json:"uncompressed_bytes"`
		GzipResponses     string         `json:"gzip_responses"`
	} `json:"http"`
	Database struct {
		Query       durationMetric `json:"query"`
		Acquire     durationMetric `json:"acquire"`
		LockQuery   durationMetric `json:"lock_query"`
		Transaction durationMetric `json:"transaction"`
		Commit      durationMetric `json:"commit"`
	} `json:"database"`
}

func TestMetricsTracksFixedRoutesDatabaseStagesAndResponseBytes(t *testing.T) {
	f := setup(t)
	seedInventory(t, f)
	client := rawClient(t)
	path := "/api/v1/networks/" + f.network.ID + "/items?limit=100"
	_, plain := rawRequest(t, client, f, "GET", path, adminToken, []string{"identity"})
	_, compressed := rawRequest(t, client, f, "GET", path, adminToken, []string{"gzip"})
	for i := 0; i < 10; i++ {
		r, _ := rawRequest(t, client, f, "GET", fmt.Sprintf("/unmatched-%d", i), "", nil)
		if r.StatusCode != 404 {
			t.Fatal(r.StatusCode)
		}
	}
	response := decode[performanceResponse](t, send(t, f, "GET", "/api/v1/admin/metrics", adminToken, ""), 200)
	foundInventory, foundUnmatched := false, false
	for _, metric := range response.HTTP {
		switch metric.Route {
		case "GET /api/v1/networks/{network}/items":
			foundInventory = true
			acquires, _ := strconv.Atoi(metric.AcquireCalls)
			if metric.Duration.Count != "2" || metric.Query.TotalMS <= 0 || acquires < 2 || metric.GzipResponses != "1" || metric.ResponseBytes != strconv.Itoa(len(plain)+len(compressed)) || metric.UncompressedBytes != strconv.Itoa(2*len(plain)) {
				t.Fatalf("incorrect inventory metrics: %+v", metric)
			}
		case "unmatched":
			foundUnmatched = true
			if metric.Duration.Count != "10" {
				t.Fatalf("unknown URLs were not aggregated: %+v", metric)
			}
		default:
			t.Fatalf("unexpected route or unbounded label: %q", metric.Route)
		}
	}
	if !foundInventory || !foundUnmatched || response.Database.Query.TotalMS <= 0 || response.Database.Transaction.TotalMS <= 0 || response.Database.Commit.TotalMS <= 0 {
		t.Fatalf("missing performance stages: %+v", response)
	}
}

func TestInventoryServerTimingAndMetricsAuthorization(t *testing.T) {
	f := setup(t)
	response, _ := rawRequest(t, rawClient(t), f, "GET", "/api/v1/networks/"+f.network.ID+"/items", adminToken, nil)
	for _, name := range []string{"db_query;dur=", "db_acquire;dur=", "app;dur="} {
		if !strings.Contains(response.Header.Get("Server-Timing"), name) {
			t.Fatalf("missing %s: %v", name, response.Header)
		}
	}
	_, token, err := f.db.CreateMember(context.Background(), f.admin.ID, "metrics-viewer", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := send(t, f, "GET", "/api/v1/admin/metrics", token, ""); r.StatusCode != 403 {
		t.Fatal("performance metrics exposed to a non-admin", r.StatusCode)
	}
}

func TestDatabaseMetricsIncludeCanceledPoolWait(t *testing.T) {
	options := store.DefaultOptions()
	options.MaxConnections = 1
	db := testutil.DatabaseWithOptions(t, options)
	if _, err := db.Bootstrap(context.Background(), adminToken); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.New(db, api.DefaultOptions()))
	t.Cleanup(server.Close)
	f := fixture{db: db, server: server}
	held, err := db.Pool().Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, err = db.Pool().Acquire(ctx)
	held.Release()
	if err == nil {
		t.Fatal("pool contention did not cancel")
	}
	metrics := decode[performanceResponse](t, send(t, f, "GET", "/api/v1/admin/metrics", adminToken, ""), 200)
	if metrics.Database.Acquire.Errors != "1" || metrics.Database.Acquire.MaxMS < 50 {
		t.Fatalf("pool wait was not measured: %+v", metrics.Database.Acquire)
	}
}

func TestDatabaseMetricsIncludeRealNetworkLockWait(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := f.db.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var blockerPID int
	if err = tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "SELECT id FROM networks WHERE id=$1 FOR UPDATE", f.network.ID); err != nil {
		t.Fatal(err)
	}
	d, err := f.db.AuthDevice(ctx, f.device)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.db.Hello(ctx, d, protocol.Hello{Protocol: 1, BootID: "lock-metrics", Source: "simulator", MaxResponseBytes: 32768})
		done <- err
	}()
	// Wait until PostgreSQL confirms an actual row-lock wait, not just a
	// goroutine scheduling delay, before releasing the lock.
	for {
		var waiting bool
		if err = f.db.Pool().QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND $1=ANY(pg_blocking_pids(pid)))", blockerPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	metrics := decode[performanceResponse](t, send(t, f, "GET", "/api/v1/admin/metrics", adminToken, ""), 200)
	if metrics.Database.LockQuery.MaxMS < 25 {
		t.Fatalf("locking statement wait was not measured: %+v", metrics.Database.LockQuery)
	}
}
