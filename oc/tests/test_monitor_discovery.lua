local monitor=require('aeorbit.monitor')
local lists,proxies={},{}
local function components(lower,upper)
 return {
  list=function(name,exact)
   assert(exact and (name=='lsc' or name=='LSC'),'only bounded exact LSC discovery is allowed')
   lists[#lists+1]=name
   local xs=name=='lsc' and lower or upper
   local index=0
   return function()index=index+1;return xs[index] end
  end,
  proxy=function(address)
   proxies[address]=(proxies[address] or 0)+1
   if address=='unsupported' then return {getStoredEU=function()error('numeric read forbidden')end} end
   return {getStoredEUString=function()error('discovery must not sample')end,getEUCapacityString=function()error('discovery must not sample')end}
  end,
 }
end
local found=monitor.discover(components({'lower','shared','unsupported'},{'upper','shared'}))
assert(#found==4 and found[1].address=='lower' and found[4].address=='upper')
for _,c in ipairs(found)do assert(c.type=='LSC') end
assert(found[1].supported and not found[3].supported)
assert(proxies.shared==1 and #lists==2,'aliases must not duplicate a controller')
assert(#monitor.discover(components({},{}))==0)
local many={};for i=1,100 do many[i]='device-'..i end
found=monitor.discover(components(many,{'extra'}))
assert(#found==32 and found[32].address=='device-32' and not proxies.extra,'discovery budget exceeded')
