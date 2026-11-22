local adapter=require('aeorbit.adapter')
local queries,requests=0,0
local current={name='water',amount=1000,hasTag=false}
local function forbidden() error('full network enumeration attempted') end
local me={getItemInNetwork=forbidden,getFluidInNetwork=function() return current end,
 allItems=forbidden,getCraftables=forbidden,getFluidsInNetwork=forbidden,getItemsInNetwork=forbidden,
 getCpus=function() return {{name='Fluid CPU',busy=false}} end}
me.getCraftable=function(detail,kind)
 assert(kind=='fluid' and detail.name=='water' and detail.amount==0,'fluid recipe parsed as item')
 queries=queries+1
 return {getStack=function() return current end,request=function(amount,power,cpu)
  assert(amount==1000 and power==true and cpu=='Fluid CPU','fluid amount or CPU changed')
  requests=requests+1;return {isDone=function()return true end}
 end}
end
local a=adapter.new(me,{inventory_mode='exact_only'})
assert(a:capabilities().craft_fluids)
local definition={kind='fluid',name='water',damage=0,exact=true,label='Water'}
local raw=a:from_definition(definition)
assert(a:recipe_status(raw))
local handle,reason,submitted=a:craft({item=definition,quantity='1000',cpu_name='Fluid CPU'})
assert(handle and not reason and submitted and requests==1)
current={name='lava',amount=1000,hasTag=false}
assert(not pcall(a.recipe_status,a,raw),'wrong output accepted')
handle,reason,submitted=a:craft({item=definition,quantity='1000',cpu_name='Fluid CPU'})
assert(not handle and not submitted and requests==1)
current={name='water',amount=1000,hasTag=true}
assert(not pcall(a.recipe_status,a,raw),'tagged output accepted')
current={name='water',damage=0,size=1000}
assert(not pcall(a.recipe_status,a,raw),'item-shaped output accepted as fluid')
me.getCraftable=function()return nil end
assert(not pcall(a.recipe_status,a,raw),'unconfirmed recipe reported absent')
assert(not adapter.new(me,{role='quantity_worker',inventory_mode='exact_only'}):capabilities().craft_fluids)

-- Exercise the cooperative signed-read and journal-before-request paths.
local fixture=require('cooperative_fixture')
local s,r=fixture({role='primary',config={inventory_mode='exact_only',batch_interval_seconds=0}})
s.me.getFluidInNetwork=function(detail)return {name=detail.name,amount=0,hasTag=false}end
s.me.getCraftable=function(detail,kind)
 assert(kind=='fluid' and detail.name=='water')
 s.recipe_reads=(s.recipe_reads or 0)+1
 return {getStack=function()return {name='water',amount=1000,hasTag=false}end,
 request=function(amount)
  assert(amount==1000 and (s.files['journal.1'] or s.files['journal.0']))
  s.fluid_requests=(s.fluid_requests or 0)+1
  return {hasFailed=function()return false end,isComputing=function()return false end,isCanceled=function()return false end,isDone=function()return true end}
 end}
end
r:start();assert(s.hello.capabilities.craft_fluids)
s.reads_ack={{id='2',item=definition,assignment_id=string.rep('a',32),kind='quantity_recipe',purpose='background',ttl_millis=30000}}
r:tick();s:drain()
local u=s:updates()[1];assert(u and u.id=='2' and u.quantity=='0' and u.craftable==true)
s.clock=s.clock+5
s.commands={{order_id='order-fluid',attempt_id='attempt-fluid',item_id='2',quantity='1000',current_quantity='0',item=definition}}
r:tick();s:drain();assert(s.fluid_requests==1 and (s.scans or 0)==0)
