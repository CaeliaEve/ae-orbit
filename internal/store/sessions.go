package store

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

type BrowserSession struct {
	Member    protocol.Member
	Token     string
	CSRF      string
	ExpiresAt time.Time
}
type SessionAuth struct {
	Member    protocol.Member
	CSRFHash  []byte
	ExpiresAt time.Time
}

func (a SessionAuth) CheckCSRF(token string) bool {
	hash := hashToken(token)
	return subtle.ConstantTimeCompare(hash[:], a.CSRFHash) == 1
}

func (s *Store) CreateSession(ctx context.Context, memberToken string) (BrowserSession, error) {
	var result BrowserSession
	m, err := s.AuthMember(ctx, memberToken)
	if err != nil {
		return result, err
	}
	result = BrowserSession{Member: m, Token: newToken("ac_session_"), CSRF: newToken("ac_csrf_"), ExpiresAt: time.Now().UTC().Add(24 * time.Hour)}
	hash, csrf := hashToken(result.Token), hashToken(result.CSRF)
	err = s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT id FROM members WHERE id=$1 FOR UPDATE", m.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO web_sessions(token_hash,member_id,csrf_hash,expires_at) VALUES($1,$2,$3,$4)", hash[:], m.ID, csrf[:], result.ExpiresAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM web_sessions WHERE token_hash IN (SELECT token_hash FROM web_sessions WHERE member_id=$1 ORDER BY expires_at DESC OFFSET 20)`, m.ID)
		return err
	})
	return result, err
}
func (s *Store) AuthSession(ctx context.Context, token string) (SessionAuth, error) {
	var result SessionAuth
	if len(token) < 32 || len(token) > 256 {
		return result, ErrUnauthorized
	}
	hash := hashToken(token)
	err := s.pool.QueryRow(ctx, `SELECT m.id,m.name,m.is_admin,s.csrf_hash,s.expires_at FROM web_sessions s JOIN members m ON m.id=s.member_id
        WHERE s.token_hash=$1 AND s.expires_at>now() AND NOT m.revoked`, hash[:]).Scan(&result.Member.ID, &result.Member.Name, &result.Member.Admin, &result.CSRFHash, &result.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrUnauthorized
	}
	return result, err
}
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	hash := hashToken(token)
	_, err := s.pool.Exec(ctx, "DELETE FROM web_sessions WHERE token_hash=$1", hash[:])
	return err
}

func (s *Store) RevokeMember(ctx context.Context, adminID, userID string) error {
	if err := s.RequireAdmin(ctx, adminID); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "UPDATE members SET revoked=true WHERE id=$1 AND NOT is_admin", userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrConflict
		}
		_, err = tx.Exec(ctx, "DELETE FROM web_sessions WHERE member_id=$1", userID)
		return err
	})
}
func (s *Store) Members(ctx context.Context, adminID string) ([]protocol.Member, error) {
	if err := s.RequireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, "SELECT id,name,is_admin FROM members WHERE NOT revoked ORDER BY name LIMIT 1000")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []protocol.Member{}
	for rows.Next() {
		var m protocol.Member
		if err = rows.Scan(&m.ID, &m.Name, &m.Admin); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
func (s *Store) DeleteGrant(ctx context.Context, adminID, userID, networkID string) error {
	if err := s.RequireAdmin(ctx, adminID); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, "DELETE FROM network_grants WHERE member_id=$1 AND network_id=$2", userID, networkID)
	return err
}
