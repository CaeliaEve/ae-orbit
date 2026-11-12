ALTER TABLE networks ADD COLUMN jobs_revision bigint NOT NULL DEFAULT 0 CHECK(jobs_revision>=0);
ALTER TABLE networks ADD COLUMN jobs_admission_cursor_created_at timestamptz;
ALTER TABLE networks ADD COLUMN jobs_admission_cursor_id text NOT NULL DEFAULT '';

CREATE TABLE production_job_authority_epochs (
    member_id text NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    network_id text NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    epoch bigint NOT NULL DEFAULT 0 CHECK(epoch>=0),
    PRIMARY KEY(member_id,network_id)
);
CREATE TABLE production_jobs (
    id text PRIMARY KEY,
    network_id text NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    member_id text NOT NULL REFERENCES members(id),
    name text NOT NULL DEFAULT '' CHECK(octet_length(name)<=128),
    manifest_digest text NOT NULL,
    ttl_seconds integer NOT NULL CHECK(ttl_seconds BETWEEN 1 AND 86400),
    control text NOT NULL DEFAULT 'active' CHECK(control IN ('active','paused','stopped')),
    control_version bigint NOT NULL DEFAULT 1 CHECK(control_version>0),
    version bigint NOT NULL DEFAULT 0 CHECK(version>=0),
    owner_authority_epoch bigint NOT NULL CHECK(owner_authority_epoch>=0),
    total integer NOT NULL CHECK(total BETWEEN 1 AND 500),
    counts jsonb NOT NULL DEFAULT '{}',
    reason text NOT NULL DEFAULT '',
    result text NOT NULL DEFAULT '' CHECK(result IN ('','completed','canceled','finished_with_issues')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    settled_at timestamptz,
    UNIQUE(network_id,id)
);
CREATE INDEX production_jobs_admission ON production_jobs(network_id,created_at,id) WHERE settled_at IS NULL;
CREATE INDEX production_jobs_owner ON production_jobs(network_id,member_id) WHERE settled_at IS NULL;
CREATE INDEX production_jobs_history ON production_jobs(network_id,settled_at DESC,id) WHERE settled_at IS NOT NULL;

CREATE TABLE production_job_entries (
    id text PRIMARY KEY,
    job_id text NOT NULL REFERENCES production_jobs(id) ON DELETE CASCADE,
    network_id text NOT NULL,
    entry_key text NOT NULL,
    ordinal integer NOT NULL CHECK(ordinal BETWEEN 0 AND 499),
    requested_item_id bigint NOT NULL CHECK(requested_item_id>0),
    item_id bigint NOT NULL CHECK(item_id>0),
    quantity bigint NOT NULL CHECK(quantity>0),
    state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','rejected','queued','assigned','computing','running','needs_review','done','failed','canceled','expired')),
    reason text NOT NULL DEFAULT '',
    order_id text NOT NULL DEFAULT '',
    attempt_id text NOT NULL DEFAULT '',
    terminal_at timestamptz,
    UNIQUE(job_id,ordinal),
    UNIQUE(job_id,entry_key),
    FOREIGN KEY(network_id,job_id) REFERENCES production_jobs(network_id,id) ON DELETE CASCADE
);
CREATE INDEX production_job_entries_pending ON production_job_entries(job_id,ordinal) WHERE state='pending';
CREATE UNIQUE INDEX production_job_entries_order ON production_job_entries(order_id) WHERE order_id<>'';
ALTER TABLE orders ADD COLUMN production_job_entry_id text UNIQUE REFERENCES production_job_entries(id) ON DELETE SET NULL;

CREATE TABLE production_job_receipts (
    member_id text NOT NULL REFERENCES members(id),
    network_id text NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    request_key text NOT NULL,
    manifest_digest text NOT NULL,
    job_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(member_id,network_id,request_key)
);
CREATE INDEX production_job_receipts_network ON production_job_receipts(network_id);
CREATE TABLE production_job_entry_receipts (
    member_id text NOT NULL REFERENCES members(id),
    network_id text NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    entry_key text NOT NULL,
    job_id text NOT NULL,
    entry_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(member_id,network_id,entry_key)
);
CREATE INDEX production_job_entry_receipts_network ON production_job_entry_receipts(network_id);
CREATE TABLE production_job_changes (
    network_id text NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
    revision bigint NOT NULL CHECK(revision>0),
    job_id text NOT NULL,
    version bigint NOT NULL,
    summary jsonb,
    removed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(network_id,revision)
);
CREATE TABLE production_job_actions (
    job_id text NOT NULL REFERENCES production_jobs(id) ON DELETE CASCADE,
    operation_key text NOT NULL,
    member_id text NOT NULL REFERENCES members(id),
    action text NOT NULL CHECK(action IN ('pause','resume','stop')),
    expected_control_version bigint NOT NULL,
    result jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(job_id,operation_key)
);
