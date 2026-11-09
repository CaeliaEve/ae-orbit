local fixture=require("cooperative_fixture")
local a,b=string.rep("a",32),string.rep("b",32)
local function tick(r)
  local ok,reason=pcall(r.tick,r)
  assert(ok,"legal poll failed: "..tostring(type(reason)=="table" and reason.code or reason))
end

-- A legal control heartbeat need not contain a newly claimed grant.
for _,variant in ipairs({"absent","empty","zero","empty_zero"}) do
  local s,r=fixture();r:start()
  s.mutate_poll_ack=function(ack)
    ack.scan_budget=0
    ack.grant_id=(variant=="empty" or variant=="empty_zero") and "" or nil
    ack.grant_ttl_millis=nil
    if variant=="zero" or variant=="empty_zero" then ack.grant_ttl_millis=0 end
  end
  s.reads_ack={s:task("test:first",a)}
  tick(r);s:drain()
  assert(r.poll_seq=="1" and #s.reads==0 and r.tasks.count==0 and r.credits==0,
    "no-grant heartbeat failed or admitted work without authorization")
end

-- Held authorized work can still complete on a heartbeat without a new grant.
do
  local s,r=fixture({grant=4});r:start();s.grant_id="held";s.grant_ttl=3000
  s.fail_read=function() return true end
  s.reads_ack={s:task("test:first",a)};tick(r)
  assert(#s.reads==1 and r.tasks.count==1 and r.credits==3)
  s.clock=1;s.fail_read=nil
  s.mutate_poll_ack=function(ack)
    ack.scan_budget=0;ack.grant_id=nil;ack.grant_ttl_millis=nil
  end
  s.reads_ack={s:task("test:second",b)};tick(r);s:drain()
  assert(#s.reads==2 and r.tasks.count==0 and r.credits==2 and r.grant_deadline==3,
    "no-grant heartbeat blocked held work or admitted new work")
  assert(#s:updates()==1 and s:updates()[1].id=="1")
end

-- No new grant cannot clear, replenish or extend a previously held grant.
do
  local s,r=fixture({grant=4});r:start();s.grant_id="held";s.grant_ttl=1000
  tick(r)
  local deadline=r.grant_deadline
  s.clock=0.25
  s.mutate_poll_ack=function(ack)
    ack.scan_budget=0;ack.grant_id=nil;ack.grant_ttl_millis=nil
  end
  s.reads_ack={s:task("test:first",a)};tick(r)
  assert(r.grant_id=="held" and r.credits==4 and r.grant_deadline==deadline and r.tasks.count==0,
    "no-grant response changed held authorization or admitted new work")
  s.clock=1.01;tick(r);s:drain()
  assert(r:_available()==0 and #s.reads==0,"no-grant response extended expired authorization")
end

-- Invalid or incomplete grants fail closed, including nonzero credit without identity.
for _,variant in ipairs({"missing_id","empty_id","missing_ttl","zero_ttl","too_long","partial_zero"}) do
  local s,r=fixture();r:start();s.reads_ack={s:task("test:first",a)}
  s.mutate_poll_ack=function(ack)
    if variant=="missing_id" then ack.grant_id=nil
    elseif variant=="empty_id" then ack.grant_id=""
    elseif variant=="missing_ttl" then ack.grant_ttl_millis=nil
    elseif variant=="zero_ttl" then ack.grant_ttl_millis=0
    elseif variant=="too_long" then ack.grant_ttl_millis=30001
    else ack.scan_budget=0;ack.grant_id=nil;ack.grant_ttl_millis=1000 end
  end
  assert(not pcall(r.tick,r) and #s.reads==0 and r.tasks.count==0,
    "malformed grant admitted hardware work")
end

-- Repeating a grant after some transit never replenishes it or extends its deadline.
do
  local s,r=fixture({grant=2});r:start();s.grant_id="same";s.grant_ttl=1000
  s.reads_ack={s:task("test:first",a)};tick(r);s:drain()
  assert(r.credits==1 and r.grant_deadline==1)
  s.clock=0.5;s.reads_ack={s:task("test:second",b)};tick(r);s:drain()
  assert(#s.reads==2 and r.credits==0 and r.grant_deadline==1,
    "repeated grant replenished credit or extended its deadline")
end

-- Native command execution cannot extend the grant or read lease received with it.
local extended={}
for _,expires in ipairs({"grant","lease"}) do
  local s,r=fixture({role="primary",grant=4});r:start();r.next_scan=math.huge
  local original=s.me.getCraftable
  s.me.getCraftable=function(detail)
    local result=original(detail)
    local request=result.request
    result.request=function(...)
      s.clock=s.clock+2
      return request(...)
    end
    return result
  end
  s.commands={{order_id="order",attempt_id="attempt",quantity="2",
    item={kind="item",name="test:craft",damage=0,exact=true}}}
  s.grant_ttl=expires=="grant" and 1000 or 3000
  s.reads_ack={s:task("test:first",a,"quantity","refresh",expires=="lease" and 1000 or 30000)}
  tick(r);s:drain()
  assert(s.crafts==1 and s.clock==2,"native command delay fixture did not execute")
  if #s.reads~=0 then extended[#extended+1]=expires end
end
assert(#extended==0,"native command processing extended authorization: "..table.concat(extended,", "))
