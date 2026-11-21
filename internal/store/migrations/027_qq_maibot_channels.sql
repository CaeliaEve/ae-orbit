-- MaiBot is the authority for notification channels. Old manually entered
-- groups remain unavailable until the authenticated connector synchronizes.
ALTER TABLE qq_connectors ADD COLUMN catalog_ready boolean NOT NULL DEFAULT false,
 ADD COLUMN catalog_at timestamptz, ADD COLUMN catalog_digest text NOT NULL DEFAULT '',
 ADD COLUMN allow_private boolean NOT NULL DEFAULT false;
UPDATE qq_groups SET enabled=false;

CREATE OR REPLACE FUNCTION qq_enqueue(grp text, net text, owner_id text, source text, event_kind text,
 target_group text, target_qq text, private_copy boolean, content jsonb) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM qq_connectors c JOIN qq_groups g ON g.server_group=c.server_group
   WHERE c.server_group=grp AND c.enabled AND c.catalog_ready AND g.group_id=target_group AND g.enabled) THEN RETURN; END IF;
 INSERT INTO qq_outbox(id,server_group,network_id,member_id,source_key,kind,group_id,recipient,channel,payload)
 VALUES(md5(random()::text||clock_timestamp()::text),grp,net,owner_id,source,event_kind,target_group,target_qq,'group',content)
 ON CONFLICT DO NOTHING;
 IF private_copy AND target_qq<>'' AND EXISTS(SELECT 1 FROM qq_connectors WHERE server_group=grp AND allow_private) THEN
  INSERT INTO qq_outbox(id,server_group,network_id,member_id,source_key,kind,group_id,recipient,channel,payload)
  VALUES(md5(random()::text||clock_timestamp()::text),grp,net,owner_id,source,event_kind,target_group,target_qq,'private',content)
  ON CONFLICT DO NOTHING;
 END IF;
END $$;
