package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/plugins"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func qqFixture(t *testing.T) (*store.Store, protocol.Member, protocol.Network, store.QQAuth) {
	t.Helper()
	s, a, n, _ := fixture(t)
	if err := s.SetQQSettings(ctx, a.ID, n.ServerGroup, store.QQSettings{Enabled: true, Alarms: true, Exceptions: true, Recoveries: true, Groups: nil}); err != nil {
		t.Fatal(err)
	}
	token, err := s.QQToken(ctx, a.ID, n.ServerGroup)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := s.AuthQQ(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.QQSyncChannels(ctx, auth, store.QQChannelSync{Groups: []store.QQChannel{{ID: "1081348823"}}, AllowPrivate: true}); err != nil {
		t.Fatal(err)
	}
	return s, a, n, auth
}
func enqueueQQ(t *testing.T, s *store.Store, n protocol.Network) {
	t.Helper()
	if _, err := s.Pool().Exec(ctx, `SELECT qq_enqueue($1,$2,NULL,'source-1','craft','1081348823','12345678',false,'{"title":"合成完成"}')`, n.ServerGroup, n.ID); err != nil {
		t.Fatal(err)
	}
}
func enableQQSending(t *testing.T, s *store.Store, a protocol.Member, n protocol.Network) {
	t.Helper()
	v, err := s.QQSettings(ctx, a.ID, n.ServerGroup)
	if err != nil {
		t.Fatal(err)
	}
	v.SendingEnabled = true
	v.Groups = nil
	if err = s.SetQQSettings(ctx, a.ID, n.ServerGroup, v); err != nil {
		t.Fatal(err)
	}
}
func TestQQDeliveryDurabilityAndAmbiguousSend(t *testing.T) {
	s, a, n, auth := qqFixture(t)
	enqueueQQ(t, s, n)
	enqueueQQ(t, s, n)
	in := store.QQBridgePoll{BotID: "12345678", Adapter: "snowluma"}
	p, err := s.QQBridgePoll(ctx, auth, in)
	if err != nil || len(p.Deliveries) != 0 || p.SendingEnabled {
		t.Fatal(p, err)
	}
	rows, err := s.QQDeliveries(ctx, a.ID, n.ServerGroup)
	if err != nil || len(rows) != 1 || rows[0].State != "queued" {
		t.Fatal(rows, err)
	}
	enableQQSending(t, s, a, n)
	p, err = s.QQBridgePoll(ctx, auth, in)
	if err != nil || len(p.Deliveries) != 1 {
		t.Fatal(p, err)
	}
	d := p.Deliveries[0]
	if err = s.QQDeliveryReceipt(ctx, auth, d.ID, store.QQReceipt{AttemptID: d.AttemptID, State: "confirmed", ExternalID: "9"}); !errors.Is(err, store.ErrConflict) {
		t.Fatal("confirmed before send", err)
	}
	if err = s.QQBeginDelivery(ctx, auth, d.ID, d.AttemptID); err != nil {
		t.Fatal(err)
	}
	if err = s.QQBeginDelivery(ctx, auth, d.ID, d.AttemptID); !errors.Is(err, store.ErrConflict) {
		t.Fatal("double begin", err)
	}
	if _, err = s.Pool().Exec(ctx, `UPDATE qq_outbox SET lease_until=now()-interval '1 second' WHERE id=$1`, d.ID); err != nil {
		t.Fatal(err)
	}
	p, err = s.QQBridgePoll(ctx, auth, in)
	if err != nil || len(p.Deliveries) != 0 {
		t.Fatal("ambiguous send replayed", p, err)
	}
	rows, err = s.QQDeliveries(ctx, a.ID, n.ServerGroup)
	if err != nil || rows[0].State != "uncertain" {
		t.Fatal(rows, err)
	}
	receipt := store.QQReceipt{AttemptID: d.AttemptID, State: "confirmed", ExternalID: "987654"}
	if err = s.QQDeliveryReceipt(ctx, auth, d.ID, receipt); err != nil {
		t.Fatal(err)
	}
	if err = s.QQDeliveryReceipt(ctx, auth, d.ID, receipt); err != nil {
		t.Fatal("receipt not idempotent", err)
	}
	if err = s.QQDeliveryReceipt(ctx, store.QQAuth{Group: "wrong"}, d.ID, receipt); !errors.Is(err, store.ErrConflict) {
		t.Fatal("cross-group receipt", err)
	}
}
func TestQQLeasedWorkAndMemberCache(t *testing.T) {
	s, a, n, auth := qqFixture(t)
	enqueueQQ(t, s, n)
	enableQQSending(t, s, a, n)
	in := store.QQBridgePoll{BotID: "12345678", Adapter: "snowluma"}
	p, err := s.QQBridgePoll(ctx, auth, in)
	if err != nil {
		t.Fatal(err)
	}
	d := p.Deliveries[0]
	if _, err = s.Pool().Exec(ctx, `UPDATE qq_outbox SET lease_until=now()-interval '1 second' WHERE id=$1`, d.ID); err != nil {
		t.Fatal(err)
	}
	next, err := s.QQBridgePoll(ctx, auth, in)
	if err != nil || len(next.Deliveries) != 1 || next.Deliveries[0].AttemptID == d.AttemptID {
		t.Fatal(next, err)
	}
	if err = s.QQBeginDelivery(ctx, auth, d.ID, d.AttemptID); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old lease authorized", err)
	}
	if err = s.QQSyncGroup(ctx, auth, "1081348823", store.QQGroupSync{Version: 1, Members: []store.QQMember{{ID: "12345678", Name: "群友"}}}); err != nil {
		t.Fatal(err)
	}
	members, err := s.QQMembers(ctx, a.ID, n.ServerGroup, "1081348823", false)
	if err != nil || len(members.Members) != 1 {
		t.Fatal(members, err)
	}
	if err = s.QQSyncGroup(ctx, auth, "9999999", store.QQGroupSync{Version: 1}); !errors.Is(err, store.ErrViewChanged) {
		t.Fatal("unconfigured group", err)
	}
	outsider, _, err := s.CreateMember(ctx, a.ID, "outsider", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.QQMembers(ctx, outsider.ID, n.ServerGroup, "1081348823", false); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("member cache exposed", err)
	}
}
func TestQQProductionNotificationAtomicAndCanceled(t *testing.T) {
	s, a, n, _, _, _, item := orderFixture(t)
	if err := s.SetQQSettings(ctx, a.ID, n.ServerGroup, store.QQSettings{Enabled: true, Groups: nil}); err != nil {
		t.Fatal(err)
	}
	if err := s.QQSyncChannels(ctx, store.QQAuth{Group: n.ServerGroup}, store.QQChannelSync{Groups: []store.QQChannel{{ID: "1081348823"}}, AllowPrivate: true}); err != nil {
		t.Fatal(err)
	}
	in := jobInput(item, "qq-job-0001", "qq-source-0001")
	in.Entries[0].Notification = &protocol.QQNotification{GroupID: "1081348823", Recipient: "12345678", PrivateCopy: true}
	accepted, err := s.CreateProductionJob(ctx, a.ID, n.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	e := accepted.Job.Entries[0].ID
	if _, err = s.Pool().Exec(ctx, `UPDATE production_job_entries SET state='needs_review' WHERE id=$1`, e); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, `UPDATE production_job_entries SET state='done' WHERE id=$1`, e); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, `UPDATE production_job_entries SET state='done' WHERE id=$1`, e); err != nil {
		t.Fatal(err)
	}
	out, err := s.QQDeliveries(ctx, a.ID, n.ServerGroup)
	if err != nil || len(out) != 4 {
		t.Fatal(out, err)
	}
	in.Entries[0].Notification.Recipient = "87654321"
	if _, err = s.CreateProductionJob(ctx, a.ID, n.ID, in); !errors.Is(err, store.ErrConflict) {
		t.Fatal("changed notification replay accepted", err)
	}
	in = jobInput(item, "qq-job-0002", "qq-source-0002")
	in.Entries[0].Notification = &protocol.QQNotification{GroupID: "1081348823"}
	accepted, err = s.CreateProductionJob(ctx, a.ID, n.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE production_job_entries SET state='done' WHERE id=$1`, accepted.Job.Entries[0].ID); err != nil {
		t.Fatal(err)
	}
	tx.Rollback(ctx)
	out, err = s.QQDeliveries(ctx, a.ID, n.ServerGroup)
	if err != nil || len(out) != 4 {
		t.Fatal("rolled back notification escaped", out, err)
	}
	if _, err = s.Pool().Exec(ctx, `UPDATE production_job_entries SET state='canceled' WHERE id=$1`, accepted.Job.Entries[0].ID); err != nil {
		t.Fatal(err)
	}
	out, err = s.QQDeliveries(ctx, a.ID, n.ServerGroup)
	if err != nil || len(out) != 5 {
		t.Fatal(out, err)
	}
}

func TestQQCPUWatchEndsWithoutClaimingSuccess(t *testing.T) {
	s, a, n, token := fixture(t)
	d, w := hello(t, s, token, "qq-cpu-watch")
	if err := s.SetQQSettings(ctx, a.ID, n.ServerGroup, store.QQSettings{Enabled: true, Groups: nil}); err != nil {
		t.Fatal(err)
	}
	if err := s.QQSyncChannels(ctx, store.QQAuth{Group: n.ServerGroup}, store.QQChannelSync{Groups: []store.QQChannel{{ID: "1081348823"}}}); err != nil {
		t.Fatal(err)
	}
	cpus := []protocol.CPU{{ID: "1:1", Name: "CPU A", Busy: true, Storage: 4096, Coprocessors: 2, Output: &protocol.Stack{Name: "test:plate", Label: "Plate", Quantity: 64}}}
	if _, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 1, CPUs: &cpus}); err != nil {
		t.Fatal(err)
	}
	in := store.QQReminderInput{RequestKey: "qq-watch-0001", SessionID: w.SessionID, CPUID: "1:1", Notification: protocol.QQNotification{GroupID: "1081348823"}}
	if _, err := s.AddQQCPUReminder(ctx, a.ID, n.ID, in); err != nil {
		t.Fatal(err)
	}
	cpus[0].ID = "2:1"
	cpus[0].Output.Quantity = 32
	if _, err := s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 2, CPUs: &cpus}); err != nil {
		t.Fatal(err)
	}
	reminders, err := s.QQReminders(ctx, a.ID, n.ID)
	if err != nil || len(reminders) != 1 || reminders[0].State != "watching" {
		t.Fatal(reminders, err)
	}
	cpus[0].Busy = false
	cpus[0].Output = nil
	if _, err = s.Poll(ctx, d, protocol.Poll{SessionID: w.SessionID, Sequence: 3, CPUs: &cpus}); err != nil {
		t.Fatal(err)
	}
	reminders, err = s.QQReminders(ctx, a.ID, n.ID)
	if err != nil || reminders[0].State != "uncertain" {
		t.Fatal(reminders, err)
	}
	deliveries, err := s.QQDeliveries(ctx, a.ID, n.ServerGroup)
	if err != nil || len(deliveries) != 1 || deliveries[0].Kind != "cpu_end" {
		t.Fatal(deliveries, err)
	}
}
func TestQQMonitorRecoveryRemainsSilent(t *testing.T) {
	s, a, n, auth := qqFixture(t)
	if _, err := s.Pool().Exec(ctx, `INSERT INTO plugin_installations(server_group,plugin_id,enabled,settings) VALUES($1,'gtnh.lsc',true,'{}');`, n.ServerGroup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, `INSERT INTO monitor_collectors(id,server_group,name,token_hash) VALUES('qq-collector',$1,'Collector','\x0102')`, n.ServerGroup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, `INSERT INTO plugin_objects(id,server_group,plugin_id,collector_id,component_address,name) VALUES('qq-object',$1,'gtnh.lsc','qq-collector','address','LSC')`, n.ServerGroup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, `INSERT INTO monitor_alerts(id,object_id,kind,state,detail) VALUES('qq-alert','qq-object','low_energy','active','Low energy')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, `UPDATE monitor_alerts SET state='resolved',resolve_reason='energy_recovered',resolved_at=now() WHERE id='qq-alert'`); err != nil {
		t.Fatal(err)
	}
	out, err := s.QQDeliveries(ctx, a.ID, n.ServerGroup)
	if err != nil || len(out) != 1 {
		t.Fatal(out, err)
	}
	enableQQSending(t, s, a, n)
	p, err := s.QQBridgePoll(ctx, auth, store.QQBridgePoll{BotID: "12345678", Adapter: "snowluma"})
	if err != nil || len(p.Deliveries) != 0 {
		t.Fatal(p, err)
	}
}

