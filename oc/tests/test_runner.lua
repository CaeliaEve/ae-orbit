local runner = require("aeorbit.runner")
local adapter = require("aeorbit.adapter")
local journal = require("aeorbit.journal")
local json = require("aeorbit.json")
-- OC owns collection and does not expose this standard Lua function.
collectgarbage=nil
local clock=0
local free_memory=131072
local amounts={}
local function raw(index)
  return {name="test:item_"..index,damage=0,label="Item "..index,hasTag=false,size=amounts[index] or index*10,isCraftable=true}
end
local me={
  allItems=function() local index=0;return function() index=index+1;if index<=10 then return raw(index) end end end,
  getItemsInNetwork=function() error("full inventory array used") end,
  getItemInNetwork=function(detail) return raw(assert(tonumber(detail.name:match("_(%d+)$")))) end,
  getCraftable=function() return nil end,
  getCpus=function() return {} end
}
local backend={inventory_seq=0,poll_seq=0,ids={},names={},items={},next_id=0,received=0,acks={},lost=false,commands_sent=false}
local client={request_bytes=2048,response_bytes=4096}
function client:send(path,body)
  if path=="/bridge/v1/hello" then return self:post(path,json.decode(body)) end
  local b=json.decode(body)
  if path=="/bridge/v1/inventory" then
    backend.received=backend.received+1
    local seq=tonumber(b.sequence)
    if seq==backend.inventory_seq then
      assert(backend.acks[seq].body==body,"retry mutated the pending batch")
      return backend.acks[seq].ack
    end
    assert(seq==backend.inventory_seq+1 and #b.updates<=3 and #body<=2048)
    local bindings=json.array({})
    for index,update in ipairs(b.updates) do
      local id=update.id
      if update.item then
        local name=update.item.name
        if not backend.ids[name] then backend.next_id=backend.next_id+1;backend.ids[name]=tostring(backend.next_id);backend.names[tostring(backend.next_id)]=name end
        id=backend.ids[name];bindings[#bindings+1]={index=index,id=id}
      end
      backend.items[assert(backend.names[id])]=update.quantity
    end
    backend.inventory_seq=seq
    local ack={ack=b.sequence,revision=b.sequence,bindings=bindings}
    backend.acks[seq]={body=body,ack=ack}
    if not backend.lost then backend.lost=true;error({code="transport_failed",fatal=false}) end
    return ack
  elseif path=="/bridge/v1/poll" then
    if b.cpus then
      assert(#b.cpus<=2,"negotiated CPU limit was ignored")
      backend.last_cpu_count=#b.cpus;backend.cpus_truncated=b.cpus_truncated
    end
    backend.poll_seq=backend.poll_seq+1
    assert(b.sequence==tostring(backend.poll_seq))
    return {ack=b.sequence,commands=json.array({}),verify=json.array({}),scan_budget=8,poll_millis=1}
  end
  error("unexpected path")
end
function client:post(path,value)
  if path=="/bridge/v1/boot-id" then return {boot_id="server-generated-boot"} end
  if path=="/bridge/v1/hello" then
    assert(value.source=="simulator")
    return {protocol=1,session_id="session",inventory_ack="0",poll_ack="0",limits={batch_items=3,batch_bytes=2048,response_bytes=4096,commands=4,cpus=2,poll_millis=1}}
  end
  return self:send(path,json.encode(value))
end
local files={}
local j=journal.new({read=function(p) return files[p] end,write=function(p,v) files[p]=v;return true end},"runner",32768)
local r=runner.new({source="simulator",cache_entries=2,cache_bytes=512,dirty_entries=3,dirty_bytes=1024,max_batch_bytes=2048,max_response_bytes=4096,scan_interval=60,cpu_interval=10,scan_slice_seconds=0.1},
  {adapter=adapter.new(me),client=client,journal=j,now=function() return clock end,free_memory=function() return free_memory end,yield=function() end})
r:start()
for _=1,12 do r:tick();clock=clock+1 end
for index=1,10 do assert(backend.items["test:item_"..index]==tostring(index*10),"catalog did not arrive") end
assert(r.ids.count<=2 and backend.poll_seq==12,"inventory retry starved control traffic")
amounts[1]=777
local changed=raw(1);changed.size=-5;changed.hasTag=0;changed.isCraftable=0
r:on_item_changed(changed)
r:tick()
assert(backend.items["test:item_1"]=="777","event delta was treated as an absolute quantity")
assert(not r.diagnostics.event_identity_unavailable,"OC's numeric NBT flag rejected a valid inventory event")
for index=1,10 do r:on_item_changed(raw(index)) end
assert(r.dirty.count<=3 and r.dirty.bytes<=1024,"dirty queue grew without bound")
me.getCpus=function()
  local cpus={};for index=1,8 do cpus[index]={name="CPU "..index,storage=4096,coprocessors=1,busy=false} end
  return cpus
end
r.next_cpu=0;r:tick()
assert(backend.last_cpu_count==2 and backend.cpus_truncated,"CPU truncation was not declared")
local previous_poll,previous_inventory=backend.poll_seq,backend.inventory_seq
free_memory=8192
r:tick()
assert(backend.poll_seq==previous_poll+1 and backend.inventory_seq==previous_inventory,"low memory stopped control traffic or continued acquisition")
assert(r.diagnostics.low_memory=="1","low memory was not reported")
free_memory=131072
r:tick()
assert(backend.poll_seq==previous_poll+2,"bridge did not recover after memory became available")
