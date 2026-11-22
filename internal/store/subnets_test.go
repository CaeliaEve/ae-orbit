package store_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/protocol"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"github.com/CaeliaEve/ae-orbit/internal/testutil"
)

func TestSubnetExactLifecycleAndAlerts(t *testing.T) {
	s, a, n, _ := qqFixture(t)
	// The fixture supplies catalog identities directly; no game enumeration.
	if _, err := s.Pool().Exec(ctx, `INSERT INTO subnet_plugins(server_group,enabled) VALUES($1,true);`, n.ServerGroup); err != nil {
		t.Fatal(err)
	}
	first, second := strings.Repeat("1", 32), strings.Repeat("2", 32)
	for i, id := range []string{first, second} {
		kind, name, label := "item", "minecraft:stone", "石头"
		if i == 1 {
			kind, name, label = "fluid", "water", "水"
		}
		if _, err := s.Pool().Exec(ctx, `INSERT INTO subnet_catalog(id,kind,name,damage,label,search_label) VALUES($1,$2,$3,0,$4,$5)`, id, kind, name, label, "shitou st"); err != nil {
			t.Fatal(err)
		}
	}
	address := "11111111-1111-1111-1111-111111111111"
	input := store.SubnetInput{Name: "生产线", Address: address, Enabled: true, Interval: 1, History: 10, Stale: 30}
	id, token, err := s.CreateSubnet(ctx, a.ID, n.ServerGroup, input)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := s.AuthMonitor(ctx, token)
	if err != nil || auth.Purpose != "subnet" {
		t.Fatal(err)
	}
	if _, err = s.MonitorHello(ctx, auth, store.MonitorHello{BootID: strings.Repeat("a", 32)}); err == nil {
		t.Fatal("subnet token entered LSC bridge")
	}
	cols, err := s.MonitorCollectors(ctx, a.ID, n.ServerGroup)
	if err != nil || len(cols) != 0 {
		t.Fatal("LSC collector list polluted", cols, err)
	}
	h := store.SubnetHello{BootID: strings.Repeat("a", 32), Components: []store.MonitorComponent{{Address: address, Type: "me_interface", Supported: true}}}
	w, err := s.SubnetHello(ctx, auth, h)
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range []string{first, second} {
		if err = s.AddSubnetItem(ctx, a.ID, n.ServerGroup, id, item); err != nil {
			t.Fatal(err)
		}
		target := store.SubnetNotification{QQNotification: protocol.QQNotification{GroupID: "1081348823"}, Recipients: []string{"987654321", "123456789", "987654321"}}
		if i == 1 {
			target.Recipients = []string{"123456789", "987654321"}
		}
		_, err = s.SetSubnetRule(ctx, a.ID, n.ServerGroup, id, "", store.SubnetRuleInput{ItemID: item, Kind: "low", Threshold: 100, Recover: 120, Enabled: true, Notification: &target})
		if err != nil {
			t.Fatal(err)
		}
	}
	poll := store.SubnetPoll{Session: w.Session, FreeMemory: 1048576}
	request := func(samples []store.SubnetResult) store.SubnetWelcome {
		t.Helper()
		poll.Sequence++
		poll.Samples = samples
		out, e := s.SubnetPoll(ctx, auth, poll)
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	force := func() {
		t.Helper()
		if _, e := s.Pool().Exec(ctx, `UPDATE server_groups SET next_scan_at=now()-interval '1 second' WHERE name=$1`, n.ServerGroup); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Pool().Exec(ctx, `UPDATE subnet_items SET next_read_at=now()-interval '1 second',assignment_until=NULL WHERE subnet_id=$1`, id); e != nil {
			t.Fatal(e)
		}
	}
	request(nil)
	force()
	w = request(nil)
	if len(w.Reads) != 2 || w.Budget != 2 {
		t.Fatal("no bounded exact assignments", w)
	}
	sample := func(read store.SubnetRead, q *protocol.Quantity, e string, ms int64) store.SubnetResult {
		return store.SubnetResult{ItemID: read.ItemID, AssignmentID: read.AssignmentID, Revision: read.Revision, Quantity: q, Error: e, SampleMillis: ms}
	}
	zero := protocol.Quantity(0)
	w = request([]store.SubnetResult{sample(w.Reads[0], &zero, "", 100), sample(w.Reads[1], &zero, "", 100)})
	view, e := s.SubnetView(ctx, a.ID, n.ServerGroup, id)
	if e != nil || len(view.Items) != 2 || !view.Items[0].Known || len(view.Alerts) != 2 {
		t.Fatal("zero must be known, low alerts", view, e)
	}
	deliveries, e := s.QQDeliveries(ctx, a.ID, n.ServerGroup)
	if e != nil || len(deliveries) != 1 || deliveries[0].Kind != "subnet" {
		t.Fatal("same poll not combined", deliveries, e)
	}
	var payload struct {
		Items      []json.RawMessage `json:"items"`
		Recipients []string          `json:"recipients"`
	}
	if e = json.Unmarshal(deliveries[0].Payload, &payload); e != nil || len(payload.Items) != 2 {
		t.Fatal("batch values missing", e)
	}
	if strings.Join(payload.Recipients, ",") != "123456789,987654321" || deliveries[0].Recipient != "" {
		t.Fatal("expected one delivery with distinct group mentions", deliveries[0], payload.Recipients)
	}
	replay, e := s.SubnetPoll(ctx, auth, poll)
	if e != nil || len(replay.Reads) != 0 || replay.Budget != 0 {
		t.Fatal("replay reissued work", replay, e)
	}
	force()
	w = request(nil)
	if len(w.Reads) != 2 {
		t.Fatal(w)
	}
	request([]store.SubnetResult{sample(w.Reads[0], nil, "read_failed", 200), sample(w.Reads[1], nil, "read_failed", 200)})
	view, e = s.SubnetView(ctx, a.ID, n.ServerGroup, id)
	if e != nil || view.Items[0].Known || view.Items[0].Quantity == nil {
		t.Fatal("failed query must be unknown with last value", view, e)
	}
	force()
	w = request(nil)
	q := protocol.Quantity(130)
	request([]store.SubnetResult{sample(w.Reads[0], &q, "", 300), sample(w.Reads[1], &q, "", 300)})
	deliveries, e = s.QQDeliveries(ctx, a.ID, n.ServerGroup)
	if e != nil || len(deliveries) != 1 {
		t.Fatal("recovery sent notification", deliveries, e)
	}
	view, e = s.SubnetView(ctx, a.ID, n.ServerGroup, id)
	if e != nil || view.Rules[0].Active {
		t.Fatal("recovery didn't clear alert", e)
	}
	// Offline time must not count towards the duration of a new condition.
	rule := view.Rules[0]
	rule.Duration = 10
	rule.Revision = view.Rules[0].Revision
	rule.Notification = nil
	if _, e = s.SetSubnetRule(ctx, a.ID, n.ServerGroup, id, rule.ID, rule.SubnetRuleInput); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Pool().Exec(ctx, `UPDATE subnet_rules SET since=now()-interval '20 seconds' WHERE id=$1`, rule.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Pool().Exec(ctx, `UPDATE subnet_latest SET observed_at=now()-interval '40 seconds' WHERE subnet_id=$1`, id); e != nil {
		t.Fatal(e)
	}
	force()
	w = request(nil)
	request([]store.SubnetResult{sample(w.Reads[0], &zero, "", 400), sample(w.Reads[1], &zero, "", 400)})
	view, e = s.SubnetView(ctx, a.ID, n.ServerGroup, id)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range view.Rules {
		if r.ID == rule.ID && r.Active {
			t.Fatal("stale gap counted as continuous low stock")
		}
	}
	input.Revision = 1
	input.Address = "22222222-2222-2222-2222-222222222222"
	if e = s.UpdateSubnet(ctx, a.ID, n.ServerGroup, id, input); e != nil {
		t.Fatal(e)
	}
	view, e = s.SubnetView(ctx, a.ID, n.ServerGroup, id)
	if e != nil || view.Items[0].Known {
		t.Fatal("old interface value remained current", e)
	}
	if e = s.RemoveSubnetItem(ctx, a.ID, n.ServerGroup, id, first); e != nil {
		t.Fatal(e)
	}
	view, e = s.SubnetView(ctx, a.ID, n.ServerGroup, id)
	if e != nil || len(view.Items) != 1 {
		t.Fatal("item wasn't removed", e)
	}
	history, e := s.SubnetHistory(ctx, a.ID, n.ServerGroup, id, first)
	if e != nil || len(history) == 0 {
		t.Fatal("removal erased history", e)
	}
	replacement, e := s.ResetSubnetToken(ctx, a.ID, n.ServerGroup, id)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AuthMonitor(ctx, token); !errors.Is(e, store.ErrUnauthorized) {
		t.Fatal("old token still works", e)
	}
	if _, e = s.AuthMonitor(ctx, replacement); e != nil {
		t.Fatal(e)
	}
	outsider, _, e := s.CreateMember(ctx, a.ID, "outsider", nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SubnetView(ctx, outsider.ID, n.ServerGroup, id); !errors.Is(e, store.ErrForbidden) {
		t.Fatal("cross-group view allowed", e)
	}
}

func TestSubnetNESQLCatalogImport(t *testing.T) {
	root := os.Getenv("AE_TEST_PLAIN_CATALOG_ROOT")
	if root == "" {
		root = writeResourceCatalogFixture(t)
	}
	o := store.DefaultOptions()
	o.CatalogRoot = root
	s := testutil.DatabaseWithOptions(t, o)
	a, err := s.Bootstrap(ctx, adminToken)
	if err != nil {
		t.Fatal(err)
	}
	n, _, err := s.CreateNetwork(ctx, a.ID, "Catalog", "catalog-subnet", "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSubnetPlugin(ctx, a.ID, n.ServerGroup, true, 0); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"item", "fluid"} {
		items, e := s.SearchSubnetCatalog(ctx, a.ID, n.ServerGroup, kind, "")
		if e != nil || len(items) == 0 {
			t.Fatal("catalog missing", kind, e)
		}
	}
	words, e := s.SearchSubnetCatalog(ctx, a.ID, n.ServerGroup, "item", "@minecraft shitou")
	if e != nil || len(words) == 0 {
		t.Fatal("pinyin/mod search failed", e)
	}
	if err = s.SetSubnetPlugin(ctx, a.ID, n.ServerGroup, false, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSubnetPlugin(ctx, a.ID, n.ServerGroup, true, 2); err != nil {
		t.Fatal("idempotent catalog enable", err)
	}
}

func TestSubnetSharedBudgetIsBoundedAndFair(t *testing.T) {
	s, a, n, _ := fixture(t)
	if _, e := s.Pool().Exec(ctx, `INSERT INTO subnet_plugins(server_group,enabled) VALUES($1,true)`, n.ServerGroup); e != nil {
		t.Fatal(e)
	}
	item := strings.Repeat("1", 32)
	if _, e := s.Pool().Exec(ctx, `INSERT INTO subnet_catalog(id,kind,name,damage,label) VALUES($1,'item','minecraft:stone',0,'石头')`, item); e != nil {
		t.Fatal(e)
	}
	type node struct {
		id   string
		auth store.MonitorAuth
		poll store.SubnetPoll
	}
	nodes := []node{}
	for i := 0; i < 12; i++ {
		id, token, e := s.CreateSubnet(ctx, a.ID, n.ServerGroup, store.SubnetInput{Name: "子网", Address: "11111111-1111-1111-1111-111111111111", Enabled: true, Interval: 1, History: 10, Stale: 30})
		if e != nil {
			t.Fatal(e)
		}
		auth, e := s.AuthMonitor(ctx, token)
		if e != nil {
			t.Fatal(e)
		}
		w, e := s.SubnetHello(ctx, auth, store.SubnetHello{BootID: strings.Repeat("a", 32)})
		if e != nil {
			t.Fatal(e)
		}
		if e = s.AddSubnetItem(ctx, a.ID, n.ServerGroup, id, item); e != nil {
			t.Fatal(e)
		}
		p := store.SubnetPoll{Session: w.Session, Sequence: 1, FreeMemory: 1048576}
		if _, e = s.SubnetPoll(ctx, auth, p); e != nil {
			t.Fatal(e)
		}
		nodes = append(nodes, node{id, auth, p})
	}
	observed := map[string]bool{}
	for round := 0; round < 4; round++ {
		if _, e := s.Pool().Exec(ctx, `UPDATE server_groups SET next_scan_at=now()-interval '1 second' WHERE name=$1`, n.ServerGroup); e != nil {
			t.Fatal(e)
		}
		total := 0
		for i := range nodes {
			node := &nodes[i]
			node.poll.Sequence++
			w, e := s.SubnetPoll(ctx, node.auth, node.poll)
			if e != nil {
				t.Fatal(e)
			}
			total += w.Budget
			if w.Budget > 0 {
				observed[node.id] = true
			}
		}
		if total > 32 {
			t.Fatal("shared group credit exceeded", total)
		}
		var issued int
		if e := s.Pool().QueryRow(ctx, `SELECT sum(a.grant_budget) FROM subnet_read_intents a JOIN monitored_subnets n ON n.id=a.subnet_id WHERE n.server_group=$1`, n.ServerGroup).Scan(&issued); e != nil || issued > 32 {
			t.Fatal("allocated more than group budget", issued, e)
		}
		if _, e := s.Pool().Exec(ctx, `UPDATE subnet_items SET assignment_until=NULL`); e != nil {
			t.Fatal(e)
		}
	}
	if len(observed) != len(nodes) {
		t.Fatal("a subnet starved", len(observed))
	}
}
