local runner = require("aeorbit.runner")
local adapter = require("aeorbit.adapter")
local journal = require("aeorbit.journal")
local json = require("aeorbit.json")

local function fixture(credits, read_seconds, batch_seconds)
  local clock=0
  local observed={items={},polls=0,scans=0,completed=0,lookups=0,reads=0,grant=credits,memory=131072}
  local function raw(name)
    return {name=name,damage=0,label="名称 "..name,hasTag=false,size=42,isCraftable=false}
  end
  local me={
    allItems=function()
      observed.scans=observed.scans+1
      local index=0
      return function()
        clock=clock+read_seconds;observed.reads=observed.reads+1;index=index+1
        if index<=12 then return raw("test:catalog_"..index) end
      end
    end,
    getItemsInNetwork=function() error("full inventory array used") end,
    getItemInNetwork=function(detail)
      clock=clock+read_seconds;observed.lookups=observed.lookups+1
      return raw(detail.name)
    end,
  }
  local ids,names,next_id={},{},0
  local client={request_bytes=2048,response_bytes=4096}
  function client:post(path,value)
    if path=="/bridge/v1/boot-id" then return {boot_id="boot"} end
    assert(path=="/bridge/v1/hello")
    return {protocol=1,session_id="session",inventory_ack="0",poll_ack="0",
      limits={batch_items=3,batch_bytes=2048,response_bytes=4096,cpus=2,poll_millis=1000}}
  end
  function client:send(path,body)
    assert(#body<=self.request_bytes,"request exceeded the negotiated limit")
    local request=json.decode(body)
    if path=="/bridge/v1/poll" then
      observed.polls=observed.polls+1
      return {ack=request.sequence,commands=json.array({}),verify=json.array({}),scan_budget=observed.grant,poll_millis=1000}
    end
    assert(path=="/bridge/v1/inventory")
    if observed.fail_inventory then
      local failure=observed.fail_inventory;observed.fail_inventory=nil;observed.failed_body=body
      error(failure)
    end
    if observed.failed_body then
      assert(body==observed.failed_body,"inventory retry changed the pending batch")
      observed.failed_body=nil;observed.retried=true
    end
    local bindings=json.array({})
    for index,update in ipairs(request.updates) do
      local id=update.id
      if update.item then
        local name=update.item.name
        if not ids[name] then next_id=next_id+1;ids[name]=tostring(next_id);names[ids[name]]=name end
        id=ids[name];bindings[#bindings+1]={index=index,id=id}
      end
      observed.items[assert(names[id])]=update.quantity
    end
    if request.scan_completed then observed.completed=observed.completed+1 end
    return {ack=request.sequence,bindings=bindings}
  end
  local files={}
  local log=journal.new({read=function(path) return files[path] end,
    write=function(path,value) files[path]=value;return true end},"fairness",32768)
  local bridge=runner.new({cache_entries=4,cache_bytes=2048,dirty_entries=8,dirty_bytes=4096,
    max_batch_bytes=2048,max_response_bytes=4096,scan_slice_seconds=0.05,scan_interval=60,batch_interval_seconds=batch_seconds},
    {adapter=adapter.new(me),client=client,journal=log,now=function() return clock end,
      free_memory=function() return observed.memory end,yield=function() end})
  bridge:start()
  local function tick()
    -- Keep the event queue nonempty, including when only one component read
    -- fits in this cycle. None of these hints describe the cold catalog items.
    for index=1,12 do
      local hint=raw("test:hot_"..index);hint.hasTag=0;hint.size=-1
      bridge:on_item_changed(hint)
    end
    clock=clock+1;bridge:tick()
    assert(bridge.dirty.count<=8 and bridge.dirty.bytes<=4096,"event memory grew without bound")
  end
  return observed,tick,bridge
end

-- Both a small server grant and OC callbacks that consume the wall-clock slice
-- must allow an entire scan to finish while real-time updates keep arriving.
for _,case in ipairs({{credits=1,read_seconds=0},{credits=32,read_seconds=0.06}}) do
  local observed,tick=fixture(case.credits,case.read_seconds)
  for _=1,48 do tick() end
  assert(observed.completed==1,"continuous inventory events starved the catalog scan")
  for index=1,12 do assert(observed.items["test:catalog_"..index]=="42","cold inventory item was never refreshed") end
  assert(observed.lookups>0 and observed.polls==48,"scanning starved live updates or control traffic")
  assert(observed.scans==1,"event pressure ignored the interval between completed scans")
end

local observed,tick=fixture(0,0)
for _=1,3 do tick() end
assert(observed.scans==0 and observed.lookups==0 and observed.polls==3,"acquisition bypassed the server budget")

-- A slow OC read ends a processing slice, but the remaining server grant is
-- still useful before the next control poll. Continuing must not obtain more
-- credit or turn the bounded slices into one long blocking acquisition.
local observed,_,bridge=fixture(6,0.06)
local status=bridge:tick()
assert(observed.reads==1,"a processing slice ignored its time budget")
for _=1,8 do
  local before=observed.reads
  if bridge.continue_inventory then bridge:continue_inventory() end
  assert(observed.reads-before<=1,"continued acquisition ignored its processing slice")
end
assert(observed.reads==6 and observed.polls==1,"granted reads were discarded between control polls")
assert(observed.items["test:catalog_6"]=="42","continued acquisition did not reach the backend")
assert(status.inventory_work,"the runner did not advertise remaining inventory work")

local observed,_,bridge=fixture(6,0.06)
bridge:tick();observed.grant=0
local status=bridge:tick()
assert(not status.inventory_work and not bridge:continue_inventory(),"old credit survived the next control cycle")
assert(observed.reads==1 and observed.polls==2,"expired credit triggered another component read")

local observed,_,bridge=fixture(6,0.06)
bridge:tick();observed.memory=8192
assert(not bridge:continue_inventory(),"low memory kept acquisition scheduled")
assert(observed.reads==1,"continued acquisition ignored the memory reserve")

local observed,_,bridge=fixture(6,0.06,0)
bridge:tick();observed.fail_inventory={code="transport_failed",fatal=false}
assert(not bridge:continue_inventory(),"transport failure kept acquisition scheduled")
assert(observed.failed_body and bridge.diagnostics.inventory_transport_failed=="1")
bridge:tick()
assert(observed.retried and observed.polls==2,"inventory retry bypassed control traffic")

local observed,_,bridge=fixture(6,0.06,0)
bridge:tick();observed.fail_inventory={code="invalid_inventory_confirmation",fatal=true}
assert(not pcall(bridge.continue_inventory,bridge),"continued acquisition swallowed a fatal protocol failure")
