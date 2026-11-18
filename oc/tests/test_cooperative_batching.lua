local fixture=require("cooperative_fixture")
local function background(s,index)
  local task=s:task("test:cold_"..index,string.format("%032x",index),"quantity","background")
  task.id=tostring(index);return task
end
local function catalog(s,count)
  s.catalog={}
  for index=1,count do s.catalog[index]={name="test:cold_"..index,damage=0,label="Cold "..index,size="42"} end
end

-- Both roles coalesce slow, one-native-call slices instead of one HTTP per item.
for _,role in ipairs({"primary","quantity_worker"}) do
  local s,r=fixture({role=role,grant=32,read_delay=0.05,memory=1048576})
  s.grant_ttl=30000;r:start()
  if role=="primary" then catalog(s,16)
  else for index=1,16 do s.reads_ack[index]=background(s,index) end end
  local result=r:tick()
  assert(result.inventory_work and #s:updates()==0,"first bounded slice was prematurely flushed")
  s:drain()
  assert(#s:updates()==16 and #s.uploads<=5,"cooperative collection still uploads once per native call")
  assert(r.statistics.updates==16 and r.statistics.batches==#s.uploads and r.statistics.collect_millis>=800)
end

-- A short scan sends its tail together with completion; urgent reads flush now.
do
  local s,r=fixture({role="primary",grant=16});catalog(s,2);r:start();r:tick();s:drain()
  assert(#s.uploads==1 and #s:updates()==2 and s.uploads[1].value.scan_completed)
  local worker,reader=fixture({grant=4,slice=0})
  reader:start();worker.reads_ack={worker:task("test:urgent")};reader:tick()
  assert(#worker.uploads==1 and worker.uploads[1].value.updates[1].assignment_id,
    "batch delay postponed an explicit refresh receipt")
end

-- Item and encoded-byte ceilings both split without dropping carry or tokens.
for _,options in ipairs({{batch_items=2},{config={max_batch_bytes=600}}}) do
  options.role="primary";options.grant=16
  local s,r=fixture(options);catalog(s,8);r:start();r:tick();s:drain()
  assert(#s:updates()==8)
  for _,upload in ipairs(s.uploads) do
    assert(#upload.body<=r.client.request_bytes and #upload.value.updates<=r.limits.batch_items)
  end
end

-- Low memory retains the unsent tail; expired grants flush without a new read.
do
  local s,r=fixture({role="primary",grant=16,read_delay=0.05})
  catalog(s,16);r:start();r:tick()
  assert(r.staged_inventory and #s:updates()==0)
  s.memory=8192;assert(not r:continue_inventory() and #s:updates()==0)
  s.memory=131072;s.clock=2;s.grant=0;r:tick()
  assert(#s:updates()==1 and r.statistics.credits_used==1,
    "expired grant lost a buffered observation or authorized another native read")
end

-- Lost confirmation freezes the whole body and counts only the successful ACK.
do
  local s,r=fixture({role="primary",grant=2,read_delay=0.05,http_delay=0.1})
  catalog(s,2);r:start();s.fail_upload=true;r:tick();r:continue_inventory()
  assert(r.pending_inventory and #s.uploads==1)
  local reads=s.scans;local bytes=s.uploads[1].body
  s.grant=0;s.clock=2;r:tick()
  assert(not r.pending_inventory and s.uploads[2].body==bytes and s.scans==reads)
  assert(r.statistics.batches==1 and r.statistics.updates==2 and r.statistics.bytes==#bytes)
end

-- An aged HTTP flush consumes its own slice before another native call.
do
  local s,r=fixture({role="primary",grant=16,read_delay=0.05,http_delay=0.1})
  catalog(s,16);r:start();r:tick();s.clock=s.clock+0.3
  local count=#r.staged_inventory.entries
  r:continue_inventory()
  assert(#s:updates()==count and not r.staged_inventory,"a slow flush overran the native read slice")
end

-- Larger configured caches retain a complete modest inventory within a byte cap.
do
  local s,r=fixture({role="primary",grant=512,memory=1048576,config={cache_entries=2048,cache_bytes=393216}})
  catalog(s,1000);r:start()
  for _=1,3 do r:tick();s:drain() end
  assert(r.ids.count==1000 and r.ids.bytes<=math.floor(s.memory/3),"larger cache still discarded most known identities")
end

for _,delay in ipairs({-1,1.1,math.huge}) do
  assert(not pcall(fixture,{config={batch_interval_seconds=delay}}),"unbounded batch delay accepted")
end
