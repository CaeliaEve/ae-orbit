local watches=require('aeorbit.cpu_watches')
local cache=require('aeorbit.cache')
local snapshots=cache.new(2,2048)
local active,busy=true,true
local output={name='test:goal',damage=0,hasTag=false,label='Goal'}
local function forbidden()error('inventory/list access forbidden')end
local original={isActive=function()return active end,isBusy=function()return busy end,finalOutput=function()return output end,
 activeItems=forbidden,pendingItems=forbidden,storedItems=forbidden}
snapshots:put('1:1',{cpu=original,output_identity=output},512)
local target=watches.new()
local a={target_id=string.rep('a',32),cpu_id='1:1',ttl_millis=120000}
assert(target:read({a},snapshots,0)[1].status=='busy')
-- Another identical CPU at this enumeration slot cannot replace the reference.
snapshots:remove('1:1')
snapshots:put('2:1',{cpu={isActive=forbidden,isBusy=forbidden,finalOutput=forbidden}},512)
output={name='test:goal',damage=0,hasTag=false,label='Goal',size=12}
assert(target:read({a},snapshots,10)[1].status=='busy')
busy=false
assert(target:read({a},snapshots,20)[1].status=='idle')
busy=true
assert(target:read({a},snapshots,30)[1].status=='idle','terminal task silently resumed')
-- A restarted process cannot rebind an expired observation by index.
assert(watches.new():read({a},snapshots,40)[1].status=='missing')
snapshots:put('3:1',{cpu=original,output_identity=output},512)
local b={target_id=string.rep('b',32),cpu_id='3:1',ttl_millis=120000}
active=false
assert(target:read({b},snapshots,50)[1].status=='inactive')
active=true
local c={target_id=string.rep('c',32),cpu_id='3:1',ttl_millis=120000}
output={name='test:different',damage=0,hasTag=false,label='Other'}
assert(target:read({c},snapshots,60)[1].status=='changed')
