local fixture=require("cooperative_fixture")

local s,r=fixture({role="primary",memory=1500000,grant=4,
  config={scan_memory_floor=2097152}})
s.catalog={{name="test:one",damage=0,label="One",size="1",hasTag=false}}
r:start()

-- Native item conversion needs reserve beyond the HTTP receiver budget.
r:tick()
assert((s.scans or 0)==0,"low-memory discovery called the native allItems iterator")
assert(r.diagnostics.scan_deferred_low_memory=="1","low-memory discovery was not reported")

-- Once memory is available again, discovery resumes without a restart.
s.memory=3000000
s.clock=5
r:tick()
for _=1,8 do
  if (s.scans or 0)>0 then break end
  r:continue_inventory()
end
assert((s.scans or 0)==1,"discovery did not resume after memory recovered")

-- A two-module OC with about 1.5 MiB free can use the bounded native iterator;
-- the default must not permanently suppress its inventory discovery.
for _,module in ipairs({"aeorbit.cooperative","aeorbit.runner"}) do
  local bridge=require(module).new({bridge_mode="cooperative"}, {free_memory=function() return 1500000 end,adapter={},client={}})
  assert(bridge:_scan_memory_ready(),module.." blocked healthy 1.5 MiB OC by default")
end

-- Recheck the reserve before converting each item, including an already-open
-- iterator. Pausing must preserve its position and continue heartbeats.
local consumed=0
s.me.allItems=function()
  return function()
    consumed=consumed+1
    return {name="test:item_"..consumed,damage=0,size="1",label="Item"}
  end
end
r.iterator=nil
r.next_scan=0
s.memory=3000000
r.credits=4;r.grant_deadline=s.clock+30
r:_discover()
assert(consumed==1 and r.iterator)
s.memory=1500000
r:_discover()
assert(consumed==1,"low-memory open iterator still converted a native item")
local polls=#s.polls
s.clock=s.clock+1
r:tick()
assert(#s.polls>polls,"memory-paused discovery suppressed heartbeat")
assert(consumed==1,"heartbeat resumed low-memory native conversion")
s.memory=3000000
r.credits=4;r.grant_deadline=s.clock+30
r:_discover()
assert(consumed==2,"paused iterator lost its position or failed to resume")

do
  local stock,bridge=require("inventory_fixture")({grant=4,delay=0,config={scan_memory_floor=2097152}})
  stock.memory=3000000
  bridge.credits=4
  local first=bridge:_next_raw()
  assert(first and first.name=="test:cold_1" and stock.reads==1)
  stock.memory=1500000
  local next_item=bridge:_next_raw()
  assert(next_item==nil and stock.reads==1,"v1 converted a low-memory native item")
  local count=#stock.polls
  stock.clock=stock.clock+1
  bridge:tick()
  assert(#stock.polls>count and stock.reads==1,"v1 pause lost heartbeat or converted an item")
  stock.memory=3000000
  bridge.credits=4
  next_item=bridge:_next_raw()
  assert(next_item and next_item.name=="test:cold_2","v1 iterator failed to resume in place")
end
