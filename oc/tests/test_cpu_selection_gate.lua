local adapter = require("aeorbit.adapter")
local raw = {name="minecraft:stone",damage=0,label="Stone",hasTag=false,size=1}
local cpu = {name="Main",busy=false,storage=4096,coprocessors=1}
local requests, lookups, waits, now = {}, 0, 0, 0
local me = {
  allItems=function() return function() return nil end end,
  getItemInNetwork=function() return raw end,
  getCpus=function() return {cpu,{name="Other",busy=false,storage=4096,coprocessors=0}} end,
  getCraftable=function()
    lookups=lookups+1
    return {getStack=function() return raw end,request=function(...)
      requests[#requests+1]={...}
      return {}
    end}
  end
}
local a=adapter.new(me,{now=function() return now end,wait=function(seconds)
  waits=waits+1
  now=now+seconds
  cpu.busy=false
end})
assert(a:capabilities().cpu_selection==true,"strict CPU selection was not advertised")
local worker=adapter.new(me,{role="quantity_worker"})
assert(worker:capabilities().cpu_selection==false,"quantity worker advertised CPU selection")
local definition=a:definition(raw)

local handle,reason,submitted=a:craft({item=definition,quantity="64",cpu_name="Missing"})
assert(handle==nil and reason=="requested CPU not found" and submitted==false,
  "missing CPU was not rejected before submission")
assert(#requests==0 and lookups==0,"missing CPU reached the recipe/request path")

cpu.busy=true
handle,reason,submitted=a:craft({item=definition,quantity="64",cpu_name="Main"})
assert(handle==nil and reason=="requested CPU is busy" and submitted==false,
  "busy CPU with wait=false was not rejected")
assert(#requests==0 and lookups==0,"busy CPU without waiting reached submission")

local duplicate_busy={name="Duplicate",busy=true,storage=4096,coprocessors=1}
local duplicate_idle={name="Duplicate",busy=false,storage=4096,coprocessors=1}
me.getCpus=function() return {duplicate_busy,duplicate_idle} end
handle,reason,submitted=a:craft({item=definition,quantity="64",cpu_name="Duplicate"})
assert(handle==nil and reason=="requested CPU is ambiguous" and submitted==false,
  "duplicate CPU names were not rejected as ambiguous")
assert(#requests==0 and lookups==0,"ambiguous CPU target reached the recipe/request path")
handle,reason,submitted=a:craft({item=definition,quantity="64",cpu_name="Duplicate",wait_for_cpu=true})
assert(handle==nil and reason=="requested CPU is ambiguous" and submitted==false,
  "waiting on duplicate CPU names was not rejected as ambiguous")
assert(#requests==0 and lookups==0,"ambiguous waiting target reached the recipe/request path")
me.getCpus=function() return {cpu,{name="Other",busy=false,storage=4096,coprocessors=0}} end

handle,reason,submitted=a:craft({item=definition,quantity="64",cpu_name="Main",wait_for_cpu=true})
assert(handle~=nil and reason==nil and submitted==true,"wait_for_cpu did not submit after CPU became idle")
assert(waits==1 and #requests==1 and requests[1][1]==64 and requests[1][2]==true and
  requests[1][3]=="Main","strict CPU name was not passed to AE request")

handle,reason,submitted=a:craft({item=definition,quantity="64",wait_for_cpu=true})
assert(handle==nil and reason=="wait_for_cpu requires cpu_name" and submitted==false,
  "wait_for_cpu without a target was accepted")

handle,reason,submitted=a:craft({item=definition,quantity="64",cpu_name=false})
assert(handle==nil and reason=="invalid CPU target" and submitted==false,
  "invalid CPU target silently fell back to automatic crafting")

handle,reason,submitted=a:craft({item=definition,quantity="64",wait_for_cpu="true"})
assert(handle==nil and reason=="invalid CPU wait flag" and submitted==false,
  "invalid CPU wait flag silently fell back to automatic crafting")

handle,reason,submitted=a:craft({item=definition,quantity="64"})
assert(handle~=nil and reason==nil and submitted==true and #requests==2,
  "automatic crafting regressed")
