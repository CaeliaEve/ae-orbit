package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const plainStockRecheck = 30 * time.Second
const plainZeroRecheck = time.Hour

// Stock and recipe work starts from the small observed stock set, rather than
// walking every scheduled candidate to find the few positive observations.
func (s *Store) plainBackgroundPool(ctx context.Context, tx pgx.Tx, d Device, stock, recipe bool) ([]cooperativeCandidate, error) {
	kind, due := "quantity", "q.next_quantity_at"
	if recipe {
		kind, due = "quantity_recipe", "q.next_verify_at"
	}
	columns := `d.id,d.kind,d.name,d.damage,d.nbt,d.label,d.exact,'',now()+interval '30 seconds'`
	from := `FROM inventory_schedule q
		JOIN network_inventory_profiles p ON p.network_id=q.network_id
		JOIN LATERAL (SELECT d.* FROM plain_catalog_candidates c
			JOIN item_definitions d ON d.network_id=c.network_id AND d.id=c.item_id
			WHERE c.network_id=q.network_id AND c.item_id=q.item_id OFFSET 0) d ON true
		LEFT JOIN LATERAL (SELECT i.item_id,i.quantity FROM inventory i
			WHERE i.network_id=q.network_id AND i.item_id=q.item_id AND i.observed_at>=p.enabled_at
			OFFSET 0) i ON true`
	prefix, observed := "", " AND (i.item_id IS NULL OR i.quantity=0)"
	if stock {
		prefix = `WITH stock AS MATERIALIZED (
			SELECT i.item_id FROM inventory i JOIN network_inventory_profiles p ON p.network_id=i.network_id
			WHERE i.network_id=$1 AND i.quantity>0 AND i.observed_at>=p.enabled_at)
		`
		// Keep the ordered LIMIT from moving the full schedule ahead of stock.
		// Each lateral lookup visits one identity; OFFSET 0 prevents flattening
		// it back into a scan of every scheduled row for each stock identity.
		from = `FROM stock i
			JOIN LATERAL (SELECT q.* FROM inventory_schedule q
				WHERE q.network_id=$1 AND q.item_id=i.item_id OFFSET 0) q ON true
			JOIN LATERAL (SELECT d.* FROM plain_catalog_candidates c
				JOIN item_definitions d ON d.network_id=c.network_id AND d.id=c.item_id
				WHERE c.network_id=q.network_id AND c.item_id=q.item_id OFFSET 0) d ON true`
		observed = ""
	}
	query := prefix + `SELECT ` + columns + ` ` + from + `
		WHERE q.network_id=$1 AND d.kind='item' AND d.exact AND d.nbt='' AND ` + due + `<=now()` + observed + `
		AND ` + noActiveCooperativeQuantity + `
		AND NOT EXISTS(SELECT 1 FROM inventory_refreshes hot WHERE hot.network_id=q.network_id
			AND hot.item_id=q.item_id AND hot.confirmed_at IS NULL AND hot.expires_at>now())
		AND NOT EXISTS(SELECT 1 FROM inventory_rechecks urgent WHERE urgent.network_id=q.network_id AND urgent.item_id=q.item_id)`
	if recipe {
		query += ` AND ` + noActiveCooperativeRecipe
	}
	query += ` ORDER BY ` + due + `,q.item_id LIMIT `
	if recipe {
		query += "1"
	} else {
		query += "64"
	}
	rows, err := tx.Query(ctx, query, d.NetworkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []cooperativeCandidate{}
	for rows.Next() {
		c := cooperativeCandidate{NextQuantityTurn: -1, NextResourceTurn: -1, NextFluidRecipeTurn: -1}
		c.Kind, c.Purpose = kind, "background"
		if err := rows.Scan(&c.ID, &c.Item.Kind, &c.Item.Name, &c.Item.Damage, &c.Item.NBT,
			&c.Item.Label, &c.Item.Exact, &c.RequestID, &c.ExpiresAt); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (s *Store) plainBackgroundCandidates(ctx context.Context, tx pgx.Tx, d Device, turn int) ([]cooperativeCandidate, error) {
	hot, err := s.plainBackgroundPool(ctx, tx, d, true, false)
	if err != nil {
		return nil, fmt.Errorf("plain stock quantity candidates: %w", err)
	}
	cold, err := s.plainBackgroundPool(ctx, tx, d, false, false)
	if err != nil {
		return nil, fmt.Errorf("plain cold quantity candidates: %w", err)
	}
	result := []cooperativeCandidate{}
	if d.Role == "primary" {
		var pendingRecipe bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cooperative_read_fields f
			WHERE f.network_id=$1 AND f.device_id=$2 AND f.field='recipe'
			AND f.completed_at IS NULL AND f.expires_at>now()
			AND EXISTS(SELECT 1 FROM devices owner WHERE owner.id=f.device_id AND owner.current_session=f.session_id))`,
			d.NetworkID, d.ID).Scan(&pendingRecipe); err != nil {
			return nil, err
		}
		if !pendingRecipe {
			recipes, err := s.plainBackgroundPool(ctx, tx, d, true, true)
			if err != nil {
				return nil, fmt.Errorf("plain stock recipe candidates: %w", err)
			}
			result = append(result, recipes...)
		}
	}
	// Persist the quantity turn actually reached by dispatch. Even one-slot
	// clients get cold work after at most three hot opportunities.
	for len(hot)+len(cold) > 0 {
		useHot := (turn != 3 && len(hot) > 0) || len(cold) == 0
		var c cooperativeCandidate
		if useHot {
			c, hot = hot[0], hot[1:]
		} else {
			c, cold = cold[0], cold[1:]
		}
		turn = (turn + 1) % 4
		c.NextQuantityTurn = turn
		result = append(result, c)
	}
	return result, nil
}
