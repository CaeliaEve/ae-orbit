local fixture=require("cooperative_fixture")
local json=require("aeorbit.json")
local base64=require("aeorbit.base64")
local failures={}
local function test(name,run)
  local ok,reason=pcall(run)
  if not ok then failures[#failures+1]=name..": "..tostring(reason) end
end
local function setup(options)
  options=options or {}
  if options.diagnostic_samples==nil then options.diagnostic_samples=4 end
  local s,r=fixture(options)
  r:start();r.next_scan=math.huge
  return s,r
end
local function next_samples(s,r)
  s.grant=0;r:tick()
  return s.polls[#s.polls].value.health.read_failures
end

for _,role in ipairs({"primary","quantity_worker"}) do
  for purpose,source in pairs({order="priority",refresh="refresh",background="recheck"}) do
    test(role.." "..purpose.." exact failure keeps its identity and pending work",function()
      local s,r=setup({role=role})
      s.fail_read=function() return true end
      s.reads_ack={s:task("test:failed",string.rep("a",32),"quantity",purpose)}
      r:tick()
      local samples=next_samples(s,r)
      assert(samples and #samples==1,"cooperative read discarded its diagnostic detail")
      local sample=samples[1]
      assert(sample.code=="exact_lookup_failed" and sample.source==source)
      assert(sample.item_id=="1" and sample.name=="test:failed" and sample.damage==0 and sample.nbt_bytes==0)
      assert(sample.message:find("quantity: ",1,true)==1 and sample.message:find("hardware unavailable",1,true))
      assert(sample.occurrences=="1" and r.diagnostics.read_failed=="1")
      assert(r.tasks.count==1 and #s:updates()==0,"diagnostics consumed work or invented stock")
    end)
  end
end

test("recipe errors are distinguished from quantity errors",function()
  local s,r=setup({role="primary"})
  s.me.getCraftable=function() error("native recipe failure",0) end
  s.reads_ack={s:task("test:recipe",string.rep("b",32),"quantity_recipe","order")}
  r:tick()
  local sample=assert(next_samples(s,r),"recipe failure lost its reason")[1]
  assert(sample.code=="recipe_lookup_failed" and sample.source=="priority")
  assert(sample.message:find("recipe: ",1,true)==1 and sample.message:find("recipe lookup failed",1,true))
  assert(sample.item_id=="1" and r.tasks.count==1 and #s:updates()==0 and r.diagnostics.read_failed=="1")
end)

test("candidate validation is not presented as a native call failure",function()
  local s,r=setup()
  s.stock["test:invalid"]="not a decimal quantity"
  s.reads_ack={s:task("test:invalid",string.rep("c",32),"quantity","background")}
  r:tick()
  local sample=assert(next_samples(s,r),"candidate failure lost its reason")[1]
  assert(sample.code=="exact_lookup_failed" and sample.source=="recheck")
  assert(sample.message:find("candidate: ",1,true)==1,"candidate error was attributed to the native lookup")
  assert(r.tasks.count==1 and #s:updates()==0 and r.diagnostics.read_failed=="1")
end)

test("unknown event read failures retain bounded discovery context",function()
  local s,r=setup({role="primary"})
  s.fail_read=function() return true end
  r:on_item_changed({name="test:event",damage=0,label="Event"})
  r:tick()
  local sample=assert(next_samples(s,r),"event read discarded its reason")[1]
  assert(sample.source=="event" and sample.item_id==nil and sample.name=="test:event")
  assert(sample.message:find("quantity: ",1,true)==1 and r.diagnostics.discovery_read_failed=="1")
  assert(r.candidates.count==1 and #s:updates()==0)
end)

test("inconclusive tagged recipes remain unconfirmed while worker quantities can succeed",function()
  local file=assert(io.open(TEST_ROOT.."/internal/nbt/testdata/vectors.json","rb"))
  local vectors=json.decode(file:read("*a"));file:close()
  local nbt
  for _,vector in ipairs(vectors) do if vector.name=="ordered" then nbt=vector.gzip end end
  assert(nbt)
  local s,r=setup({role="primary"})
  s.me.getItemInNetwork=function() return nil end
  s.me.getCraftable=function() return nil end
  s.me.getCraftables=function() return {} end
  local assignment=s:task("test:tagged",string.rep("d",32),"quantity_recipe","background")
  assignment.item.nbt=nbt;s.reads_ack={assignment}
  r:tick()
  local sample=assert(next_samples(s,r),"inconclusive recipe lost its diagnostic")[1]
  assert(sample.code=="recipe_lookup_failed" and sample.message:find("recipe NBT encoding could not be verified",1,true))
  assert(sample.nbt_bytes==#base64.decode(nbt) and sample.nbt_adler32 and not sample.tag and not sample.nbt)
  assert(r.tasks.count==1 and #s:updates()==0,"an inconclusive recipe invented an atomic joint receipt")
  local worker,reader=setup()
  worker.me.getItemInNetwork=function() return nil end
  local quantity=worker:task("test:tagged",string.rep("e",32),"quantity","background")
  quantity.item.nbt=nbt;worker.reads_ack={quantity}
  reader:tick()
  assert(worker:updates()[1].quantity=="0" and worker:updates()[1].craftable==json.null)
  assert(reader.tasks.count==0 and not reader.diagnostics.read_failed)
end)

test("lost poll ACK freezes samples and cannot acknowledge newer failures",function()
  local s,r=setup()
  s.grant_ttl=30000;s.fail_read=function() return true end
  s.reads_ack={s:task("test:retry")}
  r:tick();s.fail_poll=true
  assert(not r:tick().control_ok)
  local body=s.polls[#s.polls].body
  assert(json.decode(body).health.read_failures,"fixture did not freeze a diagnostic snapshot")
  s.clock=2;r:continue_inventory();s.memory=120000
  assert(r:tick().control_ok and s.polls[#s.polls].body==body,"retry changed frozen poll bytes")
  assert(not next_samples(s,r),"new diagnostic ignored its ACK-relative throttle")
  s.clock=33
  local samples=assert(next_samples(s,r),"old ACK discarded a newer diagnostic generation")
  assert(samples[1].occurrences=="2")
  s.clock=64
  assert(not next_samples(s,r),"unchanged diagnostic was sent again")
end)

test("order reports retain priority and deferred samples are not discarded",function()
  local s,r=setup({role="primary",config={max_batch_bytes=600}})
  s.me.getItemInNetwork=function() error(string.rep("x",160),0) end
  s.reads_ack={s:task("test:budget")}
  r:tick()
  r.tracker.data.attempts.attempt={order_id="order",attempt_id="attempt",state="failed",reason=string.rep("r",160)}
  assert(not next_samples(s,r),"oversized optional detail displaced essential control work")
  local poll=s.polls[#s.polls].value
  assert(#poll.reports==1 and poll.reports[1].state=="failed" and #s.polls[#s.polls].body<=600)
  assert(not r.tracker.data.attempts.attempt,"real report did not receive its ACK")
  assert(next_samples(s,r),"budget-deferred diagnostic was discarded")
end)

test("hostile error objects are not stringified or retained",function()
  local s,r=setup()
  s.me.getItemInNetwork=function()
    error(setmetatable({secret="private payload"},{__tostring=function() error("must not stringify",0) end}),0)
  end
  s.reads_ack={s:task("test:object")}
  r:tick()
  local samples=assert(next_samples(s,r),"non-string exception lost its diagnostic")
  assert(samples[1].message=="quantity: non-string error (table)" and samples[1].truncated)
  assert(not json.encode(samples):find("private payload",1,true))
end)

for _,limit in ipairs({0,-1,5,1.5,"4",false}) do
  test("invalid negotiation remains disabled: "..tostring(limit),function()
    local s,r=setup({diagnostic_samples=limit})
    s.fail_read=function() return true end;s.reads_ack={s:task("test:disabled")}
    r:tick()
    assert(not next_samples(s,r) and r.diagnostics.read_failed=="1")
  end)
end
test("missing negotiation remains disabled",function()
  local s,r=fixture();r:start()
  s.fail_read=function() return true end;s.reads_ack={s:task("test:legacy")}
  r:tick()
  assert(not next_samples(s,r) and r.diagnostics.read_failed=="1")
end)
test("the negotiated sample count bounds transmitted details",function()
  local s,r=setup({diagnostic_samples=1})
  s.fail_read=function() return true end
  s.reads_ack={s:task("test:first",string.rep("a",32)),s:task("test:second",string.rep("b",32))}
  r:tick()
  assert(#assert(next_samples(s,r),"bounded diagnostic absent")==1)
end)

assert(#failures==0,table.concat(failures,"\n"))
