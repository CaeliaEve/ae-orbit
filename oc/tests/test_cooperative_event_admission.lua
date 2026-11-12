local fixture=require("cooperative_fixture")
local failures={}
local function test(name,run)
  local ok,reason=pcall(run)
  if not ok then failures[#failures+1]=name..": "..tostring(reason) end
end
local function task(s,id,purpose)
  local read=s:task("test:assigned_"..id,string.format("%032x",id),"quantity",purpose or "order")
  read.id=tostring(id)
  return read
end
local function event(r,id)
  local raw={name="test:event_"..id,damage=0,label="Event",size=1}
  r:on_item_changed(raw)
  return raw
end

test("events during a poll cannot take advertised assignment capacity",function()
  local s,r=fixture({role="primary",grant=8,memory=1048576})
  r:start();r.next_scan=math.huge;r.limits.read_bytes=4096
  for i=1,4 do s.reads_ack[i]=task(s,i) end
  local before_count,before_bytes,candidate_size,evicted
  s.mutate_poll_ack=function()
    s.catalog={}
    for i=1,8 do s.catalog[i]=event(r,i) end
    before_count,before_bytes=r.candidates.count,r.candidates.bytes
    candidate_size=r.candidates.last.size
    evicted=r.candidates.last.value.raw.name
  end
  r:_control()
  assert(s.polls[1].value.read_bytes==4096,"fixture did not advertise assignment room")
  assert(r.tasks.count==4 and not r.diagnostics.read_queue_full,
    "events during the poll stole admission from a valid assignment")
  assert(r.candidates.count>0 and r.candidates.count<before_count,
    "admission failed to retain bounded discovery work")
  assert(r.tasks.bytes+r.candidates.bytes<=4096 and
    r.tasks.bytes+r.candidates.bytes+candidate_size>4096,
    "admission reclaimed more discovery memory than necessary")
  assert(r.candidates.bytes<before_bytes and #s:updates()==0 and #s.reads==0,
    "admission published evidence or read hardware before a granted work turn")
  s:drain()
  assert(#s.reads>=2 and s.reads[1].name=="test:assigned_1" and
    s.reads[2].name:match("^test:event_"),"assignment admission starved the retained discovery lane")
  s.mutate_poll_ack=nil;s.clock=1;s.grant=32;r.next_scan=0
  r:tick();s:drain()
  local found=false
  for _,update in ipairs(s:updates()) do
    if update.item and update.item.name==evicted then
      found=true
      assert(update.assignment_id==nil,"catalog fallback invented assigned freshness")
    end
  end
  assert(found,"reclaimed event could not be recovered by catalog discovery")
end)

test("slot pressure reclaims a candidate without removing assigned work",function()
  local s,r=fixture({role="primary",grant=0,memory=1048576})
  r:start();r.next_scan=math.huge
  for i=1,12 do local read=task(s,i,"background");r:_admit(read,r:_validate_read(read),0) end
  for i=1,4 do event(r,i) end
  local read=task(s,13)
  r:_admit(read,r:_validate_read(read),0)
  assert(r.tasks.count==13 and r.candidates.count==3,
    "slot pressure rejected an assignment or discarded extra candidates")
  for i=1,13 do assert(r.tasks.entries[string.format("%032x",i)],"admission evicted assigned work") end
end)

test("inadmissible assignments do not discard discovery candidates",function()
  for _,reason in ipairs({"expired","oversized","fast_limit","task_bytes"}) do
    local s,r=fixture({role="primary",grant=0,memory=1048576})
    r:start();r.next_scan=math.huge
    if reason=="fast_limit" or reason=="task_bytes" then
      for i=1,8 do local read=task(s,i);r:_admit(read,r:_validate_read(read),0) end
    end
    for i=1,4 do event(r,i) end
    local before_count,before_bytes=r.candidates.count,r.candidates.bytes
    local oldest=r.candidates.last
    local read=task(s,20,reason=="task_bytes" and "background" or "order")
    if reason=="expired" then s.clock=31 end
    if reason=="oversized" then read.item.label=string.rep("x",9000) end
    if reason=="task_bytes" then r.limits.read_bytes=r.tasks.bytes end
    r:_admit(read,r:_validate_read(read),0)
    assert(not r.tasks.entries[read.assignment_id] and r.diagnostics.read_queue_full=="1",
      reason.." assignment bypassed admission checks")
    assert(r.candidates.count==before_count and r.candidates.bytes==before_bytes and r.candidates.last==oldest,
      reason.." assignment unnecessarily reclaimed discoveries")
  end
end)

assert(#failures==0,table.concat(failures,"\n"))
