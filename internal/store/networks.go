package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

const networkColumns = `n.id,n.name,n.server_group,n.source,n.current_session,n.revision,n.last_seen,n.last_inventory,n.last_scan,n.capabilities,n.health`

func (s *Store) CreateNetwork(ctx context.Context, adminID, name, group, source string) (protocol.Network, string, error) {
	n := protocol.Network{ID: newID(), Name: strings.TrimSpace(name), ServerGroup: strings.TrimSpace(group), Source: source}
	if err := s.RequireAdmin(ctx, adminID); err != nil {
		return n, "", err
	}
	if len(n.Name) < 1 || len(n.Name) > 128 || len(n.ServerGroup) < 1 || len(n.ServerGroup) > 128 || (source != "oc" && source != "simulator") {
		return n, "", ErrInvalid
	}
	token := newToken("ac_dev_")
	h := hashToken(token)
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO server_groups(name) VALUES($1) ON CONFLICT DO NOTHING", n.ServerGroup); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "INSERT INTO networks(id,name,server_group,source,device_hash) VALUES($1,$2,$3,$4,$5)", n.ID, n.Name, n.ServerGroup, n.Source, h[:])
		return err
	})
	if err != nil {
		return n, "", err
	}
	n.Quality = "unverified"
	return n, token, nil
}

// Rotation keeps the catalog, but retires all credentials and tracking handles
// associated with the previous device. Possible game effects need review.
func (s *Store) RotateDeviceToken(ctx context.Context, adminID, networkID string) (string, error) {
	if err := s.RequireAdmin(ctx, adminID); err != nil {
		return "", err
	}
	token := newToken("ac_dev_")
	hash := hashToken(token)
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, "SELECT id FROM networks WHERE id=$1 FOR UPDATE", networkID).Scan(&id); err != nil {
			return notFound(err)
		}
		if err := s.markUncertain(ctx, tx, networkID, "device credential replaced; execution outcome requires review"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE bridge_sessions SET retired=true WHERE network_id=$1 AND NOT retired", networkID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE networks SET device_hash=$2,current_session='',capabilities='{}',health='{}',
			last_seen=NULL,last_scan=NULL,revision=revision+1,catalog_revision=catalog_revision+1 WHERE id=$1`, networkID, hash[:]); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM cpu_snapshots WHERE network_id=$1", networkID); err != nil {
			return err
		}
		return s.event(ctx, tx, networkID, "session")
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

type scanner interface{ Scan(...any) error }

func (s *Store) scanNetwork(row scanner) (protocol.Network, error) {
	var n protocol.Network
	var caps []byte
	err := row.Scan(&n.ID, &n.Name, &n.ServerGroup, &n.Source, &n.SessionID, &n.Revision, &n.LastSeen, &n.LastInventory, &n.LastScan, &caps, &n.Health)
	if err != nil {
		return n, notFound(err)
	}
	if err = json.Unmarshal(caps, &n.Capabilities); err != nil {
		return n, err
	}
	n.Online = n.LastSeen != nil && time.Since(*n.LastSeen) < s.options.OfflineAfter
	n.Quality = "unverified"
	var health protocol.Health
	if json.Unmarshal(n.Health, &health) == nil {
		for _, d := range health.Diagnostics {
			if d.Count > 0 {
				n.Quality = "degraded"
				break
			}
		}
	}
	return n, nil
}

func (s *Store) ListNetworks(ctx context.Context, userID string) ([]protocol.Network, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+networkColumns+` FROM networks n JOIN members m ON m.id=$1
        LEFT JOIN network_grants g ON g.member_id=m.id AND g.network_id=n.id WHERE NOT m.revoked AND (m.is_admin OR g.member_id IS NOT NULL) ORDER BY n.name,n.id LIMIT 1000`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []protocol.Network{}
	for rows.Next() {
		n, err := s.scanNetwork(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

func (s *Store) Network(ctx context.Context, userID, networkID string) (protocol.Network, error) {
	if err := s.RequireAccess(ctx, userID, networkID, false); err != nil {
		return protocol.Network{}, err
	}
	return s.scanNetwork(s.pool.QueryRow(ctx, `SELECT `+networkColumns+` FROM networks n WHERE n.id=$1`, networkID))
}

// Every device mutation locks its network and rechecks the credential and epoch.
func lockNetwork(ctx context.Context, tx pgx.Tx, d Device, sessionID string) (string, error) {
	var current, group string
	var hash []byte
	err := tx.QueryRow(ctx, "SELECT current_session,server_group,device_hash FROM networks WHERE id=$1 FOR UPDATE", d.NetworkID).Scan(&current, &group, &hash)
	if err != nil {
		return "", notFound(err)
	}
	if !bytes.Equal(hash, d.TokenHash[:]) {
		return "", ErrUnauthorized
	}
	if sessionID != "" && sessionID != current {
		return "", ErrStaleSession
	}
	return group, nil
}

func (s *Store) negotiatedLimits(responseBytes int) protocol.Limits {
	l := s.options.Limits
	l.ResponseBytes = min(responseBytes, l.ResponseBytes)
	l.BatchItems = min(l.BatchItems, l.ResponseBytes/96)
	return l
}

func (s *Store) Hello(ctx context.Context, d Device, h protocol.Hello) (protocol.Welcome, error) {
	var w protocol.Welcome
	if err := h.Validate(); err != nil {
		return w, errors.Join(ErrInvalid, err)
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := lockNetwork(ctx, tx, d, ""); err != nil {
			return err
		}
		var source string
		if err := tx.QueryRow(ctx, "SELECT source FROM networks WHERE id=$1", d.NetworkID).Scan(&source); err != nil {
			return err
		}
		if source != h.Source {
			return ErrConflict
		}
		var retired bool
		var responseBytes int
		err := tx.QueryRow(ctx, "SELECT id,retired,response_bytes,inventory_seq,poll_seq FROM bridge_sessions WHERE network_id=$1 AND boot_id=$2", d.NetworkID, h.BootID).Scan(&w.SessionID, &retired, &responseBytes, &w.InventoryAck, &w.PollAck)
		if err == nil {
			if retired {
				return ErrStaleSession
			}
			w.Limits = s.negotiatedLimits(responseBytes)
		} else if errors.Is(err, pgx.ErrNoRows) {
			w.SessionID = newID()
			w.Limits = s.negotiatedLimits(h.MaxResponseBytes)
			if err = s.markUncertain(ctx, tx, d.NetworkID, "bridge restarted; previous tracking handles cannot be recovered"); err != nil {
				return err
			}
			caps, _ := json.Marshal(h.Capabilities)
			if _, err = tx.Exec(ctx, "UPDATE bridge_sessions SET retired=true WHERE network_id=$1 AND NOT retired", d.NetworkID); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "INSERT INTO bridge_sessions(id,network_id,boot_id,capabilities,response_bytes) VALUES($1,$2,$3,$4,$5)", w.SessionID, d.NetworkID, h.BootID, caps, w.Limits.ResponseBytes); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "UPDATE networks SET current_session=$2,capabilities=$3,revision=revision+1,catalog_revision=catalog_revision+1,last_scan=NULL WHERE id=$1", d.NetworkID, w.SessionID, caps); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "DELETE FROM cpu_snapshots WHERE network_id=$1", d.NetworkID); err != nil {
				return err
			}
			if err = s.event(ctx, tx, d.NetworkID, "session"); err != nil {
				return err
			}
		} else {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE networks SET last_seen=now() WHERE id=$1", d.NetworkID)
		w.Protocol = 1
		w.NetworkID = d.NetworkID
		return err
	})
	return w, err
}
