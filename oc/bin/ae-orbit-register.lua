-- Read one real chest slot. Never call ME enumeration or count the sample as
-- network inventory. Requires the bridge to be running with an active session.
local args={...}
local component=require('component')
local sides=require('sides')
local json=require('aeorbit.json')
local adapter=require('aeorbit.adapter')
local client=require('aeorbit.client')
local computer=require('computer')
local event=require('event')
local config=assert(loadfile(args[3] or '/etc/ae-orbit.cfg'))()
local statusFile=assert(io.open('/var/lib/ae-orbit/service-status.json','rb'),'Start ae-orbit before registering a sample')
local status=json.decode(statusFile:read(1025),1024);statusFile:close()
assert(status.session,'Start ae-orbit before registering a sample')
local side=tonumber(args[1]) or sides[args[1] or '']
local slot=tonumber(args[2])
assert(side and slot and slot>=1 and slot%1==0,'Usage: ae-orbit-register <side> <slot> [config]')
local inventory=component.isAvailable('transposer') and component.getPrimary('transposer') or component.getPrimary('inventory_controller')
assert(inventory and inventory.getStackInSlot,'Attach an inventory controller or transposer to the sample chest')
local raw=assert(inventory.getStackInSlot(side,slot),'The sample slot is empty')
local me=component.proxy(config.me_address)
local a=adapter.new(me,{inventory_mode='exact_only',role=config.role,identity_bytes=config.identity_bytes or 4096})
local definition=a:definition(raw)
local internet=config.internet_address and component.proxy(config.internet_address) or component.getPrimary('internet')
local c=client.new(internet,{url=config.url,token=config.token,request_bytes=32768,response_bytes=4096,timeout=15},
 {now=computer.uptime,sleep=function(seconds)event.pull(seconds)end})
local result=c:send('/bridge/v2/register-sample',json.encode({session_id=status.session,item=definition},32768))
assert(result.registered and result.id,'Registration was not confirmed')
print('Registered item #'..result.id..'. AE quantity will be queried separately.')
