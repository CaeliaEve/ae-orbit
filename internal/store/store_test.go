package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"github.com/jackc/pgx/v5"
)

var ctx = context.Background()

const adminToken = "test-admin-token-abcdefghijklmnopqrstuvwxyz0123456789"

func testDB(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("AE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AE_TEST_DATABASE_URL is required for real PostgreSQL integration tests")
	}
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "test_" + hex.EncodeToString(random[:])
	if _, err = c.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	s, err := store.Open(ctx, u.String(), store.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		_, _ = c.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		_ = c.Close(ctx)
	})
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func fixture(t *testing.T) (*store.Store, protocol.Member, protocol.Network, string) {
	t.Helper()
	s := testDB(t)
	admin, err := s.Bootstrap(ctx, adminToken)
	if err != nil {
		t.Fatal(err)
	}
	n, token, err := s.CreateNetwork(ctx, admin.ID, "Factory", "server-1", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	return s, admin, n, token
}

func hello(t *testing.T, s *store.Store, token, boot string) (store.Device, protocol.Welcome) {
	t.Helper()
	d, err := s.AuthDevice(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.Hello(ctx, d, protocol.Hello{Protocol: 1, BootID: boot, Source: "simulator", MaxResponseBytes: 32768, Capabilities: protocol.Capabilities{Inventory: true, Craft: true, ExactQuantities: true, CPUOutput: true, MaxCraftQuantity: 9223372036854775807, Enumeration: "simulated"}})
	if err != nil {
		t.Fatal(err)
	}
	return d, w
}

func TestMigrationAndTokenStorage(t *testing.T) {
	s, admin, _, _ := fixture(t)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.AuthMember(ctx, adminToken)
	if err != nil || got.ID != admin.ID {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err = s.AuthMember(ctx, adminToken+"x"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal(err)
	}
	var hash []byte
	if err = s.Pool().QueryRow(ctx, "SELECT token_hash FROM members WHERE id=$1", admin.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if len(hash) != 32 || string(hash) == adminToken {
		t.Fatal("raw token stored")
	}
	got, err = s.Bootstrap(ctx, adminToken)
	if err != nil || got.ID != admin.ID {
		t.Fatal("bootstrap not idempotent", err)
	}
}

func TestNetworkIsolation(t *testing.T) {
	s, admin, n, _ := fixture(t)
	viewer, token, err := s.CreateMember(ctx, admin.ID, "viewer", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthMember(ctx, token); err != nil {
		t.Fatal(err)
	}
	if err = s.RequireAccess(ctx, viewer.ID, n.ID, false); !errors.Is(err, store.ErrForbidden) {
		t.Fatal(err)
	}
	if err = s.SetGrant(ctx, admin.ID, viewer.ID, protocol.Grant{NetworkID: n.ID, CanCraft: false}); err != nil {
		t.Fatal(err)
	}
	if err = s.RequireAccess(ctx, viewer.ID, n.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = s.RequireAccess(ctx, viewer.ID, n.ID, true); !errors.Is(err, store.ErrForbidden) {
		t.Fatal(err)
	}
	if _, _, err = s.CreateNetwork(ctx, viewer.ID, "not-allowed", "server-1", "oc"); !errors.Is(err, store.ErrForbidden) {
		t.Fatal(err)
	}
	networks, err := s.ListNetworks(ctx, viewer.ID)
	if err != nil || len(networks) != 1 || networks[0].ID != n.ID {
		t.Fatalf("%+v %v", networks, err)
	}
}

func TestSessionRejectsRetiredBoot(t *testing.T) {
	s, _, _, token := fixture(t)
	d, first := hello(t, s, token, "boot-1")
	_, repeat := hello(t, s, token, "boot-1")
	if first.SessionID != repeat.SessionID {
		t.Fatal("hello retry changed session")
	}
	_, second := hello(t, s, token, "boot-2")
	if second.SessionID == first.SessionID {
		t.Fatal("boot did not change session")
	}
	_, err := s.Hello(ctx, d, protocol.Hello{Protocol: 1, BootID: "boot-1", Source: "simulator", MaxResponseBytes: 32768})
	if !errors.Is(err, store.ErrStaleSession) {
		t.Fatal(err)
	}
}
