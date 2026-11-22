ALTER TABLE cooperative_rotation ADD COLUMN fluid_recipe_turn integer NOT NULL DEFAULT 0 CHECK(fluid_recipe_turn BETWEEN 0 AND 1);
-- Metadata schedules may await a primary that supports fluid recipes. No
-- observation, quantity, item epoch or existing item schedule is rewritten.
UPDATE inventory_schedule q SET next_verify_at=now()
FROM resource_catalog_candidates c JOIN item_definitions d ON d.network_id=c.network_id AND d.id=c.item_id
WHERE q.network_id=c.network_id AND q.item_id=c.item_id AND d.kind='fluid' AND q.next_verify_at='infinity';
