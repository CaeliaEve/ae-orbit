-- Existing last_scan values cannot recover the first completion of a boot.
-- Leave them unknown; the next new session records its first accepted marker.
ALTER TABLE networks ADD COLUMN first_scan timestamptz;
