package store

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"time"
	"unicode/utf8"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

const passwordIterations = 600000

func validGameID(value string) bool {
	if len(value) < 3 || len(value) > 16 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func passwordDigest(password string, salt []byte) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
}

func newPassword(password string) ([]byte, []byte, error) {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 10 || len(password) > 256 {
		return nil, nil, ErrInvalid
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, err
	}
	digest, err := passwordDigest(password, salt)
	return salt, digest, err
}

func (s *Store) PlayerLogin(ctx context.Context, gameID, password string) (BrowserSession, error) {
	var empty BrowserSession
	if !validGameID(gameID) || len(password) < 1 || len(password) > 256 {
		return empty, ErrUnauthorized
	}
	var member protocol.Member
	var salt, expected []byte
	err := s.pool.QueryRow(ctx, `SELECT id,name,is_admin,game_id,password_salt,password_hash,auth_version FROM members WHERE lower(game_id)=lower($1) AND NOT revoked`, gameID).Scan(&member.ID, &member.Name, &member.Admin, &member.GameID, &salt, &expected, &member.AuthVersion)
	if err != nil && err != pgx.ErrNoRows {
		return empty, err
	}
	// Missing identities perform the same password work; no account enumeration.
	if len(salt) != 16 {
		salt = make([]byte, 16)
	}
	digest, deriveErr := passwordDigest(password, salt)
	if deriveErr != nil {
		return empty, deriveErr
	}
	if len(expected) != 32 || subtle.ConstantTimeCompare(digest, expected) != 1 {
		return empty, ErrUnauthorized
	}
	member.PasswordLogin = true
	return s.createMemberSession(ctx, member, expected)
}

type PlayerInvitation struct {
	ID        string    `json:"id"`
	NetworkID string    `json:"network_id"`
	CanCraft  bool      `json:"can_craft"`
	MaxUses   int       `json:"max_uses"`
	Uses      int       `json:"uses"`
	ExpiresAt time.Time `json:"expires_at"`
	Revoked   bool      `json:"revoked"`
}

func (s *Store) CreatePlayerInvitation(ctx context.Context, admin, network string, craft bool, uses int) (PlayerInvitation, string, error) {
	var invite PlayerInvitation
	if err := s.RequireAdmin(ctx, admin); err != nil {
		return invite, "", err
	}
	if uses < 1 || uses > 100 {
		return invite, "", ErrInvalid
	}
	code := newToken("ac_invite_")
	hash := hashToken(code)
	invite = PlayerInvitation{ID: newID(), NetworkID: network, CanCraft: craft, MaxUses: uses, ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour)}
	_, err := s.pool.Exec(ctx, `INSERT INTO player_invitations(id,code_hash,network_id,created_by,can_craft,max_uses,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, invite.ID, hash[:], network, admin, craft, uses, invite.ExpiresAt)
	return invite, code, err
}

func (s *Store) PlayerInvitations(ctx context.Context, admin string) ([]PlayerInvitation, error) {
	if err := s.RequireAdmin(ctx, admin); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id,network_id,can_craft,max_uses,uses,expires_at,revoked FROM player_invitations ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PlayerInvitation{}
	for rows.Next() {
		var row PlayerInvitation
		if err = rows.Scan(&row.ID, &row.NetworkID, &row.CanCraft, &row.MaxUses, &row.Uses, &row.ExpiresAt, &row.Revoked); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *Store) RevokePlayerInvitation(ctx context.Context, admin, id string) error {
	if err := s.RequireAdmin(ctx, admin); err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, `UPDATE player_invitations SET revoked=true WHERE id=$1`, id)
	if err == nil && command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) RegisterPlayer(ctx context.Context, gameID, password, code string) (BrowserSession, error) {
	var session BrowserSession
	if !validGameID(gameID) || len(code) < 32 || len(code) > 256 {
		return session, ErrInvalid
	}
	salt, digest, err := newPassword(password)
	if err != nil {
		return session, err
	}
	hash := hashToken(code)
	member := protocol.Member{ID: newID(), GameID: gameID, PasswordLogin: true}
	member.Name = "player:" + member.ID
	// This unused random token keeps the legacy token column valid; it is never returned.
	memberToken := hashToken(newToken("unused_"))
	err = s.transaction(ctx, func(tx pgx.Tx) error {
		var network string
		var craft bool
		err := tx.QueryRow(ctx, `UPDATE player_invitations SET uses=uses+1 WHERE code_hash=$1 AND NOT revoked AND expires_at>now() AND uses<max_uses RETURNING network_id,can_craft`, hash[:]).Scan(&network, &craft)
		if err == pgx.ErrNoRows {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO members(id,name,token_hash,game_id,password_salt,password_hash) VALUES($1,$2,$3,$4,$5,$6)`, member.ID, member.Name, memberToken[:], gameID, salt, digest); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO network_grants(member_id,network_id,can_craft) VALUES($1,$2,$3)`, member.ID, network, craft)
		return err
	})
	if err != nil {
		return session, err
	}
	return s.createMemberSession(ctx, member, digest)
}

func (s *Store) UpdatePlayerProfile(ctx context.Context, memberID, gameID, currentPassword, password string, adminReset bool) (protocol.Member, error) {
	var member protocol.Member
	if !validGameID(gameID) {
		return member, ErrInvalid
	}
	var newSalt, newDigest []byte
	var err error
	if password != "" {
		newSalt, newDigest, err = newPassword(password)
		if err != nil {
			return member, err
		}
	}
	err = s.transaction(ctx, func(tx pgx.Tx) error {
		var salt, expected []byte
		if err := tx.QueryRow(ctx, `SELECT id,name,is_admin,game_id,password_salt,password_hash FROM members WHERE id=$1 AND NOT revoked FOR UPDATE`, memberID).Scan(&member.ID, &member.Name, &member.Admin, &member.GameID, &salt, &expected); err != nil {
			return notFound(err)
		}
		if len(expected) > 0 && !adminReset {
			if len(currentPassword) > 256 {
				return ErrUnauthorized
			}
			actual, err := passwordDigest(currentPassword, salt)
			if err != nil {
				return err
			}
			if subtle.ConstantTimeCompare(actual, expected) != 1 {
				return ErrUnauthorized
			}
		}
		if err := tx.QueryRow(ctx, `UPDATE members SET auth_version=auth_version+1,game_id=$2,password_salt=CASE WHEN $3::bytea IS NULL THEN password_salt ELSE $3 END,password_hash=CASE WHEN $4::bytea IS NULL THEN password_hash ELSE $4 END WHERE id=$1 RETURNING auth_version`, memberID, gameID, newSalt, newDigest).Scan(&member.AuthVersion); err != nil {
			return err
		}
		// Password/identity changes invalidate other devices; callers establish a new session.
		if _, err := tx.Exec(ctx, `DELETE FROM web_sessions WHERE member_id=$1`, memberID); err != nil {
			return err
		}
		member.GameID = gameID
		member.PasswordLogin = len(expected) > 0 || len(newDigest) > 0
		return nil
	})
	return member, err
}

func (s *Store) SessionForMember(ctx context.Context, member protocol.Member) (BrowserSession, error) {
	return s.createMemberSession(ctx, member, nil)
}
