package store_test

import (
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"strings"
	"testing"
)

func TestSubnetNativeSnapshotsReplaceOnlySuccessfulCompleteLists(t *testing.T) {
	s, a, n, _ := qqFixture(t)
	if _, err := s.Pool().Exec(ctx, `INSERT INTO subnet_plugins(server_group,enabled) VALUES($1,true)`, n.ServerGroup); err != nil {
		t.Fatal(err)
	}
	in := store.SubnetInput{Name: "独立子网", Address: "11111111-1111-1111-1111-111111111111", Enabled: true, Interval: 1, History: 10, Stale: 30, DiscoveryMode: "native"}
	id, token, err := s.CreateSubnet(ctx, a.ID, n.ServerGroup, in)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := s.AuthMonitor(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.SubnetHello(ctx, auth, store.SubnetHello{BootID: strings.Repeat("a", 32), DiscoveryCapable: true, SnapshotCapable: true})
	if err != nil {
		t.Fatal(err)
	}
	p := store.SubnetPoll{Session: w.Session, FreeMemory: 1048576}
	poll := func(snapshot *store.SubnetSnapshotResult) store.SubnetWelcome {
		t.Helper()
		p.Sequence++
		p.Snapshot = snapshot
		out, e := s.SubnetPoll(ctx, auth, p)
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	w = poll(nil)
	if w.Snapshot == nil || w.Snapshot.Kind != "item" || len(w.Reads) != 0 {
		t.Fatal("no native snapshot or catalog enumeration continued", w)
	}
	item := protocol.Definition{Kind: "item", Name: "minecraft:stone", Label: "Stone", Exact: true}
	value := func(a *store.SubnetSnapshotAssignment, items []store.SubnetSnapshotItem, e string) *store.SubnetSnapshotResult {
		return &store.SubnetSnapshotResult{ID: a.ID, Kind: a.Kind, Revision: a.Revision, Items: items, Error: e}
	}
	initial := w.Snapshot
	w = poll(value(initial, []store.SubnetSnapshotItem{{Item: item, Quantity: 24}}, ""))
	if w.Snapshot == nil || w.Snapshot.Kind != "fluid" {
		t.Fatal("fluid discovery missing", w)
	}
	replay, e := s.SubnetPoll(ctx, auth, p)
	if e != nil || replay.Snapshot != nil {
		t.Fatal("replay reissued snapshot", e)
	}
	fluid := protocol.Definition{Kind: "fluid", Name: "water", Label: "Water", Exact: true}
	w = poll(value(w.Snapshot, []store.SubnetSnapshotItem{{Item: fluid, Quantity: 1000}}, ""))
	if w.Snapshot != nil {
		t.Fatal("snapshot did not respect 30 second cadence")
	}
	page, e := s.SubnetDiscovered(ctx, a.ID, n.ServerGroup, id, "item", "", 0)
	if e != nil || page.Total != 1 || page.Status.Mode != "native" || !page.Status.Capable {
		t.Fatal(page, e)
	}
	selectedID := page.Items[0].ID
	if e = s.AddSubnetItem(ctx, a.ID, n.ServerGroup, id, selectedID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Pool().Exec(ctx, `UPDATE subnet_native_snapshots SET next_read_at=now()-interval '1 second' WHERE subnet_id=$1 AND kind='item'`, id); e != nil {
		t.Fatal(e)
	}
	w = poll(nil)
	if w.Snapshot == nil {
		t.Fatal("snapshot didn't become due")
	}
	w = poll(value(w.Snapshot, nil, "read_failed"))
	page, e = s.SubnetDiscovered(ctx, a.ID, n.ServerGroup, id, "item", "", 0)
	if e != nil || page.Total != 0 {
		t.Fatal("failed list remained current", page, e)
	}
	var retained int
	if e = s.Pool().QueryRow(ctx, `SELECT count(*) FROM subnet_discovered WHERE subnet_id=$1`, id).Scan(&retained); e != nil || retained != 2 {
		t.Fatal("failure erased stored observations", retained, e)
	}
	if _, e = s.Pool().Exec(ctx, `UPDATE subnet_native_snapshots SET next_read_at=now()-interval '1 second' WHERE subnet_id=$1 AND kind='item'`, id); e != nil {
		t.Fatal(e)
	}
	w = poll(nil)
	poll(value(w.Snapshot, []store.SubnetSnapshotItem{}, ""))
	page, e = s.SubnetDiscovered(ctx, a.ID, n.ServerGroup, id, "item", "", 0)
	if e != nil || page.Total != 0 {
		t.Fatal("empty list retained depleted item", page, e)
	}
	view, e := s.SubnetView(ctx, a.ID, n.ServerGroup, id)
	if e != nil || len(view.Items) != 1 {
		t.Fatal("snapshot removed selected monitor", view, e)
	}
	deliveries, e := s.QQDeliveries(ctx, a.ID, n.ServerGroup)
	if e != nil || len(deliveries) != 0 {
		t.Fatal("snapshot generated alert", e)
	}
	// Changing the binding makes in-flight snapshots and old stock obsolete.
	if _, e = s.Pool().Exec(ctx, `UPDATE subnet_native_snapshots SET next_read_at=now()-interval '1 second' WHERE subnet_id=$1 AND kind='item'`, id); e != nil {
		t.Fatal(e)
	}
	w = poll(nil)
	old := w.Snapshot
	in.Revision = 1
	in.Address = "22222222-2222-2222-2222-222222222222"
	if e = s.UpdateSubnet(ctx, a.ID, n.ServerGroup, id, in); e != nil {
		t.Fatal(e)
	}
	poll(value(old, []store.SubnetSnapshotItem{{Item: item, Quantity: 42}}, ""))
	page, e = s.SubnetDiscovered(ctx, a.ID, n.ServerGroup, id, "item", "", 0)
	if e != nil || page.Total != 0 {
		t.Fatal("stale binding snapshot accepted", page, e)
	}
}
