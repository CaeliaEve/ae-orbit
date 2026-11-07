package store

import (
	"context"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

// ItemsByID reads a bounded selection from the persisted inventory. Missing IDs
// remain explicit; neither a missing row nor an old session means zero stock.
func (s *Store) ItemsByID(ctx context.Context, userID, networkID string, ids []protocol.Quantity) (protocol.ItemLookup, error) {
	result := protocol.ItemLookup{Items: []protocol.Item{}, MissingIDs: []protocol.Quantity{}}
	if err := s.RequireAccess(ctx, userID, networkID, false); err != nil {
		return result, err
	}
	if len(ids) < 1 || len(ids) > 200 {
		return result, ErrInvalid
	}
	seen := make(map[protocol.Quantity]bool, len(ids))
	unique := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id < 1 {
			return result, ErrInvalid
		}
		if !seen[id] {
			seen[id] = true
			unique = append(unique, int64(id))
		}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(context.Background())
	var session string
	if err = tx.QueryRow(ctx, "SELECT revision,current_session FROM networks WHERE id=$1", networkID).Scan(&result.Revision, &session); err != nil {
		return result, notFound(err)
	}
	rows, err := tx.Query(ctx, `SELECT d.id,d.kind,d.name,d.damage,d.label,d.exact,i.quantity,i.craftable,
        i.observed_at,i.craftable_observed_at,i.session_id=$2
        FROM item_definitions d JOIN inventory i ON i.network_id=d.network_id AND i.item_id=d.id
        WHERE d.network_id=$1 AND d.id=ANY($3::bigint[])`, networkID, session, unique)
	if err != nil {
		return result, err
	}
	items := make(map[protocol.Quantity]protocol.Item, len(unique))
	for rows.Next() {
		var item protocol.Item
		if err = rows.Scan(&item.ID, &item.Kind, &item.Name, &item.Damage, &item.Label, &item.Exact,
			&item.Quantity, &item.Craftable, &item.ObservedAt, &item.CraftableObservedAt, &item.CurrentSession); err != nil {
			rows.Close()
			return result, err
		}
		items[item.ID] = item
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return result, err
	}
	for _, value := range unique {
		id := protocol.Quantity(value)
		if item, found := items[id]; found {
			result.Items = append(result.Items, item)
		} else {
			result.MissingIDs = append(result.MissingIDs, id)
		}
	}
	return result, tx.Commit(ctx)
}
