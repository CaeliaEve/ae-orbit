local json=require("aeorbit.json")
local packet=require("aeorbit.cpu_packet")
local adapter=require("aeorbit.adapter")
local values={}
for index=1,18 do
  values[index]={name="",busy=true,storage=4096,coprocessors=16,
    cpu={finalOutput=function() return {name="test:result",label=string.rep("x",500),size=1} end}}
end
local a=adapter.new({getItemInNetwork=function() end,getCpus=function() return values end},{inventory_mode="exact_only"})
local cpus,truncated=a:cpus()
assert(#cpus==18 and not truncated,"default CPU limit omitted clusters")
local body={session_id="session",sequence="1",health={}}
packet.pack(body,cpus,truncated,64,4096)
assert(#body.cpus==18 and not body.cpus_truncated,"optional outputs crowded out basic records")
assert(#json.encode(body)<=4096)
for index,cpu in ipairs(body.cpus) do
  assert(cpu.id==cpus[index].id and cpu.name=="" and cpu.storage=="4096")
end
packet.pack(body,cpus,false,8,4096)
assert(#body.cpus==8 and body.cpus_truncated,"hard count limit was ignored")
packet.pack(body,cpus,true,64,1200)
assert(#body.cpus<18 and body.cpus_truncated and #json.encode(body)<=1200)
for _,limit in ipairs({0,65,1.5}) do
  assert(not pcall(adapter.new,{getItemInNetwork=function() end},{inventory_mode="exact_only",cpu_limit=limit}))
end
