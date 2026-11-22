local adapter=require('aeorbit.adapter')
local proxy=require('component_proxy')
local json=require('aeorbit.json')
local original=package.loaded['aeorbit.cpu_details']
package.loaded['aeorbit.cpu_details']=nil
local preload=package.preload['aeorbit.cpu_details']
package.preload['aeorbit.cpu_details']=function()error('optional detail module is broken',0)end
local function forbidden()error('unexpected full-network read',0)end
for _,wrap in ipairs({function(v)return v end,proxy.new}) do
 local outputs={
  {name='test:good',label='Good',damage=0,size=64},
  {name='test:bad',label='bad\255label',damage=0,size=1},
  {name='test:cut',label=string.rep('a',511)..'锭',damage=0,size=1},
  {name='',label='Missing registry',size=1},
  {name='test:huge',label='Huge',size='9223372036854775808'},
 }
 local values={}
 for i,o in ipairs(outputs) do values[i]={name='CPU '..i,busy=true,storage=4096,coprocessors=4,cpu=wrap({finalOutput=function()return o end})}end
 values[6]={name='Broken monitor',busy=true,storage=4096,cpu=wrap({finalOutput=function()error('broken CPU output',0)end})}
 values[7]={name='Idle',busy=false,storage=4096}
 local a=adapter.new(wrap({getCpus=function()return values end,getItemInNetwork=forbidden,getItemsInNetwork=forbidden}),{inventory_mode='exact_only'})
 local cpus,truncated=a:cpus()
 assert(#cpus==7 and not truncated and cpus[1].output.quantity=='64')
 assert(cpus[2].output==nil and cpus[2].output_unavailable=='output metadata unavailable')
 assert(cpus[3].output==nil and cpus[4].output==nil and cpus[5].output==nil)
 assert(cpus[6].output_unavailable=='monitor unavailable' and not cpus[7].busy)
 assert(a.cpu_detail_targets:get(cpus[1].id).output_identity.name=='test:good')
 assert(json.encode(cpus))
end
package.loaded['aeorbit.cpu_details']=original;package.preload['aeorbit.cpu_details']=preload
