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

-- A regular quantity read can finish just after the shared recipe cooldown.
-- Pending priority work must receive that opportunity, even when the ordinary
-- lane was selected before the cooldown expired.
for _,lane in ipairs({"event","recheck"}) do
  local s,r,raw=fixture({grant=16,delay=0.06})
  local me=r.adapter.me
  local read,recipes=me.getItemInNetwork,me.getCraftables
  local recipe_names={}
  me.getItemInNetwork=function(detail)
    local value=read(detail)
    if detail.name=="test:ordinary_zero" then value.size=0 end
    return value
  end
  me.getCraftables=function(filter)
    recipe_names[#recipe_names+1]=filter.name
    return recipes(filter)
  end
  local token=string.rep("b",32)
  s.verify={s:verification("test:priority",token)}
  if lane=="event" then r:on_item_changed(raw("test:ordinary_zero"),true)
  else
    local ordinary=s:verification("test:ordinary_zero");ordinary.craftable=true
    s.verify[#s.verify+1]=ordinary
  end
  r.next_recipe_check=0.05
  r:tick()
  assert(#recipe_names==0,"ordinary "..lane.." work extended the cooldown of a pending priority read")
  r:continue_inventory()
  assert(recipe_names[1]=="test:priority" and s.items["test:priority"].verification==token,
    "priority read missed the first available recipe opportunity")
  assert(r.credits==11,"priority scheduling changed the native collection cost")
  s.clock=6;s.grant=16
  r:tick();for _=1,5 do if recipe_names[2] then break end;r:continue_inventory() end
  assert(recipe_names[2]=="test:ordinary_zero","deferred ordinary recipe never resumed after the priority confirmation")
end
