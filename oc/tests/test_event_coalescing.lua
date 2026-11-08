local fixture=require("inventory_fixture")

-- A repeated hint must not send the oldest pending identity to the tail. The
-- receiver must obtain a fresh component read, never the event's delta/count.
local s,r,raw=fixture({grant=1,config={batch_interval_seconds=0}})
r:on_item_changed(raw("test:active"))
r:on_item_changed(raw("test:other"))
for _=1,100 do
  local hint=raw("test:active");hint.size=-99
  r:on_item_changed(hint)
end
r:tick()
assert(s.items["test:active"] and s.items["test:active"].quantity=="42",
  "repeated events postponed the oldest pending item")
assert(not s.items["test:other"],"a newer event bypassed the oldest item")

-- Repeated hints with an oversized, nonauthoritative label must not evict
-- another queued identity. The eventual read supplies its current label.
local s,r,raw=fixture({grant=8,config={batch_interval_seconds=0}})
r:on_item_changed(raw("test:active"))
r:on_item_changed(raw("test:other"))
for _=1,20 do
  local hint=raw("test:active");hint.label=string.rep("x",3000)
  r:on_item_changed(hint)
end
r:tick();for _=1,10 do if not r:continue_inventory() then break end end
assert(s.items["test:active"] and s.items["test:other"],"coalesced events lost a queued item")
assert(s.items["test:active"].item.label=="test:active","an event label replaced the real observation")
assert(not r.diagnostics.dirty_entry_too_large and not r.diagnostics.dirty_queue_evicted,
  "duplicate events consumed additional queue capacity")

-- Coalescing retains a recipe-check request until a real zero-stock lookup
-- verifies it, even if ordinary quantity hints arrive afterwards.
local s,r,raw=fixture({grant=4,config={batch_interval_seconds=0}})
s.me.getItemInNetwork=function(detail)
  local item=raw(detail.name);item.size=0;return item
end
r:on_item_changed(raw("test:recipe"),true)
r:on_item_changed(raw("test:recipe"),false)
r:tick()
assert(s.items["test:recipe"] and s.items["test:recipe"].quantity=="0" and
  s.items["test:recipe"].craftable==true,"coalescing forgot a pending recipe check")
