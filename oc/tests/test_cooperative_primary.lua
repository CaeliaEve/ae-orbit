local fixture=require("cooperative_fixture")
local json=require("aeorbit.json")
local a,b=string.rep("a",32),string.rep("b",32)

-- Unknown events wait for a grant, then discover in a bounded lane ahead of a distant scan.
do
  local s,r=fixture({role="primary",grant=0});r:start();r.next_scan=math.huge
  r:on_item_changed({name="test:event_new",damage=0,label="New item",size=999})
  r:tick();s:drain()
  assert(#s.reads==0 and #s:updates()==0,"event performed immediate unauthorized acquisition")
  assert(s.polls[1].value.read_slots==15,"discovery candidate did not reserve a bounded slot")
  s.grant=1;r:tick();s:drain()
  local update=s:updates()[1]
  assert(update and update.item.name=="test:event_new" and update.quantity=="42" and update.assignment_id==nil,
    "unknown event waited for a full catalog scan")
  r:on_item_changed({name="test:event_new",damage=0,label="New item",size=999})
  r:tick();s:drain()
  assert(#s.reads==1 and s.polls[3].value.hints[1].id=="10","discovered event did not switch to hints")
end

-- A changed label needs discovery metadata; a short-ID hint cannot carry it.
do
  local s,r=fixture({role="primary",grant=8});r:start()
  s.catalog={{name="test:metadata",damage=0,label="Before",size=1}}
  r:tick();s:drain()
  s.catalog[1].label="After";s.catalog[1].size=2;s.clock=61
  r:tick();s:drain()
  local update=s:updates()[2]
  assert(update and update.item.label=="After" and update.assignment_id==nil,
    "known-ID cache suppressed discovery metadata changes")
end

-- Known events must become hints; they must not issue an unauthorized exact read.
do
  local s,r=fixture({role="primary",grant=1});r:start();r.next_scan=math.huge
  s.reads_ack={s:task("test:first",a)};r:tick();s:drain()
  r:on_item_changed({name="test:first",damage=0,size=999})
  r:tick();s:drain()
  local hints=s.polls[2].value.hints
  assert(hints and #hints==1 and hints[1].id=="1" and hints[1].quantity==nil)
  assert(#s.reads==1,"known event performed an unauthorized read")
end

-- Discovery uses bounded iteration and uploads only new definitions without tokens.
do
  local s,r=fixture({role="primary",grant=8});r:start()
  s.catalog={{name="test:discovered",damage=0,label="Discovered",size="9007199254740993",isCraftable=true}}
  r:tick();s:drain()
  local update=s:updates()[1]
  assert(update.item.name=="test:discovered" and update.quantity=="9007199254740993")
  assert(update.craftable==true and update.assignment_id==nil)
  assert(s.uploads[#s.uploads].value.scan_completed==true)
  assert(s.cpu_reads==1)
  s.clock=61;r:tick();s:drain();r:tick()
  assert(#s:updates()==1,"known discovery overwrote established evidence")
  local hints=s.polls[#s.polls].value.hints
  assert(hints and hints[1].id=="10")
end

-- A joint task is atomic: failed recipe reads never publish a quantity-only receipt.
do
  local s,r=fixture({role="primary",grant=4});r:start();r.next_scan=math.huge
  s.me.getCraftable=function() error("recipe unavailable") end
  s.reads_ack={s:task("test:first",a,"quantity_recipe","order")}
  r:tick();s:drain()
  assert(#s:updates()==0,"failed joint task leaked a partial receipt")
  s.clock=5
  s.me.getCraftable=function() return {getStack=function() return {name="test:first",damage=0} end} end
  r:tick();s:drain()
  local update=s:updates()[1]
  assert(update.id=="1" and update.assignment_id==a and update.craftable==true)
end

-- Journaled command execution, duplicate suppression, terminal reports remain A-only.
do
  local s,r=fixture({role="primary",grant=0});r:start();r.next_scan=math.huge
  local command={order_id="order",attempt_id="attempt",quantity="2",
    item={kind="item",name="test:craft",damage=0,exact=true}}
  s.commands={command};r:tick()
  assert(s.crafts==1 and r.tracker.data.attempts.attempt.state=="computing")
  s.commands={command};r:tick()
  local reports=s.polls[2].value.reports
  assert(#reports==1 and reports[1].state=="done" and s.crafts==1)
  s.commands={command};r:tick();assert(s.crafts==1)
end

-- The task pool is bounded at sixteen, with at most eight fast entries.
do
  local s,r=fixture({grant=0,memory=1048576});r:start()
  for group=0,2 do
    s.reads_ack={}
    for i=1,4 do
      local task=s:task("test:fast"..(group*4+i),string.format("%032x",group*4+i))
      task.id=tostring(group*4+i);s.reads_ack[i]=task
    end
    r:tick()
  end
  r:tick()
  assert(s.polls[#s.polls].value.read_slots==8,"fast queue exceeded its reserved bound")
  for group=0,2 do
    s.reads_ack={}
    for i=1,4 do
      local n=20+group*4+i
      local task=s:task("test:cold"..n,string.format("%032x",n),"quantity","background")
      task.id=tostring(n);s.reads_ack[i]=task
    end
    r:tick()
  end
  r:tick()
  assert(s.polls[#s.polls].value.read_slots==0,"total task queue exceeded sixteen")
end

-- Malformed short-ID bindings fail before caching an incorrect definition mapping.
do
  local s,r=fixture({role="primary",grant=1});r:start()
  s.catalog={{name="test:unbound",damage=0,size=1}}
  s.mutate_ack=function(ack) ack.bindings=json.array({{index=2,id="1"}}) end
  assert(not pcall(r.tick,r),"invalid short ID binding was accepted")
end
