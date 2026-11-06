local adapter = require("aeorbit.adapter")
local component_proxy = require("component_proxy")

local function all_items() return function() end end
local function lookup() return nil end
local function full_array() error("full inventory array fallback is forbidden") end

-- Unsupported hardware must identify the missing API before any acquisition.
for _,case in ipairs({
  {me={getItemInNetwork=lookup},missing="allItems"},
  {me={allItems={},getItemInNetwork=lookup},missing="allItems"},
  {me={allItems=all_items,getItemsInNetwork=full_array},missing="getItemInNetwork"},
  {me={allItems=all_items,getItemInNetwork={}},missing="getItemInNetwork"},
}) do
  local ok,err=pcall(adapter.new,case.me)
  assert(not ok and tostring(err):find(case.missing,1,true),"missing method was not identified: "..case.missing)
end

-- Optional methods must not advertise support for plain, non-callable tables.
local me={allItems=all_items,getItemInNetwork=lookup,getCraftable={},getCpus={}}
local a=adapter.new(me)
assert(not a:capabilities().craft and not a:capabilities().cpu_output)
local cpus,truncated=a:cpus();assert(#cpus==0 and not truncated)

-- Changing optional methods to real OC callbacks must expose their capabilities.
me.getCraftable=component_proxy.callback(function() return nil end)
me.getCpus=component_proxy.callback(function() return {{name="Idle",busy=false,storage=1024,coprocessors=0}} end)
assert(a:capabilities().craft and a:capabilities().cpu_output)
cpus,truncated=a:cpus();assert(#cpus==1 and cpus[1].name=="Idle" and not truncated)
