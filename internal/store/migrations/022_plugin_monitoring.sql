CREATE TABLE plugin_installations (
 server_group text NOT NULL REFERENCES server_groups(name), plugin_id text NOT NULL,
 enabled boolean NOT NULL DEFAULT false, settings jsonb NOT NULL,
 revision bigint NOT NULL DEFAULT 1, updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(server_group,plugin_id)
);
CREATE TABLE monitor_collectors (
 id text PRIMARY KEY,server_group text NOT NULL REFERENCES server_groups(name),name text NOT NULL,
 token_hash bytea NOT NULL UNIQUE,revoked boolean NOT NULL DEFAULT false,current_session text NOT NULL DEFAULT '',
 components jsonb NOT NULL DEFAULT '[]',modules jsonb NOT NULL DEFAULT '[]',
 last_seen timestamptz,free_memory bigint NOT NULL DEFAULT 0,created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(server_group,id)
);
CREATE TABLE monitor_sessions (
 id text PRIMARY KEY,collector_id text NOT NULL REFERENCES monitor_collectors(id),boot_id text NOT NULL,
 retired boolean NOT NULL DEFAULT false,sequence bigint NOT NULL DEFAULT 0,last_sample_millis bigint NOT NULL DEFAULT 0,last_sample_at timestamptz,
 receipt_hash bytea,receipt jsonb,created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(collector_id,boot_id)
);
CREATE TABLE plugin_objects (
 id text PRIMARY KEY,server_group text NOT NULL,plugin_id text NOT NULL,
 collector_id text NOT NULL,component_address text NOT NULL,name text NOT NULL,
 location text NOT NULL DEFAULT '',enabled boolean NOT NULL DEFAULT true,
 revision bigint NOT NULL DEFAULT 1,created_at timestamptz NOT NULL DEFAULT now(),alerts_checked_at timestamptz NOT NULL DEFAULT '-infinity',
 FOREIGN KEY(server_group,plugin_id) REFERENCES plugin_installations(server_group,plugin_id),
 FOREIGN KEY(server_group,collector_id) REFERENCES monitor_collectors(server_group,id),
 UNIQUE(collector_id,component_address)
);
CREATE UNIQUE INDEX plugin_objects_location ON plugin_objects(server_group,location) WHERE location<>'';
CREATE TABLE monitor_latest (
 object_id text PRIMARY KEY REFERENCES plugin_objects(id),session_id text NOT NULL,
 sample_millis bigint NOT NULL DEFAULT 0,stored_eu numeric(128,0),capacity_eu numeric(128,0),
 epoch bigint NOT NULL DEFAULT 1,observed_at timestamptz,error text NOT NULL DEFAULT '',
 reported_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE monitor_energy_records (
 id bigserial PRIMARY KEY,object_id text NOT NULL REFERENCES plugin_objects(id),epoch bigint NOT NULL,
 session_id text NOT NULL,stored_eu numeric(128,0) NOT NULL,capacity_eu numeric(128,0) NOT NULL,
 observed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX monitor_energy_records_object_time ON monitor_energy_records(object_id,observed_at,id);
CREATE TABLE monitor_energy_rollups (
 object_id text NOT NULL REFERENCES plugin_objects(id),epoch bigint NOT NULL,granularity text NOT NULL CHECK(granularity IN ('hour','day')),
 bucket timestamptz NOT NULL,capacity_eu numeric(128,0) NOT NULL,
 first_eu numeric(128,0) NOT NULL,last_eu numeric(128,0) NOT NULL,min_eu numeric(128,0) NOT NULL,max_eu numeric(128,0) NOT NULL,
 first_at timestamptz NOT NULL,last_at timestamptz NOT NULL,samples bigint NOT NULL DEFAULT 1,
 PRIMARY KEY(object_id,granularity,bucket,epoch)
);
CREATE TABLE monitor_conditions (
 object_id text NOT NULL REFERENCES plugin_objects(id),kind text NOT NULL,since timestamptz NOT NULL,
 PRIMARY KEY(object_id,kind)
);
CREATE TABLE monitor_alerts (
 id text PRIMARY KEY,object_id text NOT NULL REFERENCES plugin_objects(id),kind text NOT NULL,
 state text NOT NULL CHECK(state IN ('active','resolved')),detail text NOT NULL,
 triggered_at timestamptz NOT NULL DEFAULT now(),resolved_at timestamptz,resolve_reason text NOT NULL DEFAULT '',
 acknowledged_at timestamptz,acknowledged_by text REFERENCES members(id)
);
CREATE UNIQUE INDEX monitor_alerts_active ON monitor_alerts(object_id,kind) WHERE state='active';
CREATE INDEX monitor_alerts_object_time ON monitor_alerts(object_id,triggered_at DESC);
CREATE TABLE plugin_audit (
 id bigserial PRIMARY KEY,server_group text NOT NULL REFERENCES server_groups(name),member_id text REFERENCES members(id),
 action text NOT NULL,target_id text NOT NULL,created_at timestamptz NOT NULL DEFAULT now()
);
