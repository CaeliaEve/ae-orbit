package api_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/api"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"github.com/CaeliaEve/ae-orbit/internal/testutil"
)

func TestPlainCatalogImportHasSeparateExecutionBudget(t *testing.T) {
	options := store.DefaultOptions()
	options.MaxConnections = 1
	db := testutil.DatabaseWithOptions(t, options)
	if _, err := db.Bootstrap(context.Background(), adminToken); err != nil {
		t.Fatal(err)
	}
	httpOptions := api.DefaultOptions()
	httpOptions.RequestTimeout = 50 * time.Millisecond
	server := httptest.NewServer(api.New(db, httpOptions))
	t.Cleanup(server.Close)
	f := fixture{db: db, server: server}
	held, err := db.Pool().Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(held.Release)
	// Ordinary authenticated requests must still cancel while the pool is busy.
	if response := send(t, f, "GET", "/api/v1/me", adminToken, ""); response.StatusCode != 503 {
		t.Fatal("ordinary request did not retain its deadline", response.StatusCode)
	}
	release := time.AfterFunc(200*time.Millisecond, held.Release)
	defer release.Stop()
	// Once authentication can proceed, the empty test catalog produces the
	// expected capability error rather than the ordinary request timeout.
	response := send(t, f, "POST", "/api/v1/networks/test/inventory-profile/nesql-plain", adminToken, "")
	if response.StatusCode != 409 {
		t.Fatal("catalog import did not receive its execution budget", response.StatusCode)
	}
}
