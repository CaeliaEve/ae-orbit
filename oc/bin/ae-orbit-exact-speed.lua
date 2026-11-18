-- Keep exact-only safety while allowing bounded 16 KiB network packets.
local filesystem=require("filesystem")
local computer=require("computer")
local service=require("aeorbit.service")
local path="/etc/ae-orbit.cfg"
local backup=path..".before-exact-speed"
local temporary=path..".exact-speed-new"
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
assert(type(config)=="table" and config.bridge_mode=="cooperative","Run this profile only on a cooperative OC")
local role=config.role or "primary"
assert(role=="primary" or role=="quantity_worker","Unsupported cooperative OC role")
assert(computer.totalMemory()>=1048576,"This profile requires at least 1 MiB total memory")
local active=config.inventory_mode=="exact_only" and config.enable_events==false and
  config.max_response_bytes==16384 and (config.max_batch_bytes or 0)>=16384 and
  (config.batch_interval_seconds or 0.25)<=0.25
assert(active or not filesystem.exists(backup),"Existing backup kept: "..backup)
local origin=assert(config.url):gsub("/+$","")
assert(origin:match("^https?://[A-Za-z0-9%.%-%[%]:]+$"),"Invalid AE Orbit origin")
local updated="local config=(function()\n"..original.."\nend)()\n"..
  "config.inventory_mode='exact_only'\nconfig.enable_events=false\n"..
  "config.max_response_bytes=16384\nconfig.max_batch_bytes=math.max(config.max_batch_bytes or 0,16384)\n"..
  "config.batch_interval_seconds=math.min(config.batch_interval_seconds or 0.25,0.25)\nreturn config\n"
assert(#updated<=65536 and load(updated,"="..temporary,"t"),"Updated configuration exceeds size limit")
assert(service.get():stop(10),"Bridge must stop safely before updating")
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
    error("Could not activate exact-speed profile; original configuration restored",0)
  end
end
print("Exact-speed profile installed. Native scans and event subscriptions remain disabled.")
print("Role: "..role.."; response: 16 KiB; batch window: at most 250 ms.")
if not active then print("Original configuration: "..backup) end
print("Run reboot, then ae-orbit start. Group read limits stay unchanged.")
