local json=require("aeorbit.json")
local failures={}
local function test(name,run)
  local ok,reason=pcall(run)
  if not ok then failures[#failures+1]=name..": "..tostring(reason) end
end
local item={kind="item",name="test:item",damage=0,label="Item",exact=true}
local function setup(protocol)
  local s,r
  if protocol==1 then s,r=require("inventory_fixture")({quantity_refresh=true})
  else s,r=require("cooperative_fixture")({role="primary"});r:start() end
  local record={order_id="old",attempt_id="old",state="done",reason=""}
  r.tracker.data.attempts.old=record
  r.pending_poll={body="{}",sequence="1",reports=json.array({record}),hints={}}
  local log=r.deps.journal
  local writes,crafts=0,0
  local save=log.save
  log.save=function(self,data) writes=writes+1;return save(self,data) end
  r.adapter.craft=function() crafts=crafts+1;return nil,"fixture failed craft" end
  return r,function()
    assert(writes==0 and crafts==0,"malformed response applied commands or journal acknowledgement")
    assert(r.tracker.data.attempts.old==record,"malformed response retired report")
    assert(r.poll_seq=="0" and r.pending_poll,"malformed response advanced control sequence")
    assert(r.ids.count==0 and (not r.tasks or r.tasks.count==0),"malformed response partially admitted identities")
  end
end
local function poll(protocol)
  local ack={ack="1",commands=json.array({}),scan_budget=0,poll_millis=1000}
  if protocol==1 then ack.verify=json.array({}) else ack.reads=json.array({}) end
  return ack
end
local cases={
  {name="scalar root",mutate=function() return false end},
  {name="scalar command",mutate=function(ack)
    ack.commands=json.array({{order_id="new",attempt_id="new",quantity="2",item=item},false});return ack
  end},
  {name="invalid command id",mutate=function(ack)
    ack.commands=json.array({{order_id="new",attempt_id=1,quantity="2",item=item}});return ack
  end},
  {name="fractional poll interval",mutate=function(ack) ack.poll_millis=1.5;return ack end},
}
for _,protocol in ipairs({1,2}) do
  for _,case in ipairs(cases) do
    test("v"..protocol.." poll "..case.name,function()
      local r,unchanged=setup(protocol)
      local ack=case.mutate(poll(protocol))
      r.client.send=function() return ack end
      local ok,err=pcall(r._control,r)
      assert(not ok and type(err)=="table" and err.fatal==true,"malformed poll did not fail as protocol error")
      unchanged()
    end)
  end
  for _,bad in ipairs({false,{index=2,id="bad"},{index=2,id="0"}}) do
    test("v"..protocol.." invalid later binding "..tostring(bad),function()
      local r=setup(protocol)
      r.pending_inventory={body="frozen",sequence="1",entries={{item=true,key="first",label="First"},{item=true,key="second",label="Second"}}}
      r.client.send=function() return {ack="1",bindings=json.array({{index=1,id="1"},bad})} end
      local ok,err=pcall(r._flush,r)
      assert(not ok and type(err)=="table" and err.fatal==true,"malformed binding did not fail as protocol error")
      assert(r.ids.count==0 and r.inventory_seq=="0" and r.pending_inventory.body=="frozen","malformed bindings partially committed")
    end)
  end
  test("v"..protocol.." scalar inventory acknowledgement",function()
    local r=setup(protocol)
    r.pending_inventory={body="frozen",sequence="1",entries={}}
    r.client.send=function() return false end
    local ok,err=pcall(r._flush,r)
    assert(not ok and type(err)=="table" and err.fatal==true,"scalar inventory acknowledgement escaped protocol handling")
  end)
end
for _,field in ipairs({"verify","refresh"}) do
  for _,bad in ipairs({false,{id="bad",item=item,token=string.rep("a",32),ttl_millis=1000}}) do
    test("v1 malformed "..field.." element "..tostring(bad),function()
      local r,unchanged=setup(1)
      local ack=poll(1)
      ack[field]=json.array({{id="1",item=item,token=string.rep("b",32),ttl_millis=1000},bad})
      r.client.send=function() return ack end
      local ok,err=pcall(r._control,r)
      assert(not ok and type(err)=="table" and err.fatal==true,"malformed element did not fail as protocol error")
      unchanged()
    end)
  end
end
test("v2 malformed read leaves commands unexecuted",function()
  local r,unchanged=setup(2)
  local ack=poll(2);ack.reads=json.array({false})
  ack.commands=json.array({{order_id="new",attempt_id="new",quantity="2",item=item}})
  r.client.send=function() return ack end
  local ok,err=pcall(r._control,r)
  assert(not ok and type(err)=="table" and err.fatal==true)
  unchanged()
end)
assert(#failures==0,table.concat(failures,"\n"))
