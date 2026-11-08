CREATE INDEX ON orbit_nbt_keys(network_id,identity_key,id);
ANALYZE orbit_nbt_keys;
CREATE TEMP TABLE orbit_nbt_groups ON COMMIT DROP AS
SELECT network_id,identity_key,min(id) AS canonical_id,count(*) AS members
FROM orbit_nbt_keys GROUP BY network_id,identity_key;
CREATE UNIQUE INDEX ON orbit_nbt_groups(network_id,identity_key);
ANALYZE orbit_nbt_groups;

INSERT INTO item_aliases(network_id,alias_id,canonical_id)
SELECT k.network_id,k.id,g.canonical_id FROM orbit_nbt_keys k
JOIN orbit_nbt_groups g USING(network_id,identity_key) WHERE k.id<>g.canonical_id;

-- Retain legacy definition rows for historical foreign keys. Free any current
-- canonical key held by a younger duplicate before assigning the oldest ID.
UPDATE item_definitions d SET identity_key='alias:'||d.id::text
FROM item_aliases a WHERE a.network_id=d.network_id AND a.alias_id=d.id;
UPDATE item_definitions d SET identity_key=g.identity_key
FROM orbit_nbt_groups g WHERE d.network_id=g.network_id AND d.id=g.canonical_id;

CREATE TEMP TABLE orbit_nbt_merged ON COMMIT DROP AS
SELECT g.network_id,g.canonical_id,q.quantity,q.session_id,q.observation,q.observed_at,
       r.craftable,r.craftable_observation,r.craftable_observed_at,q.nbt,q.label
FROM orbit_nbt_groups g
JOIN LATERAL (
    SELECT i.*,d.nbt,d.label
    FROM orbit_nbt_keys k JOIN inventory i ON i.network_id=k.network_id AND i.item_id=k.id
    JOIN item_definitions d ON d.network_id=k.network_id AND d.id=k.id
    JOIN networks n ON n.id=k.network_id LEFT JOIN bridge_sessions b ON b.id=i.session_id
    WHERE k.network_id=g.network_id AND k.identity_key=g.identity_key
    ORDER BY (i.session_id=n.current_session) DESC,coalesce(b.created_at,i.observed_at) DESC,
             i.session_id,i.observation DESC,i.observed_at DESC,i.item_id
    LIMIT 1
) q ON true
JOIN LATERAL (
    SELECT i.craftable,i.craftable_observation,i.craftable_observed_at
    FROM orbit_nbt_keys k JOIN inventory i ON i.network_id=k.network_id AND i.item_id=k.id
    WHERE k.network_id=g.network_id AND k.identity_key=g.identity_key AND i.session_id=q.session_id
    ORDER BY i.craftable_observation DESC,i.craftable_observed_at DESC NULLS LAST,i.item_id
    LIMIT 1
) r ON true
WHERE g.members>1;

INSERT INTO inventory(network_id,item_id,quantity,craftable,session_id,observation,observed_at,craftable_observation,craftable_observed_at)
SELECT network_id,canonical_id,quantity,craftable,session_id,observation,observed_at,craftable_observation,craftable_observed_at
FROM orbit_nbt_merged
ON CONFLICT(network_id,item_id) DO UPDATE SET quantity=excluded.quantity,craftable=excluded.craftable,
    session_id=excluded.session_id,observation=excluded.observation,observed_at=excluded.observed_at,
    craftable_observation=excluded.craftable_observation,craftable_observed_at=excluded.craftable_observed_at;
UPDATE item_definitions d SET nbt=m.nbt,label=m.label
FROM orbit_nbt_merged m WHERE d.network_id=m.network_id AND d.id=m.canonical_id;

INSERT INTO inventory_schedule(network_id,item_id,next_verify_at)
SELECT g.network_id,g.canonical_id,min(q.next_verify_at)
FROM orbit_nbt_groups g JOIN orbit_nbt_keys k USING(network_id,identity_key)
JOIN inventory_schedule q ON q.network_id=k.network_id AND q.item_id=k.id
WHERE g.members>1 GROUP BY g.network_id,g.canonical_id
ON CONFLICT(network_id,item_id) DO UPDATE SET next_verify_at=excluded.next_verify_at;

-- Coalescing priority requests keeps the earliest deadline. Retain one token;
-- an in-flight result with another token cannot clear this outstanding work.
INSERT INTO inventory_rechecks(network_id,item_id,token,next_attempt_at)
SELECT network_id,canonical_id,token,due FROM (
    SELECT g.network_id,g.canonical_id,r.token,
        min(r.next_attempt_at) OVER(PARTITION BY g.network_id,g.canonical_id) AS due,
        row_number() OVER(PARTITION BY g.network_id,g.canonical_id ORDER BY r.next_attempt_at DESC,r.item_id) AS rank
    FROM orbit_nbt_groups g JOIN orbit_nbt_keys k USING(network_id,identity_key)
    JOIN inventory_rechecks r ON r.network_id=k.network_id AND r.item_id=k.id WHERE g.members>1
) chosen WHERE rank=1
ON CONFLICT(network_id,item_id) DO UPDATE SET token=excluded.token,next_attempt_at=excluded.next_attempt_at;

DELETE FROM inventory_schedule q USING item_aliases a WHERE q.network_id=a.network_id AND q.item_id=a.alias_id;
DELETE FROM inventory i USING item_aliases a WHERE i.network_id=a.network_id AND i.item_id=a.alias_id;

WITH changed AS (
    UPDATE networks n SET revision=revision+1,catalog_revision=catalog_revision+1,craftable_revision=craftable_revision+1
    WHERE EXISTS(SELECT 1 FROM item_aliases a WHERE a.network_id=n.id) RETURNING id,revision
)
INSERT INTO network_events(network_id,kind,revision) SELECT id,'inventory',revision FROM changed;
