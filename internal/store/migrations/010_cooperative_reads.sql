ALTER TABLE inventory
    ADD COLUMN craftable_session_id text NOT NULL DEFAULT '',
    ADD COLUMN quantity_generation bigint NOT NULL DEFAULT 0,
    ADD COLUMN craftable_generation bigint NOT NULL DEFAULT 0;
UPDATE inventory SET craftable_session_id=session_id;

ALTER TABLE item_definitions
    ADD COLUMN discovery_session_id text NOT NULL DEFAULT '',
    ADD COLUMN discovery_observation bigint NOT NULL DEFAULT 0;
ALTER TABLE inventory_schedule
    ADD COLUMN next_quantity_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX inventory_schedule_quantity ON inventory_schedule(network_id,next_quantity_at,item_id);

-- Only the latest attempt for each field survives; retired attempts do not
-- accumulate a second history proportional to the number of reads.
CREATE TABLE cooperative_read_fields (
    network_id text NOT NULL,
    item_id bigint NOT NULL,
    field text NOT NULL CHECK(field IN ('quantity','recipe')),
    generation bigint NOT NULL CHECK(generation>0),
    assignment_id text NOT NULL CHECK(assignment_id ~ '^[0-9a-f]{32}$'),
    device_id text NOT NULL,
    session_id text NOT NULL,
    expires_at timestamptz NOT NULL,
    purpose text NOT NULL CHECK(purpose IN ('order','refresh','background')),
    request_id text NOT NULL DEFAULT '',
    dispatched_at timestamptz NOT NULL DEFAULT now(),
    observation_floor bigint NOT NULL DEFAULT 0,
    completed_at timestamptz,
    PRIMARY KEY(network_id,item_id,field),
    FOREIGN KEY(network_id,item_id) REFERENCES item_definitions(network_id,id) ON DELETE CASCADE,
    FOREIGN KEY(network_id,device_id) REFERENCES devices(network_id,id) ON DELETE CASCADE
);
CREATE INDEX cooperative_read_owner ON cooperative_read_fields(device_id,session_id,expires_at)
    WHERE completed_at IS NULL;
CREATE INDEX cooperative_read_active ON cooperative_read_fields(network_id,expires_at,device_id,session_id)
    WHERE field='quantity' AND completed_at IS NULL;

CREATE TABLE cooperative_rotation (
    network_id text PRIMARY KEY REFERENCES networks(id) ON DELETE CASCADE,
    opportunity integer NOT NULL DEFAULT 0,
    background_turn integer NOT NULL DEFAULT 0,
    device_cursor text NOT NULL DEFAULT ''
);

-- Receipts authenticate retired tokens too, so stale immutable batches can
-- be ACKed without accepting arbitrary forged task identifiers.
CREATE TABLE cooperative_secrets (
    singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
    signing_key bytea NOT NULL CHECK(octet_length(signing_key)=32)
);

CREATE TABLE acquisition_rounds (
    server_group text PRIMARY KEY REFERENCES server_groups(name) ON DELETE CASCADE,
    network_cursor text NOT NULL DEFAULT ''
);
CREATE TABLE acquisition_intents (
    device_id text PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    session_id text NOT NULL,
    slots integer NOT NULL DEFAULT 0,
    bytes integer NOT NULL DEFAULT 0,
    last_intent_at timestamptz NOT NULL DEFAULT now(),
    grant_id text NOT NULL DEFAULT '',
    grant_budget integer NOT NULL DEFAULT 0,
    grant_until timestamptz NOT NULL DEFAULT '-infinity',
    claimed boolean NOT NULL DEFAULT false
);
