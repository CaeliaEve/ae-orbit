ALTER TABLE monitor_collectors ADD COLUMN purpose text NOT NULL DEFAULT 'lsc' CHECK(purpose IN ('lsc','subnet'));
CREATE TABLE subnet_plugins (
 server_group text PRIMARY KEY REFERENCES server_groups(name), enabled boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL DEFAULT 1
);
CREATE TABLE subnet_catalog (
 id text PRIMARY KEY,kind text NOT NULL CHECK(kind IN ('item','fluid')),name text NOT NULL,
 damage int NOT NULL,nbt text NOT NULL DEFAULT '',label text NOT NULL,search_label text NOT NULL DEFAULT ''
);
CREATE TABLE monitored_subnets (
 id text PRIMARY KEY,server_group text NOT NULL REFERENCES subnet_plugins(server_group),
 collector_id text NOT NULL UNIQUE REFERENCES monitor_collectors(id),name text NOT NULL,
 component_address text NOT NULL DEFAULT '',location text NOT NULL DEFAULT '',enabled boolean NOT NULL DEFAULT true,
 revision bigint NOT NULL DEFAULT 1,interval_seconds int NOT NULL DEFAULT 1 CHECK(interval_seconds BETWEEN 1 AND 3600),
 cycle_millis bigint NOT NULL DEFAULT 0,
 history_seconds int NOT NULL DEFAULT 10 CHECK(history_seconds BETWEEN 5 AND 3600),
 stale_seconds int NOT NULL DEFAULT 30 CHECK(stale_seconds BETWEEN 10 AND 10800),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE subnet_items (
 subnet_id text NOT NULL REFERENCES monitored_subnets(id),item_id text NOT NULL REFERENCES subnet_catalog(id),
 enabled boolean NOT NULL DEFAULT true,
 next_read_at timestamptz NOT NULL DEFAULT now(),assignment_id text NOT NULL DEFAULT '',assignment_until timestamptz,
 assignment_session text NOT NULL DEFAULT '',assignment_revision bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(subnet_id,item_id)
);
CREATE TABLE subnet_read_intents (
 subnet_id text PRIMARY KEY REFERENCES monitored_subnets(id),session_id text NOT NULL,
 last_intent_at timestamptz NOT NULL DEFAULT now(),grant_id text NOT NULL DEFAULT '',grant_budget int NOT NULL DEFAULT 0,
 claimed boolean NOT NULL DEFAULT false,grant_until timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE subnet_latest (
 subnet_id text NOT NULL,item_id text NOT NULL,session_id text NOT NULL,
 quantity bigint,observed_at timestamptz,error text NOT NULL DEFAULT '',reported_at timestamptz NOT NULL DEFAULT now(),
 sample_millis bigint NOT NULL DEFAULT 0,last_recorded_at timestamptz,epoch bigint NOT NULL DEFAULT 1,
 revision bigint NOT NULL DEFAULT 0,
 FOREIGN KEY(subnet_id,item_id) REFERENCES subnet_items(subnet_id,item_id),PRIMARY KEY(subnet_id,item_id)
);
CREATE TABLE subnet_history (
 id bigserial PRIMARY KEY,subnet_id text NOT NULL,item_id text NOT NULL,session_id text NOT NULL,
 quantity bigint NOT NULL,epoch bigint NOT NULL,observed_at timestamptz NOT NULL,
 FOREIGN KEY(subnet_id,item_id) REFERENCES subnet_items(subnet_id,item_id)
);
CREATE INDEX subnet_history_item_time ON subnet_history(subnet_id,item_id,observed_at DESC);
CREATE TABLE subnet_rules (
 id text PRIMARY KEY,subnet_id text NOT NULL,item_id text NOT NULL,member_id text NOT NULL REFERENCES members(id),
 kind text NOT NULL CHECK(kind IN ('low','high','zero')),threshold bigint NOT NULL,recover bigint NOT NULL,
 duration_seconds int NOT NULL DEFAULT 3 CHECK(duration_seconds BETWEEN 0 AND 3600),enabled boolean NOT NULL DEFAULT true,
 notification jsonb,revision bigint NOT NULL DEFAULT 1,since timestamptz,
 FOREIGN KEY(subnet_id,item_id) REFERENCES subnet_items(subnet_id,item_id),UNIQUE(subnet_id,item_id,member_id,kind)
);
CREATE TABLE subnet_alerts (
 id text PRIMARY KEY,rule_id text NOT NULL REFERENCES subnet_rules(id),quantity bigint NOT NULL,
 state text NOT NULL CHECK(state IN ('active','resolved')),triggered_at timestamptz NOT NULL DEFAULT now(),
 resolved_at timestamptz,resolve_reason text NOT NULL DEFAULT ''
 ,notification_queued boolean NOT NULL DEFAULT false
);
CREATE UNIQUE INDEX subnet_alerts_active ON subnet_alerts(rule_id) WHERE state='active';
