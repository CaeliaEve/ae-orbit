-- Optional observations: old OC packets and old history remain unknown.
ALTER TABLE monitor_latest ADD COLUMN flow jsonb, ADD COLUMN flow_error text NOT NULL DEFAULT '';
ALTER TABLE monitor_energy_records ADD COLUMN flow jsonb, ADD COLUMN flow_error text NOT NULL DEFAULT '';
ALTER TABLE monitor_energy_rollups ADD COLUMN flow jsonb, ADD COLUMN flow_error text NOT NULL DEFAULT '';
