package api_test

import (
	"context"
	"github.com/CaeliaEve/ae-orbit/internal/plugins"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"strings"
	"testing"
)

func TestMonitorOneSecondLiveAndSparseHistory(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	base := "/api/v1/monitoring/groups/" + f.network.ServerGroup
	settings := plugins.Defaults()
	settings.IntervalSeconds = 1
	settings.HistorySeconds = 10
	if err := f.db.SetMonitoringPlugin(ctx, f.admin.ID, f.network.ServerGroup, plugins.LSC, true, settings, 0); err != nil {
		t.Fatal(err)
	}
	collector, token, err := f.db.CreateMonitorCollector(ctx, f.admin.ID, f.network.ServerGroup, "fast LSC")
	if err != nil {
		t.Fatal(err)
	}
	h := store.MonitorHello{BootID: strings.Repeat("a", 32), Modules: []string{plugins.LSC}, Components: []store.MonitorComponent{{Address: "11111111-1111-1111-1111-111111111111", Type: "LSC", Supported: true}}}
	w := decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/hello", token, wire(t, h)), 200)
	id, err := f.db.SetMonitorObject(ctx, f.admin.ID, f.network.ServerGroup, "", store.MonitorObjectInput{PluginID: plugins.LSC, CollectorID: collector.ID, Address: h.Components[0].Address, Name: "LSC", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	poll := store.MonitorPoll{Session: w.Session, Sequence: 1, FreeMemory: 1048576}
	receipt := decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/poll", token, wire(t, poll)), 200)
	if receipt.Objects[0].Interval != 10 || receipt.PollSeconds != 5 {
		t.Fatal("old collector received unsupported fast interval", receipt)
	}
	h.BootID = strings.Repeat("b", 32)
	h.MinInterval = 1
	w = decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/hello", token, wire(t, h)), 200)
	if w.Objects[0].Interval != 1 || w.PollSeconds != 1 {
		t.Fatal("fast handshake failed", w)
	}
	poll = store.MonitorPoll{Session: w.Session, Sequence: 1, FreeMemory: 1048576, Samples: []store.MonitorSample{{ObjectID: id, Revision: 1, InstallationRevision: 1, SampleMillis: 1000, Stored: "100", Capacity: "1000"}}}
	for i := 1; i <= 6; i++ {
		if i > 1 {
			if _, err = f.db.Pool().Exec(ctx, `UPDATE monitor_latest SET reported_at=now()-interval '1 second' WHERE object_id=$1`, id); err != nil {
				t.Fatal(err)
			}
		}
		poll.Sequence = int64(i)
		poll.Samples[0].SampleMillis = int64(i * 1000)
		poll.Samples[0].Stored = string(rune('0'+i)) + "00"
		decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/poll", token, wire(t, poll)), 200)
	}
	objects := decode[[]store.MonitorObject](t, send(t, f, "GET", base+"/objects", adminToken, ""), 200)
	if objects[0].Stored != "600" || objects[0].SamplingInterval != 1 || objects[0].HistoryInterval != 10 {
		t.Fatal(objects)
	}
	var count int
	f.db.Pool().QueryRow(ctx, `SELECT count(*) FROM monitor_energy_records WHERE object_id=$1`, id).Scan(&count)
	if count != 1 {
		t.Fatal("each fast live sample persisted", count)
	}
	if _, err = f.db.Pool().Exec(ctx, `UPDATE monitor_latest SET reported_at=now()-interval '1 second',last_recorded_at=now()-interval '11 seconds' WHERE object_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	poll.Sequence++
	poll.Samples[0].SampleMillis += 1000
	decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/poll", token, wire(t, poll)), 200)
	decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/poll", token, wire(t, poll)), 200)
	f.db.Pool().QueryRow(ctx, `SELECT count(*) FROM monitor_energy_records WHERE object_id=$1`, id).Scan(&count)
	if count != 2 {
		t.Fatal("cadence or replay failed", count)
	}
	// Capacity changes are retained immediately even before the history deadline.
	f.db.Pool().Exec(ctx, `UPDATE monitor_latest SET reported_at=now()-interval '1 second' WHERE object_id=$1`, id)
	poll.Sequence++
	poll.Samples[0].SampleMillis += 1000
	poll.Samples[0].Capacity = "2000"
	decode[store.MonitorWelcome](t, send(t, f, "POST", "/bridge/monitor/poll", token, wire(t, poll)), 200)
	f.db.Pool().QueryRow(ctx, `SELECT count(*) FROM monitor_energy_records WHERE object_id=$1`, id).Scan(&count)
	if count != 3 {
		t.Fatal("capacity transition lost", count)
	}
}
