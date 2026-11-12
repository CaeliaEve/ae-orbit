local cancel=require("aeorbit.cancel")
local M={}
local safe_codes={}
for code in ("interrupted config_error setup_failed component_failed journal_failed cleanup_failed runtime_error bridge_error "..
  "transport_failed connection_failed timeout incomplete_http_response response_too_large invalid_http_response invalid_json_response "..
  "stale_session unauthorized forbidden invalid_token invalid_boot_identity invalid_welcome invalid_device_limits "..
  "invalid_read_assignment invalid_read_identity assignment_identity_changed invalid_poll_confirmation invalid_verification_token "..
  "invalid_refresh_confirmation invalid_refresh_token invalid_refresh_ttl invalid_refresh_identity invalid_inventory_confirmation "..
  "invalid_short_id_binding missing_short_id_binding event_failed"):gmatch("%S+") do safe_codes[code]=true end
local function safe_code(err,fallback)
  local code=type(err)=="table" and err.code
  if type(code)=="string" and (safe_codes[code] or code:match("^http_[45]%d%d$")) then return code end
  return fallback
end
function M.run(context,config_path,report,mode)
  local event,me,bridge,changed
  local listener_owned,subscription_owned=false,false
  local phase="setup_failed"
  local foreground=mode=="foreground"
  local function status(state,code)
    if report then report(state,code,bridge and bridge.session) end
  end
  local ok,err=xpcall(function()
    context.check()
    local component=require("component")
    local computer=require("computer")
    event=require("event")
    local filesystem=require("filesystem")
    local adapter=require("aeorbit.adapter")
    local client=require("aeorbit.client")
    local journal=require("aeorbit.journal")
    phase="config_error"
    local config=assert(loadfile(config_path or "/etc/ae-orbit.cfg"))()
    assert(type(config)=="table")
    local bridge_mode=config.bridge_mode or "single"
    local role=config.role or "primary"
    assert(bridge_mode=="single" or bridge_mode=="cooperative")
    assert(role=="primary" or role=="quantity_worker")
    assert(role~="quantity_worker" or bridge_mode=="cooperative")
    assert(type(config.me_address)=="string")
    local primary=role=="primary"
    local runner=require(bridge_mode=="cooperative" and "aeorbit.cooperative" or "aeorbit.runner")
    phase="component_failed"
    context.check()
    me=assert(component.proxy(config.me_address))
    local internet=config.internet_address and component.proxy(config.internet_address) or component.getPrimary("internet")
    assert(internet)
    phase="journal_failed"
    local state_dir=config.state_dir or "/var/lib/ae-orbit"
    if not filesystem.exists(state_dir) then assert(filesystem.makeDirectory(state_dir)) end
    local fs={}
    function fs.read(path,limit)
      if not filesystem.exists(path) then return nil end
      local file=assert(io.open(path,"rb"))
      local read_ok,text=pcall(file.read,file,limit)
      local close_ok,value,reason=pcall(file.close,file)
      if not read_ok then error(text,0) end
      assert(close_ok and value~=false and reason==nil)
      return text
    end
    function fs.write(path,text)
      local file=assert(io.open(path,"wb"))
      local write_ok,reason=pcall(function()
        assert(file:write(text))
        assert(file:flush())
      end)
      local close_ok,value,close_reason=pcall(file.close,file)
      if not write_ok then error(reason,0) end
      -- OpenOS close may return no values on success.
      assert(close_ok and value~=false and close_reason==nil)
      return true
    end
    local store=primary and journal.new(fs,state_dir.."/journal",32768) or nil
    phase="config_error"
    local clock={now=computer.uptime,sleep=context.sleep,check=context.check}
    bridge=runner.new(config,{
      adapter=adapter.new(me,{identity_bytes=config.identity_bytes or 4096,cpu_limit=config.cpu_limit or 16,role=role}),
      client=client.new(internet,{url=config.url,token=config.token,timeout=config.http_timeout or 10,
        response_bytes=config.max_response_bytes or 16384,request_bytes=config.max_batch_bytes or 16384},clock),
      journal=store,now=computer.uptime,free_memory=computer.freeMemory,yield=function() context.sleep(0) end
    })
    local accepted_sources
    if config.event_sources then
      accepted_sources={}
      for _,address in ipairs(config.event_sources) do accepted_sources[address]=true end
    end
    changed=function(_,address,...)
      if accepted_sources and not accepted_sources[address] then return end
      for index=1,select("#",...) do
        local item=select(index,...)
        if type(item)=="table" then bridge:on_item_changed(item) end
      end
    end
    local function wait_until(deadline)
      context.check()
      while computer.uptime()<deadline do
        if bridge.tracker then bridge.tracker:pump() end
        context.sleep(math.min(1,deadline-computer.uptime()))
      end
    end
    phase="runtime_error"
    local startup_backoff=1
    while true do
      context.check()
      status("starting")
      local started,failure=pcall(bridge.start,bridge)
      if started then break end
      cancel.rethrow(failure)
      if type(failure)~="table" or failure.fatal~=false then error(failure,0) end
      status("backoff",safe_code(failure,"transport_failed"))
      wait_until(computer.uptime()+startup_backoff*(1+math.random()*0.2))
      startup_backoff=math.min(startup_backoff*2,30)
    end
    context.check()
    if primary then
      -- Mark ownership before calls: interruption may occur after native side effects.
      listener_owned=true
      local listened=event.listen("network_item_changed",changed)
      if listened==false then listener_owned=false;error({fatal=true,code="event_failed"},0) end
      if adapter.has_method(me,"setItemEventSubscription") then
        subscription_owned=true
        local subscribed,value=pcall(me.setItemEventSubscription,true)
        if subscribed and value==false then subscription_owned=false end
        if not subscribed then
          cancel.rethrow(value)
          bridge:diagnose("subscription_unavailable")
        elseif value==false then bridge:diagnose("subscription_unavailable") end
      else bridge:diagnose("subscription_unavailable") end
    end
    status("running")
    if foreground then print("AE Orbit connected.") end
    local next_status,backoff=0,1
    while true do
      context.check()
      local started_at=computer.uptime()
      local ticked,result=pcall(bridge.tick,bridge)
      local retry_code="transport_failed"
      if not ticked then
        cancel.rethrow(result)
        if type(result)~="table" or result.fatal~=false then error(result,0) end
        retry_code=safe_code(result,"transport_failed")
        result={control_ok=false,control_started_at=started_at,control_completed_at=computer.uptime(),inventory_work=false}
      end
      if result.control_ok then backoff=1 else backoff=math.min(backoff*2,30) end
      if result.control_ok then status("running") else status("backoff",retry_code) end
      local anchor=result.control_ok and result.control_started_at or result.control_completed_at
      local delay=math.max(bridge.poll_delay,backoff)*(1+math.random()*0.2)
      local next_poll=anchor+delay
      local work_deadline=result.control_completed_at+delay
      local more=result.inventory_work
      while more and computer.uptime()<work_deadline do
        context.check()
        more=bridge:continue_inventory()
        if more then context.sleep(0) end
      end
      if foreground and computer.uptime()>=next_status then
        print(string.format("rows=%d cache=%d dirty=%d free=%d",bridge.uploaded,bridge.ids.count,bridge.dirty.count,computer.freeMemory()))
        next_status=computer.uptime()+60
      end
      wait_until(next_poll)
      context.sleep(0)
    end
  end,function(value) return value end)
  local cleanup_failed=false
  if listener_owned then
    local cleaned,value=pcall(event.ignore,"network_item_changed",changed)
    if not cleaned or value==false then cleanup_failed=true end
  end
  if subscription_owned then
    local cleaned,value=pcall(me.setItemEventSubscription,false)
    if not cleaned or value==false then cleanup_failed=true end
  end
  local result
  if cleanup_failed then result={kind="blocked",code="cleanup_failed"}
  elseif ok or cancel.is_stop(err) then result={kind="stopped",code="interrupted"}
  elseif phase~="runtime_error" then result={kind="blocked",code=safe_code(err,phase)}
  elseif type(err)=="table" and err.fatal==true then result={kind="blocked",code=safe_code(err,"bridge_error")}
  else result={kind="crashed",code="runtime_error"} end
  if foreground then pcall(print,"AE Orbit stopped: "..result.code) end
  return result
end
return M
