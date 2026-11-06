-- A completed craft schedules a read, never an arithmetic stock adjustment.
-- Coalesce work by item; retain it across polls and bridge/backend restarts.
CREATE TABLE inventory_rechecks (
    network_id text NOT NULL,
    item_id bigint NOT NULL,
    token text NOT NULL,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(network_id,item_id),
    FOREIGN KEY(network_id,item_id) REFERENCES inventory(network_id,item_id) ON DELETE CASCADE
);
CREATE INDEX inventory_rechecks_due ON inventory_rechecks(network_id,next_attempt_at,item_id);

-- Recover outputs from the previous version that have never been observed
-- since completion. This queues only reads, including after offline upgrades.
INSERT INTO inventory_rechecks(network_id,item_id,token)
SELECT DISTINCT ON (o.network_id,o.item_id) o.network_id,o.item_id,o.id
FROM orders o JOIN inventory i ON i.network_id=o.network_id AND i.item_id=o.item_id
WHERE o.state IN ('done','failed','canceled') AND o.attempt_id<>'' AND i.observed_at<o.updated_at
ORDER BY o.network_id,o.item_id,o.updated_at DESC,o.id;
