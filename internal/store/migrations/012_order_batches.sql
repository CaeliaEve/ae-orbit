ALTER TABLE orders ADD COLUMN batch_id text NOT NULL DEFAULT ''
    CHECK (octet_length(batch_id) <= 128);
