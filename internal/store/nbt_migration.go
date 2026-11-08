package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

//go:embed nbt_migration.sql
var nbtMigration string

// Backfill inside the schema migration transaction. Only a bounded batch of
// compressed definitions is resident in Go; grouping and observation selection
// happen in PostgreSQL. Unsupported legacy tags keep their original identity.
func migrateNBTIdentity(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `LOCK TABLE networks IN EXCLUSIVE MODE;
        LOCK TABLE item_definitions,inventory,inventory_schedule,inventory_rechecks,orders,bridge_sessions IN SHARE ROW EXCLUSIVE MODE;
        CREATE TEMP TABLE orbit_nbt_keys(network_id text,id bigint PRIMARY KEY,identity_key text NOT NULL) ON COMMIT DROP`); err != nil {
		return err
	}
	type entry struct {
		NetworkID string `json:"network_id"`
		ID        int64  `json:"id"`
		Key       string `json:"identity_key"`
	}
	var after int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,network_id,kind,name,damage,nbt,label,exact FROM item_definitions
            WHERE id>$1 AND nbt<>'' ORDER BY id LIMIT 256`, after)
		if err != nil {
			return err
		}
		batch := make([]entry, 0, 256)
		found := false
		for rows.Next() {
			var e entry
			var d protocol.Definition
			if err = rows.Scan(&e.ID, &e.NetworkID, &d.Kind, &d.Name, &d.Damage, &d.NBT, &d.Label, &d.Exact); err != nil {
				rows.Close()
				return err
			}
			found = true
			after = e.ID
			if d.Validate() != nil {
				continue
			}
			e.Key = d.Key()
			batch = append(batch, e)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		if !found {
			break
		}
		if len(batch) > 0 {
			data, _ := json.Marshal(batch)
			if _, err = tx.Exec(ctx, `INSERT INTO orbit_nbt_keys SELECT network_id,id,identity_key
                FROM jsonb_to_recordset($1::jsonb) AS x(network_id text,id bigint,identity_key text)`, data); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(ctx, nbtMigration); err != nil {
		return fmt.Errorf("merge NBT identity: %w", err)
	}
	return nil
}

func canonicalItemID(ctx context.Context, tx pgx.Tx, networkID string, id protocol.Quantity) (protocol.Quantity, error) {
	var canonical protocol.Quantity
	err := tx.QueryRow(ctx, `SELECT coalesce((SELECT canonical_id FROM item_aliases WHERE network_id=$1 AND alias_id=$2),$2)`, networkID, int64(id)).Scan(&canonical)
	return canonical, err
}
