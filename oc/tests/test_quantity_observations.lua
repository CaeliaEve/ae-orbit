local adapter = require("aeorbit.adapter")
local runner = require("aeorbit.runner")
local json = require("aeorbit.json")

local calls=0
local raw={name="gregtech:gt.metaitem.01",damage=17032,label="Iron plate",size=30795,hasTag=false,isCraftable=false}
local a=adapter.new({
  allItems=function() return function() end end,
  getItemInNetwork=function() calls=calls+1;return raw end,
  getCraftables=function() error("hot quantity observations must not scan recipes") end,
  getItemsInNetwork=function() error("full inventory array fallback is forbidden") end,
})
local r=runner.new({},{adapter=a,client={request_bytes=4096},free_memory=function() return 131072 end})
local observed=a:read(raw)
assert(observed.size==30795 and observed.isCraftable==nil,"quantity lookup falsely claimed that a recipe was unavailable")
assert(raw.isCraftable==false,"native result was mutated")
local candidate=json.decode(r:_candidate(observed).encoded)
assert(candidate.quantity=="30795" and candidate.craftable==json.null,"unknown craftability was converted to false or omitted")

raw.isCraftable=true
candidate=json.decode(r:_candidate(raw).encoded)
assert(candidate.craftable==true,"authoritative iterator lost a craftable recipe")
raw.isCraftable=false
candidate=json.decode(r:_candidate(raw).encoded)
assert(candidate.craftable==false,"authoritative recipe removal was ignored")

local identity=a:descriptor(raw)
raw=nil
observed=a:read(identity)
candidate=json.decode(r:_candidate(observed).encoded)
assert(candidate.quantity=="0" and candidate.craftable==json.null,"zero stock was confused with recipe removal")
assert(calls==2)
