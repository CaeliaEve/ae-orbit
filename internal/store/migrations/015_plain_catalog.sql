-- Candidate metadata has no stock until an authenticated OC read succeeds.
CREATE TABLE network_inventory_profiles (
    network_id text PRIMARY KEY REFERENCES networks(id) ON DELETE CASCADE,
    mode text NOT NULL CHECK(mode='nesql_plain'),
    enabled_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE plain_catalog_candidates (
    network_id text NOT NULL REFERENCES network_inventory_profiles(network_id) ON DELETE CASCADE,
    item_id bigint NOT NULL,
    label text NOT NULL,
    PRIMARY KEY(network_id,item_id),
    FOREIGN KEY(network_id,item_id) REFERENCES item_definitions(network_id,id) ON DELETE CASCADE
);
CREATE INDEX plain_catalog_candidates_label ON plain_catalog_candidates(network_id,lower(label),item_id);
ALTER TABLE inventory_refreshes DROP CONSTRAINT inventory_refreshes_network_id_item_id_fkey;
ALTER TABLE inventory_refreshes ADD FOREIGN KEY(network_id,item_id)
    REFERENCES item_definitions(network_id,id) ON DELETE CASCADE;
