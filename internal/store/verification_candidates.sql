WITH ordinary AS MATERIALIZED (
    SELECT q.item_id,q.next_verify_at AS due
    FROM inventory_schedule q WHERE q.network_id=$1 AND q.next_verify_at<=now()
      AND NOT EXISTS(SELECT 1 FROM inventory_rechecks r WHERE r.network_id=q.network_id AND r.item_id=q.item_id)
    ORDER BY q.next_verify_at,q.item_id LIMIT $2
), evidence AS MATERIALIZED (
    SELECT q.item_id,
        CASE WHEN o.session_id=n.current_session AND o.craftable_observed_at IS NOT NULL
            THEN least(o.observed_at,o.craftable_observed_at)+$4::double precision*interval '1 second'
            ELSE NULL END AS fresh_until
    FROM ordinary q JOIN inventory o ON o.network_id=$1 AND o.item_id=q.item_id
    JOIN networks n ON n.id=$1
), renewed AS (
    UPDATE inventory_schedule q SET next_verify_at=e.fresh_until FROM evidence e
    WHERE q.network_id=$1 AND q.item_id=e.item_id AND e.fresh_until>now()
    RETURNING q.item_id
), due AS (
    (SELECT r.item_id,r.token,r.next_attempt_at AS due,true AS priority
     FROM inventory_rechecks r WHERE r.network_id=$1 AND r.next_attempt_at<=now()
     ORDER BY r.next_attempt_at,r.item_id LIMIT 4)
    UNION ALL
    (SELECT q.item_id,'' AS token,q.due,false AS priority FROM ordinary q JOIN evidence e USING(item_id)
     WHERE e.fresh_until IS NULL OR e.fresh_until<=now()
     ORDER BY q.due,q.item_id LIMIT $3)
)
SELECT d.id,d.kind,d.name,d.damage,d.nbt,d.label,d.exact,i.craftable,due.token
FROM due JOIN item_definitions d ON d.network_id=$1 AND d.id=due.item_id
JOIN inventory i ON i.network_id=$1 AND i.item_id=due.item_id
ORDER BY due.priority DESC,due.due,due.item_id LIMIT 16
