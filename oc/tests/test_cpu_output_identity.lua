local adapter=require('aeorbit.adapter')
local json=require('aeorbit.json')
local packet=require('aeorbit.cpu_packet')
local function forbidden() error('unexpected ME query') end
local outputs={
  {name='gregtech:gt.metaitem.01',label='Plate',size=64,damage=32001,hasTag=false},
  {name='water',label='Water',amount=1000,hasTag=false},
  {name='minecraft:paper',label='Custom paper',size=1,damage=0,hasTag=true,tag='never upload me'},
  {name='legacy:item',label='Legacy',size=5},
}
local cpus={}
for i,output in ipairs(outputs) do
  cpus[i]={name='CPU '..i,busy=true,storage=4096,coprocessors=1,cpu={finalOutput=function()return output end}}
end
local a=adapter.new({getItemInNetwork=forbidden,getCpus=function()return cpus end,
  getItemsInNetwork=forbidden,getFluidsInNetwork=forbidden,getFluidInNetwork=forbidden},{inventory_mode='exact_only'})
local snapshot,truncated=a:cpus()
assert(#snapshot==4 and not truncated)
assert(snapshot[1].output.kind=='item' and snapshot[1].output.damage==32001 and snapshot[1].output.has_tag==false)
assert(snapshot[2].output.kind=='fluid' and snapshot[2].output.quantity=='1000' and snapshot[2].output.has_tag==false)
assert(snapshot[3].output.has_tag==true and snapshot[3].output.tag==nil)
assert(snapshot[4].output.kind==nil and snapshot[4].output.damage==nil and snapshot[4].output.has_tag==nil)
local body={session_id='session',sequence='1'}
packet.pack(body,snapshot,false,64,4096)
local encoded=json.encode(body)
assert(encoded:find('"has_tag":false',1,true) and encoded:find('"damage":32001',1,true))
assert(not encoded:find('never upload',1,true))
packet.pack(body,snapshot,false,64,450)
assert(#body.cpus==4 and not body.cpus_truncated,'output metadata crowded out CPU rows')
