local adapter = require("aeorbit.adapter")
local lookups, requests = 0, 0
local raw = {name="minecraft:stone",damage=0,label="Stone",hasTag=false,size=1}
local me = {
  allItems=function() return function() return nil end end,
  getItemInNetwork=function() return raw end,
  getCpus=function() return {{name="Main",busy=false,storage=4096,coprocessors=1}} end,
  getCraftable=function()
    lookups=lookups+1
    return {getStack=function() return raw end,request=function(amount)
      requests=requests+1
      assert(amount==64)
      return {}
    end}
  end
}
local a=adapter.new(me,{})
assert(a:capabilities().cpu_selection==false,"CPU observation must not imply strict selection support")
local worker=adapter.new(me,{role="quantity_worker"})
assert(worker:capabilities().cpu_selection==false,"worker advertised CPU selection")
local definition=a:definition(raw)
for _,command in ipairs({
  {item=definition,quantity="64",cpu_name="Main"},
  {item=definition,quantity="64",cpu_name="Missing",wait_for_cpu=true},
  {item=definition,quantity="64",wait_for_cpu=true},
  {item=definition,quantity="64",cpu_name=false},
  {item=definition,quantity="64",wait_for_cpu="true"}
}) do
  local handle,reason,submitted=a:craft(command)
  assert(handle==nil and reason=="strict CPU selection is unavailable" and submitted==false,
    "unsupported CPU command was not rejected before submission")
end
assert(requests==0 and lookups==0,"unsupported targets reached the game request path")
local handle,reason,submitted=a:craft({item=definition,quantity="64"})
assert(handle~=nil and reason==nil and submitted==true and requests==1,"automatic crafting regressed")
