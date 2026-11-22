package api_test

import (
	"context"
	"github.com/CaeliaEve/ae-orbit/internal/store"
	"strings"
	"testing"
)

func TestSubnetRoutesAndTokenIsolation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	group := f.network.ServerGroup
	base := "/api/v1/monitoring/groups/" + group + "/subnets"
	p := decode[store.SubnetPlugin](t, send(t, f, "GET", base+"/plugin", adminToken, ""), 200)
	if p.Enabled || p.Plugin.ID != "aeorbit.subnet" {
		t.Fatal(p)
	}
	if _, err := f.db.Pool().Exec(ctx, `INSERT INTO subnet_plugins(server_group,enabled) VALUES($1,true)`, group); err != nil {
		t.Fatal(err)
	}
	created := decode[map[string]string](t, send(t, f, "POST", base, adminToken, `{"name":"专用子网","enabled":true,"interval_seconds":1,"history_seconds":10,"stale_seconds":30}`), 201)
	h := store.SubnetHello{BootID: strings.Repeat("a", 32), Components: []store.MonitorComponent{}}
	decode[store.SubnetWelcome](t, send(t, f, "POST", "/bridge/subnet/hello", created["token"], wire(t, h)), 200)
	if send(t, f, "POST", "/bridge/monitor/hello", created["token"], wire(t, store.MonitorHello{BootID: h.BootID})).StatusCode == 200 {
		t.Fatal("cross-purpose token allowed")
	}
	if send(t, f, "POST", "/bridge/subnet/hello", f.device, wire(t, h)).StatusCode != 401 {
		t.Fatal("AE token entered subnet bridge")
	}
	decode[store.SubnetView](t, send(t, f, "GET", base+"/"+created["id"], adminToken, ""), 200)
	if send(t, f, "PUT", base+"/"+created["id"], adminToken, `{"name":"bad"}`).StatusCode != 400 {
		t.Fatal("invalid config accepted")
	}
	newToken := decode[map[string]string](t, send(t, f, "POST", base+"/"+created["id"]+"/token", adminToken, ""), 200)
	if newToken["token"] == created["token"] {
		t.Fatal("token unchanged")
	}
	if send(t, f, "POST", "/bridge/subnet/hello", created["token"], wire(t, h)).StatusCode != 401 {
		t.Fatal("old token still authenticates")
	}
}
