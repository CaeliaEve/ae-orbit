package api_test

import (
 "testing"
 "github.com/CaeliaEve/ae-orbit/internal/store"
)

func TestQQConnectorCredentialsCannotUseOCOrMemberAPIs(t *testing.T){
 f:=setup(t);base:="/api/v1/monitoring/groups/"+f.network.ServerGroup+"/qq"
 settings:=store.QQSettings{Enabled:true,Groups:nil}
 if send(t,f,"PUT",base,adminToken,wire(t,settings)).StatusCode!=204{t.Fatal("QQ configure failed")}
 token:=decode[map[string]string](t,send(t,f,"POST",base+"/token",adminToken,`{}`),200)["token"]
 if send(t,f,"PUT","/bridge/qq/v1/channels",token,`{"groups":[{"group_id":"1081348823","name":"Base"}],"allow_private":false}`).StatusCode!=204{t.Fatal("channel sync failed")}
 for _,path:=range []string{"/bridge/v2/boot-id","/bridge/monitor/hello"}{if send(t,f,"POST",path,token,`{}`).StatusCode!=401{t.Fatal("QQ token accessed game bridge",path)}}
 if send(t,f,"GET","/api/v1/me",token,"").StatusCode!=401{t.Fatal("QQ token became member")}
 if send(t,f,"POST","/bridge/qq/v1/poll",f.device,`{"bot_id":"12345678","adapter":"snowluma"}`).StatusCode!=401{t.Fatal("OC token became QQ connector")}
 v:=decode[store.QQBridgeAck](t,send(t,f,"POST","/bridge/qq/v1/poll",token,`{"bot_id":"12345678","adapter":"snowluma"}`),200)
 if !v.Enabled||v.SendingEnabled||len(v.Groups)!=1{t.Fatal(v)}
}
