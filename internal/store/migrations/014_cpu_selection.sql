ALTER TABLE orders ADD COLUMN cpu_name text NOT NULL DEFAULT ''
    CHECK (octet_length(cpu_name) <= 256);
ALTER TABLE orders ADD COLUMN wait_for_cpu boolean NOT NULL DEFAULT false;

ALTER TABLE production_job_entries ADD COLUMN cpu_name text NOT NULL DEFAULT ''
    CHECK (octet_length(cpu_name) <= 256);
ALTER TABLE production_job_entries ADD COLUMN wait_for_cpu boolean NOT NULL DEFAULT false;
