local runner = require("aeorbit.runner")
local adapter = require("aeorbit.adapter")
local journal = require("aeorbit.journal")
local json = require("aeorbit.json")

local function fixture()
  local state={clock=0,grant=0,memory=131072,reads=0,recipes=0,scanned=0,polls=0,updates={}}
  local function raw(name)
    return {name=name,damage=0,label=name,hasTag=false,size=30859,isCraftable=true}
  end
  local a=adapter.new({
    allItems=function()
      local index=0
      return function()
        index=index+1;state.scanned=state.scanned+1
        if index<=12 then return raw("test:cold_"..index) end
      end
    end,
    getItemInNetwork=function(detail)
      state.reads=state.reads+1
      if state.read_error then error("lookup failed") end
      return raw(detail.name)
    end,
    getItemsInNetwork=function() error("full inventory array used") end,
    getCraftable=function() return nil end,
    getCraftables=function(filter)
      state.recipes=state.recipes+1
      if state.recipe_error then error("recipe failed") end
      return {{getStack=function() return raw(filter.name) end}}
    end,
  })
  local client={request_bytes=2048,response_bytes=4096}
  function client:post(path)
    if path=="/bridge/v1/boot-id" then return {boot_id="boot"} end
    return {protocol=1,session_id="session",inventory_ack="0",poll_ack="0",
      limits={batch_items=8,batch_bytes=2048,response_bytes=4096,cpus=2,poll_millis=1000}}
  end
  function client:send(path,body)
    assert(#body<=self.request_bytes,"request budget exceeded")
    local value=json.decode(body)
    if path=="/bridge/v1/poll" then
      state.polls=state.polls+1
      local verify=state.verify or {};state.verify=nil
      return {ack=value.sequence,commands=json.array({}),verify=json.array(verify),scan_budget=state.grant,poll_millis=1000}
    end
    assert(path=="/bridge/v1/inventory")
    if state.fail_upload then
      state.fail_upload=false;state.pending_body=body
      error({fatal=false,code="transport_failed"})
    end
    if state.pending_body then
      assert(state.pending_body==body,"verification retry changed its observation")
      state.pending_body=nil;state.retried=true
    end
    local bindings={}
    for index,update in ipairs(value.updates) do
      state.updates[#state.updates+1]=update
      if update.item then bindings[#bindings+1]={index=index,id=tostring(#state.updates+100)} end
    end
    return {ack=value.sequence,bindings=json.array(bindings)}
  end
  local files={}
  local log=journal.new({read=function(path) return files[path] end,
    write=function(path,value) files[path]=value;return true end},"priority",32768)
  local bridge=runner.new({cache_entries=4,cache_bytes=2048,dirty_entries=4,dirty_bytes=2048},
    {adapter=a,client=client,journal=log,now=function() return state.clock end,
      free_memory=function() return state.memory end,yield=function() end})
  bridge:start()
  local function verify(index,token)
    return {id=tostring(index),item={kind="item",name="test:target_"..index,damage=0,label="test:target_"..index,exact=true},craftable=true,token=token or string.format("%032x",index)}
  end
  local function flood()
    for index=1,128 do bridge:on_item_changed(raw("test:hot_"..index)) end
  end
  return state,bridge,verify,flood
end

local s,r,verify,flood=fixture()
s.verify={verify(1)};r:tick()
assert(s.reads==0 and s.recipes==0,"priority work escaped a zero grant")
flood();s.clock=1;s.grant=4;r:tick()
local found
for _,u in ipairs(s.updates) do if u.id=="1" then found=u end end
assert(found and found.quantity=="30859" and found.craftable==true and found.verification==verify(1).token,
  "ordinary event pressure lost the completed output or its fresh recipe confirmation")
assert(s.recipes==1,"priority verification skipped the actual recipe query")
assert(r.dirty.count<=4 and r.dirty.bytes<=2048)

-- Keep the oldest admitted request when repeated responses and overflow arrive.
local s,r,verify,flood=fixture()
for round=0,7 do
  s.verify={}
  for index=1,4 do s.verify[#s.verify+1]=verify(round*4+index) end
  r:tick()
end
s.verify={verify(1)};r:tick();flood();s.clock=5;s.grant=4;r:tick()
assert(s.updates[1] and s.updates[1].id=="1" and s.updates[1].verification==verify(1).token,
  "duplicate or overflowing priority requests displaced the oldest queued read")
assert(r.verifications.count<=16 and r.verifications.bytes<=4096,"priority queue exceeded its memory bound")

-- A failed upload retries exactly the same verified observation, without a new read.
local s,r,verify=fixture()
s.verify={verify(1)};s.grant=4;s.fail_upload=true;r:tick()
assert(s.pending_body and s.recipes==1)
s.grant=0;r:tick()
assert(s.retried and s.recipes==1 and s.updates[1].verification==verify(1).token,
  "transport recovery lost the verification or repeated native work")

-- Respect memory, native-call cost, recipe backoff, and normal scan progress.
local s,r,verify,flood=fixture()
s.verify={verify(1)};r:tick()
s.memory=8192;s.grant=32;r:tick()
assert(s.reads==0 and s.recipes==0,"priority reads ignored the low-memory reserve")
s.memory=131072;s.grant=3;r:tick()
assert(s.recipes==0 and r.verifications.count==1,"recipe verification exceeded a small grant")
s.grant=4;s.clock=5;r:tick()
assert(s.recipes==1)
s.verify={verify(2)};r:tick()
assert(s.recipes==1,"priority recipe queries bypassed the five-second interval")
for index=3,10 do
  s.clock=s.clock+5;s.verify={verify(index)};flood();r:tick()
end
assert(s.scanned>0 and s.polls>=10,"priority traffic starved scanning or control")

-- Lookup failure cannot acknowledge an unobserved quantity or an unknown recipe.
for _,failure in ipairs({"read_error","recipe_error"}) do
  local s,r,verify=fixture()
  s[failure]=true;s.verify={verify(1)};s.grant=4;r:tick()
  for _,u in ipairs(s.updates) do assert(not u.verification,"failed lookup acknowledged the verification") end
  s[failure]=false;s.clock=5;s.verify={verify(1)};r:tick();r:tick()
  local confirmed=false
  for _,u in ipairs(s.updates) do if u.verification==verify(1).token then confirmed=true end end
  assert(confirmed,"failed priority read did not recover on reissue")
end
