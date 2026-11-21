-- Snapshot references are short lived; a selected target is pinned in the OC
-- process before the user spends time choosing a notification recipient.
CREATE TABLE qq_cpu_candidates (
 network_id text NOT NULL REFERENCES networks(id), session_id text NOT NULL,
 cpu_id text NOT NULL, cpu jsonb NOT NULL, observed_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(network_id,session_id,cpu_id)
);
CREATE TABLE qq_cpu_targets (
 id text PRIMARY KEY, network_id text NOT NULL REFERENCES networks(id),
 member_id text NOT NULL REFERENCES members(id), request_key text NOT NULL,
 session_id text NOT NULL, cpu_id text NOT NULL, cpu jsonb NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','busy','idle','changed','missing','inactive')),
 observed_at timestamptz, next_read_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '2 minutes',
 UNIQUE(network_id,member_id,request_key)
);
ALTER TABLE qq_reminders ADD COLUMN cpu_target_id text REFERENCES qq_cpu_targets(id);
CREATE UNIQUE INDEX qq_reminders_cpu_target_owner ON qq_reminders(cpu_target_id,member_id)
 WHERE cpu_target_id IS NOT NULL AND state='watching';
