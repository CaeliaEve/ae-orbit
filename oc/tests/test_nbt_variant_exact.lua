local adapter=require('aeorbit.adapter')
local base64=require('aeorbit.base64')
local nbt=require('aeorbit.nbt')
local json=require('aeorbit.json')
local file=assert(io.open(TEST_ROOT..'/internal/nbt/testdata/vectors.json','rb'))
local vectors=json.decode(file:read('*a'));file:close()
-- Existing interoperability vectors contain a compressed exact tag.
local tag
for _,v in ipairs(vectors) do if v.gzip then tag=base64.decode(v.gzip);break end end
if not tag then return end
local calls=0
local me={getItemInNetwork=function(detail)calls=calls+1;assert(detail.tag==tag);return{name=detail.name,damage=detail.damage,tag=tag,hasTag=true,size=4}end,
getItemsInNetwork=function()error('enumeration forbidden')end}
local a=adapter.new(me,{inventory_mode='exact_only'})
assert(a:capabilities().exact_nbt and a:capabilities().max_nbt_bytes==4096)
local definition={kind='item',name='test:variant',damage=1,nbt=base64.encode(tag),exact=true}
local raw=a:from_definition(definition);local value=a:read(raw)
assert(a:key(raw)==a:key(value) and a:count(value)=='4' and calls==1)
