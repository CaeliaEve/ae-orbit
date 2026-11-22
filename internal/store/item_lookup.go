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
	plain, err := plainProfile(ctx, tx, networkID)
	if err != nil {
		return result, err
	}
	rows, err := tx.Query(ctx, `SELECT requested.id,CASE WHEN requested.id<>d.id THEN d.id ELSE 0 END,d.kind,d.name,d.damage,coalesce(c.label,d.label),d.exact,d.nbt,coalesce(i.quantity,0),coalesce(i.craftable,false),
        coalesce(i.observed_at,'epoch'::timestamptz),i.craftable_observed_at,`+quantityCurrentSessionSQL+`,`+recipeCurrentSessionSQL+`,i.item_id IS NOT NULL
        FROM item_definitions requested
        LEFT JOIN item_aliases a ON a.network_id=requested.network_id AND a.alias_id=requested.id
        JOIN item_definitions d ON d.network_id=requested.network_id AND d.id=coalesce(a.canonical_id,requested.id)
        LEFT JOIN item_catalog_candidates c ON c.network_id=d.network_id AND c.item_id=d.id
        LEFT JOIN inventory i ON i.network_id=d.network_id AND i.item_id=d.id AND `+catalogObservedSQL+`
        WHERE requested.network_id=$1 AND requested.id=ANY($3::bigint[]) AND `+catalogScopeSQL+`
        AND (i.item_id IS NOT NULL OR $4)`, networkID, session, unique, plain)
	if err != nil {
		return result, err
	}
	items := make(map[protocol.Quantity]protocol.Item, len(unique))
	for rows.Next() {
		var item protocol.Item
		var known bool
		if err = rows.Scan(&item.ID, &item.CanonicalID, &item.Kind, &item.Name, &item.Damage, &item.Label, &item.Exact, &item.NBT,
			&item.Quantity, &item.Craftable, &item.ObservedAt, &item.CraftableObservedAt, &item.CurrentSession, &item.CraftableCurrentSession, &known); err != nil {
			rows.Close()
			return result, err
		}
		if plain {
			item.QuantityKnown = &known
		}
		items[item.ID] = item
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return result, err
	}
	refreshes, err := itemRefreshes(ctx, tx, networkID, unique)
	if err != nil {
		return result, err
	}
	for _, value := range unique {
		id := protocol.Quantity(value)
		if item, found := items[id]; found {
			result.Items = append(result.Items, item)
		} else {
			result.MissingIDs = append(result.MissingIDs, id)
		}
		if refresh, found := refreshes[id]; found {
			result.Refreshes = append(result.Refreshes, refresh)
		}
	}
	return result, tx.Commit(ctx)
}
