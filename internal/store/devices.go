package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

func (s *Store) HelloV2(ctx context.Context, d Device, h protocol.Hello) (protocol.Welcome, error) {
	return s.helloDevice(ctx, d, h, 2)
}

func (s *Store) CheckDeviceProtocol(ctx context.Context, d Device, version int) error {
	return s.transaction(ctx, func(tx pgx.Tx) error {
		_, err := lockDevice(ctx, tx, d, "", version)
		return err
	})
}

func (s *Store) Devices(ctx context.Context, userID, networkID string) ([]protocol.DeviceView, error) {
	if err := s.RequireAccess(ctx, userID, networkID, false); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT d.id,d.network_id,d.name,d.role,d.current_session,d.capabilities,d.health,
		d.last_seen,d.last_inventory,d.revoked,
		coalesce(NOT d.revoked AND NOT bs.retired AND bs.device_id=d.id
			AND bs.network_id=d.network_id AND bs.protocol=CASE n.bridge_mode WHEN 'single' THEN 1 ELSE 2 END
			AND (n.bridge_mode='cooperative' OR d.role='primary')
			AND d.last_seen>now()-$2::double precision*interval '1 second',false)
		FROM devices d JOIN networks n ON n.id=d.network_id
		LEFT JOIN bridge_sessions bs ON bs.id=d.current_session
		WHERE d.network_id=$1 ORDER BY d.created_at,d.id`, networkID, s.options.OfflineAfter.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []protocol.DeviceView{}
	for rows.Next() {
		var d protocol.DeviceView
		var caps []byte
		if err = rows.Scan(&d.ID, &d.NetworkID, &d.Name, &d.Role, &d.SessionID, &caps, &d.Health, &d.LastSeen, &d.LastInventory, &d.Revoked, &d.Online); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(caps, &d.Capabilities); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

func (s *Store) CreateDevice(ctx context.Context, adminID, networkID, name string) (protocol.DeviceView, string, error) {
	d := protocol.DeviceView{ID: newID(), NetworkID: networkID, Name: strings.TrimSpace(name), Role: "quantity_worker", Health: json.RawMessage(`{}`)}
	if err := s.RequireAdmin(ctx, adminID); err != nil {
		return d, "", err
	}
	if len(d.Name) < 1 || len(d.Name) > 128 {
		return d, "", ErrInvalid
	}
	token := newToken("ac_dev_")
	hash := hashToken(token)
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, "SELECT id FROM networks WHERE id=$1 FOR UPDATE", networkID).Scan(&id); err != nil {
			return notFound(err)
		}
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM devices WHERE network_id=$1 AND NOT revoked", networkID).Scan(&count); err != nil {
			return err
		}
		if count >= 8 {
			return ErrCapacity
		}
		if _, err := tx.Exec(ctx, `INSERT INTO devices(id,network_id,name,role,token_hash) VALUES($1,$2,$3,$4,$5)`, d.ID, networkID, d.Name, d.Role, hash[:]); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE networks SET revision=revision+1 WHERE id=$1", networkID); err != nil {
			return err
		}
		return s.event(ctx, tx, networkID, "session")
	})
	if err != nil {
		return d, "", err
	}
	return d, token, nil
}

func (s *Store) EnableCooperation(ctx context.Context, adminID, networkID string) error {
	if err := s.RequireAdmin(ctx, adminID); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		var mode string
		if err := tx.QueryRow(ctx, "SELECT bridge_mode FROM networks WHERE id=$1 FOR UPDATE", networkID).Scan(&mode); err != nil {
			return notFound(err)
		}
		if mode == "cooperative" {
			return nil
		}
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders WHERE network_id=$1 AND state IN ('assigned','computing','running'))`, networkID).Scan(&active); err != nil {
			return err
		}
		if active {
			return ErrConflict
		}
		if _, err := tx.Exec(ctx, "UPDATE bridge_sessions SET retired=true WHERE network_id=$1 AND NOT retired", networkID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE devices SET current_session='',capabilities='{}',health='{}',last_seen=NULL WHERE network_id=$1`, networkID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE networks SET bridge_mode='cooperative',current_session='',capabilities='{}',health='{}',
			last_seen=NULL,last_scan=NULL,first_scan=NULL,revision=revision+1,catalog_revision=catalog_revision+1 WHERE id=$1`, networkID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM cpu_snapshots WHERE network_id=$1", networkID); err != nil {
			return err
		}
		return s.event(ctx, tx, networkID, "session")
	})
}

