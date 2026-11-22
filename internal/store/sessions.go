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
	return s.createMemberSession(ctx, m, nil)
}

func (s *Store) createMemberSession(ctx context.Context, m protocol.Member, passwordHash []byte) (BrowserSession, error) {
	var result BrowserSession
	result = BrowserSession{Member: m, Token: newToken("ac_session_"), CSRF: newToken("ac_csrf_"), ExpiresAt: time.Now().UTC().Add(24 * time.Hour)}
	hash, csrf := hashToken(result.Token), hashToken(result.CSRF)
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var revoked bool
		var currentHash []byte
		var authVersion int64
		if err := tx.QueryRow(ctx, "SELECT revoked,password_hash,name,is_admin,game_id,auth_version FROM members WHERE id=$1 FOR UPDATE", m.ID).Scan(&revoked, &currentHash, &result.Member.Name, &result.Member.Admin, &result.Member.GameID, &authVersion); err != nil {
			return err
		}
		if revoked || authVersion != m.AuthVersion || (passwordHash != nil && subtle.ConstantTimeCompare(passwordHash, currentHash) != 1) {
			return ErrUnauthorized
		}
		result.Member.PasswordLogin = len(currentHash) > 0
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
	err := s.pool.QueryRow(ctx, `SELECT m.id,m.name,m.is_admin,m.game_id,m.password_hash IS NOT NULL,s.csrf_hash,s.expires_at FROM web_sessions s JOIN members m ON m.id=s.member_id
		WHERE s.token_hash=$1 AND s.expires_at>now() AND NOT m.revoked`, hash[:]).Scan(&result.Member.ID, &result.Member.Name, &result.Member.Admin, &result.Member.GameID, &result.Member.PasswordLogin, &result.CSRFHash, &result.ExpiresAt)
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
		if _, err = tx.Exec(ctx, "UPDATE production_job_authority_epochs SET epoch=epoch+1 WHERE member_id=$1", userID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "DELETE FROM web_sessions WHERE member_id=$1", userID)
		return err
	})
}
func (s *Store) Members(ctx context.Context, adminID string) ([]protocol.Member, error) {
	if err := s.RequireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, "SELECT id,name,is_admin,game_id,password_hash IS NOT NULL FROM members WHERE NOT revoked ORDER BY name LIMIT 1000")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []protocol.Member{}
	for rows.Next() {
		var m protocol.Member
		if err = rows.Scan(&m.ID, &m.Name, &m.Admin, &m.GameID, &m.PasswordLogin); err != nil {
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
	return s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT id FROM members WHERE id=$1 FOR UPDATE", userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE production_job_authority_epochs SET epoch=epoch+1 WHERE member_id=$1 AND network_id=$2 AND NOT EXISTS(SELECT 1 FROM members WHERE id=$1 AND is_admin) AND EXISTS(SELECT 1 FROM network_grants WHERE member_id=$1 AND network_id=$2 AND can_craft)`, userID, networkID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "DELETE FROM network_grants WHERE member_id=$1 AND network_id=$2", userID, networkID)
		return err
	})
}
