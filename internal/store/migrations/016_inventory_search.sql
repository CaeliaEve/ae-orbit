ALTER TABLE plain_catalog_candidates ADD COLUMN search_text text NOT NULL DEFAULT '';
ALTER TABLE item_definitions ADD COLUMN search_label text NOT NULL DEFAULT '';
-- Search metadata is independent of quantities and scan/assignment state.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX plain_catalog_search ON plain_catalog_candidates USING gin(search_text gin_trgm_ops);