func TestQQMaiBotOwnsChannelsAndRevocationStopsLeases(t *testing.T) {
	s, a, n, auth := qqFixture(t)
	cfg, err := s.QQSettings(ctx, a.ID, n.ServerGroup)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetQQSettings(ctx, a.ID, n.ServerGroup, cfg); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("browser changed channel policy", err)
	}
	enqueueQQ(t, s, n)
	enableQQSending(t, s, a, n)
	p, err := s.QQBridgePoll(ctx, auth, store.QQBridgePoll{BotID: "12345678", Adapter: "snowluma"})
	if err != nil || len(p.Deliveries) != 1 {
		t.Fatal(p, err)
	}
	if err = s.QQSyncChannels(ctx, auth, store.QQChannelSync{Groups: []store.QQChannel{}}); err != nil {
		t.Fatal(err)
	}
	d := p.Deliveries[0]
	if err = s.QQBeginDelivery(ctx, auth, d.ID, d.AttemptID); !errors.Is(err, store.ErrConflict) {
		t.Fatal("revoked group lease authorized", err)
	}
	out, err := s.QQDeliveries(ctx, a.ID, n.ServerGroup)
	if err != nil || out[0].State != "expired" {
		t.Fatal(out, err)
	}
	if err = s.QQSyncChannels(ctx, auth, store.QQChannelSync{Groups: []store.QQChannel{{ID: "1081348823"}}, AllowPrivate: false}); err != nil {
		t.Fatal(err)
	}
	cfg, err = s.QQSettings(ctx, a.ID, n.ServerGroup)
	if err != nil || !cfg.CatalogReady || cfg.AllowPrivate {
		t.Fatal(cfg, err)
	}
	if _, err = s.Pool().Exec(ctx, `SELECT qq_enqueue($1,$2,NULL,'private-disabled','craft','1081348823','12345678',true,'{}')`, n.ServerGroup, n.ID); err != nil {
		t.Fatal(err)
	}
	out, err = s.QQDeliveries(ctx, a.ID, n.ServerGroup)
	if err != nil || len(out) != 2 {
		t.Fatal("private created after policy revoked", out, err)
	}
}

