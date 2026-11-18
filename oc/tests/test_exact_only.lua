local adapter=require("aeorbit.adapter")
local proxy=require("component_proxy")
local fixture=require("cooperative_fixture")
local json=require("aeorbit.json")

local function exercise(wrap)
  local full_calls,requests=0,0
  local raw={name="test:known",damage=0,size=42,hasTag=false}
  local mode="found"
  local recipe=wrap({getStack=function() return raw end,request=function()
    requests=requests+1;return {}
  end})
  local function forbidden() full_calls=full_calls+1;error("full-list API invoked") end
  local me=wrap({allItems=forbidden,getCraftables=forbidden,getItemsInNetwork=forbidden,
    getItemInNetwork=function()
      if mode=="lookup_error" then return nil,"native lookup failed" end
      if mode=="absent" then return nil end
      return raw
    end,
    getCraftable=function() if mode=="found" then return recipe end end})
  local a=adapter.new(me,{inventory_mode="exact_only"})
  assert(a:capabilities().enumeration=="exact_only" and a:capabilities().craft)
  assert(a:read(raw).size==42 and a:recipe_status(raw)==true)
  local handle,reason,submitted=a:craft({item=a:definition(raw),quantity="1"})
  assert(handle and submitted and requests==1)
  assert(not pcall(a.iterator,a) and full_calls==0,"native iterator was constructed")
  mode="absent"
  assert(a:read(raw).size==0,"verified native absence did not report zero")
  assert(not pcall(a.recipe_status,a,raw),"unknown recipe was reported as absent")
  handle,reason,submitted=a:craft({item=a:definition(raw),quantity="1"})
  assert(not handle and not submitted and requests==1 and reason:find("exact-only",1,true))
  mode="lookup_error"
  assert(not pcall(a.read,a,raw),"native nil+error was reported as zero inventory")
  assert(full_calls==0,"exact-only mode invoked a filtered full-list fallback")
end
exercise(function(methods) return methods end)
exercise(proxy.new)

-- Existing known definitions remain readable even if enumeration is unsupported.
local a=adapter.new({getItemInNetwork=function() return nil end},{inventory_mode="exact_only"})
assert(a:read({name="test:empty",damage=0}).size==0)
assert(not pcall(adapter.new,{getItemInNetwork=function() end},{inventory_mode="unexpected"}))

do
  local s,r=fixture({role="quantity_worker",memory=2097152,config={inventory_mode="exact_only"}})
  s.me.allItems=function() error("worker created an unsafe native iterator") end
  r:start()
  assert(r.diagnostics.native_scan_disabled=="1")
  assert(s.hello.capabilities.native_scan_disabled and s.hello.capabilities.events_disabled)
  s.reads_ack={s:task("test:known",string.rep("a",32),"quantity","refresh")}
  r:tick();s:drain()
  local value=assert(s:updates()[1])
  assert(value.quantity=="42" and value.id=="1")
  assert(not r.iterator and not s.scans and not s.cpu_reads and not s.recipe_reads and not s.crafts)
end

do
  local s,r=fixture({role="primary",memory=2097152,grant=8,
    config={inventory_mode="exact_only",enable_events=true},background_recipe_inconclusive=true})
  s.me.allItems=function() error("unsafe native iterator created") end
  s.me.getCraftables=function() error("unsafe recipe enumeration") end
  s.me.getItemsInNetwork=function() error("unsafe storage enumeration") end
  s.me.getCraftable=function() return nil end
  r:start()
  assert(s.hello.capabilities.enumeration=="exact_only" and r.diagnostics.native_scan_disabled=="1")
  r:on_item_changed({name="test:undiscovered",damage=0,size=999})
  s.reads_ack={s:task("test:known",string.rep("a",32),"quantity_recipe","background")}
  r:tick();s:drain()
  local value=assert(s:updates()[1])
  assert(value.quantity=="42" and value.id=="1" and value.recipe_inconclusive==true and value.craftable==json.null)
  assert(not r.iterator and r.candidates.count==0 and r.dirty.count==0 and not s.scans)
  for _,time in ipairs({61,122}) do s.clock=time;r:tick();s:drain() end
  assert(not s.scans and not r.scan_completed and s.cpu_reads>0)
  for _,upload in ipairs(s.uploads) do assert(not upload.value.scan_completed) end
  s.reads_ack={s:task("test:second",string.rep("b",32),"quantity","refresh")}
  r:tick();s:drain()
  value=s:updates()[2]
  assert(value.id=="2" and value.quantity=="42" and not value.recipe_inconclusive)
end

do
  local s,r=fixture({role="primary",memory=2097152,config={inventory_mode="exact_only"},
    background_recipe_inconclusive=true})
  s.me.getCraftable=function() return nil end
  s.me.getCraftables=function() error("unsafe recipe fallback") end
  s.reads_ack={s:task("test:ordered",string.rep("a",32),"quantity_recipe","order")}
  r:start();r:tick();s:drain()
  assert(#s:updates()==0 and r.tasks.count==1,"unconfirmed recipe satisfied an order verification")
end
