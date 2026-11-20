package store

import (
	"context"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/jackc/pgx/v5"
)

// Resource inventory support alone never authorizes recipe reads or commands.
// Fluid crafting is negotiated by the primary for each boot.
func craftSupportedSQL(caps protocol.Capabilities) string {
	if caps.Craft && caps.Fluids && caps.CraftFluids {
		return `(` + exactItemSQL(caps) + ` OR (d.kind='fluid' AND d.exact AND d.damage=0 AND d.nbt=''
		 AND EXISTS(SELECT 1 FROM resource_catalog_candidates fluid WHERE fluid.network_id=d.network_id AND fluid.item_id=d.id)))`
	}
	return exactItemSQL(caps)
}

func (s *Store) fluidRecipeCandidate(ctx context.Context, tx pgx.Tx, d Device, caps protocol.Capabilities) ([]cooperativeCandidate, error) {
	if d.Role != "primary" || !caps.CraftFluids || !caps.Fluids || !caps.Craft {
		return nil, nil
	}
	var pending bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cooperative_read_fields f
	 WHERE f.network_id=$1 AND f.device_id=$2 AND f.field='recipe' AND f.completed_at IS NULL AND f.expires_at>now()
	 AND EXISTS(SELECT 1 FROM devices owner WHERE owner.id=f.device_id AND owner.current_session=f.session_id))`, d.NetworkID, d.ID).Scan(&pending); err != nil {
		return nil, err
	}
	if pending {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT d.id,d.kind,d.name,d.damage,d.nbt,c.label,d.exact,now()+interval '30 seconds'
	 FROM resource_catalog_candidates c JOIN item_definitions d ON d.network_id=c.network_id AND d.id=c.item_id
	 JOIN inventory_schedule q ON q.network_id=c.network_id AND q.item_id=c.item_id
	 JOIN network_inventory_profiles p ON p.network_id=c.network_id
	 JOIN inventory i ON i.network_id=c.network_id AND i.item_id=c.item_id AND i.observed_at>=p.enabled_at
	 WHERE c.network_id=$1 AND d.kind='fluid' AND d.exact AND d.nbt='' AND q.next_verify_at<=now()
	 AND `+noActiveCooperativeQuantity+` AND `+noActiveCooperativeRecipe+`
	 AND NOT EXISTS(SELECT 1 FROM inventory_refreshes hot WHERE hot.network_id=q.network_id AND hot.item_id=q.item_id AND hot.confirmed_at IS NULL AND hot.expires_at>now())
	 AND NOT EXISTS(SELECT 1 FROM inventory_rechecks urgent WHERE urgent.network_id=q.network_id AND urgent.item_id=q.item_id)
	 ORDER BY q.next_verify_at,q.item_id LIMIT 1`, d.NetworkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []cooperativeCandidate{}
	for rows.Next() {
		c := cooperativeCandidate{NextQuantityTurn: -1, NextResourceTurn: -1, NextFluidRecipeTurn: -1}
		c.Kind, c.Purpose = "quantity_recipe", "background"
		if err = rows.Scan(&c.ID, &c.Item.Kind, &c.Item.Name, &c.Item.Damage, &c.Item.NBT, &c.Item.Label, &c.Item.Exact, &c.ExpiresAt); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
