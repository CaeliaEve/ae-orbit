local fixture=require("inventory_fixture")

-- Slow native calls end every processing slice. Flushing each slice creates
-- sixteen HTTP transactions; bounded coalescing must deliver all sixteen in <=5.
local s,r=fixture()
local status=r:tick()
assert(s.reads==1 and status.inventory_work,"collection escaped its slice or lost remaining work")
for _=1,20 do
  local before=s.reads
  if not r:continue_inventory() then break end
  assert(s.reads-before<=1,"batching turned bounded slices into an unbounded scan")
end
for index=1,16 do assert(s.items["test:cold_"..index].quantity=="42","buffered inventory never reached the receiver") end
assert(#s.requests<=5,"slow component calls still force one HTTP upload per item")

-- A short catalog flushes its tail immediately instead of waiting for a full batch.
local s,r=fixture({catalog_size=2,grant=16})
r:tick();for _=1,5 do if not r:continue_inventory() then break end end
assert(s.items["test:cold_2"] and s.requests[#s.requests].value.scan_completed)
assert(#s.requests==1,"short catalog was fragmented into unnecessary uploads")

-- Retry a frozen body after a lost ACK; metrics count successful confirmations,
-- not wire attempts. Control is serviced before retrying the pending inventory.
local s,r=fixture({grant=2})
s.fail_upload=true
r:tick();r:continue_inventory()
assert(#s.requests==1 and r.pending_inventory,"fixture never lost the batch confirmation")
local reads=s.reads
s.grant=0;r:tick()
assert(#s.polls==2 and s.reads==reads and #s.requests==2,"retry bypassed control or reread inventory")
r:tick()
local counters=s.polls[3].health.inventory
assert(counters and counters.batches=="1" and counters.updates=="2","confirmation counters included a retry")
assert(counters.bytes==tostring(#s.requests[1].body) and counters.credits_used=="2")

-- A priority result cannot sit in the ordinary aggregation window.
local s,r=fixture({grant=8})
r:tick()
local token=string.rep("a",32)
s.verify={s:verification("test:priority",token)}
local before=s.clock
r:tick()
local got=s.items["test:priority"]
assert(got and got.verification==token and got.craftable==true,"priority observation waited for a normal batch")
assert(s.requests[#s.requests].at-before<0.25,"aggregation delayed a completed order's inventory read")

-- Small byte budgets split data without exceeding the wire limit or losing tails.
local s,r=fixture({bytes=512,grant=8})
r:tick();for _=1,20 do if not r:continue_inventory() then break end end
for index=1,8 do assert(s.items["test:cold_"..index],"byte-limited batching lost an item") end

-- Low memory pauses sampling with an unsent tail intact, then safely resumes.
local s,r=fixture({grant=2})
r:tick();s.memory=8192
assert(not r:continue_inventory() and s.reads==1)
s.memory=131072;r:continue_inventory()
assert(s.items["test:cold_1"] and s.items["test:cold_2"],"memory backpressure lost a buffered observation")

-- Expired credit still flushes the old tail, without borrowing the next grant.
local s,r=fixture({grant=16})
r:tick();s.grant=0;r:tick()
assert(s.reads==1 and s.items["test:cold_1"],"a control cycle discarded its unsent tail or reused expired credit")

-- The aggregation window ends even for a partial, ongoing scan.
local s,r=fixture({grant=16,config={batch_interval_seconds=0.2}})
r:tick();s.clock=s.clock+0.3;r:continue_inventory()
assert(s.items["test:cold_1"],"an aged partial batch waited indefinitely for more data")

for _,delay in ipairs({-1,1.1,math.huge}) do
  assert(not pcall(fixture,{config={batch_interval_seconds=delay}}),"invalid aggregation delay accepted")
end

-- Sending an aged batch can itself consume the slice. Do not start another
-- native read until the caller has had a chance to service its control deadline.
local s,r=fixture({grant=16,http_delay=0.1})
r:tick();s.clock=s.clock+0.3
local before=s.reads
r:continue_inventory()
assert(s.reads==before and s.items["test:cold_1"],"a slow batch flush overran the collection slice")

-- A successful retry consumes the same time slice as a first transmission.
-- Yield before reading again so the main loop can recheck its control deadline.
local s,r=fixture({grant=2,http_delay=0.1})
s.fail_upload=true
r:tick();r:continue_inventory()
assert(r.pending_inventory,"fixture never retained the unconfirmed batch")
local before=s.reads
s.grant=16
local status=r:tick()
assert(status.control_ok and status.inventory_ok and status.inventory_work)
assert(s.reads==before and not r.pending_inventory,"a slow successful retry started another native read in the same slice")

-- Zero-length slices still permit one native acquisition when there was no
-- pending transmission. They must not return "more work" forever without work.
local s,r=fixture({grant=2,config={scan_slice_seconds=0}})
local status=r:tick()
assert(s.reads==1 and status.inventory_work,"an empty processing slice never started acquisition")
assert(not r:continue_inventory() and s.reads==2 and s.items["test:cold_2"])

-- Scheduling timestamps cover control only; an upload in the same tick must
-- not become part of the next successful poll's starting point.
do
  local s,r=fixture({grant=1,http_delay=0.35})
  local status=r:tick()
  assert(status.control_started_at==0 and status.control_completed_at==0.35,"control timing did not bracket its request")
  assert(s.clock>status.control_completed_at,"fixture did not include inventory work after control")
end

do
  local s,r=fixture({grant=0})
  r.client.send=function()
    s.clock=s.clock+3
    error({code="transport_failed",fatal=false})
  end
  local status=r:tick()
  assert(not status.control_ok and status.control_started_at==0 and status.control_completed_at==3,"failed control timing lost the completion boundary for backoff")
end

-- CPU discovery can be slow. Its preparation time is not the HTTP request's
-- start: otherwise a short next poll could arrive before the server grant is due.
do
  local s,r=fixture({grant=1,http_delay=0.35})
  r.adapter.cpus=function() s.clock=s.clock+2;return {},false end
  local status=r:tick()
  assert(status.control_started_at==2 and status.control_completed_at==2.35,"control preparation was counted as network request time")
end
