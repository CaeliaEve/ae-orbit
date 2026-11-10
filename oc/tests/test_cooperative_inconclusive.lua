local fixture=require("cooperative_fixture")
local json=require("aeorbit.json")
local failures={}
local a,b=string.rep("a",32),string.rep("b",32)
local function test(name,run)
  local ok,reason=pcall(run)
  if not ok then failures[#failures+1]=name..": "..tostring(reason) end
end
local function setup(options)
  options=options or {role="primary",background_recipe_inconclusive=true,diagnostic_samples=4}
  local s,r=fixture(options)
  return s,r
end
local function unavailable(s)
  s.me.getCraftable=function()
    s.recipe_reads=(s.recipe_reads or 0)+1
    error("fixture recipe unavailable",0)
  end
end
local function start(s,r,assignment)
  s.reads_ack={assignment or s:task("test:exact",a,"quantity_recipe","background")}
  r:start();r.next_scan=math.huge;r:tick()
end
local function samples(s,r)
  s.grant=0;r:tick()
  return s.polls[#s.polls].value.health.read_failures
end

test("negotiated recipe failure uploads exact quantity with the original joint token",function()
  local s,r=setup()
  unavailable(s)
  start(s,r)
  local update=assert(s:updates()[1],"background quantity was withheld")
  assert(update.quantity=="42" and update.craftable==json.null)
  assert(update.recipe_inconclusive==true and update.assignment_id==a and update.id=="1")
  assert(update.observation=="1" and update.item==nil and update.kind==nil)
  assert(s.uploads[1].body:find('"recipe_inconclusive":true',1,true))
  assert(s.uploads[1].body:find('"craftable":null',1,true))
  assert(r.tasks.count==0 and r.diagnostics.read_failed=="1" and r.credits==0)
  local sample=assert(samples(s,r),"recipe failure lost bounded detail")[1]
  assert(sample.code=="recipe_lookup_failed" and sample.source=="recheck" and sample.item_id=="1")
  assert(sample.message:find("recipe: ",1,true)==1 and sample.occurrences=="1")
  assert(not sample.nbt and not sample.tag and #sample.message<=160)
end)

for _,case in ipairs({
  {name="absent"},{name="false",value=false},{name="string",value="true"},
  {name="zero",value=0},{name="one",value=1},{name="object",value={enabled=true}},
  {name="array",value=json.array({true})},{name="null",value=json.null},
}) do
  test(case.name.." negotiation retains fail-closed retry",function()
    local s,r=setup({role="primary",background_recipe_inconclusive=case.value})
    unavailable(s);start(s,r)
    assert(#s:updates()==0 and r.tasks.count==1 and r.diagnostics.read_failed=="1")
    assert(r.tasks.entries[a].value.kind=="quantity_recipe" and r.tasks.entries[a].value.assignment_id==a)
    s.clock=5
    s.me.getCraftable=function(detail)
      return {getStack=function() return {name=detail.name,damage=0} end}
    end
    r:tick()
    local update=assert(s:updates()[1],"strict fallback failed to retry")
    assert(update.craftable==true and update.assignment_id==a and update.recipe_inconclusive==nil)
  end)
end

for _,purpose in ipairs({"order","refresh"}) do
  test("negotiated "..purpose.." joint remains strict",function()
    local s,r=setup()
    unavailable(s)
    start(s,r,s:task("test:strict",a,"quantity_recipe",purpose))
    assert(#s:updates()==0 and r.tasks.count==1 and r.diagnostics.read_failed=="1")
    assert(r.tasks.entries[a].value.kind=="quantity_recipe")
    local sample=assert(samples(s,r),"strict joint failure lost diagnostic")[1]
    assert(sample.source==(purpose=="order" and "priority" or "refresh"))
    s.clock=5;s.grant=4
    s.me.getCraftable=function() return nil end
    s.me.getCraftables=function() return {} end
    r:tick()
    local update=assert(s:updates()[1],"strict task did not retry conclusive recipe")
    assert(update.quantity=="42" and update.craftable==false and update.recipe_inconclusive==nil)
  end)
end

for _,craftable in ipairs({true,false}) do
  test("conclusive "..tostring(craftable).." background recipe keeps ordinary receipt",function()
    local s,r=setup()
    if not craftable then
      s.me.getCraftable=function() return nil end
      s.me.getCraftables=function() return {} end
    end
    start(s,r)
    local update=assert(s:updates()[1])
    assert(update.quantity=="42" and update.craftable==craftable and update.recipe_inconclusive==nil)
    assert(r.tasks.count==0 and not r.diagnostics.read_failed)
  end)
end

for _,role in ipairs({"primary","quantity_worker"}) do
  test(role.." quantity assignment never becomes an inconclusive recipe attempt",function()
    local s,r=setup({role=role,background_recipe_inconclusive=true})
    unavailable(s)
    start(s,r,s:task("test:quantity",a,"quantity","background"))
    local update=assert(s:updates()[1])
    assert(update.quantity=="42" and update.craftable==json.null and update.recipe_inconclusive==nil)
    assert(not s.recipe_reads and not r.diagnostics.read_failed)
    if role=="quantity_worker" then assert(not r.background_recipe_inconclusive) end
  end)
end

test("worker still rejects a joint assignment despite a true welcome limit",function()
  local s,r=setup({role="quantity_worker",background_recipe_inconclusive=true})
  unavailable(s)
  s.reads_ack={s:task("test:joint",a,"quantity_recipe","background")}
  r:start()
  assert(not pcall(r.tick,r) and #s.reads==0 and #s:updates()==0)
end)

test("discovery never synthesizes a recipe attempt",function()
  local s,r=setup()
  unavailable(s)
  s.catalog={{name="test:discovery",damage=0,size="7"}}
  r:start();r:tick()
  local update=assert(s:updates()[1])
  assert(update.item.name=="test:discovery" and update.quantity=="7" and update.craftable==json.null)
  assert(update.assignment_id==nil and update.recipe_inconclusive==nil and not s.recipe_reads)
end)

test("exact native lookup failure cannot publish an inconclusive receipt",function()
  local s,r=setup()
  unavailable(s);s.fail_read=function() return true end
  start(s,r)
  assert(#s:updates()==0 and r.tasks.count==1 and not s.recipe_reads)
  local sample=assert(samples(s,r))[1]
  assert(sample.code=="exact_lookup_failed" and sample.message:find("quantity: ",1,true)==1)
end)

test("exact identity mismatch cannot publish an inconclusive receipt",function()
  local s,r=setup()
  unavailable(s)
  s.me.getItemInNetwork=function() return {name="test:other",damage=0,size="42"} end
  start(s,r)
  assert(#s:updates()==0 and r.tasks.count==1 and not s.recipe_reads)
  assert(r.diagnostics.read_failed=="1")
end)

local function tagged_task(s)
  local file=assert(io.open(TEST_ROOT.."/internal/nbt/testdata/vectors.json","rb"))
  local vectors=json.decode(file:read("*a"));file:close()
  local assignment=s:task("test:tagged",a,"quantity_recipe","background")
  for _,vector in ipairs(vectors) do
    if vector.name=="ordered" then assignment.item.nbt=vector.gzip end
  end
  assert(assignment.item.nbt)
  return assignment
end

test("same-name untagged exact response cannot satisfy tagged ownership",function()
  local s,r=setup()
  unavailable(s)
  s.me.getItemInNetwork=function() return {name="test:tagged",damage=0,size="42"} end
  start(s,r,tagged_task(s))
  assert(#s:updates()==0 and r.tasks.count==1 and not s.recipe_reads)
  assert(r.diagnostics.read_failed=="1")
  local sample=assert(samples(s,r))[1]
  assert(sample.code=="exact_lookup_failed" and sample.nbt_bytes>0 and sample.nbt_adler32)
  assert(not sample.nbt and not sample.tag)
end)

test("tagged recipe uncertainty publishes validated zero without recipe evidence",function()
  local s,r=setup()
  s.me.getItemInNetwork=function() return nil end
  s.me.getCraftable=function() return nil end
  s.me.getCraftables=function() return {} end
  start(s,r,tagged_task(s))
  local update=assert(s:updates()[1],"tagged quantity was withheld")
  assert(update.quantity=="0" and update.craftable==json.null and update.recipe_inconclusive==true)
  assert(update.id=="1" and update.assignment_id==a and update.item==nil and r.tasks.count==0)
  local sample=assert(samples(s,r))[1]
  assert(sample.code=="recipe_lookup_failed" and sample.nbt_bytes>0 and sample.nbt_adler32)
  assert(sample.message:find("recipe NBT encoding could not be verified",1,true))
  assert(not sample.nbt and not sample.tag)
end)

for _,count in ipairs({"-1","1.5","042","9223372036854775808",9007199254740992.0}) do
  test("invalid count "..tostring(count).." cannot publish an inconclusive receipt",function()
    local s,r=setup()
    unavailable(s);s.stock["test:exact"]=count
    start(s,r)
    assert(#s:updates()==0 and r.tasks.count==1 and s.recipe_reads==1)
    assert(r.diagnostics.read_failed=="2","candidate failure was swallowed with the recipe failure")
    local details=assert(samples(s,r))
    assert(#details==2 and details[1].message:find("candidate: ",1,true)==1)
  end)
end

test("large exact string quantities retain their decimal precision",function()
  local s,r=setup()
  unavailable(s);s.stock["test:exact"]="9007199254740993"
  start(s,r)
  local update=assert(s:updates()[1])
  assert(update.quantity=="9007199254740993" and update.recipe_inconclusive==true)
end)

test("lease expiry during failed recipe lookup prevents quantity progress",function()
  local s,r=setup()
  s.me.getCraftable=function() s.clock=1;error("fixture recipe unavailable",0) end
  start(s,r,s:task("test:exact",a,"quantity_recipe","background",1000))
  assert(#s:updates()==0 and not r.pending_inventory and r.diagnostics.read_failed=="1")
  s.grant=0;r:tick()
  assert(r.tasks.count==0,"expired task retained ownership")
end)

test("candidate encoding failure stays a failed attempt",function()
  local s,r=setup()
  unavailable(s)
  s.reads_ack={s:task("test:exact",a,"quantity_recipe","background")}
  r:start();r.next_scan=math.huge;r:_control()
  r.client.request_bytes=64
  r:continue_inventory()
  assert(#s:updates()==0 and not r.pending_inventory and r.tasks.count==1)
  assert(not r.tasks.entries[a].value.in_flight and r.diagnostics.read_failed=="2")
end)

test("outer batch encoding failure cannot freeze or ACK a partial receipt",function()
  local s,r=setup()
  unavailable(s)
  s.reads_ack={s:task("test:exact",a,"quantity_recipe","background")}
  r:start();r.next_scan=math.huge;r:_control()
  r.client.request_bytes=180
  r:continue_inventory()
  assert(#s:updates()==0 and not r.pending_inventory and r.tasks.count==1)
  assert(r.diagnostics.entry_exceeds_batch_budget=="1" and not r.tasks.entries[a].value.in_flight)
  assert(r.tasks.entries[a].value.kind=="quantity_recipe" and r.tasks.entries[a].value.assignment_id==a)
end)

test("unknown attempts retain four-credit cost and shared five-second cooldown",function()
  local s,r=setup({role="primary",background_recipe_inconclusive=true,grant=3})
  unavailable(s)
  s.reads_ack={s:task("test:exact",a,"quantity_recipe","background"),
    s:task("test:second",b,"quantity_recipe","background")}
  r:start();r.next_scan=math.huge;r:tick()
  assert(#s.reads==0 and not s.recipe_reads and r.credits==3)
  s.grant=8;s.grant_ttl=30000;s.clock=1;r:tick();s:drain()
  assert(#s:updates()==1 and #s.reads==1 and s.recipe_reads==1 and r.credits==4)
  s.clock=5.99;r:continue_inventory()
  assert(#s.reads==1 and r.credits==4,"unknown receipt bypassed shared recipe cooldown")
  s.clock=6;r:continue_inventory()
  assert(#s:updates()==2 and #s.reads==2 and s.recipe_reads==2 and r.credits==0 and r.tasks.count==0)
end)

test("lost upload ACK retries immutable unknown bytes without hardware reads",function()
  local s,r=setup()
  unavailable(s);s.fail_upload=true
  start(s,r)
  local pending=assert(r.pending_inventory,"unknown receipt was not frozen")
  local body=pending.body
  assert(r.tasks.entries[a].value.in_flight and r.tasks.entries[a].value.kind=="quantity_recipe")
  assert(json.decode(body).updates[1].recipe_inconclusive==true)
  s.clock=1;s.grant=0;s.stock["test:exact"]="99";r:tick()
  assert(#s.uploads==2 and s.uploads[1].body==body and s.uploads[2].body==body)
  assert(#s.reads==1 and s.recipe_reads==1 and r.tasks.count==0 and r.pending_inventory==nil)
end)

test("superseding ownership cannot change the old receipt or be removed by its ACK",function()
  local s,r=setup()
  unavailable(s);s.fail_upload=true
  start(s,r)
  local body=assert(r.pending_inventory).body
  s.clock=1;s.fail_upload=true
  s.reads_ack={s:task("test:exact",b,"quantity_recipe","background")}
  r:tick()
  assert(r.pending_inventory.body==body and r.tasks.count==1 and r.tasks.entries[b])
  assert(not r.tasks.entries[b].value.in_flight and #s.reads==1)
  s.clock=2;s.grant=0;r:tick()
  assert(r.pending_inventory==nil and r.tasks.count==1 and r.tasks.entries[b])
  assert(s.uploads[1].body==body and s.uploads[2].body==body and s.uploads[3].body==body)
  s.clock=5;s.grant=4;s.stock["test:exact"]="99";r:tick()
  local update=s.uploads[4].value.updates[1]
  assert(update.assignment_id==b and update.quantity=="99" and update.observation=="2")
  assert(update.recipe_inconclusive==true and #s.reads==2 and s.recipe_reads==2 and r.tasks.count==0)
end)

test("negotiation does not expand bounded task admission",function()
  local s,r=setup({role="primary",background_recipe_inconclusive=true,grant=0,memory=1048576})
  r:start();r.next_scan=math.huge
  for index=1,16 do
    local task=s:task("test:bounded"..index,string.format("%032x",index),"quantity_recipe","background")
    task.id=tostring(index);s.reads_ack[index]=task
  end
  r:tick()
  assert(r.tasks.count==16 and r.tasks.bytes<=r.task_bytes and #s.reads==0)
  s.reads_ack={s:task("test:overflow",a,"quantity_recipe","background")}
  r:tick()
  assert(r.tasks.count==16 and r.diagnostics.read_queue_full=="1")
  assert(s.polls[2].value.read_slots==0 and #s:updates()==0)
end)

assert(#failures==0,table.concat(failures,"\n"))
