package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

const networkColumns = `n.id,n.name,n.server_group,n.source,n.current_session,n.revision,n.last_seen,n.last_inventory,n.last_scan,n.capabilities,n.health,
    n.first_scan,(SELECT created_at FROM bridge_sessions bs WHERE bs.id=n.current_session),n.bridge_mode,
	EXISTS(SELECT 1 FROM devices d JOIN bridge_sessions bs ON bs.id=d.current_session
		WHERE d.network_id=n.id AND d.role='primary' AND NOT d.revoked AND NOT bs.retired
		AND bs.device_id=d.id AND bs.network_id=n.id AND d.current_session=n.current_session
		AND bs.protocol=CASE n.bridge_mode WHEN 'single' THEN 1 ELSE 2 END),
	(SELECT jsonb_build_object(
		'quantity_readers',count(*) FILTER(WHERE d.capabilities->>'inventory'='true' AND d.capabilities->>'exact_quantities'='true'),
		'discovery',coalesce(bool_or(d.role='primary' AND d.capabilities->>'inventory'='true'),false),
		'recipes',coalesce(bool_or(d.role='primary' AND d.capabilities->>'inventory'='true'),false),
		'craft',coalesce(bool_or(d.role='primary' AND d.capabilities->>'craft'='true'),false))
		FROM devices d JOIN bridge_sessions bs ON bs.id=d.current_session
		WHERE d.network_id=n.id AND NOT d.revoked AND NOT bs.retired AND bs.device_id=d.id AND bs.network_id=n.id
		AND bs.protocol=CASE n.bridge_mode WHEN 'single' THEN 1 ELSE 2 END
		AND (n.bridge_mode='cooperative' OR d.role='primary')
		AND d.last_seen>now()-$2::double precision*interval '1 second'),
	(SELECT observed_at FROM cpu_snapshots WHERE network_id=n.id)`

func (s *Store) CreateNetwork(ctx context.Context, adminID, name, group, source string) (protocol.Network, string, error) {
	n := protocol.Network{ID: newID(), Name: strings.TrimSpace(name), ServerGroup: strings.TrimSpace(group), Source: source, BridgeMode: "single", Services: &protocol.NetworkServices{}}
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
		if _, err := tx.Exec(ctx, "INSERT INTO networks(id,name,server_group,source,device_hash) VALUES($1,$2,$3,$4,$5)", n.ID, n.Name, n.ServerGroup, n.Source, h[:]); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "INSERT INTO devices(id,network_id,name,role,token_hash) VALUES($1,$2,$3,'primary',$4)", newID(), n.ID, n.Name, h[:])
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
	var deviceID string
	if err := s.pool.QueryRow(ctx, "SELECT id FROM devices WHERE network_id=$1 AND role='primary'", networkID).Scan(&deviceID); err != nil {
		return "", notFound(err)
	}
	return s.RotateDevice(ctx, adminID, networkID, deviceID)
}

type scanner interface{ Scan(...any) error }

