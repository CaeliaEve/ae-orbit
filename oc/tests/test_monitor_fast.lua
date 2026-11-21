local monitor=require('aeorbit.monitor')
local json=require('aeorbit.json')
local now=0
local reads=0
local addr='11111111-1111-1111-1111-111111111111'
local proxy={type='LSC',getStoredEUString=function()reads=reads+1;return '100'end,getEUCapacityString=function()return '1000'end}
local components={list=function()return function()end end,proxy=function()return proxy end}
local assignment={id=string.rep('a',32),plugin_id='gtnh.lsc',component_address=addr,revision=1,installation_revision=1,interval_seconds=1}
local bodies={};local fail=false
local client={send=function(_,path,body)
 local p=json.decode(body)
 if path=='/bridge/monitor/hello' then assert(p.min_interval_seconds==1);return {session_id=string.rep('b',32),sequence=0,poll_seconds=1,objects={assignment}}end
 bodies[#bodies+1]=body
 if fail then fail=false;error({fatal=false,code='timeout'})end
 return {session_id=string.rep('b',32),sequence=p.sequence,poll_seconds=1,objects={assignment}}
end}
local bridge=monitor.new({role='monitor'},{component=components,client=client,now=function()return now end,free_memory=function()return 1048576 end})
bridge:start()
for _=1,10 do bridge:tick();now=now+1 end
assert(reads==10,'one-second sampling was not honored')
fail=true;assert(not pcall(bridge.tick,bridge));local previous=bodies[#bodies];local count=reads
now=now+5;bridge:tick();assert(reads==count and bodies[#bodies]==previous,'retry reread the machine')
assignment.interval_seconds=0.5;assert(not pcall(bridge.tick,bridge),'fractional interval accepted')
