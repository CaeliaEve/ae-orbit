local adapter=require('aeorbit.adapter')
local calls=0
local value={name='water',amount=125000,hasTag=false}
local function exact(detail)
  assert(type(detail)=='table' and detail.name and detail.amount==0,'unfiltered resource query')
  calls=calls+1;return value
end
local function forbidden() error('full network enumeration attempted') end
local a=adapter.new({getItemInNetwork=function() end,getFluidInNetwork=exact,getEssentiaInNetwork=exact,
  allItems=forbidden,getItemsInNetwork=forbidden,getFluidsInNetwork=forbidden,getCraftables=forbidden},
  {inventory_mode='exact_only'})
assert(a:capabilities().fluids and a:capabilities().essentia)
local fluid=a:from_definition({kind='fluid',name='water',damage=0,exact=true})
assert(a:count(a:read(fluid))=='125000')
assert(a:definition(fluid).kind=='fluid')
local aspect=a:from_definition({kind='essentia',name='aer',exact=true})
assert(a:key(fluid)~=a:key({kind='essentia',name='water'}),'resource kinds collide')
value={name='aer',size='9007199254740993',amount=1}
assert(a:count(a:read(aspect))=='9007199254740993','exact quantity lost precision')
value={name='aer',amount=9007199254740992.0}
assert(not pcall(function() a:count(a:read(aspect)) end),'inexact native number accepted')
value={name='different',amount=2}
assert(not pcall(a.read,a,aspect),'wrong resource accepted')
value={name='water',amount=2,hasTag=true}
assert(not pcall(a.read,a,fluid),'tagged fluid collapsed')
value=nil
assert(a:count(a:read(fluid))=='0')
assert(not pcall(a.recipe_status,a,aspect),'essentia entered recipe lookup')
local handle,reason,submitted=a:craft({item=a:definition(fluid),quantity='1'})
assert(handle==nil and not submitted and reason=='recipe lookup failed')
assert(calls==6)
local old=adapter.new({getItemInNetwork=function()end},{inventory_mode='exact_only',role='quantity_worker'})
assert(not old:capabilities().fluids and not old:capabilities().essentia)
assert(not pcall(old.from_definition,old,{kind='fluid',name='water',exact=true}))
