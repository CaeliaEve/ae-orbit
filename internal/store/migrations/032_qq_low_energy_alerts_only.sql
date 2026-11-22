-- Recovery rearms low-energy alerts internally, without a QQ broadcast.
CREATE OR REPLACE FUNCTION qq_alert_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE grp text; device text; g qq_groups; content jsonb;
BEGIN
 IF NEW.kind<>'low_energy' OR NEW.state<>'active' THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NEW.state=OLD.state THEN RETURN NEW; END IF;
 SELECT server_group,name INTO grp,device FROM plugin_objects WHERE id=NEW.object_id;
 IF NOT EXISTS(SELECT 1 FROM qq_connectors WHERE server_group=grp AND enabled AND alarms) THEN RETURN NEW; END IF;
 content:=jsonb_build_object('title','蓝波顿低电量告警','device',device,'object_id',NEW.object_id,
   'alert_kind',NEW.kind,'state',NEW.state,'reason',NEW.detail,'at',now(),'energy',qq_lsc_snapshot(NEW.object_id));
 FOR g IN SELECT * FROM qq_groups WHERE server_group=grp AND enabled LOOP
  PERFORM qq_enqueue(grp,NULL,NULL,'alert:'||NEW.id||':'||NEW.state,'alarm',g.group_id,g.alarm_recipient,false,content);
 END LOOP;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION qq_suppress_network_health() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.kind='network' OR NEW.kind='alarm' AND
   (coalesce(NEW.payload->>'alert_kind','')<>'low_energy' OR coalesce(NEW.payload->>'state','')<>'active') THEN RETURN NULL; END IF;
 RETURN NEW;
END $$;
UPDATE qq_connectors SET recoveries=false,revision=revision+1 WHERE recoveries;
UPDATE qq_outbox SET state='expired',detail='蓝波顿电量恢复通知已关闭'
 WHERE kind='alarm' AND payload->>'state'='resolved' AND state IN ('queued','leased');
