CREATE TABLE integration_keys (
 id text PRIMARY KEY,
 member_id text NOT NULL REFERENCES members(id),
 name text NOT NULL,
 token_hash bytea NOT NULL UNIQUE,
 token_prefix text NOT NULL,
 enabled boolean NOT NULL DEFAULT true,
 revision bigint NOT NULL DEFAULT 1,
 scopes text[] NOT NULL,
 network_ids text[] NOT NULL DEFAULT '{}',
 server_groups text[] NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL,
 last_used_at timestamptz
);
CREATE INDEX integration_keys_owner ON integration_keys(member_id,created_at DESC);
