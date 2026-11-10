local fixture=require("cooperative_fixture")
local failures={}
local function test(name,run)
  local ok,reason=pcall(run)
  if not ok then failures[#failures+1]=name..": "..tostring(reason) end
end
local function task(s,name,id,purpose,kind)
  local read=s:task(name,string.format("%032x",id),kind or "quantity",purpose)
  read.id=tostring(id)
  return read
end
local function expect_reads(s,names)
  local actual={}
  for _,read in ipairs(s.reads) do actual[#actual+1]=read.name end
  assert(table.concat(actual,",")==names,"unexpected native read order: "..table.concat(actual,","))
end

test("newly admitted refresh bypasses older background work",function()
  local s,r=fixture({grant=1,slice=0});r:start()
  s.reads_ack={task(s,"test:bg1",1,"background"),task(s,"test:bg2",2,"background"),
    task(s,"test:bg3",3,"background")}
  r:tick()
  s.clock=1;s.reads_ack={task(s,"test:refresh",4,"refresh")}
  r:tick()
  expect_reads(s,"test:bg1,test:refresh")
  assert(r.tasks.count==2 and r.credits==0 and not r:continue_inventory(),
    "priority selection exceeded a one-credit grant")
end)

test("weighted opportunities preserve FIFO within each eligible lane",function()
  local s,r=fixture({grant=1,slice=0,memory=1048576});r:start()
  s.reads_ack={task(s,"test:bg1",1,"background"),task(s,"test:bg2",2,"background"),
    task(s,"test:refresh1",3,"refresh"),task(s,"test:refresh2",4,"refresh"),
    task(s,"test:refresh3",5,"refresh"),task(s,"test:refresh4",6,"refresh"),
    task(s,"test:order1",7,"order"),task(s,"test:order2",8,"order")}
  for i=0,7 do s.clock=i;r:tick() end
  expect_reads(s,"test:order1,test:refresh1,test:refresh2,test:bg1,test:order2,test:refresh3,test:refresh4,test:bg2")
  assert(r.tasks.count==0,"weighted scheduling left runnable assignments behind")
end)

test("continuous refresh pressure leaves bounded background progress",function()
  local s,r=fixture({grant=1,slice=0,memory=1048576});r:start()
  for i=1,6 do s.reads_ack[i]=task(s,"test:bg"..i,i,"background") end
  for i=1,6 do
    s.clock=i-1
    s.reads_ack[#s.reads_ack+1]=task(s,"test:refresh"..i,10+i,"refresh")
    r:tick()
  end
  expect_reads(s,"test:refresh1,test:refresh2,test:bg1,test:refresh3,test:refresh4,test:bg2")
end)

test("failed native work advances its opportunity and rotates within its lane",function()
  local s,r=fixture({grant=1,slice=0});r:start()
  s.fail_read=function(detail) return detail.name=="test:failed_order" end
  s.reads_ack={task(s,"test:failed_order",1,"order"),task(s,"test:healthy_order",2,"order"),
    task(s,"test:refresh1",3,"refresh"),task(s,"test:refresh2",4,"refresh"),
    task(s,"test:bg",5,"background")}
  for i=0,4 do s.clock=i;r:tick() end
  expect_reads(s,"test:failed_order,test:refresh1,test:refresh2,test:bg,test:healthy_order")
  assert(r.diagnostics.read_failed=="1","failed lane retried before other eligible opportunities")
end)

test("backoff and in-flight tasks cannot block eligible work",function()
  local s,r=fixture({grant=4});r:start();s.grant_ttl=30000
  s.fail_read=function(detail) return detail.name=="test:failed_order" end
  s.reads_ack={task(s,"test:failed_order",1,"order"),task(s,"test:healthy_order",2,"order")}
  r:_control()
  local entry,worked=r:_read_task()
  assert(entry==nil and worked and r.credits==3)
  entry,worked=r:_read_task()
  assert(entry and worked and r.credits==2,"backoff head blocked healthy work")
  entry,worked=r:_read_task()
  assert(entry==nil and not worked and r.credits==2,"in-flight or backoff work was read again")
  s.clock=1
  entry,worked=r:_read_task()
  assert(entry==nil and worked and r.credits==1,"eligible failed assignment did not retry")
  expect_reads(s,"test:failed_order,test:healthy_order,test:failed_order")
  assert(r.tasks.entries[string.format("%032x",1)].value.retry_at==3,"retry backoff changed")
end)

test("unaffordable and cooling joint tasks preserve shared credit and cooldown bounds",function()
  local s,r=fixture({role="primary",grant=3});r:start();r.next_scan=math.huge
  s.reads_ack={task(s,"test:joint1",1,"order","quantity_recipe"),
    task(s,"test:joint2",2,"order","quantity_recipe"),task(s,"test:quantity",3,"order"),
    task(s,"test:refresh",4,"refresh"),task(s,"test:bg",5,"background")}
  r:_control()
  for _=1,3 do local entry,worked=r:_read_task();assert(entry and worked) end
  local entry,worked=r:_read_task()
  assert(entry==nil and not worked and r.credits==0 and not s.recipe_reads,
    "joint acquisition exceeded its four-credit cost")
  expect_reads(s,"test:quantity,test:refresh,test:bg")
  s.clock=1;s.grant=4;r:_control()
  entry,worked=r:_read_task()
  assert(entry and worked and r.credits==0 and s.recipe_reads==1 and r.next_recipe_check==6)
  s.clock=2;r:_control()
  entry,worked=r:_read_task()
  assert(entry==nil and not worked and r.credits==4 and s.recipe_reads==1,
    "joint task bypassed the shared five-second cooldown")
  s.clock=6;r:_control()
  entry,worked=r:_read_task()
  assert(entry and worked and r.credits==0 and s.recipe_reads==2)
  expect_reads(s,"test:quantity,test:refresh,test:bg,test:joint1,test:joint2")
end)

test("task opportunities still alternate with primary discovery",function()
  local s,r=fixture({role="primary",grant=3,slice=0});r:start();r.next_scan=math.huge
  r:on_item_changed({name="test:discovery",damage=0,label="Discovery",size=1})
  s.reads_ack={task(s,"test:refresh",1,"refresh"),task(s,"test:order",2,"order")}
  r:tick();s:drain()
  expect_reads(s,"test:order,test:discovery,test:refresh")
  assert(r.credits==0 and #s:updates()==3,"task priority starved discovery or minted credit")
end)

assert(#failures==0,table.concat(failures,"\n"))