func TestQQPowerQueryUsesCachedTelemetryAndAuthorizedGroup(t *testing.T) {
	s, a, n, auth := qqFixture(t)
	if _, err := s.Pool().Exec(ctx, `INSERT INTO plugin_installations(server_group,plugin_id,enabled,settings) VALUES($1,'gtnh.lsc',true,'{"offline_seconds":60}')`, n.ServerGroup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, `INSERT INTO monitor_collectors(id,server_group,name,token_hash,last_seen) VALUES('power-collector',$1,'Collector','\x0102',now())`, n.ServerGroup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, `INSERT INTO plugin_objects(id,server_group,plugin_id,collector_id,component_address,name) VALUES('power-object',$1,'gtnh.lsc','power-collector','address','Main LSC')`, n.ServerGroup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, `INSERT INTO monitor_latest(object_id,session_id,stored_eu,capacity_eu,observed_at) VALUES('power-object','power-session',9007199254740993,18014398509481986,now())`); err != nil {
		t.Fatal(err)
	}
	v, err := s.QQPower(ctx, auth, "1081348823", "")
	if err != nil || len(v.Devices) != 1 {
		t.Fatal(v, err)
	}
	if !strings.Contains(string(v.Devices[0]), `"stored_eu": "9007199254740993"`) {
		t.Fatal(string(v.Devices[0]))
	}
	in := store.QQPowerRequest{GroupID: "1081348823", RequestKey: "power-0001"}
	if err = s.QQRequestPower(ctx, auth, in); err != nil {
		t.Fatal(err)
	}
	if err = s.QQRequestPower(ctx, auth, in); err != nil {
		t.Fatal("same message duplicated", err)
	}
	in.RequestKey = "power-0002"
	if err = s.QQRequestPower(ctx, auth, in); err != nil {
		t.Fatal("distinct query was throttled", err)
	}
	if _, err = s.QQPower(ctx, auth, "99999999", ""); err == nil {
		t.Fatal("unauthorized group got telemetry")
	}
	if _, err = s.Pool().Exec(ctx, `INSERT INTO monitor_alerts(id,object_id,kind,state,detail) VALUES('power-alert','power-object','low_energy','active','Low')`); err != nil {
		t.Fatal(err)
	}
	out, err := s.QQDeliveries(ctx, a.ID, n.ServerGroup)
	if err != nil || len(out) != 3 {
		t.Fatal(out, err)
	}
	var found bool
	for _, d := range out {
		if d.Kind == "alarm" {
			found = strings.Contains(string(d.Payload), "9007199254740993")
		}
	}
	if !found {
		t.Fatal("alarm missed trigger telemetry")
	}
	if _, err = s.Pool().Exec(ctx, `UPDATE monitor_latest SET stored_eu=5,observed_at=now()-interval '2 minutes' WHERE object_id='power-object'`); err != nil {
		t.Fatal(err)
	}
	enableQQSending(t, s, a, n)
	p, err := s.QQBridgePoll(ctx, auth, store.QQBridgePoll{BotID: "12345678", Adapter: "snowluma"})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range p.Deliveries {
		if d.Kind == "alarm" && (!strings.Contains(string(d.Payload), "current_energy") || !strings.Contains(string(d.Payload), `"status": "stale"`)) {
			t.Fatal(string(d.Payload))
		}
	}
}

