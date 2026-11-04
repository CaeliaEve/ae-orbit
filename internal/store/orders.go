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

const orderColumns = `o.id,o.network_id,o.member_id,o.item_id,o.quantity,o.state,o.attempt_id,o.reason,o.created_at,o.updated_at,o.expires_at`

func scanOrder(row scanner) (protocol.Order, error) {
	var o protocol.Order
	err := row.Scan(&o.ID, &o.NetworkID, &o.UserID, &o.ItemID, &o.Quantity, &o.State, &o.AttemptID, &o.Reason, &o.CreatedAt, &o.UpdatedAt, &o.ExpiresAt)
	return o, notFound(err)
}

func (s *Store) CreateOrder(ctx context.Context, userID, networkID string, in protocol.OrderInput) (protocol.Order, error) {
	var result protocol.Order
	if err := s.RequireAccess(ctx, userID, networkID, true); err != nil {
		return result, err
	}
	if err := in.Validate(); err != nil {
		return result, errors.Join(ErrInvalid, err)
	}
	if in.TTLSeconds == 0 {
		in.TTLSeconds = 3600
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var capsJSON []byte
		var session string
		if err := tx.QueryRow(ctx, "SELECT capabilities,current_session FROM networks WHERE id=$1 FOR UPDATE", networkID).Scan(&capsJSON, &session); err != nil {
			return notFound(err)
		}
		existing, err := scanOrder(tx.QueryRow(ctx, "SELECT "+orderColumns+" FROM orders o WHERE o.member_id=$1 AND o.network_id=$2 AND o.idempotency_key=$3", userID, networkID, in.IdempotencyKey))
		if err == nil {
			var ttl int
			if err = tx.QueryRow(ctx, "SELECT ttl_seconds FROM orders WHERE id=$1", existing.ID).Scan(&ttl); err != nil {
				return err
			}
			if existing.ItemID != in.ItemID || existing.Quantity != in.Quantity || ttl != in.TTLSeconds {
				return ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		var caps protocol.Capabilities
		if err = json.Unmarshal(capsJSON, &caps); err != nil {
			return err
		}
		if !caps.Craft || in.Quantity > caps.MaxCraftQuantity || session == "" {
			return ErrCapability
		}
		var craftable, exact bool
		var pending int
		if err = tx.QueryRow(ctx, `SELECT i.craftable,d.exact FROM inventory i JOIN item_definitions d ON d.id=i.item_id AND d.network_id=i.network_id WHERE i.network_id=$1 AND i.item_id=$2`, networkID, int64(in.ItemID)).Scan(&craftable, &exact); err != nil {
			return notFound(err)
		}
		if !craftable || !exact {
			return ErrCapability
		}
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM orders WHERE network_id=$1 AND state='queued'", networkID).Scan(&pending); err != nil {
			return err
		}
		if pending >= s.options.MaxQueuedOrders {
			return ErrCapacity
		}
		result.ID = newID()
		if _, err = tx.Exec(ctx, `INSERT INTO orders(id,network_id,member_id,item_id,quantity,idempotency_key,ttl_seconds,state,expires_at)
            VALUES($1,$2,$3,$4,$5,$6,$7::integer,'queued',now()+$7::integer*interval '1 second')`, result.ID, networkID, userID, int64(in.ItemID), int64(in.Quantity), in.IdempotencyKey, in.TTLSeconds); err != nil {
			return err
		}
		if err = auditOrder(ctx, tx, result.ID); err != nil {
			return err
		}
		result, err = scanOrder(tx.QueryRow(ctx, "SELECT "+orderColumns+" FROM orders o WHERE o.id=$1", result.ID))
		if err != nil {
			return err
		}
		return s.event(ctx, tx, networkID, "orders")
	})
	return result, err
}

func auditOrder(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `INSERT INTO order_events(order_id,state,reason,attempt_id) SELECT id,state,reason,attempt_id FROM orders WHERE id=$1`, id)
	return err
}

func (s *Store) Order(ctx context.Context, userID, networkID, id string) (protocol.Order, error) {
	if err := s.RequireAccess(ctx, userID, networkID, false); err != nil {
		return protocol.Order{}, err
	}
	return scanOrder(s.pool.QueryRow(ctx, "SELECT "+orderColumns+" FROM orders o WHERE o.network_id=$1 AND o.id=$2", networkID, id))
}

func (s *Store) Orders(ctx context.Context, userID, networkID, state string, limit int) ([]protocol.Order, error) {
	if err := s.RequireAccess(ctx, userID, networkID, false); err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 200 {
		return nil, ErrInvalid
	}
	if state != "" && state != "queued" && state != "assigned" && !protocol.ReportState(state) && state != "expired" {
		return nil, ErrInvalid
	}
	rows, err := s.pool.Query(ctx, "SELECT "+orderColumns+" FROM orders o WHERE o.network_id=$1 AND ($2='' OR o.state=$2) ORDER BY o.created_at DESC,o.id LIMIT $3", networkID, state, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []protocol.Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, o)
	}
	return result, rows.Err()
}

