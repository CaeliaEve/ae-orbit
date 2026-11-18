-- Temporarily avoid full native AE lists on an existing cooperative device.
local filesystem=require("filesystem")
local service=require("aeorbit.service")
local path="/etc/ae-orbit.cfg"
local backup=path..".before-exact-only"
local temporary=path..".exact-only-new"
local function read(file_path)
  local file=assert(io.open(file_path,"rb"))
  local read_ok,value,read_reason=pcall(file.read,file,65537)
  local close_ok,closed,close_reason=pcall(file.close,file)
  if not read_ok then error(value,0) end
  assert(read_reason==nil,"Configuration read failed")
  assert(close_ok and closed~=false and close_reason==nil,"Configuration close failed")
  assert(type(value)=="string" and #value<=65536,"Configuration exceeds size limit")
  return value
end
local function write(file_path,value)
  local file=assert(io.open(file_path,"wb"))
  local ok,reason=pcall(function() assert(file:write(value));assert(file:flush()) end)
  local close_ok,closed,close_reason=pcall(file.close,file)
  if not ok then error(reason,0) end
  assert(close_ok and closed~=false and close_reason==nil,"Configuration close failed")
end
assert(not filesystem.exists(temporary),"Previous staged configuration exists: "..temporary)
local original=read(path)
local config=assert(load(original,"="..path,"t"))()
assert(type(config)=="table" and config.bridge_mode=="cooperative",
  "Run this profile only on a cooperative OC")
local role=config.role or "primary"
assert(role=="primary" or role=="quantity_worker","Unsupported cooperative OC role")
local active=config.inventory_mode=="exact_only" and config.enable_events==false
assert(active or not filesystem.exists(backup),"Existing backup kept: "..backup)
local origin=assert(config.url):gsub("/+$","")
assert(origin:match("^https?://[A-Za-z0-9%.%-%[%]:]+$"),"Invalid AE Orbit origin")
local updated="local config=(function()\n"..original.."\nend)()\n"..
  "config.inventory_mode='exact_only'\nconfig.enable_events=false\nreturn config\n"
assert(#updated<=65536 and load(updated,"="..temporary,"t"),"Updated configuration exceeds size limit")
local stopped=service.get():stop(10)
assert(stopped,"Bridge must stop safely before updating")
assert(loadfile("/bin/ae-orbit-install.lua"))(origin)
assert(read(path)==original,"Configuration changed during installation; current settings kept")
for _,name in ipairs({"adapter","cooperative","runtime"}) do
  local source=read("/lib/aeorbit/"..name..".lua")
  assert(source:find("inventory_mode",1,true) and source:find('"exact_only"',1,true),
    "Installed module does not support exact-only mode: "..name)
end
if not active then
  write(temporary,updated)
  assert(filesystem.rename(path,backup),"Cannot preserve configuration backup")
  if not filesystem.rename(temporary,path) then
    assert(filesystem.rename(backup,path),"Restore configuration from "..backup)
    error("Could not activate exact-only profile; original configuration restored",0)
  end
end
print("Exact-only profile installed. Full inventory scans and event subscriptions are disabled.")
print("Role: "..role)
if role=="primary" then
  print("Known quantities and CPU monitoring continue; new discovery is paused and some recipes cannot be confirmed.")
else
  print("Known quantities continue; this worker remains quantity-only.")
end
if not active then print("Original configuration: "..backup) end
print("Run reboot now to clear old native iterators and reload all modules, then ae-orbit start.")
