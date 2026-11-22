ALTER TABLE qq_groups ADD COLUMN last_power_request_at timestamptz;

-- Read cached telemetry only: QQ requests must never add game-side queries.
CREATE FUNCTION qq_lsc_snapshot(target_object text) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object(
  'object_id',o.id,'device',o.name,'location',o.location,
  'stored_eu',l.stored_eu::text,'capacity_eu',l.capacity_eu::text,
  'percent',CASE WHEN l.capacity_eu>0 THEN round(l.stored_eu/l.capacity_eu*100,2)::text ELSE NULL END,
  'observed_at',l.observed_at,'flow',l.flow,'flow_error',coalesce(l.flow_error,''),
  'status',CASE WHEN NOT p.enabled OR NOT o.enabled THEN 'paused'
   WHEN c.revoked THEN 'revoked' WHEN c.last_seen IS NULL THEN 'waiting_collector'
   WHEN c.last_seen<now()-coalesce((p.settings->>'offline_seconds')::int,60)*interval '1 second' THEN 'offline'
   WHEN l.object_id IS NULL THEN 'waiting_sample' WHEN l.error<>'' THEN 'read_error'
   WHEN l.observed_at IS NULL OR l.observed_at<now()-coalesce((p.settings->>'offline_seconds')::int,60)*interval '1 second' THEN 'stale'
   ELSE 'online' END)
 FROM plugin_objects o JOIN plugin_installations p USING(server_group,plugin_id)
 JOIN monitor_collectors c ON c.id=o.collector_id LEFT JOIN monitor_latest l ON l.object_id=o.id
 WHERE o.id=target_object AND o.plugin_id='gtnh.lsc'
$$;

CREATE OR REPLACE FUNCTION qq_alert_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE grp text; device text; g qq_groups; content jsonb;
BEGIN
 IF TG_OP='UPDATE' AND NEW.state=OLD.state THEN RETURN NEW; END IF;
 SELECT server_group,name INTO grp,device FROM plugin_objects WHERE id=NEW.object_id;
 IF NOT EXISTS(SELECT 1 FROM qq_connectors WHERE server_group=grp AND enabled AND alarms
   AND (NEW.state='active' OR recoveries AND NEW.resolve_reason IN ('energy_recovered','collector_online','reading_recovered','trend_changed'))) THEN RETURN NEW; END IF;
 content:=jsonb_build_object('title',CASE WHEN NEW.state='resolved' THEN '蓝波顿告警恢复' ELSE '蓝波顿告警' END,
   'device',device,'object_id',NEW.object_id,'alert_kind',NEW.kind,'state',NEW.state,
   'reason',NEW.detail,'resolve_reason',NEW.resolve_reason,'at',now(),
   'energy',qq_lsc_snapshot(NEW.object_id));
 FOR g IN SELECT * FROM qq_groups WHERE server_group=grp AND enabled LOOP
  PERFORM qq_enqueue(grp,NULL,NULL,'alert:'||NEW.id||':'||NEW.state,'alarm',g.group_id,g.alarm_recipient,false,content);
 END LOOP;
 RETURN NEW;
END $$;
