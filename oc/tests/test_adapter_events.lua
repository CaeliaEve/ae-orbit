local adapter = require("aeorbit.adapter")

local expected_tag
local observed = {name="minecraft:stone",damage=0,label="Stone",size=777,hasTag=false,isCraftable=true}
local a = adapter.new({
  allItems=function() return function() end end,
  getItemsInNetwork=function() error("full inventory array fallback is forbidden") end,
  getItemInNetwork=function(detail)
    assert(detail.name=="minecraft:stone" and detail.damage==0)
    assert(detail.tag==expected_tag,"event NBT identity changed before exact lookup")
    assert(detail.hasTag==(expected_tag~=nil),"event NBT flag was not normalized")
    return observed
  end,
})

-- OC stores event payloads as NBT, which turns booleans into numeric bytes.
-- The event size is a delta; the adapter must reread the absolute quantity.
for _,case in ipairs({{flag=0},{flag=false},{}}) do
  local raw={name="minecraft:stone",damage=0,label="Stone",size=-5,hasTag=case.flag,isCraftable=0}
  assert(a:key(raw)=="minecraft:stone\0".."0\0")
  assert(a:read(raw).size==777,"event delta became an absolute quantity")
  assert(raw.size==-5 and raw.hasTag==case.flag,"event input was mutated")
end

expected_tag=require("aeorbit.base64").decode("H4sIAAAAAAAAA+NiYGAAAHg/+U4EAAAA")
observed.tag=expected_tag;observed.hasTag=true
for _,flag in ipairs({1,true}) do
  local raw={name="minecraft:stone",damage=0,label="Stone",size=1,hasTag=flag,tag=expected_tag}
  assert(a:read(raw).size==777)
  assert(a:from_definition(a:definition(raw)).tag==expected_tag,"binary NBT failed to round trip")
end

for _,flag in ipairs({1,true,2,-1,"0","false",{}}) do
  local raw={name="minecraft:stone",damage=0,label="Stone",size=1,hasTag=flag}
  assert(not pcall(a.descriptor,a,raw),"ambiguous event NBT identity was accepted")
end
