-- One scalar per session preserves provenance even when another device
-- replaces an item's latest source. New boots begin at zero.
ALTER TABLE bridge_sessions
    ADD COLUMN observation_watermark bigint NOT NULL DEFAULT 0 CHECK(observation_watermark>=0);

-- Preserve all available evidence without changing values, timestamps or ACKs.
-- Historical observations already overwritten by another source are unknown.
WITH observations AS (
    SELECT network_id,session_id,observation FROM inventory
    UNION ALL
    SELECT network_id,craftable_session_id,craftable_observation FROM inventory
    UNION ALL
    SELECT network_id,discovery_session_id,discovery_observation FROM item_definitions
), maxima AS (
    SELECT network_id,session_id,max(observation) AS observation
    FROM observations WHERE session_id<>'' GROUP BY network_id,session_id
)
UPDATE bridge_sessions s SET observation_watermark=m.observation
FROM maxima m WHERE s.network_id=m.network_id AND s.id=m.session_id;
