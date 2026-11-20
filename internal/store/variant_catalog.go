package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

func exactItemSQL(caps protocol.Capabilities) string {
	if !caps.ExactNBT || caps.MaxNBTBytes < 1 {
		return "(d.kind='item' AND (d.nbt='' OR NOT EXISTS(SELECT 1 FROM network_inventory_profiles p WHERE p.network_id=d.network_id)))"
	}
	return fmt.Sprintf("(d.kind='item' AND (octet_length(d.nbt)/4*3 - CASE WHEN right(d.nbt,2)='==' THEN 2 WHEN right(d.nbt,1)='=' THEN 1 ELSE 0 END)<=%d)", caps.MaxNBTBytes)
}

func (s *Store) EnableVariantCatalog(ctx context.Context, admin, network string) (InventoryStatistics, error) {
	if err := s.RequireAdmin(ctx, admin); err != nil {
		return InventoryStatistics{}, err
	}
	definitions, err := s.catalog.VariantCandidates()
	if err != nil {
		return InventoryStatistics{}, err
	}
	if len(definitions) == 0 {
		return s.InventoryStats(ctx, admin, network)
	}
	err = s.transaction(ctx, func(tx pgx.Tx) error {
		var mode string
		if err := tx.QueryRow(ctx, "SELECT bridge_mode FROM networks WHERE id=$1 FOR UPDATE", network).Scan(&mode); err != nil {
			return err
		}
		plain, err := plainProfile(ctx, tx, network)
		if err != nil {
			return err
		}
		if !plain || mode != "cooperative" {
			return ErrClientUpgradeRequired
		}
		if _, err = tx.Exec(ctx, "SET LOCAL statement_timeout='60000'"); err != nil {
			return err
		}
		// Bounded batches keep binding/parameter limits small and preserve canonical
		// identity resolution across compressed encodings.
		for start := 0; start < len(definitions); start += 128 {
			end := min(start+128, len(definitions))
			updates := make([]protocol.Update, end-start)
			for i := range updates {
				updates[i].Item = &definitions[start+i]
			}
			ids, _, err := s.resolveDefinitions(ctx, tx, network, updates)
			if err != nil {
				return err
			}
			rows := make([][]any, len(ids))
			for i, id := range ids {
				d := definitions[start+i]
				rows[i] = []any{network, int64(id), d.Label, searchLabel(d.Label), "nesql"}
			}
			if _, err = tx.Exec(ctx, `CREATE TEMP TABLE IF NOT EXISTS variant_import(network_id text,item_id bigint,label text,search_text text,source text) ON COMMIT DROP`); err != nil {
				return err
			}
			if _, err = tx.CopyFrom(ctx, pgx.Identifier{"variant_import"}, []string{"network_id", "item_id", "label", "search_text", "source"}, pgx.CopyFromRows(rows)); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO variant_catalog_candidates SELECT network_id,item_id,label,search_text,source,now() FROM variant_import
 ON CONFLICT(network_id,item_id) DO UPDATE SET label=excluded.label,search_text=excluded.search_text WHERE variant_catalog_candidates.source='nesql'`); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO inventory_schedule(network_id,item_id,next_quantity_at,next_verify_at)
   SELECT network_id,item_id,now(),now() FROM variant_catalog_candidates WHERE network_id=$1 ON CONFLICT(network_id,item_id) DO NOTHING`, network); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "ANALYZE variant_catalog_candidates"); err != nil {
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
	return s.InventoryStats(ctx, admin, network)
}

// A sample registers identity, never its chest count as network inventory.
func (s *Store) RegisterVariantSample(ctx context.Context, device Device, session string, definition protocol.Definition) (protocol.Quantity, error) {
	if definition.Kind != "item" || definition.Validate() != nil {
		return 0, ErrInvalid
	}
	var id protocol.Quantity
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := lockDevice(ctx, tx, device, session, 2); err != nil {
			return err
		}
		var encoded []byte
		if err := tx.QueryRow(ctx, "SELECT capabilities FROM bridge_sessions WHERE id=$1 AND protocol=2", session).Scan(&encoded); err != nil {
			return err
		}
		var caps protocol.Capabilities
		if err := json.Unmarshal(encoded, &caps); err != nil {
			return err
		}
		if !caps.ExactNBT || !caps.NativeScanDisabled || !caps.EventsDisabled {
			return ErrCapability
		}
		tag, err := base64.StdEncoding.DecodeString(definition.NBT)
		if err != nil {
			return ErrInvalid
		}
		if len(tag) > caps.MaxNBTBytes {
			return ErrCapacity
		}
		plain, err := plainProfile(ctx, tx, device.NetworkID)
		if err != nil {
			return err
		}
		if !plain {
			return ErrCapability
		}
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM variant_catalog_candidates WHERE network_id=$1 AND source='sample'", device.NetworkID).Scan(&count); err != nil {
			return err
		}
		if count >= 4096 {
			return ErrCapacity
		}
		ids, _, err := s.resolveDefinitions(ctx, tx, device.NetworkID, []protocol.Update{{Item: &definition}})
		if err != nil {
			return err
		}
		id = ids[0]
		if _, err = tx.Exec(ctx, `INSERT INTO variant_catalog_candidates(network_id,item_id,label,search_text,source) VALUES($1,$2,$3,$4,'sample')
   ON CONFLICT(network_id,item_id) DO NOTHING`, device.NetworkID, int64(id), definition.Label, searchLabel(definition.Label)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO inventory_schedule(network_id,item_id,next_quantity_at,next_verify_at) VALUES($1,$2,now(),now())
   ON CONFLICT(network_id,item_id) DO UPDATE SET next_quantity_at=least(inventory_schedule.next_quantity_at,now())`, device.NetworkID, int64(id)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE networks SET revision=revision+1,catalog_revision=catalog_revision+1 WHERE id=$1", device.NetworkID); err != nil {
			return err
		}
		return s.event(ctx, tx, device.NetworkID, "inventory")
	})
	return id, err
}