func (s *Store) CancelOrder(ctx context.Context, userID, networkID, id string) (protocol.Order, error) {
	return s.finishOrder(ctx, userID, networkID, id, "canceled", "canceled before dispatch", false)
}
func (s *Store) ResolveOrder(ctx context.Context, userID, networkID, id, state, reason string) (protocol.Order, error) {
	if state != "done" && state != "failed" && state != "canceled" {
		return protocol.Order{}, ErrInvalid
	}
	if len(strings.TrimSpace(reason)) < 1 || len(reason) > 1024 {
		return protocol.Order{}, ErrInvalid
	}
	return s.finishOrder(ctx, userID, networkID, id, state, reason, true)
}

func (s *Store) finishOrder(ctx context.Context, userID, networkID, id, state, reason string, review bool) (protocol.Order, error) {
	var result protocol.Order
	if err := s.RequireAccess(ctx, userID, networkID, true); err != nil {
		return result, err
	}
	if review {
		if err := s.RequireAdmin(ctx, userID); err != nil {
			return result, err
		}
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT id FROM networks WHERE id=$1 FOR UPDATE", networkID); err != nil {
			return err
		}
		var err error
		result, err = scanOrder(tx.QueryRow(ctx, "SELECT "+orderColumns+" FROM orders o WHERE o.network_id=$1 AND o.id=$2 FOR UPDATE", networkID, id))
		if err != nil {
			return err
		}
		if !review && result.State == "canceled" {
			return nil
		}
		if (review && result.State != "needs_review") || (!review && result.State != "queued") {
			return ErrConflict
		}
		if _, err = tx.Exec(ctx, "UPDATE orders SET state=$2,reason=$3,updated_at=now() WHERE id=$1", id, state, reason); err != nil {
			return err
		}
		if err = auditOrder(ctx, tx, id); err != nil {
			return err
		}
		result, err = scanOrder(tx.QueryRow(ctx, "SELECT "+orderColumns+" FROM orders o WHERE o.id=$1", id))
		if err != nil {
			return err
		}
		return s.event(ctx, tx, networkID, "orders")
	})
	return result, err
}

type OrderEvent struct {
	ID        protocol.Quantity `json:"id"`
	State     string            `json:"state"`
	Reason    string            `json:"reason"`
	AttemptID string            `json:"attempt_id"`
	CreatedAt time.Time         `json:"created_at"`
}

func (s *Store) OrderHistory(ctx context.Context, userID, networkID, id string) ([]OrderEvent, error) {
	if _, err := s.Order(ctx, userID, networkID, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, "SELECT id,state,reason,attempt_id,created_at FROM order_events WHERE order_id=$1 ORDER BY id DESC LIMIT 100", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []OrderEvent{}
	for rows.Next() {
		var e OrderEvent
		if err = rows.Scan(&e.ID, &e.State, &e.Reason, &e.AttemptID, &e.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

func (s *Store) markUncertain(ctx context.Context, tx pgx.Tx, networkID, reason string) error {
	tag, err := tx.Exec(ctx, `WITH changed AS (UPDATE orders SET state='needs_review',reason=$2,updated_at=now()
        WHERE network_id=$1 AND state IN ('assigned','computing','running') RETURNING id,state,reason,attempt_id)
        INSERT INTO order_events(order_id,state,reason,attempt_id) SELECT id,state,reason,attempt_id FROM changed`, networkID, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return s.event(ctx, tx, networkID, "orders")
	}
	return nil
}

func (s *Store) expireQueued(ctx context.Context, tx pgx.Tx, networkID string) error {
	tag, err := tx.Exec(ctx, `WITH changed AS (UPDATE orders SET state='expired',reason='queue deadline elapsed',updated_at=now()
        WHERE network_id=$1 AND state='queued' AND expires_at<=now() RETURNING id,state,reason,attempt_id)
        INSERT INTO order_events(order_id,state,reason,attempt_id) SELECT id,state,reason,attempt_id FROM changed`, networkID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return s.event(ctx, tx, networkID, "orders")
	}
	return nil
}

// Maintain never retries a game side effect. It preserves uncertain attempts
// for explicit review and bounds completed history and expired web sessions.
func (s *Store) Maintain(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT n.id FROM networks n WHERE
        (n.last_seen<now()-$1::double precision*interval '1 second' AND EXISTS(SELECT 1 FROM orders o WHERE o.network_id=n.id AND o.state IN ('assigned','computing','running')))
        OR EXISTS(SELECT 1 FROM orders o WHERE o.network_id=n.id AND o.state='queued' AND o.expires_at<=now()) LIMIT 64`, s.options.OfflineAfter.Seconds())
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.transaction(ctx, func(tx pgx.Tx) error {
			var offline bool
			if err := tx.QueryRow(ctx, "SELECT coalesce(last_seen<now()-$2::double precision*interval '1 second',true) FROM networks WHERE id=$1 FOR UPDATE", id, s.options.OfflineAfter.Seconds()).Scan(&offline); err != nil {
				return err
			}
			if offline {
				if err := s.markUncertain(ctx, tx, id, "bridge unreachable; execution outcome unknown"); err != nil {
					return err
				}
			}
			return s.expireQueued(ctx, tx, id)
		}); err != nil {
			return err
		}
	}
	if _, err = s.pool.Exec(ctx, "DELETE FROM web_sessions WHERE expires_at<now()"); err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM orders WHERE id IN (SELECT id FROM orders WHERE state IN ('done','failed','canceled','expired') AND updated_at<now()-interval '30 days' ORDER BY updated_at LIMIT 1000)`)
	return err
}
