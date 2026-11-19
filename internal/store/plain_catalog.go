package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// All dispatch/admission paths use the same identity allowlist. Old observations
// and orders remain durable when a network switches to this profile.
const catalogScopeSQL = `(NOT EXISTS(SELECT 1 FROM network_inventory_profiles profile WHERE profile.network_id=d.network_id)
	OR (d.kind='item' AND d.exact AND d.nbt='' AND EXISTS(SELECT 1 FROM plain_catalog_candidates candidate
	WHERE candidate.network_id=d.network_id AND candidate.item_id=d.id)))`

const catalogObservedSQL = `(NOT EXISTS(SELECT 1 FROM network_inventory_profiles profile WHERE profile.network_id=d.network_id)
	OR EXISTS(SELECT 1 FROM network_inventory_profiles profile WHERE profile.network_id=d.network_id AND i.observed_at>=profile.enabled_at))`

func plainProfile(ctx context.Context, tx pgx.Tx, network string) (bool, error) {
	var enabled bool
	err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM network_inventory_profiles WHERE network_id=$1)", network).Scan(&enabled)
	return enabled, err
}

// EnablePlainCatalog is idempotent. It registers only definitions and schedules;
// in particular it does not insert zero quantities into inventory.
func (s *Store) EnablePlainCatalog(ctx context.Context, adminID, network string) (InventoryStatistics, error) {
	if err := s.RequireAdmin(ctx, adminID); err != nil {
		return InventoryStatistics{}, err
	}
	candidates := s.catalog.PlainCandidates()
	if len(candidates) == 0 {
		return InventoryStatistics{}, ErrCapability
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var mode string
		if err := tx.QueryRow(ctx, "SELECT bridge_mode FROM networks WHERE id=$1 FOR UPDATE", network).Scan(&mode); err != nil {
			return notFound(err)
		}
		if mode != "cooperative" {
			return ErrClientUpgradeRequired
		}
		enabled, err := plainProfile(ctx, tx, network)
		if err != nil || enabled {
			return err
		}
		// Index and foreign-key maintenance for a complete export can exceed
		// the ordinary statement budget. This override ends with the transaction.
		if _, err = tx.Exec(ctx, "SET LOCAL statement_timeout='30000'"); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO network_inventory_profiles(network_id,mode) VALUES($1,'nesql_plain')", network); err != nil {
			return err
		}
		// COPY avoids repeated identity resolution and round trips for large
		// exports. The temporary relation and activation remain one transaction.
		if _, err = tx.Exec(ctx, `CREATE TEMP TABLE plain_catalog_import(identity_key text,name text,damage int,label text,search_text text) ON COMMIT DROP`); err != nil {
			return fmt.Errorf("create plain catalog import: %w", err)
		}
		rows := make([][]any, len(candidates))
		searches := make(map[string]string)
		for i, d := range candidates {
			search, ok := searches[d.Label]
			if !ok {
				search = searchLabel(d.Label)
				searches[d.Label] = search
			}
			rows[i] = []any{d.Key(), d.Name, d.Damage, d.Label, search}
		}
		if _, err = tx.CopyFrom(ctx, pgx.Identifier{"plain_catalog_import"}, []string{"identity_key", "name", "damage", "label", "search_text"}, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("copy plain catalog metadata: %w", err)
		}
		// Temporary tables have no autovacuum statistics. Refresh the estimates
		// before joining, including definitions expanded by this transaction.
		if _, err = tx.Exec(ctx, "ANALYZE plain_catalog_import"); err != nil {
			return fmt.Errorf("analyze plain catalog import: %w", err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO item_definitions(network_id,identity_key,kind,name,damage,nbt,label,exact,search_label)
			SELECT $1,identity_key,'item',name,damage,'',label,true,search_text FROM plain_catalog_import
			ON CONFLICT(network_id,identity_key) DO NOTHING`, network); err != nil {
			return fmt.Errorf("register plain catalog definitions: %w", err)
		}
		if _, err = tx.Exec(ctx, "ANALYZE item_definitions"); err != nil {
			return fmt.Errorf("analyze plain catalog definitions: %w", err)
		}
		inserted, err := tx.Exec(ctx, `INSERT INTO plain_catalog_candidates(network_id,item_id,label,search_text)
			SELECT $1,d.id,x.label,x.search_text FROM plain_catalog_import x
			JOIN item_definitions requested ON requested.network_id=$1 AND requested.identity_key=x.identity_key
			LEFT JOIN item_aliases a ON a.network_id=requested.network_id AND a.alias_id=requested.id
			JOIN item_definitions d ON d.network_id=requested.network_id AND d.id=coalesce(a.canonical_id,requested.id)
			WHERE d.kind='item' AND d.exact AND d.nbt=''`, network)
		if err != nil {
			return fmt.Errorf("resolve plain catalog candidates: %w", err)
		}
		if inserted.RowsAffected() != int64(len(candidates)) {
			return ErrConflict
		}
		if _, err = tx.Exec(ctx, "ANALYZE plain_catalog_candidates"); err != nil {
			return fmt.Errorf("analyze plain catalog candidates: %w", err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO inventory_schedule(network_id,item_id,next_quantity_at,next_verify_at)
			SELECT network_id,item_id,now(),now() FROM plain_catalog_candidates WHERE network_id=$1
			ON CONFLICT(network_id,item_id) DO UPDATE SET next_quantity_at=now(),next_verify_at=now()`, network); err != nil {
			return fmt.Errorf("schedule plain catalog queries: %w", err)
		}
		// Cancel only outstanding read receipts. Existing order/job history stays.
		if _, err = tx.Exec(ctx, "UPDATE cooperative_read_fields SET completed_at=now() WHERE network_id=$1 AND completed_at IS NULL", network); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE networks SET revision=revision+1,catalog_revision=catalog_revision+1 WHERE id=$1", network); err != nil {
			return err
		}
		return s.event(ctx, tx, network, "inventory")
	})
	if err != nil {
		return InventoryStatistics{}, err
	}
	return s.InventoryStats(ctx, adminID, network)
}
