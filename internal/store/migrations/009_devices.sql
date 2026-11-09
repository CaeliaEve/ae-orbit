ALTER TABLE networks ADD COLUMN bridge_mode text NOT NULL DEFAULT 'single'
    CHECK (bridge_mode IN ('single','cooperative'));

CREATE TABLE devices (
    id text PRIMARY KEY,
    network_id text NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    name text NOT NULL,
    role text NOT NULL CHECK (role IN ('primary','quantity_worker')),
    token_hash bytea NOT NULL UNIQUE,
    revoked boolean NOT NULL DEFAULT false,
    current_session text NOT NULL DEFAULT '',
    capabilities jsonb NOT NULL DEFAULT '{}',
    health jsonb NOT NULL DEFAULT '{}',
    last_seen timestamptz,
    last_inventory timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(network_id,id)
);
CREATE UNIQUE INDEX devices_primary ON devices(network_id) WHERE role='primary';
CREATE INDEX devices_network ON devices(network_id,created_at,id);

INSERT INTO devices(id,network_id,name,role,token_hash,current_session,capabilities,health,last_seen,last_inventory)
SELECT md5('primary:' || id),id,name,'primary',device_hash,current_session,capabilities,health,last_seen,last_inventory
FROM networks;

ALTER TABLE bridge_sessions ADD COLUMN device_id text;
ALTER TABLE bridge_sessions ADD COLUMN protocol integer NOT NULL DEFAULT 1
    CHECK (protocol IN (1,2));
UPDATE bridge_sessions SET device_id=md5('primary:' || network_id);
ALTER TABLE bridge_sessions ALTER COLUMN device_id SET NOT NULL;
ALTER TABLE bridge_sessions ADD CONSTRAINT bridge_sessions_device
    FOREIGN KEY(network_id,device_id) REFERENCES devices(network_id,id);
ALTER TABLE bridge_sessions DROP CONSTRAINT bridge_sessions_network_id_boot_id_key;
ALTER TABLE bridge_sessions ADD CONSTRAINT bridge_sessions_device_boot UNIQUE(device_id,boot_id);