func TestQQNetworkTransitionsStaySilentAndOldLeasesStop(t *testing.T) {
	s, a, n, auth := qqFixture(t)
	enableQQSending(t, s, a, n)
	for _, observed := range []string{"now()", "now()-interval '1 day'", "now()"} {
		if _, err := s.Pool().Exec(ctx, `UPDATE networks SET last_seen=`+observed+` WHERE id=$1`, n.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.MaintainQQ(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Pool().Exec(ctx, `SELECT qq_enqueue($1,$2,NULL,'heartbeat','network','1081348823','',false,'{}')`, n.ServerGroup, n.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.Pool().QueryRow(ctx, `SELECT count(*) FROM qq_outbox WHERE server_group=$1 AND kind='network'`, n.ServerGroup).Scan(&count); err != nil || count != 0 {
		t.Fatal("heartbeat queued", count, err)
	}
	// Simulate records written by the previous deployed version before migration.
	tx, err := s.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, statement := range []string{
		`ALTER TABLE qq_outbox DISABLE TRIGGER qq_suppress_network_health`,
		`INSERT INTO qq_outbox(id,server_group,network_id,source_key,kind,group_id,channel,state,attempt_id,lease_until,payload) VALUES('old-network-queued',$1,$2,'old-queued','network','1081348823','group','queued','',NULL,'{}'),('old-network-leased',$1,$2,'old-leased','network','1081348823','group','leased','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',now()+interval '90 seconds','{}')`,
		`ALTER TABLE qq_outbox ENABLE TRIGGER qq_suppress_network_health`,
	} {
		if strings.HasPrefix(statement, "INSERT") {
			_, err = tx.Exec(ctx, statement, n.ServerGroup, n.ID)
		} else {
			_, err = tx.Exec(ctx, statement)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.QQBeginDelivery(ctx, auth, "old-network-leased", strings.Repeat("a", 32)); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old lease authorized", err)
	}
	enqueueQQ(t, s, n)
	ack, err := s.QQBridgePoll(ctx, auth, store.QQBridgePoll{BotID: "12345678", Adapter: "snowluma"})
	if err != nil || len(ack.Deliveries) != 1 || ack.Deliveries[0].Kind != "craft" {
		t.Fatal(ack, err)
	}
	if err = s.Pool().QueryRow(ctx, `SELECT count(*) FROM qq_outbox WHERE kind='network' AND state='expired'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("pending announcements not stopped", count, err)
	}
	settings, err := s.QQSettings(ctx, a.ID, n.ServerGroup)
	if err != nil || !settings.Enabled || !settings.SendingEnabled || !settings.Alarms || !settings.Exceptions || settings.Recoveries {
		t.Fatal("other notifications altered", settings, err)
	}
}

func TestQQLowEnergyCustomThresholdAndReconnectAreDistinct(t *testing.T) {
	s, a, n, _ := qqFixture(t)
	settings := plugins.Defaults()
	settings.LowPercent = 35
	settings.RecoverPercent = 40
	settings.LowDurationSeconds = 0
	if err := s.SetMonitoringPlugin(ctx, a.ID, n.ServerGroup, plugins.LSC, true, settings, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, `INSERT INTO monitor_collectors(id,server_group,name,token_hash,last_seen) VALUES('threshold-collector',$1,'Collector','\x0102',now())`, n.ServerGroup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, `INSERT INTO plugin_objects(id,server_group,plugin_id,collector_id,component_address,name) VALUES('threshold-object',$1,'gtnh.lsc','threshold-collector','address','LSC')`, n.ServerGroup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, `INSERT INTO monitor_latest(object_id,session_id,stored_eu,capacity_eu,observed_at) VALUES('threshold-object','session',35,100,now())`); err != nil {
		t.Fatal(err)
	}
	maintain := func(want int) {
		t.Helper()
		if err := s.MaintainMonitoring(ctx); err != nil {
			t.Fatal(err)
		}
		out, err := s.QQDeliveries(ctx, a.ID, n.ServerGroup)
		if err != nil || len(out) != want {
			t.Fatal(out, err)
		}
	}
	maintain(0) // Exactly on the low boundary is not below it.
	if _, err := s.Pool().Exec(ctx, `UPDATE monitor_collectors SET last_seen=now()-interval '1 day' WHERE id='threshold-collector'`); err != nil {
		t.Fatal(err)
	}
	maintain(0)
	if _, err := s.Pool().Exec(ctx, `UPDATE monitor_collectors SET last_seen=now() WHERE id='threshold-collector'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, `UPDATE monitor_latest SET stored_eu=34,observed_at=now() WHERE object_id='threshold-object'`); err != nil {
		t.Fatal(err)
	}
	maintain(1)
	maintain(1)
	if _, err := s.Pool().Exec(ctx, `UPDATE monitor_collectors SET last_seen=now()-interval '1 day' WHERE id='threshold-collector'`); err != nil {
		t.Fatal(err)
	}
	maintain(1)
	if _, err := s.Pool().Exec(ctx, `UPDATE monitor_collectors SET last_seen=now() WHERE id='threshold-collector'`); err != nil {
		t.Fatal(err)
	}
	maintain(1) // Same low-energy event survives a reconnect without another @.
	if _, err := s.Pool().Exec(ctx, `UPDATE monitor_latest SET stored_eu=37,observed_at=now() WHERE object_id='threshold-object'`); err != nil {
		t.Fatal(err)
	}
	maintain(1) // The recovery boundary must be reached.
	if _, err := s.Pool().Exec(ctx, `UPDATE monitor_latest SET stored_eu=40,observed_at=now() WHERE object_id='threshold-object'`); err != nil {
		t.Fatal(err)
	}
	maintain(1)
	var other int
	if err := s.Pool().QueryRow(ctx, `SELECT count(*) FROM monitor_alerts WHERE kind IN ('collector_offline','read_error')`).Scan(&other); err != nil || other != 0 {
		t.Fatal(other, err)
	}
	for _, kind := range []string{"collector_offline", "read_error", "falling", "capacity_changed"} {
		if _, err := s.Pool().Exec(ctx, `INSERT INTO monitor_alerts(id,object_id,kind,state,detail) VALUES($1,'threshold-object',$2,'active','diagnostic')`, "ignored-"+kind, kind); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.QQDeliveries(ctx, a.ID, n.ServerGroup)
	if err != nil || len(out) != 1 {
		t.Fatal("diagnostic alert reached QQ", out, err)
	}
	// Recovery rearms the rule even though no recovery notification is sent.
	if _, err = s.Pool().Exec(ctx, `UPDATE monitor_latest SET stored_eu=34,observed_at=now() WHERE object_id='threshold-object'`); err != nil {
		t.Fatal(err)
	}
	maintain(2)
}
func TestQQRecoveryCannotBeEnabledByAnOldBrowser(t *testing.T) {
	s, a, n, _ := qqFixture(t)
	settings, err := s.QQSettings(ctx, a.ID, n.ServerGroup)
	if err != nil {
		t.Fatal(err)
	}
	settings.Recoveries = true
	settings.Groups = nil
	if err = s.SetQQSettings(ctx, a.ID, n.ServerGroup, settings); err != nil {
		t.Fatal(err)
	}
	settings, err = s.QQSettings(ctx, a.ID, n.ServerGroup)
	if err != nil || settings.Recoveries {
		t.Fatal(settings, err)
	}
}
