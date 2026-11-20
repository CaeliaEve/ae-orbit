CREATE TABLE resource_catalog_candidates (
    network_id text NOT NULL REFERENCES network_inventory_profiles(network_id) ON DELETE CASCADE,
    item_id bigint NOT NULL,
    label text NOT NULL,
    search_text text NOT NULL,
    PRIMARY KEY(network_id,item_id),
    FOREIGN KEY(network_id,item_id) REFERENCES item_definitions(network_id,id) ON DELETE CASCADE
);
CREATE INDEX resource_catalog_candidates_label ON resource_catalog_candidates(network_id,lower(label),item_id);
ALTER TABLE cooperative_rotation ADD COLUMN resource_turn integer NOT NULL DEFAULT 0 CHECK(resource_turn BETWEEN 0 AND 3);
