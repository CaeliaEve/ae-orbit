CREATE TABLE item_aliases (
    network_id text NOT NULL,
    alias_id bigint NOT NULL,
    canonical_id bigint NOT NULL,
    PRIMARY KEY(network_id,alias_id),
    CHECK(alias_id<>canonical_id),
    FOREIGN KEY(network_id,alias_id) REFERENCES item_definitions(network_id,id) ON DELETE CASCADE,
    FOREIGN KEY(network_id,canonical_id) REFERENCES item_definitions(network_id,id)
);
CREATE INDEX item_aliases_canonical ON item_aliases(network_id,canonical_id);
