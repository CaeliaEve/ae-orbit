local subnet=require('aeorbit.subnet')
local json=require('aeorbit.json')
local now=0
local a={id=string.rep('a',32),kind='item',revision=1,max_items=128,ttl_millis=60000}
local enumerations,subscriptions=0,0
local function forbidden()error('iterator/recipe/CPU read forbidden')end
local proxy={getItemInNetwork=function(d)return {name=d.name,damage=d.damage,size=12}end,
 getItemsInNetwork=function()enumerations=enumerations+1;return {{name='minecraft:stone',damage=0,size=12,label='Stone'},{name='minecraft:dirt',damage=0,size=0}}end,
 getFluidsInNetwork=function()enumerations=enumerations+1;return {{name='water',amount=1000,label='Water'}}end,
 allItems=forbidden,getCpus=forbidden,getCraftables=forbidden,
 setItemEventSubscription=function(v)assert(v==false);subscriptions=subscriptions+1 end,
 setFluidEventSubscription=function(v)assert(v==false);subscriptions=subscriptions+1 end}
local r=subnet.snapshot(proxy,a,nil,function()return 1048576 end)
assert(not r.error and #r.items==1 and r.items[1].quantity=='12' and r.items[1].item.name=='minecraft:stone')
a.kind='fluid';r=subnet.snapshot(proxy,a);assert(#r.items==1 and r.items[1].quantity=='1000' and r.items[1].item.kind=='fluid')
local saved=proxy.getItemsInNetwork;a.kind='item'
proxy.getItemsInNetwork=function()local rows={};for i=1,129 do rows[i]={}end;return rows end
r=subnet.snapshot(proxy,a);assert(r.error=='too_many_items' and #r.items==0)
proxy.getItemsInNetwork=function()error('AE unavailable')end;r=subnet.snapshot(proxy,a);assert(r.error=='read_failed' and #r.items==0)
proxy.getItemsInNetwork=saved;local before=enumerations;r=subnet.snapshot(proxy,a,nil,function()return 1 end);assert(r.error=='memory_low' and enumerations==before)
-- A lost upload reuses identical encoded data and never recollects a list.
local addr='11111111-1111-1111-1111-111111111111'
local component={list=function()return function()end end,proxy=function(address)assert(address==addr);return proxy end}
local function w(seq,request)return {session_id=string.rep('b',32),subnet_id=string.rep('c',32),sequence=seq,component_address=addr,revision=1,poll_millis=250,interval_seconds=1,item_count=0,budget=0,reads=json.array({}),snapshot=request}end
local attempts,last=0,nil
local bridge=subnet.new({}, {component=component,now=function()return now end,free_memory=function()return 1048576 end,client={send=function(_,path,body)local p=json.decode(body)
 if path:find('hello')then assert(p.inventory_snapshot==true);return w(0)end
 attempts=attempts+1;if attempts==1 then return w(p.sequence,a)end
 if attempts==2 then assert(#p.snapshot.items==1);last=body;error({fatal=false,code='timeout'})end
 assert(body==last);return w(p.sequence)
 end}})
bridge:start();bridge:tick();before=enumerations;assert(subscriptions==2 and bridge.snapshot)
assert(not pcall(bridge.tick,bridge));bridge:tick();assert(bridge.snapshot==nil and enumerations==before and next(bridge.cycle_seen)==nil)
