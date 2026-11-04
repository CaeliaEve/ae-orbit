CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

CREATE TABLE members (
    id text PRIMARY KEY,
    name text NOT NULL UNIQUE,
    token_hash bytea NOT NULL UNIQUE,
    is_admin boolean NOT NULL DEFAULT false,
    revoked boolean NOT NULL DEFAULT false
);
CREATE TABLE web_sessions (
    token_hash bytea PRIMARY KEY,
    member_id text NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    csrf_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE INDEX web_sessions_expiry ON web_sessions(expires_at);
CREATE TABLE server_groups (name text PRIMARY KEY);
CREATE TABLE networks (
    id text PRIMARY KEY,
    name text NOT NULL,
    server_group text NOT NULL REFERENCES server_groups(name),
    source text NOT NULL CHECK (source IN ('oc','simulator')),
    device_hash bytea NOT NULL UNIQUE,
    current_session text NOT NULL DEFAULT '',
    revision bigint NOT NULL DEFAULT 0,
    last_seen timestamptz,
    last_inventory timestamptz,
    last_scan timestamptz,
    capabilities jsonb NOT NULL DEFAULT '{}',
    health jsonb NOT NULL DEFAULT '{}'
);
CREATE TABLE network_grants (
    member_id text NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    network_id text NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    can_craft boolean NOT NULL DEFAULT false,
    PRIMARY KEY(member_id,network_id)
);
CREATE TABLE bridge_sessions (
    id text PRIMARY KEY,
    network_id text NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    boot_id text NOT NULL,
    retired boolean NOT NULL DEFAULT false,
    capabilities jsonb NOT NULL,
    response_bytes integer NOT NULL,
    inventory_seq bigint NOT NULL DEFAULT 0,
    inventory_hash bytea,
    inventory_response jsonb,
    poll_seq bigint NOT NULL DEFAULT 0,
    poll_hash bytea,
    poll_response jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(network_id,boot_id)
);
CREATE TABLE item_definitions (
    id bigserial PRIMARY KEY,
    network_id text NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    identity_key text NOT NULL,
    kind text NOT NULL,
    name text NOT NULL,
    damage integer NOT NULL,
    nbt text NOT NULL DEFAULT '',
    label text NOT NULL,
    exact boolean NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(network_id,identity_key),
    UNIQUE(network_id,id)
);
CREATE INDEX item_names ON item_definitions(network_id,lower(label),id);
CREATE INDEX item_label_prefix ON item_definitions(network_id,lower(label) text_pattern_ops);
CREATE INDEX item_registry_prefix ON item_definitions(network_id,lower(name) text_pattern_ops);
CREATE INDEX item_search ON item_definitions USING gin ((lower(label || ' ' || name)) gin_trgm_ops);
CREATE TABLE inventory (
    network_id text NOT NULL,
    item_id bigint NOT NULL,
    quantity bigint NOT NULL CHECK(quantity>=0),
    craftable boolean NOT NULL,
    session_id text NOT NULL,
    observation bigint NOT NULL CHECK(observation>0),
    observed_at timestamptz NOT NULL DEFAULT now(),
    next_verify_at timestamptz NOT NULL,
    PRIMARY KEY(network_id,item_id),
    FOREIGN KEY(network_id,item_id) REFERENCES item_definitions(network_id,id) ON DELETE CASCADE
);
CREATE INDEX inventory_quantity ON inventory(network_id,quantity DESC,item_id);
CREATE INDEX inventory_verify ON inventory(network_id,next_verify_at,item_id);
CREATE TABLE orders (
    id text PRIMARY KEY,
    network_id text NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    member_id text NOT NULL REFERENCES members(id),
    item_id bigint NOT NULL,
    quantity bigint NOT NULL CHECK(quantity>0),
    idempotency_key text NOT NULL,
    ttl_seconds integer NOT NULL,
    state text NOT NULL CHECK(state IN ('queued','assigned','computing','running','done','failed','canceled','expired','needs_review')),
    attempt_id text NOT NULL DEFAULT '',
    attempt_session text NOT NULL DEFAULT '',
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    UNIQUE(member_id,network_id,idempotency_key),
    FOREIGN KEY(network_id,item_id) REFERENCES item_definitions(network_id,id)
);
CREATE INDEX orders_queue ON orders(network_id,state,created_at,id);
CREATE INDEX orders_history ON orders(network_id,created_at DESC,id);
CREATE INDEX orders_expiry ON orders(state,expires_at);
CREATE TABLE order_events (
    id bigserial PRIMARY KEY,
    order_id text NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    state text NOT NULL,
    reason text NOT NULL DEFAULT '',
    attempt_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE cpu_snapshots (
    network_id text PRIMARY KEY REFERENCES networks(id) ON DELETE CASCADE,
    session_id text NOT NULL,
    cpus jsonb NOT NULL,
    truncated boolean NOT NULL,
    observed_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE network_events (
    id bigserial PRIMARY KEY,
    network_id text NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    kind text NOT NULL,
    revision bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX network_events_replay ON network_events(network_id,id);
