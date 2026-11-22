local details=require('aeorbit.cpu_details')
local json=require('aeorbit.json')
local adapter=require('aeorbit.adapter')
local assignment={assignment_id=string.rep('a',32),cpu_id='1:1',kind='active'}
local calls=0
local output={name='test:goal',damage=0,label='Goal',hasTag=true,tag=string.rep('x',20000),size='1'}
local source={{name='test:part',damage=0,label='Part',size='9007199254740993',hasTag=false}}
local busy=true
local cpu={isBusy=function()return busy end,finalOutput=function()return output end,
 activeItems=function()calls=calls+1;return source end,pendingItems=function()error('wrong category')end,storedItems=function()error('wrong category')end}
local target={cpu=cpu,output_signature=details.signature(output)}
local result=details.read(target,assignment)
assert(calls==1 and result.items[1].quantity=='9007199254740993' and result.total==1 and not result.truncated)
assert(not json.encode(result):find('xxxxxxxx'))
source={};assert(details.read(target,assignment).total==0)
source={{name='test:good',size=1},{name=5,size=1}}
result=details.read(target,assignment);assert(result.error=='invalid_items' and result.items==nil and result.total==0 and not result.truncated)
source={[1]={name='test:good',size=1},bad={}}
assert(details.read(target,assignment).error=='invalid_items')
source={};for i=1,100 do source[i]={name='test:item'..i,size=1} end
result=details.read(target,assignment);assert(#result.items>0 and #result.items<=64 and result.total==100 and result.truncated and #json.encode(result)<=10000)
for i=1,100 do source[i].label=string.rep('a',512) end
result=details.read(target,assignment);assert(#result.items<64 and #json.encode(result)<=10000 and result.truncated)
output.name='test:changed';assert(details.read(target,assignment).error=='target_changed');output.name='test:goal'
cpu.activeItems=function()busy=false;return {} end
assert(details.read(target,assignment).error=='target_changed');busy=true
cpu.activeItems=function()error('interrupted',0)end
local ok,err=pcall(details.read,target,assignment);assert(not ok and type(err)=='table' and err.stop)
cpu.activeItems=nil;assert(details.read(target,assignment).error=='unsupported')
assert(details.read(nil,assignment).error=='target_expired')
-- Enumeration order changing must never retarget a retained original CPU.
cpu.activeItems=function()return {{name='test:original',size=1}}end
local other={isBusy=function()return true end,finalOutput=function()return output end,activeItems=function()error('wrong CPU')end}
local values={{name='',busy=true,storage=1024,cpu=cpu},{name='',busy=true,storage=1024,cpu=other}}
local a=adapter.new({getCpus=function()return values end,getItemInNetwork=function()error('network read forbidden')end},{role='primary',inventory_mode='exact_only'})
local first=a:cpus();assignment.cpu_id=first[1].id
values={values[2],values[1]};a:cpus()
assert(a:cpu_detail(assignment).items[1].name=='test:original')
for _=1,140 do a:cpus()end
assert(a:cpu_detail(assignment).items[1].name=='test:original')
