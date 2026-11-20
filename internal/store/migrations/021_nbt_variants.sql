CREATE TABLE variant_catalog_candidates (
 network_id text NOT NULL REFERENCES networks(id),
 item_id bigint NOT NULL REFERENCES item_definitions(id),
 label text NOT NULL,
 search_text text NOT NULL,
 source text NOT NULL CHECK(source IN ('nesql','sample')),
 registered_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(network_id,item_id)
);
CREATE VIEW item_catalog_candidates AS
 SELECT network_id,item_id,label,search_text FROM plain_catalog_candidates
 UNION ALL
 SELECT v.network_id,v.item_id,v.label,v.search_text FROM variant_catalog_candidates v
 WHERE NOT EXISTS(SELECT 1 FROM plain_catalog_candidates p WHERE p.network_id=v.network_id AND p.item_id=v.item_id);
CREATE INDEX item_definitions_base_group ON item_definitions(network_id,kind,name,damage,id);
