package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/plugins"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestMonitorPluginIndependentLifecycleAndAuthorization(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	group := f.network.ServerGroup
	base := "/api/v1/monitoring/groups/" + group
	decode[[]store.PluginInstallation](t, send(t, f, "GET", base+"/plugins", adminToken, ""), 200)
	settings := plugins.Defaults()
	settings.LowDurationSeconds = 10
	configure := map[string]any{"enabled": true, "revision": 0, "settings": settings}
	if send(t, f, "PUT", base+"/plugins/gtnh.lsc", adminToken, wire(t, configure)).StatusCode != 204 {
		t.Fatal("plugin configuration failed")
	}
	if send(t, f, "PUT", base+"/plugins/gtnh.lsc", adminToken, wire(t, configure)).StatusCode != 409 {
		t.Fatal("stale config was accepted")
	}
	created := decode[struct {
		Collector store.MonitorCollector `json:"collector"`
		Token     string                 `json:"token"`
	}](t, send(t, f, "POST", base+"/collectors", adminToken, `{"name":"energy"}`), 201)
	if send(t, f, "POST", "/bridge/v2/boot-id", created.Token, `{}`).StatusCode != 401 {
		t.Fatal("monitor token accessed AE bridge")
	}
	if send(t, f, "POST", "/bridge/monitor/hello", f.device, `{}`).StatusCode != 401 {
		t.Fatal("AE token accessed monitor bridge")
	}
	address := "11111111-1111-1111-1111-111111111111"
	hello := store.MonitorHello{BootID: strings.Repeat("a", 32), Modules: []string{plugins.LSC}, Components: []store.MonitorComponent{{Address: address, Type: "LSC", Supported: true}}}
	welcome := decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/hello", created.Token, wire(t, hello)), 200)
	object := store.MonitorObjectInput{PluginID: plugins.LSC, CollectorID: created.Collector.ID, Address: address, Name: "base energy", Location: "180:1,2,3", Enabled: true}
	binding := decode[map[string]string](t, send(t, f, "POST", base+"/objects", adminToken, wire(t, object)), 200)
	id := binding["id"]
	if send(t, f, "POST", base+"/objects", adminToken, wire(t, object)).StatusCode != 409 {
		t.Fatal("duplicate binding accepted")
	}
	member, token, err := f.db.CreateMember(ctx, f.admin.ID, "viewer", nil)
	if err != nil {
		t.Fatal(err)
	}
	if send(t, f, "GET", base+"/objects", token, "").StatusCode != 403 {
		t.Fatal("ungranted viewer accessed monitoring")
	}
	if err = f.db.SetGrant(ctx, f.admin.ID, member.ID, protocol.Grant{NetworkID: f.network.ID}); err != nil {
		t.Fatal(err)
	}
	if send(t, f, "GET", base+"/objects", token, "").StatusCode != 200 || send(t, f, "POST", base+"/collectors", token, `{"name":"unauthorized"}`).StatusCode != 403 {
		t.Fatal("viewer authority not enforced")
	}
	huge := "1234567890123456789012345678901234567890"
	cap := "12345678901234567890123456789012345678900"
	sample := store.MonitorSample{ObjectID: id, Revision: 1, InstallationRevision: 1, SampleMillis: 10000, Stored: huge, Capacity: cap}
	poll := store.MonitorPoll{Session: welcome.Session, Sequence: 1, FreeMemory: 1048576, Samples: []store.MonitorSample{sample}}
	receipt := decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/poll", created.Token, wire(t, poll)), 200)
	if len(receipt.Objects) != 1 {
		t.Fatal(receipt)
	}
	decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/poll", created.Token, wire(t, poll)), 200)
	var count int
	if err = f.db.Pool().QueryRow(ctx, `SELECT count(*) FROM monitor_energy_records WHERE object_id=$1`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("receipt replay duplicated history", count, err)
	}
	objects := decode[[]store.MonitorObject](t, send(t, f, "GET", base+"/objects", adminToken, ""), 200)
	if len(objects) != 1 || objects[0].Stored != huge || objects[0].Percent != "10.00" {
		t.Fatal(objects)
	}
	if err = f.db.MaintainMonitoring(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Pool().Exec(ctx, `UPDATE monitor_conditions SET since=now()-interval '1 minute' WHERE object_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = f.db.MaintainMonitoring(ctx); err != nil {
		t.Fatal(err)
	}
	alerts := decode[[]store.MonitorAlert](t, send(t, f, "GET", base+"/alerts", adminToken, ""), 200)
	if len(alerts) != 1 || alerts[0].Kind != "low_energy" {
		t.Fatal(alerts)
	}
	send(t, f, "POST", base+"/alerts/"+alerts[0].ID+"/acknowledge", adminToken, `{}`)
	alerts = decode[[]store.MonitorAlert](t, send(t, f, "GET", base+"/alerts", adminToken, ""), 200)
	if alerts[0].State != "active" || alerts[0].AcknowledgedAt == nil {
		t.Fatal("ack falsely recovered low energy", alerts)
	}
	if _, err = f.db.Pool().Exec(ctx, `UPDATE monitor_latest SET reported_at=now()-interval '20 seconds' WHERE object_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	poll.Sequence = 2
	poll.Samples[0].SampleMillis = 20000
	poll.Samples[0].Stored = "4000000000000000000000000000000000000000"
	decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/poll", created.Token, wire(t, poll)), 200)
	if err = f.db.MaintainMonitoring(ctx); err != nil {
		t.Fatal(err)
	}
	alerts = decode[[]store.MonitorAlert](t, send(t, f, "GET", base+"/alerts", adminToken, ""), 200)
	if alerts[0].State != "resolved" {
		t.Fatal(alerts)
	}
	history := decode[store.MonitorHistory](t, send(t, f, "GET", base+"/objects/"+id+"/history", adminToken, ""), 200)
	if len(history.Points) != 2 || history.Points[0].Stored != huge {
		t.Fatal(history)
	}
	// Late delivery must be ACKed without becoming a fresh energy observation.
	if _, err = f.db.Pool().Exec(ctx, `UPDATE monitor_sessions SET last_sample_at=now()-interval '5 minutes' WHERE id=$1`, welcome.Session); err != nil {
		t.Fatal(err)
	}
	poll.Sequence = 3
	poll.Samples[0].SampleMillis = 25000
	decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/poll", created.Token, wire(t, poll)), 200)
	if err = f.db.Pool().QueryRow(ctx, `SELECT count(*) FROM monitor_energy_records WHERE object_id=$1`, id).Scan(&count); err != nil || count != 2 {
		t.Fatal("late sample became live", count, err)
	}
	// Inconsistent capacities fail without accepting a sequence or deleting data.
	poll.Sequence = 4
	poll.Samples[0].Capacity = "1"
	if send(t, f, "POST", "/bridge/monitor/poll", created.Token, wire(t, poll)).StatusCode != 400 {
		t.Fatal("inconsistent energy accepted")
	}
	poll.Samples[0].Capacity = cap
	if _, err = f.db.Pool().Exec(ctx, `UPDATE monitor_latest SET reported_at=now()-interval '20 seconds' WHERE object_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	// Failure preserves the last valid stock and does not manufacture zero.
	poll.Samples[0].Stored = ""
	poll.Samples[0].Capacity = ""
	poll.Samples[0].Error = "component_missing"
	poll.Samples[0].SampleMillis = 30000
	decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/poll", created.Token, wire(t, poll)), 200)
	objects = decode[[]store.MonitorObject](t, send(t, f, "GET", base+"/objects", adminToken, ""), 200)
	if objects[0].Stored != "4000000000000000000000000000000000000000" || objects[0].Status != "read_error" {
		t.Fatal(objects)
	}
	if _, err = f.db.Pool().Exec(ctx, `UPDATE monitor_latest SET reported_at=now()-interval '20 seconds' WHERE object_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	poll.Sequence = 5
	poll.Samples[0].Error = ""
	poll.Samples[0].Stored = huge
	poll.Samples[0].Capacity = "24691357802469135780246913578024691357800"
	poll.Samples[0].SampleMillis = 40000
	decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/poll", created.Token, wire(t, poll)), 200)
	objects = decode[[]store.MonitorObject](t, send(t, f, "GET", base+"/objects", adminToken, ""), 200)
	if objects[0].Epoch <= 1 {
		t.Fatal("failure and capacity transition did not start a fresh epoch")
	}
	if _, err = f.db.Pool().Exec(ctx, `UPDATE monitor_collectors SET last_seen=now()-interval '2 minutes' WHERE id=$1`, created.Collector.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.db.MaintainMonitoring(ctx); err != nil {
		t.Fatal(err)
	}
	alerts = decode[[]store.MonitorAlert](t, send(t, f, "GET", base+"/alerts", adminToken, ""), 200)
	if alerts[0].Kind != "collector_offline" {
		t.Fatal(alerts)
	}
	// Disabling the plugin removes assignments and resolves active alerts.
	configure["enabled"] = false
	configure["revision"] = 1
	send(t, f, "PUT", base+"/plugins/gtnh.lsc", adminToken, wire(t, configure))
	poll.Sequence = 6
	poll.Samples = nil
	receipt = decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/poll", created.Token, wire(t, poll)), 200)
	if len(receipt.Objects) != 0 {
		t.Fatal(receipt)
	}
	if _, err = f.db.Pool().Exec(ctx, `UPDATE monitor_energy_records SET observed_at=now()-interval '90 days' WHERE object_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = f.db.PruneMonitorHistory(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.db.Pool().QueryRow(ctx, `SELECT count(*) FROM monitor_energy_records WHERE object_id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatal("raw retention", count, err)
	}
	var rollups int
	if err = f.db.Pool().QueryRow(ctx, `SELECT count(*) FROM monitor_energy_rollups WHERE object_id=$1`, id).Scan(&rollups); err != nil || rollups == 0 {
		t.Fatal("rollups removed", err)
	}
	hello.BootID = strings.Repeat("b", 32)
	decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/hello", created.Token, wire(t, hello)), 200)
	if send(t, f, "POST", "/bridge/monitor/poll", created.Token, wire(t, poll)).StatusCode != 409 {
		t.Fatal("retired session accepted")
	}
	rotated := decode[map[string]string](t, send(t, f, "POST", base+"/collectors/"+created.Collector.ID+"/token", adminToken, `{}`), 200)
	if rotated["token"] == "" || send(t, f, "POST", "/bridge/monitor/hello", created.Token, wire(t, hello)).StatusCode != 401 {
		t.Fatal("old token accepted")
	}
}
