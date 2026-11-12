local cancel=require("aeorbit.cancel")
local real_client=require("aeorbit.client")
local failures={}
local function fixture(options)
  options=options or {}
  local s={time=0,starts=0,ticks=0,created=0,pumps=0,enabled=0,disabled=0,listened=0,ignored=0,reports={},output={}}
  local old_loadfile,old_print,old_write,old_stderr=loadfile,print,io.write,io.stderr
  local modules={"component","computer","event","filesystem","aeorbit.adapter","aeorbit.client","aeorbit.journal","aeorbit.runner","aeorbit.cooperative","aeorbit.runtime"}
  local saved={};for _,name in ipairs(modules) do saved[name]=package.loaded[name];package.loaded[name]=nil end
  local context
  context=cancel.new({now=function() return s.time end,sleep=function(n)
    s.time=s.time+n
    if options.stop_at and s.time>=options.stop_at then context.request_stop() end
  end})
  local config={me_address="me",url="https://test.invalid",token=string.rep("credential_secret",2),bridge_mode=options.worker and "cooperative" or "single",role=options.worker and "quantity_worker" or "primary"}
  loadfile=function() return function() if options.config_error then error(config.token,0) end;return config end end
  print=function(...) s.output[#s.output+1]=table.concat({...}," ") end
  io.write=function(...) s.output[#s.output+1]=table.concat({...}," ") end
  io.stderr={write=function(_,v) s.output[#s.output+1]=v end}
  local me={setItemEventSubscription=function(enabled)
    if enabled then
      s.enabled=s.enabled+1
      if options.subscribe_stop then error({stop=true,code="interrupted"},0) end
      if options.subscribe_false then return false end
    else s.disabled=s.disabled+1;if options.cleanup_error then error(config.token,0) end end
    return true
  end}
  local internet={request=function()
    s.requests=(s.requests or 0)+1
    return {finishConnect=function() return true end,response=function() return 200,nil,{} end,
      read=function() context.request_stop();context.check() end,
      close=function() s.closed=(s.closed or 0)+1 end}
  end}
  package.loaded.component={proxy=function() if options.component_error then error(config.token,0) end;return me end,getPrimary=function() return internet end}
  package.loaded.computer={uptime=function() return s.time end,freeMemory=function() return 100000 end}
  package.loaded.filesystem={exists=function() return true end}
  package.loaded.event={listen=function(name,fn)
    assert(name=="network_item_changed","runtime must not own interrupted listener")
    s.listened=s.listened+1;s.listener=fn;return true
  end,ignore=function(name,fn) assert(name=="network_item_changed" and fn==s.listener);s.ignored=s.ignored+1;return true end}
  package.loaded["aeorbit.adapter"]={new=function() return {} end,has_method=function() return true end}
  package.loaded["aeorbit.client"]={new=function(card,cfg,clock)
    s.clock=clock
    return options.real_network_stop and real_client.new(card,cfg,clock) or {}
  end}
  package.loaded["aeorbit.journal"]={new=function() if options.journal_error then error({fatal=true,code="journal_failed"},0) end;return {} end}
  local runner={new=function(_,deps)
    s.created=s.created+1;s.deps=deps
    local bridge={session="session",poll_delay=1,uploaded=0,ids={count=0},dirty={count=0},tracker={pump=function() s.pumps=s.pumps+1 end}}
    function bridge:start()
      s.starts=s.starts+1
      if options.real_network_stop then deps.client:post("/bridge/v1/boot-id",{}) end
      if options.start_stop then context.request_stop();s.clock.check() end
      if s.starts<=(options.transient_starts or 0) then error({fatal=false,code="transport_failed"},0) end
    end
    function bridge:tick()
      s.ticks=s.ticks+1
      if options.thrown_retry and s.ticks<=3 then error({fatal=false,code="timeout"},0) end
      if options.slow_poll and s.ticks==1 then
        s.time=s.time+5
        return {control_ok=true,control_started_at=0,control_completed_at=5,inventory_work=true}
      end
      if options.failure then error(options.failure,0) end
      if options.network_stop then context.request_stop();s.clock.sleep(0.1) end
      if not options.running_retry or s.ticks==6 then context.request_stop();context.check() end
      return {control_ok=false,control_started_at=s.time,control_completed_at=s.time,inventory_work=false}
    end
    function bridge:diagnose() end
    function bridge:on_item_changed() end
    function bridge:continue_inventory() s.time=s.time+0.5;s.inventory_slices=(s.inventory_slices or 0)+1;return false end
    return bridge
  end}
  package.loaded["aeorbit.runner"]=runner;package.loaded["aeorbit.cooperative"]=runner
  local ok,result=pcall(function()
    local loaded,runtime=pcall(require,"aeorbit.runtime")
    assert(loaded,"one-lifetime runtime is missing")
    return runtime.run(context,"test-config",function(state,code,session) s.reports[#s.reports+1]={state=state,code=code,session=session} end,options.foreground and "foreground" or "background")
  end)
  loadfile,print,io.write,io.stderr=old_loadfile,old_print,old_write,old_stderr
  for _,name in ipairs(modules) do package.loaded[name]=saved[name] end
  assert(ok,result)
  return result,s
end
local function test(name,fn) local ok,err=pcall(fn);if not ok then failures[#failures+1]=name..": "..tostring(err) end end
test("transient startup retries one runner and cleans ownership",function()
  local r,s=fixture({transient_starts=2})
  assert(r.kind=="stopped" and s.created==1 and s.starts==3)
  assert(s.time>=3 and s.listened==1 and s.ignored==1 and s.enabled==1 and s.disabled==1)
  assert(#s.output==0,"background wrote console output")
end)
for _,boundary in ipairs({"start_stop","network_stop","subscribe_stop"}) do
  test("stop at "..boundary,function()
    local r,s=fixture({[boundary]=true})
    assert(r.kind=="stopped")
    assert(s.listened==s.ignored and s.enabled==s.disabled)
  end)
end
test("startup backoff is cancellable",function()
  local r,s=fixture({transient_starts=20,stop_at=0.2})
  assert(r.kind=="stopped" and s.starts==1 and s.time<0.4)
end)
test("real HTTP read cancellation closes its handle",function()
  local r,s=fixture({real_network_stop=true})
  assert(r.kind=="stopped" and s.closed==1 and s.requests==1 and s.starts==1)
end)
test("slow successful poll retains inventory work without extra interval",function()
  local r,s=fixture({slow_poll=true})
  assert(r.kind=="stopped" and s.time==5.5 and s.inventory_slices==1)
  for _,status in ipairs(s.reports) do
    if status.state=="running" then assert(status.code==nil,"successful control reported transport failure") end
  end
end)
test("established retry keeps tracker alive",function()
  local r,s=fixture({running_retry=true})
  assert(r.kind=="stopped" and s.starts==1 and s.created==1 and s.pumps>=20)
end)
test("transient tick failure retains session and runner",function()
  local r,s=fixture({thrown_retry=true})
  assert(r.kind=="stopped" and s.starts==1 and s.created==1 and s.ticks==4)
  assert(s.pumps>=10)
end)
test("worker has no native subscription",function()
  local r,s=fixture({worker=true});assert(r.kind=="stopped" and s.enabled==0 and s.listened==0)
end)
for _,case in ipairs({{failure={fatal=true,code="stale_session"},kind="blocked",code="stale_session"},
  {failure="credential_secret",kind="crashed",code="runtime_error"},
  {failure={fatal=true,code="credential_secret"},kind="blocked",code="bridge_error"}}) do
  test("failure classification "..case.code,function()
    local r,s=fixture({failure=case.failure})
    assert(r.kind==case.kind and r.code==case.code)
    assert(s.disabled==1 and s.ignored==1)
    for _,status in ipairs(s.reports) do assert(status.code~="credential_secret") end
    assert(#s.output==0)
  end)
end
test("cleanup error blocks replacement",function()
  local r,s=fixture({cleanup_error=true});assert(r.kind=="blocked" and r.code=="cleanup_failed")
  assert(s.ignored==1 and s.disabled==1)
end)
test("false subscription does not claim ownership",function()
  local r,s=fixture({subscribe_false=true});assert(r.kind=="stopped" and s.disabled==0 and s.ignored==1)
end)
for _,boundary in ipairs({"config_error","component_error","journal_error"}) do
  test("protected setup "..boundary,function()
    local r,s=fixture({[boundary]=true});assert(r.kind=="blocked" and s.created==0)
    assert(r.code~="credential_secret" and #s.output==0)
  end)
end
test("foreground exposes only safe error codes",function()
  local r,s=fixture({foreground=true,failure="credential_secret"})
  assert(r.kind=="crashed" and #s.output>0)
  assert(not table.concat(s.output," "):find("credential_secret",1,true))
end)
assert(#failures==0,table.concat(failures,"\n"))
