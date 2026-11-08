local adapter=require("aeorbit.adapter")
local base64=require("aeorbit.base64")
local json=require("aeorbit.json")
local file=assert(io.open(TEST_ROOT.."/internal/nbt/testdata/vectors.json","rb"))
local vectors=json.decode(file:read("*a"));file:close()
local by_name={};for _,v in ipairs(vectors) do by_name[v.name]=v end
local function stack(name)
  return {name="test:machine",damage=54,hasTag=true,tag=base64.decode(by_name[name].gzip),size=1,label="Machine"}
end
local requested,current=stack("ordered"),stack("reordered")
local requests=0
local me={allItems=function() return function() end end,getItemInNetwork=function() return current end,
  getCraftable=function() return {getStack=function() return current end,request=function() requests=requests+1;return {} end} end}
local a=adapter.new(me,{identity_bytes=12288})
assert(a:key(requested)==a:key(current),"equivalent reordered NBT split OC identity")
assert(a:read(requested).tag==current.tag,"exact read failed to retain current native NBT representation")
assert(a:craft({item=a:definition(requested),quantity="1"})~=nil and requests==1,"equivalent recipe output rejected")
current=stack("different_value")
assert(not pcall(a.read,a,requested),"different exact-read identity accepted")
local handle,_,submitted=a:craft({item=a:definition(requested),quantity="1"})
assert(handle==nil and not submitted and requests==1,"different recipe submitted")
-- The native filtered fallback compares compressed tag bytes. An empty result
-- cannot prove a tagged recipe was removed when its encoding may have changed.
local fallback=adapter.new({allItems=me.allItems,getItemInNetwork=me.getItemInNetwork,
  getCraftable=function() return nil end,getCraftables=function() return {} end})
assert(not pcall(fallback.recipe_status,fallback,requested),"byte-filter miss became confirmed absence of an NBT recipe")
local nbt=require("aeorbit.nbt")
for _,v in ipairs(vectors) do
  local ok,result=pcall(nbt.digest,base64.decode(v.gzip))
  if v.error then assert(not ok,"accepted invalid NBT: "..v.name)
  else assert(ok and result==v.sha256,"NBT digest mismatch: "..v.name..": "..tostring(result)) end
end
-- Keys and cache remain bounded even for highly compressible large tags.
assert(#a:key(stack("dynamic_large"))<#requested.name+90,"expanded NBT escaped into retained keys")

local fixture=require("inventory_fixture")
local s,r=fixture({grant=1,config={batch_interval_seconds=0}})
current=requested
s.me.getItemInNetwork=function() return current end
r:on_item_changed(requested);r:tick()
assert(r.ids.count==1,"fixture did not bind the original representation")
local short=r:_candidate(requested)
assert(json.decode(short.encoded).id and short.tag==nil,"short-ID batch retained an unbudgeted full NBT tag")
current=stack("reordered")
local candidate=r:_candidate(current)
local update=json.decode(candidate.encoded)
assert(update.item and update.item.nbt==by_name.reordered.gzip,"short ID hid the changed native NBT encoding")
assert(r.ids.bytes>=#requested.tag,"retained NBT missing from ID cache budget")

-- A tiny gzip can expand to 64 KiB. Even with GC stopped, transient tables
-- must not accumulate to a megabyte before OC can service its control loop.
local large=base64.decode(by_name.exact_expanded_limit.gzip)
collectgarbage("collect");collectgarbage("stop")
local before=collectgarbage("count")
local ok,digest=pcall(nbt.digest,large)
local allocated=(collectgarbage("count")-before)*1024
collectgarbage("restart")
assert(ok and digest==by_name.exact_expanded_limit.sha256)
assert(allocated<512*1024,"NBT decode exceeded transient allocation budget: "..allocated)
