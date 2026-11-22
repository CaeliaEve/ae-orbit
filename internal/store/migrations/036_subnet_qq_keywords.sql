CREATE TABLE subnet_qq_keywords (
 server_group text NOT NULL REFERENCES server_groups(name),subnet_id text NOT NULL,item_id text NOT NULL,
 keyword text NOT NULL,created_by text NOT NULL REFERENCES members(id),created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(subnet_id,item_id) REFERENCES subnet_items(subnet_id,item_id),
 PRIMARY KEY(subnet_id,item_id,keyword)
);
CREATE INDEX subnet_qq_keywords_lookup ON subnet_qq_keywords(server_group,keyword);
