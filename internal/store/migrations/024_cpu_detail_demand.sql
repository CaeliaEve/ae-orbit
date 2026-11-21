-- One CPU demand per network; viewers renew short leases, not a background scan.
CREATE TABLE cpu_detail_demands (
 network_id text PRIMARY KEY REFERENCES networks(id), session_id text NOT NULL,
 cpu_id text NOT NULL, cpu_name text NOT NULL, cpu jsonb NOT NULL,
 category integer NOT NULL DEFAULT 0, next_read_at timestamptz NOT NULL DEFAULT '-infinity',
 assignment_id text NOT NULL DEFAULT '', assignment_kind text NOT NULL DEFAULT '',
 assignment_expires timestamptz NOT NULL DEFAULT '-infinity',
 last_result text NOT NULL DEFAULT '', updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE cpu_detail_viewers (
 network_id text NOT NULL REFERENCES networks(id), member_id text NOT NULL REFERENCES members(id),
 viewer_id text NOT NULL, expires_at timestamptz NOT NULL,
 PRIMARY KEY(network_id,member_id,viewer_id)
);
CREATE TABLE cpu_detail_snapshots (
 network_id text NOT NULL REFERENCES networks(id), session_id text NOT NULL,cpu_id text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('active','pending','stored')),items jsonb NOT NULL DEFAULT '[]',
 total integer NOT NULL DEFAULT 0,truncated boolean NOT NULL DEFAULT false,
 error text NOT NULL DEFAULT '',observed_at timestamptz,reported_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(network_id,kind)
);
