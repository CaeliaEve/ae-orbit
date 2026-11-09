local fixture=require("cooperative_fixture")
local json=require("aeorbit.json")
local a,b=string.rep("a",32),string.rep("b",32)

-- Using v1, invoking discovery/recipes, or losing assignment provenance breaks this.
do
  local s,r=fixture()
  r:start();r:on_item_changed({name="test:ignored",damage=0,size=9})
  s.reads_ack={s:task("test:first")}
  r:tick();s:drain()
  assert(s.hello.capabilities.inventory and s.hello.capabilities.exact_quantities)
  assert(s.hello.capabilities.craft==false and s.hello.capabilities.cpu_output==false)
  assert(s.hello.capabilities.enumeration==nil and r.tracker==nil and next(s.files)==nil)
  local poll=s.polls[1].value
  assert(poll.read_slots==16 and poll.read_bytes>0 and poll.read_bytes<=16384)
  assert(poll.cpus==nil and poll.reports==nil and poll.hints==nil)
  local updates=s:updates()
  assert(#updates==1 and updates[1].id=="1" and updates[1].quantity=="42")
  assert(updates[1].assignment_id==a and updates[1].craftable==json.null)
end

-- Role/config errors fail closed before any hardware acquisition.
do
  local s,r=fixture({welcome_role="primary"})
  assert(not pcall(r.start,r) and #s.reads==0,"server role mismatch was accepted")
end
for _,kind in ipairs({"command","recipe","legacy"}) do
  local s,r=fixture();r:start()
  if kind=="command" then s.commands={{order_id="1",attempt_id="2"}}
  elseif kind=="recipe" then s.reads_ack={s:task("test:first",a,"quantity_recipe")}
  else s.reads_ack={s:task("test:first")};s.reads_ack[1].ttl_millis=30001 end
  assert(not pcall(r.tick,r) and #s.reads==0,"invalid worker authorization performed hardware work")
end

-- Same grants cannot replenish used credits; slow transit expires grants.
do
  local s,r=fixture({grant=1});r:start()
  s.grant_id="fixed";s.reads_ack={s:task("test:first",a)};r:tick();s:drain()
  s.reads_ack={s:task("test:second",b)};r:tick();s:drain()
  assert(#s.reads==1,"grant replay replenished a consumed credit")
  s.grant_id="new";s.poll_rtt=1.1;r:tick();s:drain()
  assert(#s.reads==1,"expired transit grant was spent")
  s.poll_rtt=0;s.grant_id="usable";r:tick();s:drain()
  assert(#s.reads==2)
end

-- Native failures rotate and follow 1/2/4 second backoff, without lease extension.
do
  local s,r=fixture({grant=16});r:start()
  s.fail_read=function(detail) return detail.name=="test:first" end
  s.reads_ack={s:task("test:first",a,"quantity","background",5000),s:task("test:second",b)}
  r:tick();s:drain()
  assert(#s.reads==2 and s:updates()[1].id=="2","failed oldest task blocked healthy work")
  s.clock=0.9;r:tick();s:drain();assert(#s.reads==2)
  s.clock=1;r:tick();s:drain();assert(#s.reads==3)
  s.clock=2.9;r:tick();s:drain();assert(#s.reads==3)
  s.clock=3;s.reads_ack={s:task("test:first",a,"quantity","background",30000)}
  r:tick();s:drain();assert(#s.reads==4)
  s.clock=5.1;s.fail_read=nil;r:tick();s:drain()
  assert(#s.reads==4 and s.polls[#s.polls].value.read_slots==16,"same attempt extended its original lease")
end

-- Frozen old bytes must retain their old token and leave newer work intact.
do
  local s,r=fixture({grant=1});r:start()
  s.reads_ack={s:task("test:first",a)};s.fail_upload=true;r:tick()
  assert(#s.uploads==1)
  s.clock=1;s.stock["test:first"]="99";s.reads_ack={s:task("test:first",b)}
  r:tick();s:drain()
  assert(s.uploads[1].body==s.uploads[2].body,"retry body mutated")
  local updates=s:updates()
  assert(updates[1].quantity=="42" and updates[1].assignment_id==a)
  assert(updates[#updates].quantity=="99" and updates[#updates].assignment_id==b)
end

-- Low memory keeps heartbeats alive while advertising zero and performing no read.
do
  local s,r=fixture();r:start();s.memory=1000;s.reads_ack={s:task("test:first")}
  r:tick();s:drain()
  assert(s.polls[1].value.read_slots==0 and s.polls[1].value.read_bytes==0 and #s.reads==0)
end

-- Retrying control sends the exact sequence and bytes, not newly accumulated hints.
do
  local s,r=fixture();r:start();s.fail_poll=true;r:tick();s.clock=1;r:tick()
  assert(s.polls[1].body==s.polls[2].body,"control retry changed bytes")
end

-- Returning to an older grant ID after an intervening grant cannot mint credits.
do
  local s,r=fixture({grant=1});r:start()
  s.grant_id="old";s.reads_ack={s:task("test:first",a)};r:tick();s:drain()
  s.grant_id="middle";r:tick();s:drain()
  s.grant_id="old";s.reads_ack={s:task("test:second",b)};r:tick();s:drain()
  assert(#s.reads==1,"an older grant ID minted a second credit grant")
end

-- Transit reduces the read lease as well as the separate grant lifetime.
do
  local s,r=fixture({grant=4});r:start();s.poll_rtt=0.6;s.grant_ttl=3000
  s.reads_ack={s:task("test:first",a,"quantity","refresh",500)}
  r:tick();s:drain()
  assert(#s.reads==0,"expired in-transit assignment was read")
end

-- Oversized identities cannot displace an admitted healthy task or block admission.
do
  local s,r=fixture({grant=0});r:start()
  local oversized=s:task("test:first",a)
  oversized.item.label=string.rep("x",512)
  s.memory=25000;s.reads_ack={oversized,s:task("test:second",b)}
  r:tick()
  assert(s.polls[1].value.read_bytes<=1562,"read bytes ignored current memory")
  s.memory=131072;s.grant=4;r:tick();s:drain()
  assert(#s.reads==1 and s:updates()[1].id=="2","oversized head blocked later work or ignored memory capacity")
end
