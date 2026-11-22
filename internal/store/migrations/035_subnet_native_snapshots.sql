ALTER TABLE monitored_subnets ADD COLUMN discovery_mode text NOT NULL DEFAULT 'exact' CHECK(discovery_mode IN ('exact','native'));
ALTER TABLE monitor_sessions ADD COLUMN subnet_snapshot_capable boolean NOT NULL DEFAULT false;
CREATE TABLE subnet_native_snapshots (
 subnet_id text NOT NULL REFERENCES monitored_subnets(id),kind text NOT NULL CHECK(kind IN ('item','fluid')),
 next_read_at timestamptz NOT NULL DEFAULT now(),assignment_id text NOT NULL DEFAULT '',
 assignment_session text NOT NULL DEFAULT '',assignment_until timestamptz,revision bigint NOT NULL DEFAULT 0,
 error text NOT NULL DEFAULT '',observed_at timestamptz,item_count int NOT NULL DEFAULT 0,
 PRIMARY KEY(subnet_id,kind)
);
