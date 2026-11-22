package store_test

import (
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestSubnetDiscoveryIsBoundedAndOnlyListsPositiveObservedStock(t *testing.T) {
	s, a, n, _ := qqFixture(t)
	if _, err := s.Pool().Exec(ctx, `INSERT INTO subnet_plugins(server_group,enabled) VALUES($1,true)`, n.ServerGroup); err != nil {
		t.Fatal(err)
	}
	ids := []string{strings.Repeat("1", 32), strings.Repeat("2", 32), strings.Repeat("3", 32)}
	for i, id := range ids {
		kind, name, label := "item", "minecraft:stone", "石头"
		if i == 1 {
			kind, name, label = "fluid", "water", "水"
		}
		if i == 2 {
			name, label = "minecraft:dirt", "泥土"
		}
		if _, err := s.Pool().Exec(ctx, `INSERT INTO subnet_catalog(id,kind,name,damage,label,search_label,scan_order) VALUES($1,$2,$3,0,$4,'shitou st',$5)`, id, kind, name, label, i+1); err != nil {
			t.Fatal(err)
		}
	}
	input := store.SubnetInput{Name: "子网", Address: "11111111-1111-1111-1111-111111111111", Enabled: true, Interval: 1, History: 10, Stale: 30}
	id, token, err := s.CreateSubnet(ctx, a.ID, n.ServerGroup, input)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := s.AuthMonitor(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.SubnetHello(ctx, auth, store.SubnetHello{BootID: strings.Repeat("a", 32), DiscoveryCapable: true})
	if err != nil {
		t.Fatal(err)
	}
	p := store.SubnetPoll{Session: w.Session, FreeMemory: 1048576}
	poll := func(samples []store.SubnetResult) store.SubnetWelcome {
		t.Helper()
		p.Sequence++
		p.Samples = samples
		w, err := s.SubnetPoll(ctx, auth, p)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	force := func() {
		t.Helper()
		if _, err := s.Pool().Exec(ctx, `UPDATE server_groups SET next_scan_at=now()-interval '1 second' WHERE name=$1`, n.ServerGroup); err != nil {
			t.Fatal(err)
		}
	}
	poll(nil)
	force()
	w = poll(nil)
	if len(w.Reads) != 3 || w.Budget != 3 {
		t.Fatal("discovery didn't run for empty watch list", w)
	}
	positive, zero := protocol.Quantity(42), protocol.Quantity(0)
	samples := []store.SubnetResult{}
	for i, r := range w.Reads {
		if r.Purpose != "discovery" {
			t.Fatal("unmarked discovery assignment")
		}
		q, e := &positive, ""
		if i == 1 {
			q = &zero
		}
		if i == 2 {
			q = nil
			e = "read_failed"
		}
		samples = append(samples, store.SubnetResult{ItemID: r.ItemID, AssignmentID: r.AssignmentID, Revision: r.Revision, SampleMillis: 100, Quantity: q, Error: e})
	}
	poll(samples)
	page, err := s.SubnetDiscovered(ctx, a.ID, n.ServerGroup, id, "item", "@minecraft shitou", 0)
	if err != nil || len(page.Items) != 1 || page.Items[0].Quantity != 42 || page.Total != 1 || page.Status.Checked != 3 || page.Status.Failed != 1 {
		t.Fatal("discovery advertised unknown/zero/catalog entries", page, err)
	}
	view, err := s.SubnetView(ctx, a.ID, n.ServerGroup, id)
	if err != nil || len(view.Items) != 0 || !view.Discovery.Capable {
		t.Fatal("discovery selected monitoring automatically", view, err)
	}
	deliveries, err := s.QQDeliveries(ctx, a.ID, n.ServerGroup)
	if err != nil || len(deliveries) != 0 {
		t.Fatal("discovery sent alert", err)
	}
	repeat, err := s.SubnetPoll(ctx, auth, p)
	if err != nil || len(repeat.Reads) != 0 {
		t.Fatal("replay reissued scan", err)
	}
	if _, err = s.Pool().Exec(ctx, `UPDATE subnet_discovered SET next_read_at=now()-interval '1 second' WHERE subnet_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	force()
	w = poll(nil)
	if len(w.Reads) != 1 || w.Reads[0].ItemID != ids[0] {
		t.Fatal("did not refresh discovered positives", w)
	}
	r := w.Reads[0]
	poll([]store.SubnetResult{{ItemID: r.ItemID, AssignmentID: r.AssignmentID, Revision: r.Revision, SampleMillis: 200, Quantity: &zero}})
	page, err = s.SubnetDiscovered(ctx, a.ID, n.ServerGroup, id, "item", "", 0)
	if err != nil || page.Total != 0 {
		t.Fatal("depleted item remained in picker", page, err)
	}
	// Selecting a known identity creates its independent fast monitoring lane.
	if err = s.AddSubnetItem(ctx, a.ID, n.ServerGroup, id, ids[0]); err != nil {
		t.Fatal(err)
	}
	force()
	w = poll(nil)
	if len(w.Reads) != 1 || w.Reads[0].Purpose != "monitor" {
		t.Fatal("selected inventory not prioritized", w)
	}
	r = w.Reads[0]
	poll([]store.SubnetResult{{ItemID: r.ItemID, AssignmentID: r.AssignmentID, Revision: r.Revision, SampleMillis: 300, Quantity: &positive}})
	page, err = s.SubnetDiscovered(ctx, a.ID, n.ServerGroup, id, "item", "", 0)
	if err != nil || len(page.Items) != 1 || !page.Items[0].Monitored {
		t.Fatal("monitor observation did not update picker", page, err)
	}
	input.Revision = 1
	input.Address = "22222222-2222-2222-2222-222222222222"
	if err = s.UpdateSubnet(ctx, a.ID, n.ServerGroup, id, input); err != nil {
		t.Fatal(err)
	}
	page, err = s.SubnetDiscovered(ctx, a.ID, n.ServerGroup, id, "item", "", 0)
	if err != nil || page.Total != 0 {
		t.Fatal("old binding stock leaked", err)
	}
	outsider, _, err := s.CreateMember(ctx, a.ID, "outsider", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SubnetDiscovered(ctx, outsider.ID, n.ServerGroup, id, "item", "", 0); err == nil {
		t.Fatal("ungranted member saw child network")
	}
}

func TestSubnetDiscoveryNeverDuplicatesSelectedIdentity(t *testing.T) {
	s, a, n, _ := fixture(t)
	if _, err := s.Pool().Exec(ctx, `INSERT INTO subnet_plugins(server_group,enabled) VALUES($1,true)`, n.ServerGroup); err != nil {
		t.Fatal(err)
	}
	item := strings.Repeat("1", 32)
	if _, err := s.Pool().Exec(ctx, `INSERT INTO subnet_catalog(id,kind,name,damage,label,scan_order) VALUES($1,'item','minecraft:stone',0,'石头',1)`, item); err != nil {
		t.Fatal(err)
	}
	id, token, err := s.CreateSubnet(ctx, a.ID, n.ServerGroup, store.SubnetInput{Name: "子网", Address: "11111111-1111-1111-1111-111111111111", Enabled: true, Interval: 1, History: 10, Stale: 30})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := s.AuthMonitor(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.SubnetHello(ctx, auth, store.SubnetHello{BootID: strings.Repeat("a", 32), DiscoveryCapable: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AddSubnetItem(ctx, a.ID, n.ServerGroup, id, item); err != nil {
		t.Fatal(err)
	}
	p := store.SubnetPoll{Session: w.Session, FreeMemory: 1048576, Sequence: 1}
	if _, err = s.SubnetPoll(ctx, auth, p); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(ctx, `UPDATE server_groups SET next_scan_at=now()-interval '1 second' WHERE name=$1`, n.ServerGroup); err != nil {
		t.Fatal(err)
	}
	p.Sequence = 2
	w, err = s.SubnetPoll(ctx, auth, p)
	if err != nil || len(w.Reads) != 1 || w.Reads[0].Purpose != "monitor" {
		t.Fatal("identity assigned twice", w, err)
	}
	view, err := s.SubnetView(ctx, a.ID, n.ServerGroup, id)
	if err != nil || view.Discovery.Checked != 1 {
		t.Fatal("watched candidate did not advance discovery", view.Discovery, err)
	}
}
