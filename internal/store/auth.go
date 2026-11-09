package store

import (
	"context"
	"errors"
	"strings"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

func (s *Store) Bootstrap(ctx context.Context, token string) (protocol.Member, error) {
	var member protocol.Member
	if len(token) < 32 || len(token) > 256 {
		return member, ErrInvalid
	}
	h := hashToken(token)
	err := s.pool.QueryRow(ctx, `INSERT INTO members(id,name,token_hash,is_admin) VALUES($1,'admin',$2,true)
        ON CONFLICT(name) DO UPDATE SET token_hash=excluded.token_hash,revoked=false WHERE members.is_admin
        RETURNING id,name,is_admin`, newID(), h[:]).Scan(&member.ID, &member.Name, &member.Admin)
	return member, err
}

func (s *Store) AuthMember(ctx context.Context, token string) (protocol.Member, error) {
	var m protocol.Member
	if len(token) < 32 || len(token) > 256 {
		return m, ErrUnauthorized
	}
	h := hashToken(token)
	err := s.pool.QueryRow(ctx, "SELECT id,name,is_admin FROM members WHERE token_hash=$1 AND NOT revoked", h[:]).Scan(&m.ID, &m.Name, &m.Admin)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrUnauthorized
	}
	return m, err
}

func (s *Store) AuthDevice(ctx context.Context, token string) (Device, error) {
	d := Device{TokenHash: hashToken(token)}
	if len(token) < 32 || len(token) > 256 {
		return d, ErrUnauthorized
	}
	err := s.pool.QueryRow(ctx, "SELECT id,network_id,role FROM devices WHERE token_hash=$1 AND NOT revoked", d.TokenHash[:]).Scan(&d.ID, &d.NetworkID, &d.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrUnauthorized
	}
	return d, err
}

func (s *Store) RequireAdmin(ctx context.Context, userID string) error {
	var yes bool
	err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM members WHERE id=$1 AND is_admin AND NOT revoked)", userID).Scan(&yes)
	if err != nil {
		return err
	}
	if !yes {
		return ErrForbidden
	}
	return nil
}

func (s *Store) RequireAccess(ctx context.Context, userID, networkID string, craft bool) error {
	var yes bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM members m JOIN networks n ON n.id=$2
        LEFT JOIN network_grants g ON g.member_id=m.id AND g.network_id=n.id
        WHERE m.id=$1 AND NOT m.revoked AND (m.is_admin OR (g.member_id IS NOT NULL AND (NOT $3 OR g.can_craft))))`, userID, networkID, craft).Scan(&yes)
	if err != nil {
		return err
	}
	if !yes {
		return ErrForbidden
	}
	return nil
}

func (s *Store) CreateMember(ctx context.Context, adminID, name string, grants []protocol.Grant) (protocol.Member, string, error) {
	m := protocol.Member{ID: newID(), Name: strings.TrimSpace(name)}
	if err := s.RequireAdmin(ctx, adminID); err != nil {
		return m, "", err
	}
	if len(m.Name) < 1 || len(m.Name) > 128 || m.Name == "admin" || len(grants) > 256 {
		return m, "", ErrInvalid
	}
	token := newToken("ac_user_")
	h := hashToken(token)
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO members(id,name,token_hash) VALUES($1,$2,$3)", m.ID, m.Name, h[:]); err != nil {
			return err
		}
		for _, g := range grants {
			if _, err := tx.Exec(ctx, "INSERT INTO network_grants(member_id,network_id,can_craft) VALUES($1,$2,$3)", m.ID, g.NetworkID, g.CanCraft); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return m, "", err
	}
	return m, token, nil
}

func (s *Store) SetGrant(ctx context.Context, adminID, userID string, g protocol.Grant) error {
	if err := s.RequireAdmin(ctx, adminID); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO network_grants(member_id,network_id,can_craft) VALUES($1,$2,$3)
        ON CONFLICT(member_id,network_id) DO UPDATE SET can_craft=excluded.can_craft`, userID, g.NetworkID, g.CanCraft)
	return err
}
