local fixture=require("inventory_fixture")
local function snapshot(s,r)
  s.grant=0;s.clock=math.max(s.clock,31);r:tick()
  return assert(s.polls[#s.polls].health.sync,"negotiated latency snapshot is missing")
end

-- The first event's age survives repeated hints. Queue time includes the
-- intervening control request, while upload and batch ACK use separate clocks.
local s,r,raw=fixture({grant=1,http_delay=0.01,sync_telemetry=true,config={batch_interval_seconds=0}})
r:on_item_changed(raw("test:active"));r:on_item_changed(raw("test:other"))
s.clock=0.2;r:on_item_changed(raw("test:active"));r:tick()
local sync=snapshot(s,r)
assert(sync.captured_millis=="31000" and sync.first_scan_ack_millis==nil)
local wait=assert(sync.stages.event_queue)
assert(wait.samples=="1" and wait.total_millis=="210" and wait.max_millis=="210" and wait.buckets[4]=="1",
  "event wait was reset by coalescing or mixed with native read time")
assert(sync.stages.inventory_http.total_millis=="10" and sync.stages.control_http.samples=="1")
assert(sync.stages.collection and sync.stages.collection.total_millis=="60",
  "native acquisition time was not separated from queue and HTTP waits")
assert(sync.stages.batch_ack.total_millis=="10","ACK latency did not start after sampling")
s.clock=32;r:tick()
assert(not s.polls[#s.polls].health.sync,"unchanged counters were resent every poll")

-- Failed HTTP attempts count as attempts; the inventory confirmation counts
-- once and spans the lost ACK, next control poll and successful retransmission.
local s,r=fixture({grant=2,http_delay=0.1,sync_telemetry=true,config={batch_interval_seconds=0}})
s.fail_upload=true;r:tick();s.grant=0;r:tick()
local sync=snapshot(s,r)
local http=assert(sync.stages.inventory_http)
assert(http.samples=="2" and http.errors=="1" and http.total_millis=="200" and http.max_millis=="100")
assert(sync.stages.batch_ack.samples=="1" and sync.stages.batch_ack.total_millis=="300",
  "retry changed the sample clock or double-counted the batch confirmation")
assert(s.accepted_updates==1 and s.accepted_batches==1,"timing changed retry idempotency")

-- Completing enumeration is not confirmation. The first scan timestamp is
-- retained after subsequent scans and only advances after a valid final ACK.
local s,r=fixture({catalog_size=1,grant=4,http_delay=0.1,sync_telemetry=true})
r:tick();s.fail_upload=true;r:continue_inventory()
assert(r.pending_inventory and not r.first_scan_acked)
s.grant=0;r:tick()
local sync=snapshot(s,r)
assert(sync.first_scan_ack_millis=="520" and sync.stages.scan.samples=="1" and sync.stages.scan.total_millis=="420",
  "first scan timing excluded confirmation recovery")
s.clock=70;s.grant=4;r:tick();while r:continue_inventory() do end
s.clock=101
local later=snapshot(s,r)
assert(later.first_scan_ack_millis=="520" and later.stages.scan.samples=="2","a later scan replaced the first completion")

-- Sparse timing survives control retries byte-for-byte and is only rate
-- limited after acknowledgement. Sampling remains paused during low memory.
local s,r=fixture({grant=0,sync_telemetry=true})
local lost
s.on_poll=function(body)
  if not lost then lost=body;error({fatal=false,code="transport_failed"}) end
  assert(body==lost,"a control retry rebuilt its timing snapshot");s.on_poll=nil
end
r:tick();s.clock=2;r:tick();r:tick()
assert(not s.polls[#s.polls].health.sync,"a recovered snapshot was immediately sent again")
s.clock=33;s.memory=8192;r:tick()
assert(not s.polls[#s.polls].health.sync,"timing allocation ignored low memory")
s.memory=131072;r:tick()
assert(s.polls[#s.polls].health.sync,"deferred timing was lost after memory recovered")

-- Older servers must never receive unknown fields, and timing must yield its
-- space to required poll reports under the smallest request budget.
local s,r=fixture({grant=1,bytes=512})
r:tick();s.clock=31;r:tick()
assert(not s.polls[1].health.sync and not s.polls[2].health.sync,"timing was sent without negotiation")
local s,r=fixture({grant=1,bytes=512,sync_telemetry=true})
r:tick();s.clock=31;r:tick()
assert(#s.polls==2 and s.polls[2].health.inventory and not s.polls[2].health.sync,
  "optional timing displaced control data under a small request budget")
