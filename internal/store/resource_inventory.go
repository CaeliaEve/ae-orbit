package store

import (
	"context"
	"fmt"
	"time"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

// EnableResourceCatalog adds known resource identities without manufacturing
// observations or reopening native discovery. Existing item schedules stay intact.
func (s *Store) EnableResourceCatalog(ctx context.Context, admin, network string) error {
	if err := s.RequireAdmin(ctx, admin); err != nil {
		return err
	}
	candidates := s.catalog.ResourceCandidates()
	if len(candidates) == 0 {
		return ErrCapability
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		var mode string
		if err := tx.QueryRow(ctx, "SELECT bridge_mode FROM networks WHERE id=$1 FOR UPDATE", network).Scan(&mode); err != nil {
			return notFound(err)
		}
		enabled, err := plainProfile(ctx, tx, network)
		if err != nil {
			return err
		}
		if !enabled || mode != "cooperative" {
			return ErrClientUpgradeRequired
		}
		if _, err = tx.Exec(ctx, `CREATE TEMP TABLE resource_import(identity_key text,kind text,name text,label text,search_text text) ON COMMIT DROP`); err != nil {
			return err
		}
		rows := make([][]any, len(candidates))
		for i, c := range candidates {
			rows[i] = []any{c.Key(), c.Kind, c.Name, c.Label, searchLabel(c.Label)}
		}
		if _, err = tx.CopyFrom(ctx, pgx.Identifier{"resource_import"}, []string{"identity_key", "kind", "name", "label", "search_text"}, pgx.CopyFromRows(rows)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO item_definitions(network_id,identity_key,kind,name,damage,nbt,label,exact,search_label)
		 SELECT $1,identity_key,kind,name,0,'',label,true,search_text FROM resource_import
		 ON CONFLICT(network_id,identity_key) DO NOTHING`, network); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `CREATE TEMP TABLE resource_resolved ON COMMIT DROP AS
		 SELECT d.id,x.label,x.search_text FROM resource_import x JOIN item_definitions d ON d.network_id=$1 AND d.identity_key=x.identity_key
		 WHERE d.kind=x.kind AND d.nbt='' AND d.damage=0 AND d.exact`, network); err != nil {
			return err
		}
		var count int
		if err = tx.QueryRow(ctx, "SELECT count(DISTINCT id) FROM resource_resolved").Scan(&count); err != nil {
			return err
		}
		if count != len(candidates) {
			return ErrConflict
		}
		tag, err := tx.Exec(ctx, `INSERT INTO resource_catalog_candidates(network_id,item_id,label,search_text)
		 SELECT $1,id,label,search_text FROM resource_resolved
		 ON CONFLICT(network_id,item_id) DO UPDATE SET label=excluded.label,search_text=excluded.search_text
		 WHERE resource_catalog_candidates.label IS DISTINCT FROM excluded.label OR resource_catalog_candidates.search_text IS DISTINCT FROM excluded.search_text`, network)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO inventory_schedule(network_id,item_id,next_quantity_at,next_verify_at)
		 SELECT $1,id,now(),'infinity' FROM resource_resolved ON CONFLICT(network_id,item_id) DO NOTHING`, network); err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if _, err = tx.Exec(ctx, "UPDATE networks SET revision=revision+1,catalog_revision=catalog_revision+1 WHERE id=$1", network); err != nil {
			return err
		}
		return s.event(ctx, tx, network, "inventory")
	})
}

func resourceSupportedSQL(caps protocol.Capabilities) string {
	sql := "false"
	if caps.Fluids {
		sql += " OR d.kind='fluid'"
	}
	if caps.Essentia {
		sql += " OR d.kind='essentia'"
	}
	return "(" + sql + ")"
}

type ResourceStatistics struct {
	Kind            string     `json:"kind"`
	Unit            string     `json:"unit"`
	Candidates      string     `json:"candidates"`
	Checked         string     `json:"checked"`
	Entries         string     `json:"entries"`
	TotalQuantity   string     `json:"total_quantity"`
	LastObservation *time.Time `json:"last_observation,omitempty"`
	Supported       bool       `json:"supported"`
}

