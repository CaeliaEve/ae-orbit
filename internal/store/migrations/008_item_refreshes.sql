CREATE TABLE inventory_refreshes (
    network_id text NOT NULL,
    item_id bigint NOT NULL,
    token text NOT NULL CHECK(token ~ '^[0-9a-f]{32}$'),
    requested_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT now()+interval '60 seconds',
    confirmed_at timestamptz,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    dispatched_session text NOT NULL DEFAULT '',
    PRIMARY KEY(network_id,item_id),
    FOREIGN KEY(network_id,item_id) REFERENCES inventory(network_id,item_id) ON DELETE CASCADE,
    CHECK(expires_at>requested_at)
);

CREATE INDEX inventory_refreshes_due ON inventory_refreshes(network_id,next_attempt_at,item_id)
    WHERE confirmed_at IS NULL;
