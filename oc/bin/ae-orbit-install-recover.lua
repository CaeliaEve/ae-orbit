-- Run only after an interrupted installer has returned to the shell.
-- Preserve every leftover beside its original path; never touch live settings
-- or the execution journal. Reinstall the full program before starting it.
local filesystem=require("filesystem")
local service=require("aeorbit.service")
assert(service.get():stop(10),"Bridge must stop safely before recovery")
local leftovers={}
local function collect(directory,allowed)
  if not filesystem.exists(directory) then return end
  for name in filesystem.list(directory) do
    local base=name:match("^(.*)%.ae%-orbit%-new$") or name:match("^(.*)%.ae%-orbit%-old$")
    if base and allowed(base) then
      local path=directory.."/"..name
      assert(not filesystem.isDirectory(path) and not filesystem.isLink(path),"Unexpected recovery entry: "..path)
      leftovers[#leftovers+1]=path
    end
  end
end
collect("/lib/aeorbit",function(name) return name:match("^[a-z0-9_]+%.lua$")~=nil end)
collect("/bin",function(name) return name=="ae-orbit.lua" or name=="ae-orbit-install.lua" end)
collect("/etc/rc.d",function(name) return name=="ae-orbit.lua" end)
collect("/etc",function(name) return name=="ae-orbit.cfg.example" end)
-- Never rename real configuration leftovers automatically: they may contain
-- the user's sole recoverable credentials after a first-install interruption.
for _,suffix in ipairs({".ae-orbit-new",".ae-orbit-old"}) do
  assert(not filesystem.exists("/etc/ae-orbit.cfg"..suffix),"Configuration recovery needs inspection: /etc/ae-orbit.cfg"..suffix)
end
table.sort(leftovers)
local index=1
while true do
  local available=true
  for _,path in ipairs(leftovers) do
    local target=path..".saved-"..index
    if filesystem.exists(target) or filesystem.isLink(target) then available=false;break end
  end
  if available then break end
  index=index+1
  assert(index<=1000,"Too many saved recovery files")
end
-- OpenOS has returned to the shell: the failed installer is no longer running.
-- Release only its empty directory lock; refuse an unexpected lock entry.
local lock="/tmp/ae-orbit-install.lock"
if filesystem.exists(lock) or filesystem.isLink(lock) then
  assert(filesystem.isDirectory(lock) and not filesystem.isLink(lock),"Unexpected installer lock")
  for _ in filesystem.list(lock) do error("Installer lock is not empty",0) end
  assert(filesystem.remove(lock),"Cannot release old installer lock")
end
for _,path in ipairs(leftovers) do
  local target=path..".saved-"..index
  assert(filesystem.rename(path,target),"Cannot preserve recovery file: "..path)
  print("Preserved: "..target)
end
print("Preserved "..#leftovers.." interrupted installation files. Settings and journal kept.")
print("Now rerun the full CPU updater; reboot only after it reports success.")
