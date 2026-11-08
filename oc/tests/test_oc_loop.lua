local entry=assert(loadfile(TEST_ROOT.."/oc/bin/ae-orbit.lua"))

local function run_case(fail_first_poll,first_control_seconds,work_seconds,jitter,poll_delay,steady_slow)
  first_control_seconds=first_control_seconds or 0.1
  work_seconds=work_seconds or 0.1
  jitter=jitter or 0
  poll_delay=poll_delay or 1
  local clock,remaining=0,0
  local starts,work_polls={},{}
  local bridge={session="test-session",poll_delay=poll_delay,uploaded=0,ids={count=0},dirty={count=0}}
  function bridge:start() end
  function bridge:diagnose() end
  function bridge:tick()
    if #starts==3 then error({code="test_stop",fatal=true}) end
    starts[#starts+1]=clock
    clock=clock+((#starts==1 or steady_slow) and first_control_seconds or 0.1)
    local completed=clock
    clock=clock+0.1;remaining=3
    local ok=not (fail_first_poll and #starts==1)
    return {control_ok=ok,inventory_ok=true,inventory_work=ok,control_started_at=starts[#starts],control_completed_at=completed}
  end
  function bridge:continue_inventory()
    assert(remaining>0,"the application kept polling an empty work queue")
    work_polls[#work_polls+1]=#starts
    clock=clock+work_seconds;remaining=remaining-1
    return remaining>0
  end
  local me={setItemEventSubscription=function() end}
  local modules={
    component={proxy=function() return me end,getPrimary=function() return {} end},
    computer={uptime=function() return clock end,freeMemory=function() return 131072 end},
    event={listen=function() end,ignore=function() end},
    filesystem={exists=function() return true end},
    ["aeorbit.adapter"]={new=function() return {} end,has_method=function() return true end},
    ["aeorbit.client"]={new=function() return {} end},
    ["aeorbit.journal"]={new=function() return {} end},
    ["aeorbit.runner"]={new=function() return bridge end},
  }
  local saved={}
  for name,module in pairs(modules) do saved[name]=package.loaded[name];package.loaded[name]=module end
  local old_loadfile,old_sleep,old_random,old_print,old_stderr=loadfile,os.sleep,math.random,print,io.stderr
  loadfile=function(path)
    assert(path=="/etc/ae-orbit.cfg")
    return function() return {me_address="test-me",url="http://127.0.0.1:18080",token="test-token"} end
  end
  os.sleep=function(delay) assert(delay>=0);clock=clock+delay end
  math.random=function() return jitter end
  print=function() end
  local stopped
  io.stderr={write=function(_,message) stopped=message end}
  local ok,err=pcall(entry)
  loadfile,os.sleep,math.random,print,io.stderr=old_loadfile,old_sleep,old_random,old_print,old_stderr
  for name in pairs(modules) do package.loaded[name]=saved[name] end
  assert(ok,err)
  assert(stopped=="AE Orbit stopped: test_stop\n","the application did not shut down at the fixture boundary")
  local interval=math.max(poll_delay,1)*(1+jitter*0.2)
  if work_seconds>0.1 then
    assert(#work_polls>0 and #work_polls<9,"inventory work ignored the control deadline")
    assert(starts[2]<=first_control_seconds+interval+work_seconds,"inventory work overran more than one bounded slice")
  elseif fail_first_poll then
    local retry_delay=math.max(poll_delay,2)*(1+jitter*0.2)
    assert(math.abs(starts[2]-first_control_seconds-retry_delay)<0.000001,"poll backoff was not measured from the completed request")
    assert(#work_polls==6,"the application continued acquisition during poll backoff")
    for _,poll in ipairs(work_polls) do assert(poll~=1) end
  else
    assert(math.abs(starts[2]-math.max(interval,first_control_seconds+0.4))<0.000001,"successful control latency was added to the interval or stole granted inventory work")
    assert(#work_polls==9,"a slow successful request starved its remaining inventory work")
  end
  if work_seconds==0.1 then
    local expected=steady_slow and math.max(interval,first_control_seconds+0.4) or interval
    assert(math.abs(starts[3]-starts[2]-expected)<0.000001,"control scheduling drifted or sent catch-up polls")
  end
end

run_case(false)
run_case(false,0.35)
run_case(false,3)
run_case(false,3,0.1,0,1,true)
run_case(false,0.35,0.1,0.5)
run_case(false,0.35,0.1,0.5,5)
run_case(true)
run_case(true,3)
run_case(true,3,0.1,0.5)
run_case(false,0.1,0.6)
