ALTER TABLE networks
    ADD COLUMN catalog_revision bigint NOT NULL DEFAULT 0,
    ADD COLUMN craftable_revision bigint NOT NULL DEFAULT 0;
