local component = require("component")
local computer = require("computer")
local event = require("event")
local filesystem = require("filesystem")
local adapter = require("aeorbit.adapter")
local client = require("aeorbit.client")
local journal = require("aeorbit.journal")
local runner = require("aeorbit.runner")

local args={...}
local config_path=args[1] or "/etc/ae-orbit.cfg"
local config=assert(loadfile(config_path),"Cannot open "..config_path)()
assert(type(config)=="table","Configuration must return a table")
assert(type(config.me_address)=="string","Configure me_address explicitly")
local me=component.proxy(config.me_address)
local internet=config.internet_address and component.proxy(config.internet_address) or component.getPrimary("internet")
assert(internet,"An internet card is required")
local state_dir=config.state_dir or "/var/lib/ae-orbit"
if not filesystem.exists(state_dir) then assert(filesystem.makeDirectory(state_dir)) end
local fs={}
function fs.read(path,limit)
  if not filesystem.exists(path) then return nil end
  local file=assert(io.open(path,"rb"))
  local text=file:read(limit);file:close();return text
end
function fs.write(path,text)
  local file=assert(io.open(path,"wb"))
  local ok,err=file:write(text)
  if not ok then file:close();error(err or "write failed") end
  ok,err=file:flush()
  if not ok then file:close();error(err or "flush failed") end
  -- OpenOS may return no values when close succeeds; explicit errors still fail.
  ok,err=file:close()
  assert(ok~=false and err==nil,err or "close failed")
  return true
end
local store=journal.new(fs,state_dir.."/journal",32768)
local clock={now=computer.uptime,sleep=os.sleep}
local bridge=runner.new(config,{
  adapter=adapter.new(me,{identity_bytes=config.identity_bytes or 4096,cpu_limit=config.cpu_limit or 16}),
  client=client.new(internet,{url=config.url,token=config.token,timeout=config.http_timeout or 10,
    response_bytes=config.max_response_bytes or 16384,request_bytes=config.max_batch_bytes or 16384},clock),
  journal=store,now=computer.uptime,free_memory=computer.freeMemory,yield=function() os.sleep(0) end
})
local accepted_sources
if config.event_sources then accepted_sources={};for _,address in ipairs(config.event_sources) do accepted_sources[address]=true end end
local function changed(_,address,...)
  if accepted_sources and not accepted_sources[address] then return end
  for index=1,select("#",...) do
    local item=select(index,...)
    if type(item)=="table" then bridge:on_item_changed(item) end
  end
end
local subscribed=false
local function run()
  bridge:start()
  event.listen("network_item_changed",changed)
  if adapter.has_method(me,"setItemEventSubscription") then
    local ok=pcall(me.setItemEventSubscription,true)
    subscribed=ok
    if not ok then bridge:diagnose("subscription_unavailable") end
  else bridge:diagnose("subscription_unavailable") end
  print("AE Orbit connected. Session: "..bridge.session)
  local next_status,backoff=0,1
  while true do
    local status=bridge:tick()
    if status.control_ok then backoff=1 else backoff=math.min(backoff*2,30) end
    -- Reuse the remaining grant in bounded slices until the next control poll.
    -- Successful requests consume their polling interval instead of extending
    -- it. Failed requests still get a full backoff after they return.
    local anchor=status.control_ok and status.control_started_at or status.control_completed_at
    local delay=math.max(bridge.poll_delay,backoff)*(1+math.random()*0.2)
    local next_poll=anchor+delay
    -- Keep the previous bounded work window when a slow request consumed the
    -- interval. Poll early only once available inventory work has finished.
    local work_deadline=status.control_completed_at+delay
    local more=status.inventory_work
    while more and computer.uptime()<work_deadline do
      more=bridge:continue_inventory()
      if more then os.sleep(0) end
    end
    if computer.uptime()>=next_status then
      print(string.format("rows=%d cache=%d dirty=%d free=%d",bridge.uploaded,bridge.ids.count,bridge.dirty.count,computer.freeMemory()))
      next_status=computer.uptime()+60
    end
    os.sleep(math.max(0,next_poll-computer.uptime()))
  end
end
local ok,err=xpcall(run,function(value) return value end)
event.ignore("network_item_changed",changed)
if subscribed then pcall(me.setItemEventSubscription,false) end
if not ok then
  local message=type(err)=="table" and (err.code or "bridge_error") or tostring(err)
  io.stderr:write("AE Orbit stopped: "..message.."\n")
end
