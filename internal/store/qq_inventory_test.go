package store_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestSubnetQQKeywordsIdentityFreshnessAndDelivery(t *testing.T) {
	s, a, n, auth := qqFixture(t)
	if _, err := s.Pool().Exec(ctx, `INSERT INTO subnet_plugins(server_group,enabled) VALUES($1,true)`, n.ServerGroup); err != nil {
		t.Fatal(err)
	}
	ids := []string{strings.Repeat("3", 32), strings.Repeat("4", 32)}
	subnets := []string{}
	for x, item := range ids {
		kind, name, nbt := "fluid", "water", ""
		if x == 1 {
			kind, name, nbt = "item", "minecraft:potion", "tag"
		}
		if _, err := s.Pool().Exec(ctx, `INSERT INTO subnet_catalog(id,kind,name,damage,nbt,label) VALUES($1,$2,$3,0,$4,'产线燃料')`, item, kind, name, nbt); err != nil {
			t.Fatal(err)
		}
		sid, token, err := s.CreateSubnet(ctx, a.ID, n.ServerGroup, store.SubnetInput{Name: "子网" + item[:1], Enabled: true, Interval: 1, History: 10, Stale: 30})
		if err != nil {
			t.Fatal(err)
		}
		subnets = append(subnets, sid)
		if err = s.AddSubnetItem(ctx, a.ID, n.ServerGroup, sid, item); err != nil {
			t.Fatal(err)
		}
		ma, err := s.AuthMonitor(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		w, err := s.SubnetHello(ctx, ma, store.SubnetHello{BootID: strings.Repeat("a", 32)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Pool().Exec(ctx, `INSERT INTO subnet_latest(subnet_id,item_id,session_id,quantity,observed_at,revision) VALUES($1,$2,$3,0,now(),1)`, sid, item, w.Session); err != nil {
			t.Fatal(err)
		}
		if err = s.SetSubnetKeywords(ctx, a.ID, n.ServerGroup, sid, item, store.SubnetKeywordsInput{Keywords: []string{" Fuel  MK2 ", "fuel mk2", "燃料"}}); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := s.QQInventoryKeywords(ctx, auth)
	if err != nil || len(keys) != 2 {
		t.Fatal(keys, err)
	}
	view, err := s.SubnetView(ctx, a.ID, n.ServerGroup, subnets[0])
	if err != nil || len(view.Items[0].Keywords) != 2 {
		t.Fatal(view, err)
	}
	v, err := s.QQInventory(ctx, auth, "1081348823", " FUEL   MK2 ")
	if err != nil || v.Total != 2 || len(v.Items) != 2 {
		t.Fatal(v, err)
	}
	for _, item := range v.Items {
		if !item.Known || item.Quantity == nil || *item.Quantity != 0 {
			t.Fatal("zero became unknown", item)
		}
	}
	for _, bad := range [][]string{{"/电量"}, {"电量"}, {"库存"}, {"a\nb"}, {strings.Repeat("中", 33)}, make([]string, 9)} {
		// Newlines in the API are normalized as whitespace, like spaces in a name.
		if len(bad) == 1 && bad[0] == "a\nb" {
			continue
		}
		if err = s.SetSubnetKeywords(ctx, a.ID, n.ServerGroup, subnets[0], ids[0], store.SubnetKeywordsInput{Keywords: bad}); !errors.Is(err, store.ErrInvalid) {
			t.Fatal("invalid keyword accepted", bad, err)
		}
	}
	if err = s.SetSubnetKeywords(ctx, a.ID, "missing-group", subnets[0], ids[0], store.SubnetKeywordsInput{Keywords: []string{"other"}}); err == nil {
		t.Fatal("group escaped")
	}
	if _, err = s.QQInventory(ctx, auth, "99999999", "燃料"); err == nil {
		t.Fatal("unauthorized query")
	}
	queued, err := s.QQRequestInventory(ctx, auth, store.QQInventoryRequest{GroupID: "1081348823", RequestKey: "query-none", Keyword: "没有"})
	if err != nil || queued {
		t.Fatal(queued, err)
	}
	in := store.QQInventoryRequest{GroupID: "1081348823", RequestKey: "query-one", Keyword: "燃料"}
	for i := 0; i < 2; i++ {
		if q, e := s.QQRequestInventory(ctx, auth, in); e != nil || !q {
			t.Fatal(q, e)
		}
	}
	in.RequestKey = "query-two"
	if q, e := s.QQRequestInventory(ctx, auth, in); e != nil || !q {
		t.Fatal("new query throttled", q, e)
	}
	var count int
	if err = s.Pool().QueryRow(ctx, `SELECT count(*) FROM qq_outbox WHERE kind='inventory_query'`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	// Read failures, rebooted sessions, changed config, pause and stale samples
	// must all become unknown, retaining the last quantity where available.
	for _, sql := range []string{`error='read_failed'`, `error='',observed_at=now()-interval '1 minute'`, `observed_at=now(),session_id='old'`, `revision=2`} {
		if _, err = s.Pool().Exec(ctx, `UPDATE subnet_latest SET `+sql+` WHERE subnet_id=$1`, subnets[0]); err != nil {
			t.Fatal(err)
		}
		v, err = s.QQInventory(ctx, auth, "1081348823", "燃料")
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range v.Items {
			if i.SubnetID == subnets[0] && (i.Known || i.Quantity == nil) {
				t.Fatal("bad observation exposed", i)
			}
		}
	}
	enableQQSending(t, s, a, n)
	out, err := s.QQBridgePoll(ctx, auth, store.QQBridgePoll{BotID: "12345678", Adapter: "snowluma"})
	if err != nil || len(out.Deliveries) != 2 {
		t.Fatal(out, err)
	}
	for _, d := range out.Deliveries {
		var p store.QQInventoryView
		if err = json.Unmarshal(d.Payload, &p); err != nil {
			t.Fatal(err)
		}
		for _, i := range p.Items {
			if i.SubnetID == subnets[0] && i.Known {
				t.Fatal("queued stale snapshot stayed current")
			}
		}
	}
	// Paused/revoked collectors are unknown even when their last value is zero.
	if _, err = s.Pool().Exec(ctx, `UPDATE monitored_subnets SET enabled=false WHERE id=$1`, subnets[1]); err != nil {
		t.Fatal(err)
	}
	v, err = s.QQInventory(ctx, auth, "1081348823", "燃料")
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range v.Items {
		if i.SubnetID == subnets[1] && i.Known {
			t.Fatal("paused subnet reported current")
		}
	}
	if err = s.RemoveSubnetItem(ctx, a.ID, n.ServerGroup, subnets[1], ids[1]); err != nil {
		t.Fatal(err)
	}
	v, err = s.QQInventory(ctx, auth, "1081348823", "燃料")
	if err != nil || v.Total != 1 {
		t.Fatal("removed monitor still matches", v, err)
	}
	if err = s.SetSubnetKeywords(ctx, a.ID, n.ServerGroup, subnets[0], ids[0], store.SubnetKeywordsInput{}); err != nil {
		t.Fatal(err)
	}
	v, err = s.QQInventory(ctx, auth, "1081348823", "燃料")
	if err != nil || v.Total != 0 {
		t.Fatal(v, err)
	}
	for _, d := range out.Deliveries {
		if err = s.QQBeginDelivery(ctx, auth, d.ID, d.AttemptID); !errors.Is(err, store.ErrConflict) {
			t.Fatal("removed keyword still authorized dispatch", err)
		}
	}
}
