package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

// RequestItemRefresh schedules quantity-only reads. Admission never alters
// inventory evidence or the separate recipe-required order confirmation queue.
func (s *Store) RequestItemRefresh(ctx context.Context, userID, networkID string, ids []protocol.Quantity) (protocol.ItemLookup, error) {
	var result protocol.ItemLookup
	if err := s.RequireAccess(ctx, userID, networkID, false); err != nil {
		return result, err
	}
	if len(ids) < 1 || len(ids) > 32 {
		return result, ErrInvalid
	}
	input := make([]int64, len(ids))
	for i, id := range ids {
		if id < 1 {
			return result, ErrInvalid
		}
		input[i] = int64(id)
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var encoded []byte
		// The same lock serializes admission against device mutations and other
		// members, so capacity checks and generation replacement stay atomic.
		var mode string
		if err := tx.QueryRow(ctx, "SELECT capabilities,bridge_mode FROM networks WHERE id=$1 FOR UPDATE", networkID).Scan(&encoded, &mode); err != nil {
			return notFound(err)
		}
		var caps protocol.Capabilities
		if err := json.Unmarshal(encoded, &caps); err != nil {
			return err
		}
		if mode == "cooperative" {
			var supported bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM devices d
				JOIN bridge_sessions bs ON bs.id=d.current_session AND bs.device_id=d.id AND NOT bs.retired AND bs.protocol=2
				WHERE d.network_id=$1 AND NOT d.revoked AND bs.capabilities->>'inventory'='true'
				AND bs.capabilities->>'exact_quantities'='true')`, networkID).Scan(&supported); err != nil {
				return err
			}
			if !supported {
				return ErrCapability
			}
		} else if !caps.Inventory || !caps.QuantityRefresh {
			return ErrCapability
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT i.item_id,d.kind,d.exact FROM item_definitions requested
			LEFT JOIN item_aliases a ON a.network_id=requested.network_id AND a.alias_id=requested.id
			JOIN inventory i ON i.network_id=requested.network_id AND i.item_id=coalesce(a.canonical_id,requested.id)
			JOIN item_definitions d ON d.network_id=i.network_id AND d.id=i.item_id
			WHERE requested.network_id=$1 AND requested.id=ANY($2::bigint[])
			ORDER BY i.item_id`, networkID, input)
		if err != nil {
			return err
		}
		var canonical []int64
		for rows.Next() {
			var id int64
			var kind string
			var exact bool
			if err = rows.Scan(&id, &kind, &exact); err != nil {
				rows.Close()
				return err
			}
			if kind != "item" || !exact {
				rows.Close()
				return ErrInvalid
			}
			canonical = append(canonical, id)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		if len(canonical) == 0 {
			return nil
		}
		var pending, additional int
		if err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE item_id=ANY($2::bigint[]))
			FROM inventory_refreshes WHERE network_id=$1 AND confirmed_at IS NULL AND expires_at>now()`, networkID, canonical).Scan(&pending, &additional); err != nil {
			return err
		}
		if pending+len(canonical)-additional > 128 {
			return ErrCapacity
		}
		// Retain terminal statuses until the fixed-size history needs their
		// space. Never evict live pending work or a row in this admission.
		if _, err = tx.Exec(ctx, `DELETE FROM inventory_refreshes WHERE network_id=$1 AND item_id IN (
			SELECT item_id FROM inventory_refreshes WHERE network_id=$1
			AND NOT (item_id=ANY($2::bigint[])) AND (confirmed_at IS NOT NULL OR expires_at<=now())
			ORDER BY requested_at,item_id LIMIT greatest(0,
				(SELECT count(*) FROM inventory_refreshes WHERE network_id=$1)
				+(SELECT count(*) FROM unnest($2::bigint[]) x(id)
					WHERE NOT EXISTS(SELECT 1 FROM inventory_refreshes r WHERE r.network_id=$1 AND r.item_id=x.id))-256)
			)`, networkID, canonical); err != nil {
			return err
		}
		for _, id := range canonical {
			if _, err = tx.Exec(ctx, `INSERT INTO inventory_refreshes(network_id,item_id,token) VALUES($1,$2,$3)
				ON CONFLICT(network_id,item_id) DO UPDATE SET token=excluded.token,requested_at=now(),
				expires_at=now()+interval '60 seconds',confirmed_at=NULL,next_attempt_at=now(),dispatched_session=''
				WHERE inventory_refreshes.confirmed_at IS NOT NULL OR inventory_refreshes.expires_at<=now()`, networkID, id, newID()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	return s.ItemsByID(ctx, userID, networkID, ids)
}

func itemRefreshes(ctx context.Context, tx pgx.Tx, networkID string, ids []int64) (map[protocol.Quantity]protocol.ItemRefresh, error) {
	rows, err := tx.Query(ctx, `SELECT requested.id,r.token,
		CASE WHEN r.confirmed_at IS NOT NULL THEN 'confirmed' WHEN r.expires_at<=now() THEN 'expired' ELSE 'pending' END,
		r.requested_at,r.expires_at,r.confirmed_at
		FROM item_definitions requested
		LEFT JOIN item_aliases a ON a.network_id=requested.network_id AND a.alias_id=requested.id
		JOIN inventory_refreshes r ON r.network_id=requested.network_id AND r.item_id=coalesce(a.canonical_id,requested.id)
		WHERE requested.network_id=$1 AND requested.id=ANY($2::bigint[])`, networkID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[protocol.Quantity]protocol.ItemRefresh)
	for rows.Next() {
		var r protocol.ItemRefresh
		if err := rows.Scan(&r.ID, &r.RequestID, &r.State, &r.RequestedAt, &r.ExpiresAt, &r.ConfirmedAt); err != nil {
			return nil, err
		}
		result[r.ID] = r
	}
	return result, rows.Err()
}

func (s *Store) quantityRefreshes(ctx context.Context, tx pgx.Tx, networkID, sessionID string, responseBytes, slots, queueBytes int, ack *protocol.PollAck) error {
	if slots <= 0 || queueBytes <= 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT r.item_id,d.kind,d.name,d.damage,d.nbt,d.label,d.exact,r.token,
		least(60000,floor(extract(epoch FROM r.expires_at-now())*1000))::int
		FROM inventory_refreshes r JOIN item_definitions d ON d.network_id=r.network_id AND d.id=r.item_id
		WHERE r.network_id=$1 AND r.confirmed_at IS NULL AND r.expires_at>=now()+interval '1 millisecond' AND r.next_attempt_at<=now()
		ORDER BY r.next_attempt_at,r.item_id LIMIT 32`, networkID)
	if err != nil {
		return err
	}
	var candidates []protocol.QuantityRefresh
	for rows.Next() {
		var r protocol.QuantityRefresh
		if err = rows.Scan(&r.ID, &r.Item.Kind, &r.Item.Name, &r.Item.Damage, &r.Item.NBT, &r.Item.Label, &r.Item.Exact, &r.Token, &r.TTLMillis); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, candidate := range candidates {
		if len(ack.Refresh) >= min(4, slots) {
			break
		}
		rawBytes := base64.StdEncoding.DecodedLen(len(candidate.Item.NBT))
		keyBytes := 0
		if candidate.Item.NBT != "" {
			keyBytes = 64
		}
		cost := 2*(len(candidate.Item.Name)+len(strconv.Itoa(candidate.Item.Damage))+2) +
			max(2*rawBytes, 2*keyBytes+rawBytes) + len(candidate.Item.Label) + len(candidate.Token) + 256
		fits := cost <= queueBytes
		if fits {
			ack.Refresh = append(ack.Refresh, candidate)
			data, _ := json.Marshal(ack)
			fits = len(data) <= responseBytes
			if !fits {
				ack.Refresh = ack.Refresh[:len(ack.Refresh)-1]
			}
		}
		// Oversized candidates rotate too; otherwise one large identity would
		// keep the bounded candidate window from reaching smaller requests.
		if _, err = tx.Exec(ctx, `UPDATE inventory_refreshes SET next_attempt_at=now()+interval '2 seconds',
			dispatched_session=CASE WHEN $3 THEN $4 ELSE dispatched_session END
			WHERE network_id=$1 AND item_id=$2`, networkID, int64(candidate.ID), fits, sessionID); err != nil {
			return err
		}
		if fits {
			queueBytes -= cost
		}
	}
	return nil
}
