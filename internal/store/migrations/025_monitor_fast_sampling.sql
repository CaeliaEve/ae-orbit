-- Existing collectors do not understand one-second assignments until upgraded.
ALTER TABLE monitor_sessions ADD COLUMN min_interval_seconds integer NOT NULL DEFAULT 10 CHECK(min_interval_seconds IN (1,10));
ALTER TABLE monitor_latest ADD COLUMN last_recorded_at timestamptz;
UPDATE monitor_latest l SET last_recorded_at=(SELECT max(observed_at) FROM monitor_energy_records r WHERE r.object_id=l.object_id);
UPDATE plugin_installations SET settings=jsonb_build_object('history_interval_seconds',10)||settings WHERE plugin_id='gtnh.lsc';
