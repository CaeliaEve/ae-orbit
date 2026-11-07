local fixture=require("inventory_fixture")
local json=require("aeorbit.json")

local function setup(options)
  options=options or {};options.diagnostic_samples=options.diagnostic_samples or 4
  local s,r,raw=fixture(options)
  r.next_scan=math.huge
  s.on_read=function() error(s.failure or "native item lookup failed",0) end
  return s,r,raw
end

-- Read failures retain their origin without inventing stock or confirming work.
for _,source in ipairs({"event","recheck","priority"}) do
  local s,r,raw=setup()
  if source=="event" then r:on_item_changed(raw("test:iron_plate"))
  else s.verify={s:verification("test:iron_plate",source=="priority" and string.rep("a",32) or nil)} end
  r:tick();s.grant=0;r:tick()
  local samples=s.polls[#s.polls].health.read_failures
  assert(samples and #samples==1,"read failure discarded its item and reason")
  local sample=samples[1]
  assert(sample.code=="exact_lookup_failed" and sample.source==source)
  assert(sample.name=="test:iron_plate" and sample.damage==0 and sample.nbt_bytes==0)
  assert(sample.message=="native item lookup failed" and sample.occurrences=="1")
  assert(tonumber(sample.last_seen_millis)>=60)
  if source~="event" then assert(sample.item_id=="1","backend short ID was lost from the diagnostic") end
  assert(r.diagnostics.exact_lookup_failed=="1" and s.accepted_updates==0,"a failed read invented stock")
  if source=="priority" then assert(r.verifications.count==1,"diagnostics consumed failed priority work") end
end

-- Recipe errors leave the confirmation pending.
do
  local s,r=setup()
  s.on_read=nil
  s.me.getCraftable=function() error("native recipe failure",0) end
  s.verify={s:verification("test:iron_plate",string.rep("b",32))}
  r:tick();s.grant=0;r:tick()
  local sample=assert(s.polls[#s.polls].health.read_failures)[1]
  assert(sample.code=="recipe_lookup_failed" and sample.source=="priority")
  assert(sample.message:find("recipe lookup failed",1,true) and r.verifications.count==1)
  for _,request in ipairs(s.requests) do
    for _,update in ipairs(request.value.updates) do assert(not update.verification) end
  end
end

-- Lost control ACKs freeze the body despite new hints and memory readings.
do
  local s,r,raw=setup()
  r:on_item_changed(raw("test:first"));r:tick()
  local first_body,retry_body
  s.on_poll=function(body,value)
    if not first_body then
      assert(value.health.read_failures,"fixture did not send a diagnostic snapshot")
      first_body=body;error({fatal=false,code="timeout"},0)
    end
    retry_body=body
  end
  assert(not r:tick().control_ok)
  r:on_item_changed(raw("test:second"));s.memory=120000
  assert(r:tick().control_ok and retry_body==first_body,"control retry changed its body")
  s.on_poll=nil;s.grant=0
  r:tick()
  assert(not s.polls[#s.polls].health.read_failures,"early diagnostics were uploaded again")
  s.clock=s.clock+31;r:tick()
  local samples=assert(s.polls[#s.polls].health.read_failures)
  assert(samples[1].name=="test:second","ACK of an older snapshot lost a later failure")
  s.clock=s.clock+31;r:tick()
  assert(not s.polls[#s.polls].health.read_failures,"unchanged diagnostics consumed bandwidth")
end

-- An old backend must never see an unnegotiated field.
for _,limit in ipairs({0,-1,5,1.5,"4"}) do
  local s,r,raw=setup({diagnostic_samples=limit})
  r:on_item_changed(raw("test:legacy"));r:tick();s.grant=0;r:tick()
  assert(not s.polls[#s.polls].health.read_failures,"invalid negotiation enabled new fields")
end
do
  local s,r,raw=fixture()
  r.next_scan=math.huge;s.on_read=function() error("legacy read failed",0) end
  r:on_item_changed(raw("test:legacy"));r:tick();s.grant=0;r:tick()
  assert(not s.polls[#s.polls].health.read_failures)
end

-- Real journal reports have priority over optional diagnostic details.
do
  local s,r,raw=setup({bytes=600})
  r:on_item_changed(raw("test:budget"));r:tick()
  r.tracker.data.attempts.attempt={order_id="order",attempt_id="attempt",state="failed",reason=string.rep("x",160)}
  s.grant=0;r:tick()
  local poll=s.polls[#s.polls]
  assert(#poll.reports==1 and poll.reports[1].state=="failed","diagnostics displaced an order report")
  assert(not r.tracker.data.attempts.attempt,"a transmitted report was not acknowledged")
  assert(not poll.health.read_failures and #json.encode(poll)<=600)
  r:tick()
  assert(s.polls[#s.polls].health.read_failures,"deferred diagnostics were discarded")
end
