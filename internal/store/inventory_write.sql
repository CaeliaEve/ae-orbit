WITH input AS (
    SELECT * FROM jsonb_to_recordset($2::jsonb)
    AS x(id bigint,quantity bigint,observation bigint,craftable boolean,craftable_observation bigint,label text,nbt text,verification text,verification_observation bigint,refresh text,refresh_observation bigint)
), previous AS (
    SELECT x.*,i.quantity AS previous_quantity,i.craftable AS previous_craftable,
        i.session_id AS previous_session,i.observation AS previous_observation,
        i.observed_at AS previous_time,i.craftable_observation AS previous_recipe_observation,
        i.craftable_observed_at AS previous_recipe_time,d.label AS previous_label,
        (i.item_id IS NULL OR i.session_id<>$3 OR x.observation>i.observation) AS accept_quantity,
        (x.craftable IS NOT NULL AND (i.item_id IS NULL OR i.session_id<>$3
            OR x.craftable_observation>i.craftable_observation)) AS accept_recipe
    FROM input x JOIN item_definitions d ON d.network_id=$1 AND d.id=x.id
    LEFT JOIN inventory i ON i.network_id=$1 AND i.item_id=x.id
), eligible AS MATERIALIZED (
    SELECT id,
        CASE WHEN accept_quantity THEN quantity ELSE previous_quantity END AS quantity,
        CASE WHEN accept_recipe THEN craftable WHEN previous_session=$3 THEN previous_craftable ELSE false END AS craftable,
        CASE WHEN accept_quantity THEN observation ELSE previous_observation END AS observation,
        CASE WHEN accept_quantity THEN now() ELSE previous_time END AS observed_at,
        CASE WHEN accept_recipe THEN craftable_observation WHEN previous_session=$3 THEN previous_recipe_observation ELSE 0 END AS craftable_observation,
        CASE WHEN accept_recipe THEN now() WHEN previous_session=$3 THEN previous_recipe_time ELSE NULL END AS craftable_observed_at,
        CASE WHEN accept_quantity THEN label ELSE NULL END AS label,
        CASE WHEN accept_quantity THEN nbt ELSE NULL END AS nbt,
        verification,verification_observation,refresh,refresh_observation,previous_quantity,previous_craftable,
        previous_session,previous_observation,previous_label
    FROM previous WHERE accept_quantity OR accept_recipe
), values_written AS (
    INSERT INTO inventory(network_id,item_id,quantity,craftable,session_id,observation,observed_at,craftable_observation,craftable_observed_at)
    SELECT $1,id,quantity,craftable,$3,observation,observed_at,craftable_observation,craftable_observed_at FROM eligible
    WHERE previous_quantity IS DISTINCT FROM quantity OR previous_craftable IS DISTINCT FROM craftable
       OR previous_session IS DISTINCT FROM $3
    ON CONFLICT(network_id,item_id) DO UPDATE SET quantity=excluded.quantity,craftable=excluded.craftable,
        session_id=excluded.session_id,observation=excluded.observation,observed_at=excluded.observed_at,
        craftable_observation=excluded.craftable_observation,craftable_observed_at=excluded.craftable_observed_at
    RETURNING item_id
), evidence_written AS (
    UPDATE inventory i SET observation=e.observation,observed_at=e.observed_at,
        craftable_observation=e.craftable_observation,craftable_observed_at=e.craftable_observed_at
    FROM eligible e WHERE i.network_id=$1 AND i.item_id=e.id
      AND e.previous_quantity IS NOT DISTINCT FROM e.quantity
      AND e.previous_craftable IS NOT DISTINCT FROM e.craftable AND e.previous_session=$3
    RETURNING i.item_id
), scheduled AS (
    INSERT INTO inventory_schedule(network_id,item_id,next_verify_at)
    SELECT $1,id,now()+$4::double precision*interval '1 second' FROM eligible WHERE previous_observation IS NULL
    ON CONFLICT(network_id,item_id) DO NOTHING
    RETURNING item_id
), labels AS (
    UPDATE item_definitions d SET label=coalesce(e.label,d.label),nbt=coalesce(e.nbt,d.nbt),updated_at=now() FROM eligible e
    WHERE d.network_id=$1 AND d.id=e.id AND ((e.label IS NOT NULL AND d.label IS DISTINCT FROM e.label)
       OR (e.nbt IS NOT NULL AND d.nbt IS DISTINCT FROM e.nbt))
    RETURNING d.id
), confirmed AS (
    DELETE FROM inventory_rechecks r USING eligible e
    WHERE r.network_id=$1 AND r.item_id=e.id AND r.token=e.verification AND e.verification_observation>0
      AND (e.previous_session IS DISTINCT FROM $3 OR e.verification_observation>e.previous_observation)
    RETURNING r.item_id
), refreshed AS (
    UPDATE inventory_refreshes r SET confirmed_at=now() FROM eligible e
    WHERE r.network_id=$1 AND r.item_id=e.id AND r.token=e.refresh AND e.refresh_observation>0
      AND r.dispatched_session=$3 AND r.confirmed_at IS NULL AND r.expires_at>now()
      AND (e.previous_session IS DISTINCT FROM $3 OR e.refresh_observation>e.previous_observation)
    RETURNING r.item_id
)
SELECT (SELECT count(*) FROM values_written)+(SELECT count(*) FROM evidence_written),coalesce((SELECT bool_or(
    previous_quantity IS DISTINCT FROM quantity OR previous_craftable IS DISTINCT FROM craftable
    OR previous_session IS DISTINCT FROM $3 OR (label IS NOT NULL AND previous_label IS DISTINCT FROM label)
) FROM eligible),false),
coalesce((SELECT bool_or(previous_observation IS NULL
    OR (label IS NOT NULL AND previous_label IS DISTINCT FROM label)) FROM eligible),false),
coalesce((SELECT bool_or(previous_craftable IS DISTINCT FROM craftable) FROM eligible),false),
EXISTS(SELECT 1 FROM refreshed)
