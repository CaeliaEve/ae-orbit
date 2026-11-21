-- OC heartbeat transitions stay on the website and never enter QQ delivery.
CREATE OR REPLACE FUNCTION qq_suppress_network_health() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.kind='network' THEN RETURN NULL; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS qq_suppress_network_health ON qq_outbox;
CREATE TRIGGER qq_suppress_network_health BEFORE INSERT ON qq_outbox
 FOR EACH ROW EXECUTE FUNCTION qq_suppress_network_health();

-- Stop old pending broadcasts, including leases obtained before this update.
UPDATE qq_outbox SET state='expired',detail='网络采集上下线播报已关闭'
 WHERE kind='network' AND state IN ('queued','leased');
