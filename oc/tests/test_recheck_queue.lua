local fixture=require("inventory_fixture")

-- Ordinary cold-item verification must survive hot hints while sharing the
-- same fixed collection allowance with both those hints and catalog discovery.
local s,r=fixture({grant=0,delay=0})
s.verify={s:verification("test:missed")};r:tick();s:flood()
s.grant=1
for _=1,8 do s.clock=s.clock+1;s:flood();r:tick() end
assert(s.items["test:missed"] and s.items["test:missed"].quantity=="42","hot event pressure evicted an ordinary verification")
assert(s.items["test:hot_29"] or s.items["test:hot_30"] or s.items["test:hot_31"] or s.items["test:hot_32"],"ordinary verification starved event reads")
assert(s.items["test:cold_1"],"ordinary verification starved catalog discovery")

-- Advertise bounded count and byte capacity. Duplicate input must not move the
-- oldest admitted request, and overflowing input cannot evict accepted work.
local s,r=fixture({grant=0,delay=0})
for round=1,4 do
  s.verify={}
  for index=1,16 do s.verify[#s.verify+1]=s:verification("test:verify_"..((round-1)*16+index)) end
  r:tick()
end
assert(s.reads==0,"a queued verification ignored zero collection credit")
local advertised=s.polls[#s.polls]
assert(advertised.recheck_slots==0 or advertised.recheck_bytes<300,"full receiver did not exert backpressure")
s.verify={s:verification("test:verify_1")};r:tick()
s.grant=1
for _=1,3 do s:flood();r:tick() end
assert(s.items["test:verify_1"],"duplicates or overflow displaced the first admitted verification")
assert(not s.items["test:verify_64"],"overflow replaced accepted cold-item work")

-- Same-label valid items still flow; display labels cannot identify missing mods.
local s,r,raw=fixture({grant=1,delay=0,catalog_size=0})
local value=raw("test:untranslated");value.label=".name"
r:on_item_changed(value);r:tick()
assert(s.items["test:untranslated"],"display-only placeholder label hid a valid registry identity")
