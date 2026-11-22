-- Bind a new dedicated OC to its subnet collector; preserve existing AE OC.
local args={...};local fs=require('filesystem');local path='/etc/ae-orbit.cfg'
local origin=(args[1] or ''):gsub('/+$','')
assert(origin:match('^https?://[A-Za-z0-9%.%-%[%]:]+$'),'Usage: ae-orbit-subnet-setup <origin> <subnet token>')
assert(type(args[2])=='string' and args[2]:match('^ac_sub_[A-Za-z0-9_%-]+$') and #args[2]<=256,'Use the dedicated subnet token')
local current=assert(loadfile(path))()
assert(current.role=='subnet_monitor' or current.me_address=='replace-with-the-ME-controller-or-full-interface-component-address','Configure a new dedicated OC; existing AE and LSC configuration will not be replaced')
assert(require('aeorbit.service').get():stop())
local backup=path..'.before-subnet';local n=0;while fs.exists(backup)do n=n+1;assert(n<=100);backup=path..'.before-subnet-'..n end
assert(fs.copy(path,backup));local staged=path..'.subnet-new';assert(not fs.exists(staged),'Check pending subnet configuration')
local f=assert(io.open(staged,'wb'));assert(f:write('return {role="subnet_monitor",url='..string.format('%q',origin)..',token='..string.format('%q',args[2])..'}\n'));assert(f:flush());local closed,reason=f:close();assert(closed~=false and reason==nil);assert(loadfile(staged))
local old=path..'.subnet-old';assert(not fs.exists(old));assert(fs.rename(path,old))
if not fs.rename(staged,path)then assert(fs.rename(old,path));error('Subnet configuration replacement failed')end
assert(fs.remove(old));print('Subnet monitor configured. Reboot, run ae-orbit start, then bind its ME component on the website.')
