-- QQ delivery is independent from OC credentials and browser change feeds.
ALTER TABLE production_job_entries ADD COLUMN notification jsonb;
CREATE TABLE qq_connectors (
 server_group text PRIMARY KEY REFERENCES server_groups(name),
 enabled boolean NOT NULL DEFAULT false, sending_enabled boolean NOT NULL DEFAULT false,
 revision bigint NOT NULL DEFAULT 1, token_hash bytea UNIQUE,
 last_seen timestamptz, bot_id text NOT NULL DEFAULT '', adapter text NOT NULL DEFAULT '',
 alarms boolean NOT NULL DEFAULT true, exceptions boolean NOT NULL DEFAULT true,
 recoveries boolean NOT NULL DEFAULT true, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE qq_groups (
 server_group text NOT NULL REFERENCES qq_connectors(server_group), group_id text NOT NULL,
 name text NOT NULL DEFAULT '', enabled boolean NOT NULL DEFAULT true,
 members jsonb NOT NULL DEFAULT '[]', members_at timestamptz,
 refresh_version bigint NOT NULL DEFAULT 1, refreshed_version bigint NOT NULL DEFAULT 0,
 refresh_error text NOT NULL DEFAULT '', alarm_recipient text NOT NULL DEFAULT '',
 PRIMARY KEY(server_group,group_id)
);
CREATE TABLE qq_reminders (
 id text PRIMARY KEY, network_id text NOT NULL REFERENCES networks(id),
 member_id text NOT NULL REFERENCES members(id), request_key text NOT NULL,
 entry_id text REFERENCES production_job_entries(id) ON DELETE SET NULL,
 cpu_name text NOT NULL DEFAULT '', session_id text NOT NULL DEFAULT '', cpu_fingerprint jsonb,
 group_id text NOT NULL, recipient text NOT NULL DEFAULT '', private_copy boolean NOT NULL DEFAULT false,
 state text NOT NULL DEFAULT 'watching' CHECK(state IN ('watching','finished','canceled','uncertain')),
 payload jsonb NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT now(), finished_at timestamptz,
 UNIQUE(member_id,network_id,request_key)
);
CREATE INDEX qq_reminders_watching ON qq_reminders(network_id) WHERE state='watching';
CREATE UNIQUE INDEX qq_reminders_entry_owner ON qq_reminders(entry_id,member_id) WHERE entry_id IS NOT NULL AND state='watching';
CREATE TABLE qq_outbox (
 id text PRIMARY KEY, server_group text NOT NULL REFERENCES qq_connectors(server_group),
 network_id text REFERENCES networks(id), member_id text REFERENCES members(id),
 source_key text NOT NULL, kind text NOT NULL, group_id text NOT NULL, recipient text NOT NULL DEFAULT '',
 channel text NOT NULL CHECK(channel IN ('group','private')), payload jsonb NOT NULL,
 state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','leased','sending','confirmed','uncertain','failed','expired')),
 attempt_id text NOT NULL DEFAULT '', lease_until timestamptz, attempts integer NOT NULL DEFAULT 0,
 external_id text NOT NULL DEFAULT '', detail text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL DEFAULT now()+interval '24 hours', delivered_at timestamptz,
 UNIQUE(server_group,source_key,group_id,recipient,channel)
);
CREATE INDEX qq_outbox_ready ON qq_outbox(server_group,created_at,id) WHERE state IN ('queued','leased','sending');

-- Source transitions and their immutable notification snapshot commit together.
CREATE FUNCTION qq_enqueue(grp text, net text, owner_id text, source text, event_kind text,
 target_group text, target_qq text, private_copy boolean, content jsonb) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM qq_connectors c JOIN qq_groups g ON g.server_group=c.server_group
   WHERE c.server_group=grp AND c.enabled AND g.group_id=target_group AND g.enabled) THEN RETURN; END IF;
 INSERT INTO qq_outbox(id,server_group,network_id,member_id,source_key,kind,group_id,recipient,channel,payload)
 VALUES(md5(random()::text||clock_timestamp()::text),grp,net,owner_id,source,event_kind,target_group,target_qq,'group',content)
 ON CONFLICT DO NOTHING;
 IF private_copy AND target_qq<>'' THEN
  INSERT INTO qq_outbox(id,server_group,network_id,member_id,source_key,kind,group_id,recipient,channel,payload)
  VALUES(md5(random()::text||clock_timestamp()::text),grp,net,owner_id,source,event_kind,target_group,target_qq,'private',content)
  ON CONFLICT DO NOTHING;
 END IF;
END $$;

