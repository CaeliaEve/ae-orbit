local script=assert(loadfile(TEST_ROOT.."/oc/bin/ae-orbit-exact-only.lua"))
local original_io,original_loadfile,original_print=io,loadfile,print
local function exercise(options)
  options=options or {}
  local path="/etc/ae-orbit.cfg"
  local original="return {bridge_mode='"..(options.single and "single" or "cooperative")..
    "',role='"..(options.invalid_role and "unexpected" or options.worker and "quantity_worker" or "primary")..
    "',url='http://fixture.test:8080',token='private-token',me_address='private-component',"..
    "cache_entries=2048,cache_bytes=393216,max_batch_bytes=32768,scan_memory_floor=524288"..
    (options.active and ",inventory_mode='exact_only',enable_events=false" or "").."}"
  local files={[path]=original}
  if options.backup or options.active then files[path..".before-exact-only"]="keep-backup" end
  if options.staged then files[path..".exact-only-new"]="keep-staged" end
  local stops,installs=0,0
  local output={}
  local saved_fs,saved_service=package.loaded.filesystem,package.loaded["aeorbit.service"]
  package.loaded.filesystem={exists=function(p) return files[p]~=nil end,rename=function(from,to)
    if options.rename_failure and to==path and from:match("exact%-only%-new$") then return false end
    assert(files[from] and not files[to]);files[to]=files[from];files[from]=nil;return true
  end}
  package.loaded["aeorbit.service"]={get=function() return {stop=function()
    stops=stops+1;return not options.stop_failure
  end} end}
  io={open=function(p,mode)
    local value=files[p]
    if mode=="rb" and not value then return nil end
    return {read=function() return value end,write=function(_,v) files[p]=v;return true end,
      flush=function() return true end,close=function()
        if options.close_failure then return nil,"close failed" end
        -- OpenOS close succeeds without return values.
      end}
  end}
  loadfile=function(p)
    assert(p=="/bin/ae-orbit-install.lua")
    return function(origin)
      assert(stops==1 and origin=="http://fixture.test:8080")
      installs=installs+1
      if options.install_failure then error("installation failed") end
      if options.config_changed then files[path]="return {external_change=true}" end
      for _,name in ipairs({"adapter","cooperative","runtime"}) do
        files["/lib/aeorbit/"..name..".lua"]=options.old_module and "old module" or 'inventory_mode="exact_only"'
      end
    end
  end
  print=function(value) output[#output+1]=value end
  local ok=pcall(script)
  io,loadfile,print=original_io,original_loadfile,original_print
  package.loaded.filesystem,package.loaded["aeorbit.service"]=saved_fs,saved_service
  assert(not table.concat(output," "):find("private",1,true),"profile exposed connection settings")
  if next(options)==nil or options.active or (options.worker and not options.stop_failure) then
    assert(ok and installs==1)
    local config=assert(load(files[path]))()
    assert(config.inventory_mode=="exact_only" and config.enable_events==false)
    assert(config.role==(options.worker and "quantity_worker" or "primary"),"profile changed the device role")
    assert(config.token=="private-token" and config.me_address=="private-component")
    assert(config.cache_entries==2048 and config.cache_bytes==393216 and config.max_batch_bytes==32768 and config.scan_memory_floor==524288)
    assert(files[path..".before-exact-only"]==(options.active and "keep-backup" or original))
    if options.active then assert(files[path]==original,"repeated profile rewrote configuration") end
  else
    assert(not ok)
    assert(files[path]==(options.config_changed and "return {external_change=true}" or original),"failed profile overwrote active configuration")
    if options.backup then assert(files[path..".before-exact-only"]=="keep-backup") end
    if options.single or options.invalid_role or options.backup or options.staged or options.close_failure then assert(stops==0 and installs==0) end
  end
end
exercise()
for _,name in ipairs({"active","single","worker","invalid_role","backup","staged","stop_failure","install_failure","old_module","config_changed","rename_failure","close_failure"}) do
  exercise({[name]=true})
end
exercise({worker=true,active=true})
exercise({worker=true,stop_failure=true})
