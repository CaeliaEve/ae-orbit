-- Run with OpenOS: ae-install.lua https://your-ae-service.example
local component = require("component")
local computer = require("computer")
local filesystem = require("filesystem")
local args={...}
local origin=assert(args[1],"Usage: ae-install.lua https://your-ae-service.example"):gsub("/+$","")
assert(origin:match("^https://[A-Za-z0-9%.%-%[%]:]+$") or origin:match("^http://127%.0%.0%.1:%d+$"),
  "Use an HTTPS service origin")
local internet=assert(component.getPrimary("internet"),"An internet card is required")
local files={
  {"lib/aeorbit/quantity.lua","/lib/aeorbit/quantity.lua"},
  {"lib/aeorbit/json.lua","/lib/aeorbit/json.lua"},
  {"lib/aeorbit/base64.lua","/lib/aeorbit/base64.lua"},
  {"lib/aeorbit/cache.lua","/lib/aeorbit/cache.lua"},
  {"lib/aeorbit/journal.lua","/lib/aeorbit/journal.lua"},
  {"lib/aeorbit/client.lua","/lib/aeorbit/client.lua"},
  {"lib/aeorbit/adapter.lua","/lib/aeorbit/adapter.lua"},
  {"lib/aeorbit/tracker.lua","/lib/aeorbit/tracker.lua"},
  {"lib/aeorbit/runner.lua","/lib/aeorbit/runner.lua"},
  {"bin/ae-orbit.lua","/bin/ae-orbit.lua"},
  {"config.example.lua","/etc/ae-orbit.cfg.example"},
}
local function download(source,destination)
  local handle,file
  local temporary=destination..".download"
  local ok,err=pcall(function()
    local directory=filesystem.path(destination)
    if not filesystem.exists(directory) then assert(filesystem.makeDirectory(directory)) end
    handle=assert(internet.request(origin.."/oc/"..source))
    local deadline=computer.uptime()+30
    local function wait()
      assert(computer.uptime()<deadline,"Download timeout")
      os.sleep(0.05)
    end
    while not handle.finishConnect() do wait() end
    assert(handle.response()==200,"Download failed: "..source)
    file=assert(io.open(temporary,"wb"))
    local total=0
    while true do
      assert(computer.uptime()<deadline,"Download timeout")
      local chunk=handle.read(1024)
      if chunk==nil then break end
      if #chunk==0 then wait()
      else
        total=total+#chunk;assert(total<=131072,"Download exceeds the file budget")
        assert(file:write(chunk))
      end
    end
    assert(total>0,"Empty download")
    assert(file:flush());assert(file:close());file=nil
    assert(loadfile(temporary),"Downloaded file is not valid Lua")
  end)
  if handle then pcall(handle.close) end
  if file then pcall(file.close,file) end
  if not ok then filesystem.remove(temporary);error(err,0) end
  return temporary
end
print("Downloading AE Orbit. Stop any running bridge before installing.")
-- Download and syntax-check every file before replacing program files.
for _,entry in ipairs(files) do entry[3]=download(entry[1],entry[2]) end
for _,entry in ipairs(files) do
  if filesystem.exists(entry[2]) then assert(filesystem.remove(entry[2])) end
  assert(filesystem.rename(entry[3],entry[2]))
end
print("Installed. Copy /etc/ae-orbit.cfg.example to /etc/ae-orbit.cfg for a first install.")
print("Configure url, token and me_address, then run: ae-orbit")
