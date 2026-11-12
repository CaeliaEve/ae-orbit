local json=require("aeorbit.json")
local client=require("aeorbit.client")
local journal=require("aeorbit.journal")
local failures={}
local function test(name,run)
  local ok,reason=pcall(run)
  if not ok then failures[#failures+1]=name..": "..tostring(reason) end
end
local function setup(protocol)
  local s={nonce_requests=0,hello_bodies={},memory=131072,clock=0,writes=0}
  local files={}
  local log=journal.new({read=function(p) return files[p] end,write=function(p,v)
    files[p]=v;s.writes=s.writes+1;return true
  end},"lifecycle",32768)
  log:save({boot_id="old-boot",attempts={uncertain={state="needs_review"}},completed=json.array({})})
  s.old=log.data;s.initial_writes=s.writes
  local internet={request=function(url,body)
    local response
    if url:match("/boot%-id$") then
      s.nonce_requests=s.nonce_requests+1;response={boot_id="boot-"..s.nonce_requests}
    else
      assert(url:match("/hello$"),"unexpected transport after start")
      s.hello_bodies[#s.hello_bodies+1]=body
      if s.lose_hello then error("ack lost",0) end
      response={protocol=protocol,session_id="session",device_id="device",role="primary",
        inventory_ack="0",poll_ack="0",limits={batch_items=16,batch_bytes=4096,response_bytes=4096,
          cpus=2,read_slots=16,read_bytes=8192,poll_millis=1000,background_recipe_inconclusive=true}}
      if s.invalid_welcome then response.inventory_ack="1" end
      if s.mutate_welcome then s.mutate_welcome(response) end
    end
    local text=json.encode(response)
    return {finishConnect=function() return true end,response=function() return 200,nil,{["content-length"]=tostring(#text)} end,
      read=function() return text end,close=function() end}
  end}
  local adapter={capabilities=function()
    if s.capability_stop then error("interrupted",0) end
    return {inventory=true,craft=true}
  end,key=function(_,raw) return raw.name end,descriptor=function(_,raw) return raw end,
    definition=function(_,raw) return {kind="item",name=raw.name,damage=0,exact=true} end,
    read=function(_,raw) return {name=raw.name,size="42"} end,count=function() return "42" end,
    cpus=function() return {} end,iterator=function() return function() return nil end end,
    recipe_status=function() return true end}
  local r=require(protocol==1 and "aeorbit.runner" or "aeorbit.cooperative").new(
    {bridge_mode="cooperative",role="primary",max_response_bytes=4096},
    {client=client.new(internet,{url="https://lifecycle.test",token=string.rep("x",32)},
      {now=function() return s.clock end,sleep=function(t) s.clock=s.clock+t end}),
      adapter=adapter,journal=log,now=function() return s.clock end,free_memory=function() return s.memory end})
  return s,r,log,adapter
end

for _,protocol in ipairs({1,2}) do
  for _,case in ipairs({
    {field="poll_millis",value="bad"},{field="poll_millis",value=false},
    {field="poll_millis",value=0},{field="poll_millis",value=30001},{field="poll_millis",value=1.5},
    {field="batch_items",value=1.5},{field="batch_items",value=513},
    {field="batch_bytes",value=512.5},{field="batch_bytes",value=1048577},
    {field="response_bytes",value=-1},{field="response_bytes",value=511},
    {field="response_bytes",value=512.5},{field="cpus",value=1.5},
  }) do
    test("v"..protocol.." rejects welcome "..case.field.."="..tostring(case.value),function()
      local s,r,log=setup(protocol)
      s.mutate_welcome=function(w) w.limits[case.field]=case.value end
      local ok,err=pcall(r.start,r)
      assert(not ok and type(err)=="table" and err.fatal==true and err.code=="invalid_device_limits","malformed limit did not fail as protocol error")
      assert(log.data==s.old and s.writes==s.initial_writes,"malformed limit rewrote the journal")
      assert(not r.started and not r.tracker and not r.session,"malformed limit published session state")
    end)
  end
  test("v"..protocol.." empty session leaves journal intact",function()
    local s,r,log=setup(protocol)
    s.mutate_welcome=function(w) w.session_id="" end
    local ok,err=pcall(r.start,r)
    assert(not ok and type(err)=="table" and err.code=="invalid_welcome")
    assert(log.data==s.old and s.writes==s.initial_writes)
  end)
  test("v"..protocol.." lost hello preserves identity and exact body",function()
    local s,r,log=setup(protocol)
    s.lose_hello=true
    local ok,err=pcall(r.start,r)
    assert(not ok and type(err)=="table" and err.fatal==false,"lost response must be transient")
    assert(log.data==s.old and s.writes==s.initial_writes,"journal changed before welcome")
    s.lose_hello=false;s.memory=262144;r.config.source="changed after loss"
    r:start()
    assert(s.nonce_requests==1,"retry obtained another boot identity")
    assert(s.hello_bodies[1]==s.hello_bodies[2],"retry changed serialized hello")
    assert(log.data.boot_id=="boot-1" and not next(log.data.attempts),"validated welcome not committed")
    local tracking=r.tracker;local writes=s.writes
    r.pending_inventory={body="frozen observations"}
    r:start()
    assert(r.tracker==tracking and s.writes==writes and #s.hello_bodies==2,"repeat start reset active state")
    assert(r.pending_inventory.body=="frozen observations")
  end)
  test("v"..protocol.." invalid welcome leaves journal intact",function()
    local s,r,log=setup(protocol);s.invalid_welcome=true
    local ok,err=pcall(r.start,r)
    assert(not ok and err.code=="invalid_welcome")
    assert(log.data==s.old and s.writes==s.initial_writes)
  end)
  for _,boundary in ipairs({"descriptor","cpus","iterator","read","recipe_status"}) do
    test("v"..protocol.." stop survives "..boundary,function()
      local s,r,_,adapter=setup(protocol);r:start()
      local diagnostics=json.encode(r.diagnostics)
      local stop={code="interrupted",stop=true,fatal=true}
      adapter[boundary]=function() error(stop,0) end
      local raw={name="test:item",damage=0,size="0"}
      local run
      if boundary=="descriptor" then run=function() r:on_item_changed(raw) end
      elseif boundary=="cpus" then run=function() r:_control() end
      elseif protocol==1 then
        r.credits=4
        if boundary=="recipe_status" then r:_queue_verification(raw,string.rep("a",32))
        elseif boundary=="read" then r:on_item_changed(raw) end
        run=function() r:_next_raw() end
      else
        r.credits=4;r.grant_deadline=30
        if boundary=="iterator" then run=function() r:_discover() end
        else
          local read={id="1",assignment_id=string.rep("a",32),kind=boundary=="recipe_status" and "quantity_recipe" or "quantity",
            purpose="background",ttl_millis=30000}
          r:_admit(read,raw,0)
          run=function() r:_read_task() end
        end
      end
      local ok,err=pcall(run)
      assert(not ok and err==stop,"stop swallowed or reclassified")
      assert(json.encode(r.diagnostics)==diagnostics,"stop emitted a failure diagnostic")
      assert(not r.pending_inventory,"stop created inventory output")
    end)
  end
end
assert(#failures==0,table.concat(failures,"\n"))
