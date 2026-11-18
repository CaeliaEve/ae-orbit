-- Apply a bounded throughput profile to an existing cooperative primary.
-- Downloads use the configured AE Orbit server; connection values stay intact.
local filesystem=require("filesystem")
local computer=require("computer")
local service=require("aeorbit.service")
local path="/etc/ae-orbit.cfg"
local backup=path..".before-throughput"
local temporary=path..".throughput-new"
local function read(file_path)
  local file=assert(io.open(file_path,"rb"))
  local read_ok,value,read_reason=pcall(file.read,file,65537)
  local close_ok,closed,close_reason=pcall(file.close,file)
  if not read_ok then error(value,0) end
  assert(read_reason==nil,read_reason or "Configuration read failed")
  -- OpenOS may return no values from a successful close; only an exception,
  -- explicit false, or a returned error message indicates failure.
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
assert(not filesystem.exists(backup),"Throughput backup already exists: "..backup)
assert(not filesystem.exists(temporary),"Previous staged configuration exists: "..temporary)
local original=read(path)
local config=assert(load(original,"="..path,"t"))()
assert(type(config)=="table" and config.bridge_mode=="cooperative" and (config.role or "primary")=="primary",
  "Run this profile only on the cooperative primary OC")
local total=computer.totalMemory()
assert(total>=1048576,"This profile requires at least 1 MiB total memory")
local origin=assert(config.url):gsub("/+$","")
assert(origin:match("^https?://[A-Za-z0-9%.%-%[%]:]+$"),"Invalid AE Orbit origin")
local cache_bytes=math.min(393216,math.floor(total/4))
local updated="local config=(function()\n"..original.."\nend)()\n"..
  "config.cache_entries=2048\nconfig.cache_bytes="..cache_bytes.."\n"..
  "config.max_batch_bytes=32768\nconfig.batch_interval_seconds=0.25\n"..
  "config.scan_memory_floor=math.max(config.scan_memory_floor or 0,524288)\n"..
  "config.enable_events=false\nreturn config\n"
assert(load(updated,"="..temporary,"t"))
local owner=service.get()
local stopped,reason=owner:stop(10)
assert(stopped,"Bridge must stop safely before updating: "..tostring(reason))
assert(loadfile("/bin/ae-orbit-install.lua"))(origin)
assert(read(path)==original,"Configuration changed during installation; original settings kept")
write(temporary,updated)
assert(filesystem.rename(path,backup),"Cannot preserve configuration backup")
local promoted=filesystem.rename(temporary,path)
if not promoted then
  assert(filesystem.rename(backup,path),"Restore configuration from "..backup)
  error("Could not activate throughput profile; original configuration restored",0)
end
print("Throughput profile installed. Original configuration: "..backup)
print("Cache: 2048 entries, "..cache_bytes.." bytes; batch window: 250 ms; scan reserve: at least 512 KiB.")
print("Run reboot now to reload the updated Lua modules.")
