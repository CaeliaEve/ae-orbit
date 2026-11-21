CREATE OR REPLACE FUNCTION qq_suppress_network_health() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.kind='network' OR NEW.kind='alarm' AND coalesce(NEW.payload->>'alert_kind','')<>'low_energy' THEN RETURN NULL; END IF;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION qq_alert_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE grp text; device text; g qq_groups; content jsonb;
BEGIN
 IF NEW.kind<>'low_energy' THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NEW.state=OLD.state THEN RETURN NEW; END IF;
 SELECT server_group,name INTO grp,device FROM plugin_objects WHERE id=NEW.object_id;
 IF NOT EXISTS(SELECT 1 FROM qq_connectors WHERE server_group=grp AND enabled AND alarms
   AND (NEW.state='active' OR recoveries AND NEW.resolve_reason='energy_recovered')) THEN RETURN NEW; END IF;
 content:=jsonb_build_object('title',CASE WHEN NEW.state='resolved' THEN '蓝波顿电量恢复' ELSE '蓝波顿低电量告警' END,
   'device',device,'object_id',NEW.object_id,'alert_kind',NEW.kind,'state',NEW.state,
   'reason',NEW.detail,'resolve_reason',NEW.resolve_reason,'at',now(),
   'energy',qq_lsc_snapshot(NEW.object_id));
 FOR g IN SELECT * FROM qq_groups WHERE server_group=grp AND enabled LOOP
  PERFORM qq_enqueue(grp,NULL,NULL,'alert:'||NEW.id||':'||NEW.state,'alarm',g.group_id,g.alarm_recipient,false,content);
 END LOOP;
 RETURN NEW;
END $$;

UPDATE qq_outbox SET state='expired',detail='仅保留蓝波顿低电量及电量恢复通知'
 WHERE kind='alarm' AND coalesce(payload->>'alert_kind','')<>'low_energy' AND state IN ('queued','leased');
UPDATE monitor_alerts SET state='resolved',resolved_at=now(),resolve_reason='rule_disabled'
 WHERE kind IN ('collector_offline','read_error') AND state='active';
DELETE FROM monitor_conditions WHERE kind IN ('collector_offline','read_error');
-- Low energy is the primary alert. Falling trends remain opt-in station history.
UPDATE plugin_installations SET settings=jsonb_set(settings,'{falling_minutes}','0'),revision=revision+1
 WHERE plugin_id='gtnh.lsc' AND coalesce((settings->>'falling_minutes')::int,0)>0;
UPDATE monitor_alerts SET state='resolved',resolved_at=now(),resolve_reason='rule_disabled'
 WHERE kind='falling' AND state='active';
DELETE FROM monitor_conditions WHERE kind='falling';
