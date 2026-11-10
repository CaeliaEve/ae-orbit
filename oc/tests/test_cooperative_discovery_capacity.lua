local fixture=require("cooperative_fixture")
local failures={}
local function test(name,fn)
  local ok,err=pcall(fn)
  if not ok then failures[#failures+1]=name..": "..tostring(err) end
end
local function event(r,index,label)
  r:on_item_changed({name="test:event_"..index,damage=0,label=label or "Event",size=1})
end
local function grant_when_ready(s)
  s.mutate_poll_ack=function(ack)
    local poll=s.polls[#s.polls].value
    ack.scan_budget=poll.read_slots>0 and poll.read_bytes>0 and 1 or 0
  end
end

test("a candidate-only event burst can obtain a grant and drain",function()
  local s,r=fixture({role="primary",grant=0,memory=1048576})
  r:start();r.next_scan=math.huge
  for i=1,32 do event(r,i) end
  assert(#s.reads==0,"event handler performed acquisition without credits")
  grant_when_ready(s)
  r:tick();s:drain()
  local poll=s.polls[1].value
  assert(poll.read_slots>0 and poll.read_bytes>0,"discovery exhausted its own execution eligibility")
  assert(#s.reads==1 and #s:updates()==1,"queued discovery did not drain after receiving credit")
  assert(r.candidates.count>0,"event burst lost every bounded candidate")
end)

test("a ready memory reduction leaves real admission capacity",function()
  local s,r=fixture({role="primary",grant=0,memory=1048576})
  r:start();r.next_scan=math.huge
  for i=1,8 do event(r,i,string.rep("x",100)) end
  assert(r.candidates.bytes>2500,"fixture did not build a memory-bound candidate backlog")
  s.memory=40000
  grant_when_ready(s)
  r:tick();s:drain()
  local poll=s.polls[1].value
  assert(poll.read_slots>0 and poll.read_bytes>0,"memory reduction stranded local discoveries")
  assert(#s.reads==1,"memory-reduced candidate queue could not progress")
  assert(r.candidates.bytes<=1250,"lossy candidates exceeded half the current byte budget")
end)

test("negotiated small slot limits also leave a real free slot",function()
  for _,slots in ipairs({1,2,4}) do
    local s,r=fixture({role="primary",grant=0,memory=1048576})
    r:start();r.next_scan=math.huge;r.limits.read_slots=slots
    for i=1,16 do event(r,i) end
    r:tick()
    local poll=s.polls[1].value
    assert(poll.read_slots>0,"candidates exhausted negotiated slots")
    assert(poll.read_slots==slots-r.tasks.count-r.candidates.count,"reported slots were fabricated")
    assert(poll.read_bytes==math.min(r.task_bytes,math.floor(s.memory/16),r.limits.read_bytes)
      -r.tasks.bytes-r.candidates.bytes,"reported bytes were fabricated")
  end
end)

test("low memory still blocks native reads and advertises zero",function()
  local s,r=fixture({role="primary",grant=1,memory=1048576})
  r:start();r.next_scan=math.huge
  for i=1,8 do event(r,i) end
  s.memory=1000
  r:tick();s:drain()
  local poll=s.polls[1].value
  assert(poll.read_slots==0 and poll.read_bytes==0,"reservation disguised low-memory backpressure")
  assert(#s.reads==0,"candidate reservation bypassed the memory safety gate")
end)

test("trimming discoveries never removes assigned work",function()
  local s,r=fixture({role="primary",grant=0,memory=1048576})
  r:start();r.next_scan=math.huge
  local assignment=s:task("test:assigned",string.rep("a",32),"quantity","background")
  s.reads_ack={assignment};r:tick()
  local queued=r.tasks.entries[assignment.assignment_id]
  local task_bytes=r.tasks.bytes
  for i=1,8 do event(r,i,string.rep("x",100)) end
  s.memory=40000;r:tick()
  assert(r.tasks.entries[assignment.assignment_id]==queued and r.tasks.bytes==task_bytes,
    "discovery trimming changed an assigned task or its accounting")
  assert(r.candidates.bytes<=1250,"discovery backlog was not trimmed")
end)

test("rejected large event metadata can still use catalog discovery",function()
  local s,r=fixture({role="primary",grant=1,memory=1048576})
  r:start();r.limits.read_bytes=512
  local raw={name="test:large_event",damage=0,label=string.rep("x",100),size=1}
  r:on_item_changed(raw)
  assert(r.candidates.count==0,"large event consumed the reserved discovery budget")
  s.catalog={raw}
  r:tick();s:drain()
  local update=s:updates()[1]
  assert(update and update.item.name==raw.name and update.quantity=="1" and update.assignment_id==nil,
    "rejected event metadata blocked later bounded catalog iteration")
end)

test("pending control retries preserve frozen bytes across candidate trimming",function()
  local s,r=fixture({role="primary",grant=0,memory=1048576})
  r:start();r.next_scan=math.huge
  event(r,1)
  s.fail_poll=true;r:tick()
  local first=s.polls[1]
  for i=2,16 do event(r,i,string.rep("x",100)) end
  s.memory=40000;r:tick()
  assert(s.polls[2].body==first.body,"candidate trimming changed immutable retry bytes")
  r:tick()
  assert(r.candidates.bytes<=1250,"fresh control did not trim the reduced discovery budget")
end)

assert(#failures==0,table.concat(failures,"\n"))
