local adapter = require("aeorbit.adapter")
local runner = require("aeorbit.runner")

local clock,recipe_queries,stock=0,0,0
local hint={name="test:plate",damage=0,label="Plate",hasTag=false,isCraftable=true}
local a=adapter.new({
  allItems=function() error("scan should remain idle in this test") end,
  getItemInNetwork=function()
    if stock==0 then return nil end
    return {name=hint.name,damage=0,label=hint.label,hasTag=false,size=stock,isCraftable=false}
  end,
  getCraftable=function() return nil end,
  getCraftables=function(filter)
    recipe_queries=recipe_queries+1
    assert(filter.name==hint.name and filter.damage==0 and filter.hasTag==false)
    return {}
  end,
})
local r=runner.new({},{adapter=a,client={request_bytes=4096},now=function() return clock end,free_memory=function() return 131072 end})
r.next_scan=math.huge;r.credits=4
r:on_item_changed(hint,true)
local raw,cost=r:_next_raw()
assert(raw.size==0 and raw.isCraftable==false and recipe_queries==1,"deleted zero-stock recipe was not authoritatively rechecked")
assert(cost==4,"native recipe verification escaped the acquisition budget")

clock=1;r:on_item_changed(hint,true)
-- A newer event must not discard a queued backend recipe verification.
r:on_item_changed(hint)
raw,cost=r:_next_raw()
assert(raw.isCraftable==nil and recipe_queries==1 and r.dirty.count==1,"recipe rate limit was ignored or deferred verification was lost")
clock=5;r.credits=3
raw,cost=r:_next_raw()
assert(raw.isCraftable==nil and recipe_queries==1 and r.dirty.count==1,"recipe query exceeded the remaining grant")
r.credits=4
raw,cost=r:_next_raw()
assert(raw.isCraftable==false and recipe_queries==2 and r.dirty.count==0,"deferred recipe verification did not resume")

clock=10;stock=30
r:on_item_changed(hint,true)
raw=r:_next_raw()
assert(raw.size==30 and raw.isCraftable==nil and recipe_queries==2,"positive stock verification added a full recipe scan")
stock=0;r:on_item_changed(hint)
raw=r:_next_raw()
assert(raw.size==0 and raw.isCraftable==nil and recipe_queries==2,"ordinary inventory event scanned recipes")

a.recipe_status=function() error("AE lookup unavailable") end
r:on_item_changed(hint,true)
raw=r:_next_raw()
assert(raw.isCraftable==nil and r.diagnostics.recipe_lookup_failed=="1","failed recipe check erased a known recipe")
