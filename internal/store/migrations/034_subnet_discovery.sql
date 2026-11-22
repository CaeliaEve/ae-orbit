ALTER TABLE monitor_sessions ADD COLUMN subnet_discovery_capable boolean NOT NULL DEFAULT false;
ALTER TABLE subnet_catalog ADD COLUMN scan_order bigint;
WITH ordered AS (
 SELECT id,row_number() OVER(ORDER BY CASE WHEN kind='fluid' THEN 0 WHEN nbt='' THEN 1 ELSE 2 END,id) position FROM subnet_catalog
) UPDATE subnet_catalog d SET scan_order=o.position FROM ordered o WHERE o.id=d.id;
CREATE UNIQUE INDEX subnet_catalog_scan_order ON subnet_catalog(scan_order);

CREATE TABLE subnet_discovery (
 subnet_id text PRIMARY KEY REFERENCES monitored_subnets(id),revision bigint NOT NULL,
 cursor bigint NOT NULL DEFAULT 0,checked bigint NOT NULL DEFAULT 0,failed bigint NOT NULL DEFAULT 0,
 total bigint NOT NULL DEFAULT 0,round bigint NOT NULL DEFAULT 1,
 started_at timestamptz NOT NULL DEFAULT now(),progress_at timestamptz,completed_at timestamptz
);
CREATE TABLE subnet_discovery_reads (
 subnet_id text NOT NULL REFERENCES monitored_subnets(id),item_id text NOT NULL REFERENCES subnet_catalog(id),
 position bigint NOT NULL,assignment_id text NOT NULL DEFAULT '',assignment_session text NOT NULL DEFAULT '',
 assignment_until timestamptz,revision bigint NOT NULL,
 PRIMARY KEY(subnet_id,item_id)
);
CREATE INDEX subnet_discovery_reads_due ON subnet_discovery_reads(subnet_id,assignment_until);
CREATE TABLE subnet_discovered (
 subnet_id text NOT NULL REFERENCES monitored_subnets(id),item_id text NOT NULL REFERENCES subnet_catalog(id),
 revision bigint NOT NULL,session_id text NOT NULL,quantity bigint,observed_at timestamptz,
 error text NOT NULL DEFAULT '',next_read_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(subnet_id,item_id)
);
CREATE INDEX subnet_discovered_refresh ON subnet_discovered(subnet_id,next_read_at) WHERE quantity>0;
