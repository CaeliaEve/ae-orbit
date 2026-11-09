local fixture=require("inventory_fixture")
local json=require("aeorbit.json")
local a=string.rep("a",32)
local b=string.rep("b",32)
local function request(s,name,token)
  local value=s:verification(name,token or a)
  value.craftable=nil
  value.ttl_millis=60000
  return value
end
local function drain(r)
  for _=1,200 do if not r:continue_inventory() then break end end
end
local function receipts(s)
  local values={}
  for _,batch in ipairs(s.requests) do
    for _,value in ipairs(batch.value.updates) do if value.refresh then values[#values+1]=value end end
  end
  return values
end

-- Losing negotiation or accidentally querying recipes breaks this receipt.
do
  local s,r=fixture({quantity_refresh=true,grant=1})
  assert(r.adapter:capabilities().quantity_refresh==true,"quantity refresh capability missing")
  r.next_scan=math.huge
  s.me.getCraftable=function() error("quantity refresh queried recipes") end
  s.refresh={request(s,"test:refresh")}
  r:tick();drain(r)
  assert(s.polls[1].refresh_slots==8 and s.polls[1].refresh_bytes<=16384,"refresh capacity missing")
  local values=receipts(s)
  assert(#values==1 and values[1].refresh==a and values[1].quantity=="42")
  assert(values[1].craftable==json.null and values[1].verification==nil,"quantity-only receipt leaked order verification")
  r:tick()
  assert(s.polls[2].refresh_slots==8,"ack did not free refresh queue")
end

-- An old server must never enable the new lane.
do
  local s,r=fixture({grant=1})
  r.next_scan=math.huge
  s.refresh={request(s,"test:ignored")}
  r:tick();drain(r)
  assert(s.polls[1].refresh_slots==nil and s.polls[1].refresh_bytes==nil)
  assert(#receipts(s)==0,"unnegotiated refresh executed")
end

-- Identity retries cannot displace FIFO work or grow beyond eight entries.
do
  local s,r=fixture({quantity_refresh=true,grant=0})
  r.next_scan=math.huge
  for group=0,2 do
    s.refresh={}
    for i=1,4 do s.refresh[i]=request(s,"test:q"..(group*4+i)) end
    r:tick()
  end
  r:tick()
  assert(s.polls[4].refresh_slots==0,"refresh count budget not enforced")
  s.refresh={request(s,"test:q1",b)}
  s.grant=1;r:tick();drain(r)
  local values=receipts(s)
  assert(#values==1 and values[1].id==s.ids["test:q1"] and values[1].refresh==b,"retry changed FIFO position or generation")
end

-- A busy refresh lane shares one-credit grants with ordinary hints.
do
  local s,r,raw=fixture({quantity_refresh=true,grant=1})
  r.next_scan=math.huge
  for i=1,20 do
    r:on_item_changed(raw("test:ordinary"))
    s.refresh={request(s,"test:flood",i%2==0 and a or b)}
    r:tick();drain(r)
  end
  assert(s.items["test:ordinary"],"one-credit refresh flood starved ordinary inventory")
  assert(#receipts(s)>=1,"ordinary hints starved refresh")
end

-- Refresh reads remain capped across continuation slices.
do
  local s,r=fixture({quantity_refresh=true,grant=16})
  r.next_scan=math.huge
  s.refresh={}
  for i=1,4 do s.refresh[i]=request(s,"test:cap"..i) end
  r:tick();drain(r)
  assert(#receipts(s)==2,"refresh exceeded per-grant two-read cap")
end

-- Native/exact-conversion failures never confirm, spin, or manufacture zero.
for _,failure in ipairs({"native","precision"}) do
  local s,r=fixture({quantity_refresh=true,grant=16})
  r.next_scan=math.huge
  local attempts=0
  s.me.getItemInNetwork=function(detail)
    attempts=attempts+1
    if failure=="native" then error("unavailable") end
    return {name=detail.name,damage=0,label=detail.name,size=9007199254740992.0}
  end
  s.refresh={request(s,"test:failure")}
  r:tick();drain(r)
  assert(#receipts(s)==0 and attempts==1,"failed refresh confirmed or retried without delay")
  s.clock=s.clock+10
  s.me.getItemInNetwork=function(detail) return {name=detail.name,damage=0,label=detail.name,size="9007199254740993"} end
  r:tick();drain(r)
  assert(receipts(s)[1].quantity=="9007199254740993","failed refresh was lost")
end

-- Transport retries reuse bytes and do not issue a second native lookup.
do
  local s,r=fixture({quantity_refresh=true,grant=1})
  r.next_scan=math.huge
  s.refresh={request(s,"test:retry")}
  s.fail_upload=true;r:tick()
  local before=s.reads
  r:tick();drain(r)
  assert(#s.requests==2 and s.requests[1].body==s.requests[2].body and s.reads==before)
end

-- A delayed old upload ACK must not erase a newer generation.
do
  local s,r=fixture({quantity_refresh=true,grant=1})
  r.next_scan=math.huge
  s.refresh={request(s,"test:generation",a)}
  s.refresh[1].ttl_millis=1000
  s.fail_upload=true;r:tick()
  s.clock=s.clock+1.1
  s.refresh={request(s,"test:generation",b)}
  r:tick();drain(r)
  local values=receipts(s)
  assert(values[#values].refresh==b,"old upload ACK removed newer refresh")
  assert(s.polls[2].refresh_slots==8,"expired in-flight refresh retained capacity")
end

-- Refreshing an order identity cannot consume its recipe confirmation.
do
  local s,r=fixture({quantity_refresh=true,grant=1})
  r.next_scan=math.huge
  s.verify={s:verification("test:order",a)}
  s.refresh={request(s,"test:order",b)}
  r:tick();drain(r)
  assert(receipts(s)[1].verification==nil)
  s.grant=4;r:tick();drain(r)
  assert(s.items["test:order"].verification==a,"refresh cleared pending order verification")
end

-- Long identities must consume byte capacity without evicting admitted work.
do
  local s,r=fixture({quantity_refresh=true,grant=0})
  r.next_scan=math.huge
  local names={}
  for i=1,8 do names[i]="test:"..string.rep("x",240)..i end
  for group=0,1 do
    s.refresh={}
    for i=1,4 do s.refresh[i]=request(s,names[group*4+i]) end
    r:tick()
  end
  r:tick()
  assert(s.polls[3].refresh_slots>0 and s.polls[3].refresh_slots<8,"long identities ignored byte budget")
  assert(s.polls[3].refresh_bytes>=0 and s.polls[3].refresh_bytes<1200)
  s.grant=1;r:tick();drain(r)
  assert(receipts(s)[1].id==s.ids[names[1]],"byte pressure evicted oldest refresh")
end

-- Zero credits and low memory preserve pending work without native reads.
do
  local s,r=fixture({quantity_refresh=true,grant=0})
  r.next_scan=math.huge
  s.refresh={request(s,"test:deferred")}
  r:tick();drain(r)
  assert(s.reads==0 and #receipts(s)==0,"zero credits performed native work")
  s.memory=1000;s.grant=4;r:tick();drain(r)
  assert(s.reads==0 and #receipts(s)==0,"low memory performed native work")
  s.memory=131072;r:tick();drain(r)
  assert(receipts(s)[1].refresh==a,"resource pressure lost pending refresh")
end

-- Refreshes do not borrow the recipe cooldown or displace eligible priorities.
do
  local s,r=fixture({quantity_refresh=true,grant=16})
  r.next_scan=math.huge
  s.verify={s:verification("test:priority",a)}
  s.refresh={request(s,"test:quantity",b)}
  local order={}
  s.on_read=function(detail) order[#order+1]=detail.name end
  r:tick();drain(r)
  assert(order[1]=="test:priority","refresh displaced eligible order priority")
  assert(s.items["test:quantity"].refresh==b,"recipe cooldown blocked quantity refresh")
end

-- Invalid tokens and overfull ACKs must fail before issuing native work.
for _,kind in ipairs({"token","count"}) do
  local s,r=fixture({quantity_refresh=true,grant=16})
  r.next_scan=math.huge
  s.refresh={}
  if kind=="token" then s.refresh[1]=request(s,"test:invalid",string.rep("A",32))
  else for i=1,5 do s.refresh[i]=request(s,"test:invalid"..i) end end
  local ok=pcall(r.tick,r)
  assert(not ok and s.reads==0,"invalid refresh ACK accepted")
end

-- Failed identities cannot retain all slots after the server request expires.
do
  local s,r=fixture({quantity_refresh=true,grant=16})
  r.next_scan=math.huge
  s.me.getItemInNetwork=function() error("offline") end
  for group=0,1 do
    s.refresh={}
    for i=1,4 do
      local value=request(s,"test:expiry"..(group*4+i))
      value.ttl_millis=1000;s.refresh[i]=value
    end
    r:tick();drain(r)
  end
  s.clock=0.5
  local duplicate=request(s,"test:expiry1")
  duplicate.ttl_millis=60000;s.refresh={duplicate}
  r:tick();drain(r)
  s.clock=1.1
  s.me.getItemInNetwork=function(detail) return {name=detail.name,damage=0,label=detail.name,size=42} end
  s.refresh={request(s,"test:after_expiry")}
  r:tick();drain(r)
  assert(s.polls[4].refresh_slots==8,"expired refresh saturation retained capacity")
  assert(s.items["test:after_expiry"].refresh==a,"expired saturation blocked fresh identity")
end

-- Malformed deadlines must fail before any native refresh read.
for _,ttl in ipairs({0,-1,60001,1.5,"1000",false}) do
  local s,r=fixture({quantity_refresh=true,grant=16})
  r.next_scan=math.huge
  local value=request(s,"test:bad_ttl");value.ttl_millis=ttl;s.refresh={value}
  local ok=pcall(r.tick,r)
  assert(not ok and s.reads==0,"invalid refresh TTL accepted")
end
do
  local s,r=fixture({quantity_refresh=true,grant=16})
  r.next_scan=math.huge
  local value=request(s,"test:no_ttl");value.ttl_millis=nil;s.refresh={value}
  local ok=pcall(r.tick,r)
  assert(not ok and s.reads==0,"missing refresh TTL accepted")
end

-- Replacing an expiring generation establishes a new local deadline.
do
  local s,r=fixture({quantity_refresh=true,grant=0})
  r.next_scan=math.huge
  local value=request(s,"test:new_deadline",a);value.ttl_millis=1000;s.refresh={value}
  r:tick()
  s.clock=0.5;value=request(s,"test:new_deadline",b);value.ttl_millis=2000;s.refresh={value}
  r:tick()
  s.clock=1.1;s.grant=1;r:tick();drain(r)
  assert(s.items["test:new_deadline"].refresh==b,"new generation inherited old deadline")
end

-- Repeated failures at the head cannot starve a healthy later identity.
for _,grant in ipairs({1,16}) do
  local s,r=fixture({quantity_refresh=true,grant=grant})
  r.next_scan=math.huge
  s.me.getItemInNetwork=function(detail)
    if detail.name~="test:healthy" then error("permanent failure") end
    return {name=detail.name,damage=0,label=detail.name,size=42}
  end
  s.refresh={}
  for i=1,4 do s.refresh[i]=request(s,"test:failed"..i) end
  r:tick();drain(r)
  s.refresh={request(s,"test:healthy")}
  for i=1,8 do
    s.clock=i;r:tick();drain(r)
    if s.items["test:healthy"] then break end
  end
  assert(s.items["test:healthy"] and s.items["test:healthy"].refresh==a,"failed head starved healthy refresh")
end

-- A successful quantity receipt flushes before the ordinary slice continues.
do
  local s,r=fixture({quantity_refresh=true,grant=16,delay=0.001,
    config={batch_interval_seconds=1,scan_slice_seconds=0.0005}})
  s.refresh={request(s,"test:immediate")}
  r:tick()
  assert(s.items["test:immediate"] and s.items["test:immediate"].refresh==a,"refresh receipt waited for batch deadline")
end
