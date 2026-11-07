-- Scheduling changes must not maintain the quantity sorting index.
CREATE TABLE inventory_schedule (
    network_id text NOT NULL,
    item_id bigint NOT NULL,
    next_verify_at timestamptz NOT NULL,
    PRIMARY KEY(network_id,item_id),
    FOREIGN KEY(network_id,item_id) REFERENCES item_definitions(network_id,id) ON DELETE CASCADE
);
CREATE INDEX inventory_schedule_due ON inventory_schedule(network_id,next_verify_at,item_id);
INSERT INTO inventory_schedule SELECT network_id,item_id,next_verify_at FROM inventory;

-- Repack once during the transactional migration to leave space for evidence
-- updates. Values and evidence share one write; no timestamp is indexed.
CREATE TABLE inventory_current (
    network_id text NOT NULL,
    item_id bigint NOT NULL,
    quantity bigint NOT NULL CHECK(quantity>=0),
    craftable boolean NOT NULL,
    session_id text NOT NULL,
    observation bigint NOT NULL CHECK(observation>0),
    observed_at timestamptz NOT NULL,
    craftable_observation bigint NOT NULL CHECK(craftable_observation>=0),
    craftable_observed_at timestamptz,
    PRIMARY KEY(network_id,item_id),
    FOREIGN KEY(network_id,item_id) REFERENCES item_definitions(network_id,id) ON DELETE CASCADE
) WITH (fillfactor=80);
INSERT INTO inventory_current(network_id,item_id,quantity,craftable,session_id,observation,observed_at,craftable_observation)
-- Old recipe times are unknown; use the count watermark as a conservative floor.
SELECT network_id,item_id,quantity,craftable,session_id,observation,observed_at,observation FROM inventory;

ALTER TABLE inventory_rechecks DROP CONSTRAINT inventory_rechecks_network_id_item_id_fkey;
DROP TABLE inventory;
ALTER TABLE inventory_current RENAME TO inventory;
ALTER INDEX inventory_current_pkey RENAME TO inventory_pkey;
CREATE INDEX inventory_quantity ON inventory(network_id,quantity DESC,item_id);
ALTER TABLE inventory_rechecks ADD FOREIGN KEY(network_id,item_id)
    REFERENCES inventory(network_id,item_id) ON DELETE CASCADE;
