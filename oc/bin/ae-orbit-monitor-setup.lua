-- Configure a dedicated monitoring OC after full installation. Never needs ME.
local args={...}
local fs=require('filesystem')
local service=require('aeorbit.service').get()
local path='/etc/ae-orbit.cfg'
local origin=(args[1] or ''):gsub('/+$','')
assert(origin:match('^https?://[A-Za-z0-9%.%-%[%]:]+$'),'Usage: ae-orbit-monitor-setup <service origin> <monitor token>')
assert(type(args[2])=='string' and args[2]:match('^ac_mon_[A-Za-z0-9_%-]+$') and #args[2]<=256,'Use the dedicated monitor token from the plugin market')
local current=assert(loadfile(path))()
assert(current.role=='monitor' or current.me_address=='replace-with-the-ME-controller-or-full-interface-component-address','Configure a new dedicated OC; existing AE OC settings will not be replaced')
local stopped,why=service:stop();assert(stopped,why)
local backup=path..'.before-monitor'
local suffix=1
while fs.exists(backup) do backup=path..'.before-monitor-'..suffix;suffix=suffix+1;assert(suffix<=100,'Too many monitor configuration backups') end
assert(fs.copy(path,backup))
assert(not fs.exists(path..'.monitor-new'),'Check the pending monitor configuration before retrying')
local output=assert(io.open(path..'.monitor-new','wb'))
assert(output:write('return {role="monitor",url='..string.format('%q',origin)..',token='..string.format('%q',args[2])..'}\n'))
assert(output:flush());local closed,reason=output:close();assert(closed~=false and reason==nil,'Monitor configuration close failed')
assert(loadfile(path..'.monitor-new'))
local old=path..'.monitor-old';assert(not fs.exists(old),'Check the previous monitor configuration replacement')
assert(fs.rename(path,old))
if not fs.rename(path..'.monitor-new',path) then assert(fs.rename(old,path));error('Monitor configuration replacement failed') end
assert(fs.remove(old))
print('Dedicated monitor configured. Connect adapters to LSC controllers, reboot, then run ae-orbit start.')
