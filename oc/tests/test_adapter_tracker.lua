local adapter = require("aeorbit.adapter")
local tracker = require("aeorbit.tracker")
local journal = require("aeorbit.journal")
local json = require("aeorbit.json")
local component_proxy = require("component_proxy")

local function exercise(proxy,callable)
local raw={name="minecraft:stone",damage=0,label="Stone",size=4,hasTag=false,isCraftable=true}
local stage,calls=0,0
local status=proxy({
  hasFailed=function() return false end,
  isComputing=function() return stage==0 end,
  isCanceled=function() return false end,
  isDone=function() return stage==2 end
})
local me=proxy({
  allItems=function() local done=false;return callable(function() if done then return nil end;done=true;return raw end) end,
  getItemsInNetwork=function() error("full array fallback is forbidden") end,
  getItemInNetwork=function(detail) assert(detail.name=="minecraft:stone");return raw end,
  getCraftable=function(detail,kind)
    assert(detail.name=="minecraft:stone" and kind=="item")
    return proxy({getStack=function() return raw end,request=function(amount) calls=calls+1;assert(amount==64);return status end})
  end,
  getCpus=function() return {{name="Main",storage=4096,coprocessors=1,busy=true,cpu=proxy({finalOutput=function() return nil,"No crafting monitor" end})}} end
})
local a=adapter.new(me,{identity_bytes=4096,cpu_limit=8})
local capabilities=a:capabilities()
assert(capabilities.inventory and capabilities.craft and capabilities.cpu_output,"callable methods were hidden from capabilities")
local iterator=a:iterator();assert(iterator().name==raw.name and iterator()==nil)
assert(a:read(raw).size==4,"exact item lookup failed")
local definition=a:definition(raw);assert(definition.exact and definition.name==raw.name)
assert(not pcall(function() a:definition({name="test:tool",damage=0,hasTag=true,size=1}) end),"ambiguous NBT identity accepted")
local cpus,truncated=a:cpus();assert(#cpus==1 and not truncated and cpus[1].output==nil and cpus[1].output_unavailable~="")
me.getCpus=callable(function()
  return {{name="Main",storage=4096,coprocessors=1,busy=true,cpu=proxy({finalOutput=function() return raw end})}}
end)
cpus,truncated=a:cpus()
assert(#cpus==1 and not truncated and cpus[1].output.quantity=="4" and cpus[1].output.name=="minecraft:stone","CPU output callback failed")

local files,bad={},false
local fs={read=function(path) return files[path] end,write=function(path,data) assert(not bad,"disk full");files[path]=data;return true end}
local j=journal.new(fs,"tracker",32768)
j:save({attempts={},completed=json.array({})})
local t=tracker.new(a,j,4)
local command={order_id="order-1",attempt_id="attempt-1",item_id="1",item=definition,quantity="64",current_quantity="4"}
t:accept(command);t:accept(command);assert(calls==1,"duplicate command executed")
t:pump();local reports=t:reports();assert(#reports==1 and reports[1].state=="computing")
t:ack(reports);assert(#t:reports()==0)
stage=1;t:pump();assert(t:reports()[1].state=="running")
stage=2;t:pump();reports=t:reports();assert(reports[1].state=="done");t:ack(reports)
t:accept(command);assert(calls==1,"completed command replayed")

local second={order_id="order-2",attempt_id="attempt-2",item_id="1",item=definition,quantity="64"}
bad=true
assert(not pcall(function() t:accept(second) end) and calls==1,"AE invoked before durable intent")
bad=false
local recovered=tracker.new(a,journal.new(fs,"tracker",32768),4)
assert(#recovered:reports()==0,"completed task resurrected after reload")
end

exercise(function(methods) return methods end,function(callback) return callback end)
exercise(component_proxy.new,component_proxy.callback)
