local fixture=require("cooperative_fixture")
local failures={}
local function test(name,run)
  local ok,reason=pcall(run)
  if not ok then failures[#failures+1]=name..": "..tostring(reason) end
end
local function task(s,id,purpose)
  local read=s:task("test:capacity_"..id,string.format("%032x",id),"quantity",purpose or "refresh")
  read.id=tostring(id)
  return read
end
local function unread_fast(r)
  local count=0
  for _,entry in pairs(r.tasks.entries) do
    if entry.value.purpose~="background" and not entry.value.in_flight then count=count+1 end
  end
  return count
end
local function frozen_fast_queue(role)
  local s,r=fixture({role=role,grant=8,slice=0,memory=1048576})
  r:start();r.next_scan=math.huge
  s.fail_read=function() return true end
  for group=0,1 do
    s.clock=group
    for index=1,4 do s.reads_ack[index]=task(s,group*4+index) end
    r:tick()
  end
  s.fail_read=nil;s.clock=2;s.fail_upload=true;r:tick()
  assert(r.tasks.count==8 and unread_fast(r)==7 and r.pending_inventory,
    "fixture did not retain one frozen receipt and seven unread tasks")
  assert(#s.reads==3 and r.diagnostics.read_failed=="2",
    "fixture did not exercise two bounded failures before freezing a successful read")
  return s,r
end

test("a lost ACK leaves room for one newly assigned unread fast task",function()
  for _,role in ipairs({"primary","quantity_worker"}) do
    local s,r=frozen_fast_queue(role)
    local frozen=r.pending_inventory
    local body=s.uploads[1].body
    local initial_reads=#s.reads
    local frozen_name=s.reads[initial_reads].name
    s.clock=s.clock+1;s.reads_ack={task(s,9)}
    s.fail_upload=true;r:tick()
    assert(r.tasks.entries[string.format("%032x",9)] and r.tasks.count==9 and unread_fast(r)==8,
      "frozen receipt consumed the eighth unread fast slot")
    assert(r.pending_inventory==frozen and s.uploads[#s.uploads].body==body and #s.reads==initial_reads,
      "admission changed or reread a frozen receipt")
    -- Deliberate over-assignment probes the client bound while retries stay frozen.
    for id=10,12 do
      s.clock=s.clock+1;s.reads_ack={task(s,id)};s.fail_upload=true;r:tick()
      assert(r.tasks.count==9 and unread_fast(r)==8 and not r.tasks.entries[string.format("%032x",id)],
        "repeated lost ACKs accumulated more than eight unread tasks")
      assert(r.pending_inventory==frozen and s.uploads[#s.uploads].body==body and #s.reads==initial_reads,
        "transport retries mutated the pending batch or froze another result")
    end
    assert(r.diagnostics.read_queue_full=="3","unread fast overflow did not remain bounded")
    s.stock[frozen_name]="999"
    s.clock=s.clock+1;s.grant=0;r:tick()
    assert(not r.pending_inventory and r.tasks.count==8 and r.inventory_seq=="1" and r.uploaded==1,
      "successful retry did not retire only the acknowledged frozen assignment")
    assert(s.uploads[#s.uploads].body==body and #s.reads==initial_reads,
      "ACK recovery reread changed stock instead of preserving immutable bytes")
    s.clock=s.clock+1;s.grant=8;r:tick();s:drain()
    assert(r.tasks.count==0 and r.uploaded==9 and #s.reads==initial_reads+8,
      "newly admitted task or retained unread work did not finish exactly once")
  end
end)

test("a frozen receipt still consumes the combined sixteen-task limit",function()
  local s,r=fixture({grant=8,slice=0,memory=1048576})
  r:start();s.fail_read=function() return true end
  for group=0,1 do
    for index=1,4 do s.reads_ack[index]=task(s,group*4+index) end
    r:tick()
  end
  for id=9,16 do s.reads_ack[#s.reads_ack+1]=task(s,id,"background") end
  r:tick()
  assert(r.tasks.count==16,"fixture did not fill the combined task queue")
  s.fail_read=nil;s.fail_upload=true;r:tick()
  assert(r.pending_inventory and unread_fast(r)==7)
  local bytes=r.tasks.bytes
  s.clock=1;s.reads_ack={task(s,17)};s.fail_upload=true;r:tick()
  assert(r.tasks.count==16 and r.tasks.bytes==bytes and not r.tasks.entries[string.format("%032x",17)],
    "frozen receipt was excluded from the combined count or byte accounting")
end)

test("a frozen receipt still consumes its full admission byte budget",function()
  local s,r=frozen_fast_queue("quantity_worker")
  local bytes=r.tasks.bytes
  local initial_reads=#s.reads
  r.limits.read_bytes=bytes
  s.clock=s.clock+1;s.reads_ack={task(s,9)};s.fail_upload=true;r:tick()
  assert(r.tasks.count==8 and r.tasks.bytes==bytes and not r.tasks.entries[string.format("%032x",9)],
    "frozen task bytes were omitted when admitting new unread work")
  assert(r.pending_inventory and unread_fast(r)==7 and #s.reads==initial_reads,
    "byte backpressure changed the frozen receipt")
end)

assert(#failures==0,table.concat(failures,"\n"))
