package store

import (
	"context"
	"strings"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

var integrationScopes = map[string]bool{"inventory.read": true, "cpu.read": true, "energy.read": true, "subnet.read": true}

type IntegrationKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Enabled    bool       `json:"enabled"`
	Revision   int64      `json:"revision"`
	Scopes     []string   `json:"scopes"`
	Networks   []string   `json:"network_ids"`
	Groups     []string   `json:"server_groups"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}
type IntegrationKeyInput struct {
	Name        string   `json:"name"`
	Scopes      []string `json:"scopes"`
	Networks    []string `json:"network_ids"`
	Groups      []string `json:"server_groups"`
	Enabled     bool     `json:"enabled"`
	Revision    int64    `json:"revision"`
	ExpiresDays int      `json:"expires_days"`
}
type IntegrationAuth struct {
	Key      IntegrationKey
	MemberID string
}

const integrationKeyColumns = `id,name,token_prefix,enabled,revision,scopes,network_ids,server_groups,created_at,expires_at,last_used_at`

func scanIntegrationKey(row scanner) (IntegrationKey, error) {
	var k IntegrationKey
	err := row.Scan(&k.ID, &k.Name, &k.Prefix, &k.Enabled, &k.Revision, &k.Scopes, &k.Networks, &k.Groups, &k.CreatedAt, &k.ExpiresAt, &k.LastUsedAt)
	return k, err
}
func uniqueIntegrationValues(values []string, limit int) ([]string, error) {
	if len(values) > limit {
		return nil, ErrInvalid
	}
	result := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		if !monitorText(v, 128) || strings.TrimSpace(v) == "" {
			return nil, ErrInvalid
		}
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result, nil
}
func (s *Store) integrationKeyInput(ctx context.Context, member string, in IntegrationKeyInput) (IntegrationKeyInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !monitorText(in.Name, 128) || in.Revision < 0 {
		return in, ErrInvalid
	}
	var err error
	in.Scopes, err = uniqueIntegrationValues(in.Scopes, 4)
	if err != nil || len(in.Scopes) == 0 {
		return in, ErrInvalid
	}
	in.Networks, err = uniqueIntegrationValues(in.Networks, 100)
	if err != nil {
		return in, err
	}
	in.Groups, err = uniqueIntegrationValues(in.Groups, 100)
	if err != nil {
		return in, err
	}
	hasNetwork, hasGroup := false, false
	for _, scope := range in.Scopes {
		if !integrationScopes[scope] {
			return in, ErrInvalid
		}
		if scope == "inventory.read" || scope == "cpu.read" {
			hasNetwork = true
		} else {
			hasGroup = true
		}
	}
	if hasNetwork && len(in.Networks) == 0 || hasGroup && len(in.Groups) == 0 || !hasNetwork && len(in.Networks) > 0 || !hasGroup && len(in.Groups) > 0 {
		return in, ErrInvalid
	}
	for _, id := range in.Networks {
		if !in.Enabled {
			continue
		}
		if err = s.RequireAccess(ctx, member, id, false); err != nil {
			return in, err
		}
	}
	for _, group := range in.Groups {
		if !in.Enabled {
			continue
		}
		if err = s.RequireGroupAccess(ctx, member, group); err != nil {
			return in, err
		}
	}
	return in, nil
}
func (s *Store) IntegrationKeys(ctx context.Context, member string) ([]IntegrationKey, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+integrationKeyColumns+` FROM integration_keys WHERE member_id=$1 ORDER BY created_at DESC,id`, member)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IntegrationKey{}
	for rows.Next() {
		k, err := scanIntegrationKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (s *Store) CreateIntegrationKey(ctx context.Context, member string, in IntegrationKeyInput) (IntegrationKey, string, error) {
	var k IntegrationKey
	in, err := s.integrationKeyInput(ctx, member, in)
	if err != nil {
		return k, "", err
	}
	if in.Revision != 0 || in.ExpiresDays < 1 || in.ExpiresDays > 365 {
		return k, "", ErrInvalid
	}
	token := newToken("ac_api_")
	h := hashToken(token)
	err = s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		var count int
		if _, err := tx.Exec(ctx, `SELECT id FROM members WHERE id=$1 AND NOT revoked FOR UPDATE`, member); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM integration_keys WHERE member_id=$1`, member).Scan(&count); err != nil {
			return err
		}
		if count >= 50 {
			return ErrCapacity
		}
		var e error
		k, e = scanIntegrationKey(tx.QueryRow(ctx, `INSERT INTO integration_keys(id,member_id,name,token_hash,token_prefix,enabled,scopes,network_ids,server_groups,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,now()+$10*interval '1 day') RETURNING `+integrationKeyColumns, newID(), member, in.Name, h[:], token[:15], in.Enabled, in.Scopes, in.Networks, in.Groups, in.ExpiresDays))
		return e
	})
	return k, token, err
}
func (s *Store) UpdateIntegrationKey(ctx context.Context, member, id string, in IntegrationKeyInput) (IntegrationKey, error) {
	var k IntegrationKey
	in, err := s.integrationKeyInput(ctx, member, in)
	if err != nil {
		return k, err
	}
	if !monitorID.MatchString(id) || in.Revision < 1 || in.ExpiresDays != 0 {
		return k, ErrInvalid
	}
	err = s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		var revision int64
		if err := tx.QueryRow(ctx, `SELECT revision FROM integration_keys WHERE id=$1 AND member_id=$2 FOR UPDATE`, id, member).Scan(&revision); err != nil {
			return notFound(err)
		}
		if revision != in.Revision {
			return ErrConflict
		}
		var e error
		k, e = scanIntegrationKey(tx.QueryRow(ctx, `UPDATE integration_keys SET name=$3,enabled=$4,scopes=$5,network_ids=$6,server_groups=$7,revision=revision+1 WHERE id=$1 AND member_id=$2 RETURNING `+integrationKeyColumns, id, member, in.Name, in.Enabled, in.Scopes, in.Networks, in.Groups))
		return e
	})
	return k, err
}
func (s *Store) RotateIntegrationKey(ctx context.Context, member, id string, revision int64) (IntegrationKey, string, error) {
	var k IntegrationKey
	if !monitorID.MatchString(id) || revision < 1 {
		return k, "", ErrInvalid
	}
	token := newToken("ac_api_")
	h := hashToken(token)
	err := s.monitorTransaction(ctx, func(tx pgx.Tx) error {
		var current int64
		var expires time.Time
		if err := tx.QueryRow(ctx, `SELECT revision,expires_at FROM integration_keys WHERE id=$1 AND member_id=$2 FOR UPDATE`, id, member).Scan(&current, &expires); err != nil {
			return notFound(err)
		}
		if current != revision {
			return ErrConflict
		}
		if !expires.After(time.Now()) {
			return ErrForbidden
		}
		var err error
		k, err = scanIntegrationKey(tx.QueryRow(ctx, `UPDATE integration_keys SET token_hash=$3,token_prefix=$4,revision=revision+1 WHERE id=$1 AND member_id=$2 RETURNING `+integrationKeyColumns, id, member, h[:], token[:15]))
		return err
	})
	return k, token, err
}
func (s *Store) AuthIntegration(ctx context.Context, token string) (IntegrationAuth, error) {
	var a IntegrationAuth
	if !strings.HasPrefix(token, "ac_api_") || len(token) != 50 {
		return a, ErrUnauthorized
	}
	h := hashToken(token)
	row := s.pool.QueryRow(ctx, `SELECT k.id,k.name,k.token_prefix,k.enabled,k.revision,k.scopes,k.network_ids,k.server_groups,k.created_at,k.expires_at,k.last_used_at,k.member_id FROM integration_keys k JOIN members m ON m.id=k.member_id WHERE k.token_hash=$1 AND k.enabled AND k.expires_at>now() AND NOT m.revoked`, h[:])
	k := &a.Key
	err := row.Scan(&k.ID, &k.Name, &k.Prefix, &k.Enabled, &k.Revision, &k.Scopes, &k.Networks, &k.Groups, &k.CreatedAt, &k.ExpiresAt, &k.LastUsedAt, &a.MemberID)
	if err == pgx.ErrNoRows {
		return a, ErrUnauthorized
	}
	return a, err
}
func (s *Store) TouchIntegration(ctx context.Context, a IntegrationAuth) error {
	_, err := s.pool.Exec(ctx, `UPDATE integration_keys SET last_used_at=now() WHERE id=$1 AND (last_used_at IS NULL OR last_used_at<now()-interval '30 seconds')`, a.Key.ID)
	return err
}
func integrationContains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func (s *Store) RequireIntegration(ctx context.Context, a IntegrationAuth, scope, network, group string) error {
	if scope != "" && !integrationContains(a.Key.Scopes, scope) {
		return ErrForbidden
	}
	if network != "" {
		if !integrationContains(a.Key.Networks, network) {
			return ErrForbidden
		}
		return s.RequireAccess(ctx, a.MemberID, network, false)
	}
	if group != "" {
		if !integrationContains(a.Key.Groups, group) {
			return ErrForbidden
		}
		return s.RequireGroupAccess(ctx, a.MemberID, group)
	}
	return nil
}
func (s *Store) IntegrationNetworks(ctx context.Context, a IntegrationAuth) ([]protocol.Network, error) {
	rows, err := s.ListNetworks(ctx, a.MemberID)
	if err != nil {
		return nil, err
	}
	out := []protocol.Network{}
	for _, n := range rows {
		if integrationContains(a.Key.Networks, n.ID) {
			out = append(out, n)
		}
	}
	return out, nil
}
func (s *Store) IntegrationGroups(ctx context.Context, a IntegrationAuth) ([]string, error) {
	groups, err := s.MonitoringGroups(ctx, a.MemberID)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, g := range groups {
		if integrationContains(a.Key.Groups, g) {
			out = append(out, g)
		}
	}
	return out, nil
}
