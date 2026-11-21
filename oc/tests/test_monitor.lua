local monitor=require('aeorbit.monitor')
local json=require('aeorbit.json')
local a,b,e=monitor.sample({getStoredEUString=function()return '123456789012345678901234567890' end,getEUCapacityString=function()return '246913578024691357802469135780' end})
assert(a=='123456789012345678901234567890' and b and e==nil)
assert(select(3,monitor.sample({getStoredEUString=function()return '01' end,getEUCapacityString=function()return '10' end}))=='invalid_energy')
assert(select(3,monitor.sample(nil))=='component_missing')
assert(select(3,monitor.sample({getStoredEU=function()error('must not use numeric energy')end}))=='interface_unsupported')
local now=10
local addr='11111111-1111-1111-1111-111111111111'
local component={list=function(tp)assert(tp=='LSC');local done=false;return function()if not done then done=true;return addr end end end,
 proxy=function(address)assert(address==addr);return {getStoredEUString=function()return '10'end,getEUCapacityString=function()return '100'end}end}
local attempts,payload=0,nil
local assignment={id=string.rep('a',32),plugin_id='gtnh.lsc',component_address=addr,revision=1,installation_revision=1,interval_seconds=10}
local client={send=function(_,path,body)
 local value=json.decode(body)
 if path=='/bridge/monitor/hello' then assert(#value.components==1 and value.modules[1]=='gtnh.lsc');return{session_id=string.rep('b',32),sequence=0,objects={assignment}} end
 assert(path=='/bridge/monitor/poll');attempts=attempts+1
 if attempts==1 then payload=body;error({fatal=false,code='timeout'})end
 if attempts==2 then assert(body==payload and value.samples[1].stored_eu=='10') end
 return {session_id=string.rep('b',32),sequence=value.sequence,objects={assignment}}
end}
local bridge=monitor.new({role='monitor'},{component=component,client=client,now=function()return now end,free_memory=function()return 1048576 end,address='oc'})
bridge:start();assert(not pcall(function()bridge:tick()end));now=20;bridge:tick();assert(bridge.sequence==1 and bridge.pending==nil)
-- Fair rotation even when the number of machines exceeds one round.
local observed={};local many={}
for i=1,16 do local x={};for k,v in pairs(assignment)do x[k]=v end;x.id=string.format('%032x',i);many[i]=x end
local seq=0
local clientMany={send=function(_,path,body)
 local x=json.decode(body)
 if path=='/bridge/monitor/hello' then return{session_id=string.rep('b',32),sequence=0,objects=many}end
 for _,sample in ipairs(x.samples)do observed[sample.object_id]=true end
 seq=seq+1;return{session_id=string.rep('b',32),sequence=seq,objects=many}
end}
bridge=monitor.new({role='monitor'},{component=component,client=clientMany,now=function()return now end,free_memory=function()return 1048576 end,address='oc'})
bridge:start();for _=1,4 do bridge:tick();now=now+5 end
local count=0;for _ in pairs(observed)do count=count+1 end;assert(count==16,'monitor round starved later objects')
