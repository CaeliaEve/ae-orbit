ALTER TABLE server_groups ADD COLUMN next_scan_at timestamptz NOT NULL DEFAULT '-infinity';
