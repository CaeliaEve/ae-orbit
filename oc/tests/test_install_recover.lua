local script=assert(loadfile(TEST_ROOT.."/oc/bin/ae-orbit-install-recover.lua"))
local saved_fs,saved_service,saved_print=package.loaded.filesystem,package.loaded["aeorbit.service"],print
local function exercise(option)
  local a="/lib/aeorbit/quantity.lua.ae-orbit-new"
  local b="/lib/aeorbit/quantity.lua.ae-orbit-old"
  local c="/lib/aeorbit/base64.lua.ae-orbit-old"
  local files={[a]="next",[b]="old",["/lib/aeorbit/quantity.lua"]="live",
    ["/etc/ae-orbit.cfg"]="private-configuration",["/var/lib/ae-orbit/journal.1"]="private-journal",
    ["/bin/other.lua.ae-orbit-new"]="unrelated"}
  local dirs={["/lib/aeorbit"]=true,["/bin"]=true,["/etc"]=true,["/tmp/ae-orbit-install.lock"]=true}
  if option=="collision" then files[a..".saved-1"]="previous-recovery" end
  if option=="config" then files["/etc/ae-orbit.cfg.ae-orbit-old"]="private-old-config" end
  if option=="empty" then files[a]=nil;files[b]=nil end
  if option=="digit" then files[c]="old-base64" end
  if option=="missing_live" then files["/lib/aeorbit/quantity.lua"]=nil end
  local removed,renamed,output=0,0,{}
  package.loaded.filesystem={exists=function(p) return files[p]~=nil or dirs[p] end,
    isDirectory=function(p) return dirs[p] or false end,isLink=function(p) return option=="link" and p==a end,
    list=function(dir)
      local names={}
      for p in pairs(files) do local prefix=dir.."/";if p:sub(1,#prefix)==prefix then local n=p:sub(#prefix+1);if not n:find("/",1,true) then names[#names+1]=n end end end
      local i=0;return function() i=i+1;return names[i] end
    end,
    rename=function(from,to) assert(files[from] and not files[to]);files[to]=files[from];files[from]=nil;renamed=renamed+1;return true end,
    remove=function(p) assert(p=="/tmp/ae-orbit-install.lock");dirs[p]=nil;removed=removed+1;return true end}
  package.loaded["aeorbit.service"]={get=function() return {stop=function() return option~="stop" end} end}
  print=function(line) output[#output+1]=line end
  local ok=pcall(script)
  assert(files["/etc/ae-orbit.cfg"]=="private-configuration" and files["/var/lib/ae-orbit/journal.1"]=="private-journal")
  assert(files["/bin/other.lua.ae-orbit-new"]=="unrelated")
  assert(not table.concat(output):find("private",1,true),"recovery exposed file content")
  if option=="config" or option=="stop" or option=="link" then assert(not ok and removed==0 and renamed==0)
  else
    assert(ok and removed==1)
    local suffix=option=="collision" and ".saved-2" or ".saved-1"
    if option~="empty" then assert(files[a..suffix]=="next" and files[b..suffix]=="old" and renamed==(option=="digit" and 3 or 2)) end
    if option=="digit" then assert(files[c..suffix]=="old-base64") end
    if option=="missing_live" then assert(files["/lib/aeorbit/quantity.lua"]==nil)
    else assert(files["/lib/aeorbit/quantity.lua"]=="live") end
  end
end
for _,option in ipairs({"normal","digit","collision","config","stop","link","empty","missing_live"}) do exercise(option) end
package.loaded.filesystem,package.loaded["aeorbit.service"],print=saved_fs,saved_service,saved_print