func (s *Store) scanNetwork(row scanner) (protocol.Network, error) {
	var n protocol.Network
	var caps, services []byte
	var primaryValid bool
	n.Services = &protocol.NetworkServices{}
	err := row.Scan(&n.ID, &n.Name, &n.ServerGroup, &n.Source, &n.SessionID, &n.Revision, &n.LastSeen, &n.LastInventory, &n.LastScan, &caps, &n.Health, &n.FirstScan, &n.SessionStartedAt,
		&n.BridgeMode, &primaryValid, &services, &n.Services.CPUObservedAt)
	if err != nil {
		return n, notFound(err)
	}
	if err = json.Unmarshal(caps, &n.Capabilities); err != nil {
		return n, err
	}
	if err = json.Unmarshal(services, n.Services); err != nil {
		return n, err
	}
	n.Online = primaryValid && n.LastSeen != nil && time.Since(*n.LastSeen) < s.options.OfflineAfter
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
        LEFT JOIN network_grants g ON g.member_id=m.id AND g.network_id=n.id WHERE NOT m.revoked AND (m.is_admin OR g.member_id IS NOT NULL) ORDER BY n.name,n.id LIMIT 1000`, userID, s.options.OfflineAfter.Seconds())
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
	return s.scanNetwork(s.pool.QueryRow(ctx, `SELECT `+networkColumns+` FROM networks n WHERE n.id=$1`, networkID, s.options.OfflineAfter.Seconds()))
}

// Every device mutation locks its network and rechecks the credential and epoch.
func lockNetwork(ctx context.Context, tx pgx.Tx, d Device, sessionID string) (string, error) {
	return lockDevice(ctx, tx, d, sessionID, 1)
}

func (s *Store) negotiatedLimits(responseBytes int) protocol.Limits {
	l := s.options.Limits
	l.ResponseBytes = min(responseBytes, l.ResponseBytes)
	l.BatchItems = min(l.BatchItems, l.ResponseBytes/96)
	l.DiagnosticSamples = protocol.MaxReadFailureSamples
	l.SyncTelemetry = true
	l.QuantityRefresh = true
	return l
}

func (s *Store) Hello(ctx context.Context, d Device, h protocol.Hello) (protocol.Welcome, error) {
	return s.helloDevice(ctx, d, h, 1)
}

func (s *Store) helloDevice(ctx context.Context, d Device, h protocol.Hello, version int) (protocol.Welcome, error) {
	var w protocol.Welcome
	var validation error
	if version == 2 {
		validation = h.ValidateV2()
	} else {
		validation = h.Validate()
	}
	if validation != nil {
		return w, errors.Join(ErrInvalid, validation)
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := lockDevice(ctx, tx, d, "", version); err != nil {
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
		var sessionProtocol int
		err := tx.QueryRow(ctx, `SELECT id,retired,response_bytes,inventory_seq,poll_seq,protocol FROM bridge_sessions
			WHERE device_id=$1 AND boot_id=$2`, d.ID, h.BootID).Scan(&w.SessionID, &retired, &responseBytes, &w.InventoryAck, &w.PollAck, &sessionProtocol)
		if err == nil {
			var current string
			if err = tx.QueryRow(ctx, "SELECT current_session FROM devices WHERE id=$1", d.ID).Scan(&current); err != nil {
				return err
			}
			if retired || sessionProtocol != version || current != w.SessionID {
				return ErrStaleSession
			}
			w.Limits = s.negotiatedLimits(responseBytes)
		} else if errors.Is(err, pgx.ErrNoRows) {
			w.SessionID = newID()
			w.Limits = s.negotiatedLimits(h.MaxResponseBytes)
			if d.Role == "primary" {
				if err = s.markUncertain(ctx, tx, d.NetworkID, "bridge restarted; previous tracking handles cannot be recovered"); err != nil {
					return err
				}
			}
			caps, _ := json.Marshal(deviceCapabilities(d.Role, h.Capabilities))
			if _, err = tx.Exec(ctx, "UPDATE bridge_sessions SET retired=true WHERE device_id=$1 AND NOT retired", d.ID); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO bridge_sessions(id,network_id,device_id,protocol,boot_id,capabilities,response_bytes)
				VALUES($1,$2,$3,$4,$5,$6,$7)`, w.SessionID, d.NetworkID, d.ID, version, h.BootID, caps, w.Limits.ResponseBytes); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "UPDATE devices SET current_session=$2,capabilities=$3,health='{}' WHERE id=$1", d.ID, w.SessionID, caps); err != nil {
				return err
			}
			if d.Role == "primary" {
				if _, err = tx.Exec(ctx, "UPDATE networks SET current_session=$2,capabilities=$3,health='{}',revision=revision+1,catalog_revision=catalog_revision+1,last_scan=NULL,first_scan=NULL WHERE id=$1", d.NetworkID, w.SessionID, caps); err != nil {
					return err
				}
				if _, err = tx.Exec(ctx, "DELETE FROM cpu_snapshots WHERE network_id=$1", d.NetworkID); err != nil {
					return err
				}
			} else if _, err = tx.Exec(ctx, "UPDATE networks SET revision=revision+1 WHERE id=$1", d.NetworkID); err != nil {
				return err
			}
			if err = s.event(ctx, tx, d.NetworkID, "session"); err != nil {
				return err
			}
		} else {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE devices SET last_seen=now() WHERE id=$1", d.ID); err != nil {
			return err
		}
		if d.Role == "primary" {
			if _, err = tx.Exec(ctx, "UPDATE networks SET last_seen=now() WHERE id=$1", d.NetworkID); err != nil {
				return err
			}
		}
		w.Protocol = version
		w.NetworkID = d.NetworkID
		if version == 2 {
			w.DeviceID, w.Role = d.ID, d.Role
			w.Limits.ReadSlots = 16
			w.Limits.ReadBytes = min(w.Limits.ResponseBytes, 16384)
		}
		w.Limits.BackgroundRecipeInconclusive = version == 2 && d.Role == "primary"
		return nil
	})
	return w, err
}
