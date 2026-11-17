local fixture=require("cooperative_fixture")

local s,r=fixture({role="primary",memory=1500000,grant=4,
  config={scan_memory_floor=2097152}})
s.catalog={{name="test:one",damage=0,label="One",size="1",hasTag=false}}
r:start()

-- allItems() can allocate the complete native inventory before Lua receives
-- its iterator. Low memory must therefore defer discovery before that call.
r:tick()
assert((s.scans or 0)==0,"low-memory discovery called the native allItems iterator")
assert(r.diagnostics.scan_deferred_low_memory=="1","low-memory discovery was not reported")

-- Once memory is available again, discovery resumes without a restart.
s.memory=3000000
s.clock=5
r:tick()
for _=1,8 do
  if (s.scans or 0)>0 then break end
  r:continue_inventory()
end
assert((s.scans or 0)==1,"discovery did not resume after memory recovered")
