local fixture=require('cooperative_fixture')
for _,role in ipairs({'primary','quantity_worker'}) do
  local s,r=fixture({role=role,config={inventory_mode='exact_only',batch_interval_seconds=0}})
  local calls=0
  local function exact(detail)
    assert(type(detail)=='table' and detail.name and detail.amount==0)
    calls=calls+1;return {name=detail.name,amount=125000,hasTag=false}
  end
  s.me.getFluidInNetwork=exact;s.me.getEssentiaInNetwork=exact
  r:start()
  assert(s.hello.capabilities.fluids and s.hello.capabilities.essentia,'handshake lost resource capability')
  for index,kind in ipairs({'fluid','essentia'}) do
    s.reads_ack={{id=tostring(index),item={kind=kind,name=kind=='fluid' and 'water' or 'aer',damage=0,exact=true},
      assignment_id=string.rep(index==1 and 'a' or 'b',32),kind='quantity',purpose='background',ttl_millis=30000}}
    r:tick();s:drain();s.clock=s.clock+1
  end
  local updates=s:updates()
  assert(#updates==2 and calls==2,'resource reads failed to upload')
  for _,u in ipairs(updates) do assert(u.item==nil and u.quantity=='125000' and u.assignment_id) end
  assert((s.scans or 0)==0 and (s.recipe_reads or 0)==0,'resource collection enumerated network or recipes')
end
