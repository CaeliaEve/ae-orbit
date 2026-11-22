package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestIntegrationKeysPermissionsLifecycleAndOwner(t *testing.T) {
	s, admin, n, _ := fixture(t)
	member, _, err := s.CreateMember(ctx, admin.ID, "api-player", []protocol.Grant{{NetworkID: n.ID}})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.CreateNetwork(ctx, admin.ID, "Private", "other", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	input := store.IntegrationKeyInput{Name: "Tool", Scopes: []string{"inventory.read", "subnet.read"}, Networks: []string{n.ID}, Groups: []string{n.ServerGroup}, Enabled: true, ExpiresDays: 90}
	k, token, err := s.CreateIntegrationKey(ctx, member.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.AuthIntegration(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		scope, net, group string
		allowed           bool
	}{{"inventory.read", n.ID, "", true}, {"cpu.read", n.ID, "", false}, {"inventory.read", second.ID, "", false}, {"subnet.read", "", n.ServerGroup, true}, {"subnet.read", "", "other", false}} {
		err = s.RequireIntegration(ctx, a, tc.scope, tc.net, tc.group)
		if (err == nil) != tc.allowed {
			t.Fatal(tc, err)
		}
	}
	if _, err = s.AuthMember(ctx, token); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("API key got browser privileges", err)
	}
	if _, err = s.AuthDevice(ctx, token); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("API key got device privileges", err)
	}
	var raw []byte
	if err = s.Pool().QueryRow(ctx, `SELECT token_hash FROM integration_keys WHERE id=$1`, k.ID).Scan(&raw); err != nil || string(raw) == token || len(raw) != 32 {
		t.Fatal("token not hashed")
	}
	if err = s.TouchIntegration(ctx, a); err != nil {
		t.Fatal(err)
	}
	rows, err := s.IntegrationKeys(ctx, member.ID)
	if err != nil || len(rows) != 1 || rows[0].LastUsedAt == nil {
		t.Fatal(rows, err)
	}
	rows, err = s.IntegrationKeys(ctx, admin.ID)
	if err != nil || len(rows) != 0 {
		t.Fatal("keys escaped owner", rows, err)
	}
	if _, _, err = s.RotateIntegrationKey(ctx, admin.ID, k.ID, k.Revision); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	next, newToken, err := s.RotateIntegrationKey(ctx, member.ID, k.ID, k.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthIntegration(ctx, token); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("old token survived rotation", err)
	}
	if _, err = s.AuthIntegration(ctx, newToken); err != nil {
		t.Fatal(err)
	}
	input.Revision = k.Revision
	input.ExpiresDays = 0
	if _, err = s.UpdateIntegrationKey(ctx, member.ID, k.ID, input); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale update accepted", err)
	}
	input.Revision = next.Revision
	input.Enabled = false
	next, err = s.UpdateIntegrationKey(ctx, member.ID, k.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthIntegration(ctx, newToken); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("disabled key worked")
	}
	input.Revision = next.Revision
	input.Enabled = true
	next, err = s.UpdateIntegrationKey(ctx, member.ID, k.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, `DELETE FROM network_grants WHERE member_id=$1`, member.ID); err != nil {
		t.Fatal(err)
	}
	a, err = s.AuthIntegration(ctx, newToken)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RequireIntegration(ctx, a, "inventory.read", n.ID, ""); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("removed grant survived", err)
	}
	if err = s.RequireIntegration(ctx, a, "subnet.read", "", n.ServerGroup); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("removed group grant survived", err)
	}
	input.Revision = next.Revision
	input.Enabled = false
	next, err = s.UpdateIntegrationKey(ctx, member.ID, k.ID, input)
	if err != nil {
		t.Fatal("cannot disable key after permission removal", err)
	}
	if _, err = s.Pool().Exec(ctx, `UPDATE integration_keys SET expires_at=now()-interval '1 second' WHERE id=$1`, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthIntegration(ctx, newToken); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal("expired key worked")
	}
	input = store.IntegrationKeyInput{Name: "Bad", Scopes: []string{"orders.write"}, Networks: []string{n.ID}, Enabled: true, ExpiresDays: 90}
	if _, _, err = s.CreateIntegrationKey(ctx, admin.ID, input); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("write scope allowed")
	}
	input.Scopes = []string{"inventory.read"}
	input.Networks = nil
	if _, _, err = s.CreateIntegrationKey(ctx, admin.ID, input); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("empty grants widened access")
	}
	if _, err = s.AuthIntegration(ctx, "ac_api_"+strings.Repeat("x", 42)); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatal(err)
	}
}
