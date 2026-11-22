local script=assert(loadfile(TEST_ROOT..'/oc/bin/ae-orbit-register.lua'))
local savedIO,savedLoad,savedPrint=io,loadfile,print
local component={isAvailable=function(name)return name=='transposer'end}
local chestCalls,registrations=0,0
component.getPrimary=function(kind)
 if kind=='transposer' then return {getStackInSlot=function(side,slot)assert(side==2 and slot==1);chestCalls=chestCalls+1;return{name='minecraft:paper',damage=0,hasTag=false,size=42,label='Paper'}end} end
 return {}
end
component.proxy=function()return{getItemInNetwork=function()error('sample registration queried ME')end,getItemsInNetwork=function()error('network enumeration attempted')end}end
local modules={component=component,sides={north=2},computer={uptime=function()return 1 end},event={pull=function()end},['aeorbit.client']={new=function(_,settings)
 assert(settings.token=='fixture-token-only-abcdefghijklmnopqrstuvwxyz')
 return{send=function(_,path,body)
 assert(path=='/bridge/v2/register-sample');local data=require('aeorbit.json').decode(body)
 assert(data.session_id=='sample-session' and data.item.name=='minecraft:paper' and data.item.quantity==nil and data.item.size==nil)
 registrations=registrations+1;return{id='99',registered=true}
 end}
end}}
local prior={};for name,value in pairs(modules)do prior[name]=package.loaded[name];package.loaded[name]=value end
io={open=function(path)assert(path=='/var/lib/ae-orbit/service-status.json');return{read=function()return'{"session":"sample-session","state":"running"}'end,close=function()end}end}
loadfile=function(path)assert(path=='/etc/ae-orbit.cfg');return function()return{url='http://fixture',token='fixture-token-only-abcdefghijklmnopqrstuvwxyz',me_address='fixture-me',role='primary'}end end
print=function(message)assert(not message:find('token',1,true))end
local ok,err=pcall(script,'north','1')
io,loadfile,print=savedIO,savedLoad,savedPrint;for name in pairs(modules)do package.loaded[name]=prior[name]end
assert(ok,err);assert(chestCalls==1 and registrations==1)