func (s *Store) RotateDevice(ctx context.Context, adminID, networkID, deviceID string) (string, error) {
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
		var role string
		var revoked bool
		if err := tx.QueryRow(ctx, "SELECT role,revoked FROM devices WHERE network_id=$1 AND id=$2 FOR UPDATE", networkID, deviceID).Scan(&role, &revoked); err != nil {
			return notFound(err)
		}
		if revoked {
			return ErrConflict
		}
		if role == "primary" {
			if err := s.markUncertain(ctx, tx, networkID, "device credential replaced; execution outcome requires review"); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE networks SET device_hash=$2,current_session='',capabilities='{}',health='{}',
				last_seen=NULL,last_scan=NULL,first_scan=NULL,revision=revision+1,catalog_revision=catalog_revision+1 WHERE id=$1`, networkID, hash[:]); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "DELETE FROM cpu_snapshots WHERE network_id=$1", networkID); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, "UPDATE networks SET revision=revision+1 WHERE id=$1", networkID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE bridge_sessions SET retired=true WHERE device_id=$1 AND NOT retired", deviceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE devices SET token_hash=$2,current_session='',capabilities='{}',health='{}',last_seen=NULL WHERE id=$1`, deviceID, hash[:]); err != nil {
			return err
		}
		return s.event(ctx, tx, networkID, "session")
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) RevokeDevice(ctx context.Context, adminID, networkID, deviceID string) error {
	if err := s.RequireAdmin(ctx, adminID); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, "SELECT id FROM networks WHERE id=$1 FOR UPDATE", networkID).Scan(&id); err != nil {
			return notFound(err)
		}
		var role string
		var revoked bool
		if err := tx.QueryRow(ctx, "SELECT role,revoked FROM devices WHERE network_id=$1 AND id=$2 FOR UPDATE", networkID, deviceID).Scan(&role, &revoked); err != nil {
			return notFound(err)
		}
		if role == "primary" {
			return ErrConflict
		}
		if revoked {
			return nil
		}
		if _, err := tx.Exec(ctx, "UPDATE bridge_sessions SET retired=true WHERE device_id=$1 AND NOT retired", deviceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE devices SET revoked=true,current_session='',capabilities='{}',health='{}',last_seen=NULL WHERE id=$1`, deviceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE networks SET revision=revision+1 WHERE id=$1", networkID); err != nil {
			return err
		}
		return s.event(ctx, tx, networkID, "session")
	})
}

func lockDevice(ctx context.Context, tx pgx.Tx, d Device, sessionID string, version int) (string, error) {
	var group, mode string
	if err := tx.QueryRow(ctx, "SELECT server_group,bridge_mode FROM networks WHERE id=$1 FOR UPDATE", d.NetworkID).Scan(&group, &mode); err != nil {
		return "", notFound(err)
	}
	var current, role string
	var hash []byte
	var revoked bool
	err := tx.QueryRow(ctx, `SELECT current_session,role,token_hash,revoked FROM devices
		WHERE network_id=$1 AND id=$2 FOR UPDATE`, d.NetworkID, d.ID).Scan(&current, &role, &hash, &revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnauthorized
	}
	if err != nil {
		return "", err
	}
	if revoked || !bytes.Equal(hash, d.TokenHash[:]) || d.Role != role {
		return "", ErrUnauthorized
	}
	if (version == 1 && (mode != "single" || role != "primary")) || (version == 2 && mode != "cooperative") || (version != 1 && version != 2) {
		return "", ErrConflict
	}
	if sessionID != "" {
		if current != sessionID {
			return "", ErrStaleSession
		}
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bridge_sessions
			WHERE id=$1 AND device_id=$2 AND network_id=$3 AND NOT retired AND protocol=$4)`, sessionID, d.ID, d.NetworkID, version).Scan(&valid); err != nil {
			return "", err
		}
		if !valid {
			return "", ErrStaleSession
		}
	}
	return group, nil
}

func deviceCapabilities(role string, caps protocol.Capabilities) protocol.Capabilities {
	if role == "quantity_worker" {
		return protocol.Capabilities{Inventory: caps.Inventory, ExactQuantities: caps.ExactQuantities, QuantityRefresh: caps.QuantityRefresh}
	}
	return caps
}
