ALTER TABLE cooperative_rotation ADD COLUMN plain_quantity_turn integer NOT NULL DEFAULT 0
    CHECK(plain_quantity_turn BETWEEN 0 AND 3);

-- Change only future scheduling. Existing observations and discovery progress
-- stay intact when an already enabled network upgrades.
UPDATE inventory_schedule q SET next_quantity_at=CASE WHEN i.quantity>0
    THEN least(q.next_quantity_at,i.observed_at+interval '30 seconds')
    ELSE greatest(q.next_quantity_at,i.observed_at+interval '1 hour') END
FROM inventory i JOIN network_inventory_profiles p ON p.network_id=i.network_id
JOIN plain_catalog_candidates c ON c.network_id=i.network_id AND c.item_id=i.item_id
WHERE q.network_id=i.network_id AND q.item_id=i.item_id AND i.observed_at>=p.enabled_at;
