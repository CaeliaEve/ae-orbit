// Package testutil provides isolated real PostgreSQL fixtures for integration tests.
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/store"
	"github.com/jackc/pgx/v5"
)

func Database(t *testing.T) *store.Store { return DatabaseWithOptions(t, store.DefaultOptions()) }
func DatabaseWithOptions(t *testing.T, options store.Options) *store.Store {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("AE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AE_TEST_DATABASE_URL is required for real PostgreSQL integration tests")
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "test_" + hex.EncodeToString(random[:])
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	db, err := store.Open(ctx, u.String(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = conn.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		_ = conn.Close(ctx)
	})
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}
