local subnet=require('aeorbit.subnet')
local json=require('aeorbit.json')
local cancel=require('aeorbit.cancel')
local addr='11111111-1111-1111-1111-111111111111'
local now,calls,subscriptions=10,0,0
local fail=false
local function forbidden()error('broad inventory/CPU/recipe access forbidden')end
local proxy={getItemInNetwork=function(d)calls=calls+1;if fail then error('unavailable')end;if d.name=='test:empty' then return nil end;return{name=d.name,damage=d.damage,hasTag=false,size=23}end,
 getFluidInNetwork=function(d)calls=calls+1;return{name=d.name,amount=9000}end,
 allItems=forbidden,getItemsInNetwork=forbidden,getCraftables=forbidden,getCpus=forbidden,
 setItemEventSubscription=function(v)assert(v==false);subscriptions=subscriptions+1;return true end}
local component={list=function(kind,exact)assert(exact);local done=false;return function()if kind=='me_interface' and not done then done=true;return addr end end end,proxy=function(a)assert(a==addr);return proxy end}
local reads={};for i,name in ipairs({'test:empty','test:stock','water'})do reads[i]={item_id=string.format('%032x',i),assignment_id=string.format('%032x',i+10),revision=1,ttl_millis=5000,item={kind=i==3 and 'fluid' or 'item',name=name,damage=0,exact=true}}end
local function w(seq,rs)return{session_id=string.rep('b',32),subnet_id=string.rep('c',32),sequence=seq,component_address=addr,revision=1,poll_millis=250,interval_seconds=1,item_count=3,reads=rs or {},budget=rs and #rs or 0,grant_id=string.rep('d',32),grant_ttl_millis=1000}end
local last,attempt= nil,0
local bridge=subnet.new({}, {component=component,address='oc',now=function()return now end,free_memory=function()return 1048576 end,
 client={send=function(_,path,body)local p=json.decode(body);if path=='/bridge/subnet/hello'then assert(#p.components==1);return w(0)end
 attempt=attempt+1;if attempt==1 then return w(p.sequence,reads)end
 if attempt==2 then last=body;assert(p.samples[1].quantity=='0' and p.samples[2].quantity=='23' and p.samples[3].quantity=='9000');error({fatal=false,code='timeout'})end
 if attempt==3 then assert(body==last);return w(p.sequence,reads)end
 assert(p.samples[1].quantity==nil and p.samples[1].error=='read_failed');return w(p.sequence)
 end}})
bridge:start();bridge:tick();assert(calls==3 and subscriptions==1)
assert(not pcall(bridge.tick,bridge));assert(bridge.pending==last and bridge.sequence==1)
fail=true;bridge:tick();bridge:tick();assert(bridge.sequence==3 and bridge.pending==nil)
-- Never begin component reads after an HTTP response consumes the grant.
calls=0;local slow=subnet.new({}, {component=component,now=function()return now end,free_memory=function()return 1048576 end,client={send=function(_,path,body)if path:find('hello')then return w(0)end;now=now+2;return w(json.decode(body).sequence,reads)end}})
slow:start();slow:tick();assert(calls==0)
-- Real bounded HTTP client accepts only the two new protocol paths.
local client=require('aeorbit.client').new({request=function(url)assert(url=='http://example.test/bridge/subnet/poll');local sent=false;return{finishConnect=function()return true end,response=function()return 200 end,read=function()if not sent then sent=true;return '{}'end end,close=function()end}end},
 {url='http://example.test',token=string.rep('t',32),response_bytes=32768,request_bytes=16384},{now=function()return now end,sleep=function()end})
assert(client:send('/bridge/subnet/poll','{}'))
assert(not pcall(client.send,client,'/bridge/subnet/scan','{}'))
