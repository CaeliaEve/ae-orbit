local script=assert(loadfile(TEST_ROOT.."/oc/bin/ae-orbit-throughput.lua"))
local original_io,original_loadfile=io,loadfile
local original_print=print
local function exercise(options)
  options=options or {}
  local path="/etc/ae-orbit.cfg"
  local original="return {bridge_mode='cooperative',role='"..(options.role or "primary")..
    "',url='http://fixture.test:8080',token='private-token',me_address='private-component',scan_memory_floor=524288}"
  local files={[path]=original}
  if options.backup then files[path..".before-throughput"]="keep" end
  local stopped,installed,output,closes=0,0,{},0
  package.loaded.filesystem={exists=function(p) return files[p]~=nil end,
    rename=function(from,to)
      if options.rename_failure and to==path and from:match("throughput%-new$") then return false end
      assert(files[from] and not files[to]);files[to]=files[from];files[from]=nil;return true
    end}
  package.loaded.computer={totalMemory=function() return options.memory or 2097152 end}
  package.loaded["aeorbit.service"]={get=function() return {stop=function()
    stopped=stopped+1;return not options.stop_failure,"stopping"
  end} end}
  io={open=function(p,mode)
    local data=files[p]
    if mode=="rb" and not data then return nil end
    return {read=function() return data end,write=function(_,value) files[p]=value;return true end,
      flush=function() return true end,close=function()
        closes=closes+1
        if options.close_failure and (not options.close_at or options.close_at==closes) then
          if options.close_failure=="throw" then error("close failed") end
          if options.close_failure=="false" then return false end
          return nil,"close failed"
        end
        if options.close_returns_true then return true end
        -- Real OpenOS file handles can successfully close with no return values.
      end}
  end}
  loadfile=function(p)
    assert(p=="/bin/ae-orbit-install.lua")
    return function(origin)
      assert(origin=="http://fixture.test:8080" and stopped==1)
      installed=installed+1;if options.install_failure then error("installation failed") end
    end
  end
  print=function(value) output[#output+1]=value end
  local ok=pcall(script)
  io,loadfile,print=original_io,original_loadfile,original_print
  for _,message in ipairs(output) do assert(not message:find("private",1,true),"configuration secret was printed") end
  if not options.role and not options.memory and not options.backup and not options.stop_failure and not options.install_failure and not options.rename_failure and not options.close_failure then
    assert(ok and installed==1 and files[path..".before-throughput"]==original)
    local config=assert(load(files[path]))()
    assert(config.token=="private-token" and config.me_address=="private-component")
    assert(config.cache_entries==2048 and config.cache_bytes==393216 and config.max_batch_bytes==32768)
    assert(config.batch_interval_seconds==0.25 and config.scan_memory_floor==524288 and config.enable_events==false)
  else
    assert(not ok and files[path]==original,"failed profile changed the original configuration")
    if options.backup then assert(files[path..".before-throughput"]=="keep") end
    if options.role or options.memory or options.backup then assert(stopped==0 and installed==0) end
    if options.close_failure and not options.close_at then assert(stopped==0 and installed==0) end
  end
end
exercise()
exercise({close_returns_true=true})
exercise({role="quantity_worker"})
exercise({memory=524288})
exercise({backup=true})
exercise({stop_failure=true})
exercise({install_failure=true})
exercise({rename_failure=true})
for _,mode in ipairs({"throw","false","nil_error"}) do
  exercise({close_failure=mode})
  exercise({close_failure=mode,close_at=3})
end