CREATE FUNCTION qq_entry_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r qq_reminders; grp text; content jsonb; heading text; target_group_record qq_groups;
BEGIN
 IF NEW.state=OLD.state THEN RETURN NEW; END IF;
 IF NEW.state NOT IN ('done','failed','canceled','expired','rejected','needs_review') THEN RETURN NEW; END IF;
 SELECT server_group INTO grp FROM networks WHERE id=NEW.network_id;
 IF NOT EXISTS(SELECT 1 FROM qq_connectors WHERE server_group=grp AND enabled) THEN RETURN NEW; END IF;
 heading:=CASE NEW.state WHEN 'done' THEN '合成完成' WHEN 'needs_review' THEN '合成结果待确认'
   WHEN 'canceled' THEN '合成已取消' ELSE '合成异常' END;
 FOR r IN SELECT * FROM qq_reminders WHERE entry_id=NEW.id AND state='watching' FOR UPDATE LOOP
  content:=r.payload||jsonb_build_object('title',heading,'state',NEW.state,'reason',NEW.reason,'at',now());
  PERFORM qq_enqueue(grp,NEW.network_id,r.member_id,'reminder:'||r.id||':'||NEW.state,'craft',r.group_id,r.recipient,r.private_copy,content);
  UPDATE qq_reminders SET state=CASE WHEN NEW.state='needs_review' THEN 'watching' ELSE 'finished' END,
    finished_at=CASE WHEN NEW.state='needs_review' THEN NULL ELSE now() END WHERE id=r.id;
 END LOOP;
 IF NEW.state IN ('failed','expired','rejected','needs_review') THEN
  SELECT jsonb_build_object('title',heading,'state',NEW.state,'reason',NEW.reason,'at',now(),
    'cpu',NEW.cpu_name,'item',d.label,'registry',d.name,'quantity',NEW.quantity::text,
    'unit',CASE WHEN d.kind='fluid' THEN 'mB' ELSE '件' END,'owner',coalesce(nullif(m.game_id,''),m.name))
    INTO content FROM production_jobs j JOIN members m ON m.id=j.member_id JOIN item_definitions d ON d.network_id=NEW.network_id AND d.id=NEW.item_id WHERE j.id=NEW.job_id;
  FOR target_group_record IN SELECT qg.* FROM qq_groups qg JOIN qq_connectors c ON c.server_group=qg.server_group
    WHERE qg.server_group=grp AND qg.enabled AND c.enabled AND c.exceptions
      AND NOT EXISTS(SELECT 1 FROM qq_outbox q WHERE q.server_group=grp AND q.group_id=qg.group_id AND q.kind='craft' AND q.payload->>'state'=NEW.state AND q.source_key IN (SELECT 'reminder:'||qr.id||':'||NEW.state FROM qq_reminders qr WHERE qr.entry_id=NEW.id)) LOOP
   PERFORM qq_enqueue(grp,NEW.network_id,NULL,'entry:'||NEW.id||':'||NEW.state,'exception',target_group_record.group_id,target_group_record.alarm_recipient,false,content);
  END LOOP;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER qq_entry_transition AFTER UPDATE OF state ON production_job_entries FOR EACH ROW EXECUTE FUNCTION qq_entry_transition();

CREATE FUNCTION qq_alert_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE grp text; device text; g qq_groups; content jsonb;
BEGIN
 IF TG_OP='UPDATE' AND NEW.state=OLD.state THEN RETURN NEW; END IF;
 SELECT server_group,name INTO grp,device FROM plugin_objects WHERE id=NEW.object_id;
 IF NOT EXISTS(SELECT 1 FROM qq_connectors WHERE server_group=grp AND enabled AND alarms
   AND (NEW.state='active' OR recoveries AND NEW.resolve_reason IN ('energy_recovered','collector_online','reading_recovered','trend_changed'))) THEN RETURN NEW; END IF;
 content:=jsonb_build_object('title',CASE WHEN NEW.state='resolved' THEN '蓝波顿告警恢复' ELSE '蓝波顿告警' END,
   'device',device,'alert_kind',NEW.kind,'state',NEW.state,'reason',NEW.detail,'resolve_reason',NEW.resolve_reason,'at',now());
 FOR g IN SELECT * FROM qq_groups WHERE server_group=grp AND enabled LOOP
  PERFORM qq_enqueue(grp,NULL,NULL,'alert:'||NEW.id||':'||NEW.state,'alarm',g.group_id,g.alarm_recipient,false,content);
 END LOOP;
 RETURN NEW;
END $$;
CREATE TRIGGER qq_alert_transition AFTER INSERT OR UPDATE OF state ON monitor_alerts FOR EACH ROW EXECUTE FUNCTION qq_alert_transition();

CREATE TABLE qq_network_health (
 network_id text PRIMARY KEY REFERENCES networks(id), offline boolean NOT NULL DEFAULT false,
 episode text NOT NULL, changed_at timestamptz NOT NULL DEFAULT now()
);