func (s *Store) resourceStats(ctx context.Context, network string) ([]ResourceStatistics, error) {
	rows, err := s.pool.Query(ctx, `SELECT d.kind,count(*)::text,count(i.item_id)::text,
	 count(*) FILTER(WHERE i.quantity>0)::text,coalesce(sum(i.quantity),0)::text,max(i.observed_at),
	 EXISTS(SELECT 1 FROM devices worker JOIN bridge_sessions bs ON bs.id=worker.current_session AND NOT bs.retired
	  WHERE worker.network_id=$1 AND NOT worker.revoked AND bs.protocol=2
	  AND bs.capabilities->>'exact_quantities'='true' AND bs.capabilities->>'native_scan_disabled'='true'
	  AND bs.capabilities->>'events_disabled'='true'
	  AND bs.capabilities->>(CASE d.kind WHEN 'fluid' THEN 'fluids' ELSE 'essentia' END)='true')
	 FROM resource_catalog_candidates c JOIN item_definitions d ON d.network_id=c.network_id AND d.id=c.item_id
	 JOIN network_inventory_profiles p ON p.network_id=c.network_id
	 LEFT JOIN inventory i ON i.network_id=c.network_id AND i.item_id=c.item_id AND i.observed_at>=p.enabled_at
	 WHERE c.network_id=$1 GROUP BY d.kind ORDER BY d.kind`, network)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ResourceStatistics{}
	for rows.Next() {
		var r ResourceStatistics
		if err = rows.Scan(&r.Kind, &r.Candidates, &r.Checked, &r.Entries, &r.TotalQuantity, &r.LastObservation, &r.Supported); err != nil {
			return nil, err
		}
		r.Unit = "源质"
		if r.Kind == "fluid" {
			r.Unit = "mB"
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *Store) resourceCandidates(ctx context.Context, tx pgx.Tx, d Device, caps protocol.Capabilities) ([]cooperativeCandidate, error) {
	if !caps.Fluids && !caps.Essentia {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT d.id,d.kind,d.name,d.damage,d.nbt,c.label,d.exact
	 FROM resource_catalog_candidates c JOIN item_definitions d ON d.network_id=c.network_id AND d.id=c.item_id
	 JOIN inventory_schedule q ON q.network_id=c.network_id AND q.item_id=c.item_id
	 WHERE c.network_id=$1 AND d.exact AND d.nbt='' AND q.next_quantity_at<=now() AND `+resourceSupportedSQL(caps)+`
	 AND `+noActiveCooperativeQuantity+`
	 AND NOT EXISTS(SELECT 1 FROM inventory_refreshes hot WHERE hot.network_id=q.network_id
	  AND hot.item_id=q.item_id AND hot.confirmed_at IS NULL AND hot.expires_at>now())
	 ORDER BY q.next_quantity_at,q.item_id LIMIT 16`, d.NetworkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []cooperativeCandidate{}
	for rows.Next() {
		c := cooperativeCandidate{NextQuantityTurn: -1, NextResourceTurn: -1}
		c.Kind, c.Purpose, c.ExpiresAt = "quantity", "background", time.Now().Add(30*time.Second)
		if err = rows.Scan(&c.ID, &c.Item.Kind, &c.Item.Name, &c.Item.Damage, &c.Item.NBT, &c.Item.Label, &c.Item.Exact); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (s *Store) mergeResourceCandidates(ctx context.Context, tx pgx.Tx, d Device, caps protocol.Capabilities, items []cooperativeCandidate, turn int) ([]cooperativeCandidate, error) {
	resources, err := s.resourceCandidates(ctx, tx, d, caps)
	if err != nil {
		return nil, fmt.Errorf("resource quantity candidates: %w", err)
	}
	if len(resources) == 0 {
		return items, nil
	}
	result := []cooperativeCandidate{}
	for len(items)+len(resources) > 0 {
		var c cooperativeCandidate
		if (turn == 3 || len(items) == 0) && len(resources) > 0 {
			c, resources = resources[0], resources[1:]
		} else {
			c, items = items[0], items[1:]
		}
		turn = (turn + 1) % 4
		c.NextResourceTurn = turn
		result = append(result, c)
	}
	return result, nil
}
