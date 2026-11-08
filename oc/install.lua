-- OpenOS: ae-orbit-install [github [branch/tag/commit] | https://your-ae-service]
-- The installed updater uses the same entry point as the first installation.
local component = require("component")
local computer = require("computer")
local filesystem = require("filesystem")
local args = {...}
local source = args[1] or "github"
local repository = "CaeliaEve/ae-orbit"
local internet = assert(component.getPrimary("internet"), "An internet card is required")
local chunk_bytes, file_budget, timeout = 1024, 131072, 30
local origin, revision
local lock_path, locked = "/tmp/ae-orbit-install.lock", false

if source == "github" then
  revision = args[2] or "main"
  assert(type(revision) == "string" and #revision > 0 and #revision <= 200 and
    not revision:find("[%c%s]"), "Use a GitHub branch, tag or full commit SHA")
  assert(args[3] == nil, "Usage: ae-orbit-install github [branch/tag/commit]")
else
  origin = source:gsub("/+$", "")
  assert(origin:match("^https://[A-Za-z0-9%.%-%[%]:]+$") or
    origin:match("^http://127%.0%.0%.1:%d+$"), "Use an HTTPS service origin")
  assert(args[2] == nil, "Usage: ae-orbit-install https://your-ae-service")
end

local files = {
  {"lib/aeorbit/quantity.lua", "/lib/aeorbit/quantity.lua"},
  {"lib/aeorbit/json.lua", "/lib/aeorbit/json.lua"},
  {"lib/aeorbit/base64.lua", "/lib/aeorbit/base64.lua"},
  {"lib/aeorbit/cache.lua", "/lib/aeorbit/cache.lua"},
  {"lib/aeorbit/bitops.lua", "/lib/aeorbit/bitops.lua"},
  {"lib/aeorbit/sha256.lua", "/lib/aeorbit/sha256.lua"},
  {"lib/aeorbit/gzip.lua", "/lib/aeorbit/gzip.lua"},
  {"lib/aeorbit/nbt.lua", "/lib/aeorbit/nbt.lua"},
  {"lib/aeorbit/journal.lua", "/lib/aeorbit/journal.lua"},
  {"lib/aeorbit/client.lua", "/lib/aeorbit/client.lua"},
  {"lib/aeorbit/adapter.lua", "/lib/aeorbit/adapter.lua"},
  {"lib/aeorbit/tracker.lua", "/lib/aeorbit/tracker.lua"},
  {"lib/aeorbit/runner.lua", "/lib/aeorbit/runner.lua"},
  {"lib/aeorbit/read_diagnostics.lua", "/lib/aeorbit/read_diagnostics.lua"},
  {"bin/ae-orbit.lua", "/bin/ae-orbit.lua"},
  {"config.example.lua", "/etc/ae-orbit.cfg.example"},
  {"install.lua", "/bin/ae-orbit-install.lua"},
}

local function exists(path)
  return filesystem.exists(path) or filesystem.isLink(path)
end

local function close_file(file)
  -- OpenOS returns no values on a successful close. Flush writes separately.
  local closed, reason = file:close()
  assert(closed ~= false and reason == nil, reason or "File close failed")
end

local function status_error(code, label, metadata)
  if metadata and (code == 403 or code == 429) then
    error("GitHub HTTP " .. code .. ": API access denied or rate limited. Retry later, or use " ..
      "ae-orbit-install github <full 40-character commit SHA>", 0)
  end
  error("Download failed: " .. label .. " (HTTP " .. tostring(code or "unavailable") .. ")", 0)
end

-- Metadata is capped at 128 bytes. Program bodies go straight to disk in 1 KiB
-- chunks; only one file is compiled at a time, and downloads are never executed.
local function download(url, destination, budget, metadata)
  local handle, file
  local ok, value = pcall(function()
    local headers = { ["User-Agent"] = "AE-Orbit-OpenOS", ["Accept-Encoding"] = "identity" }
    if metadata then
      headers.Accept = "application/vnd.github.sha"
      headers["X-GitHub-Api-Version"] = "2022-11-28"
    end
    handle = assert(internet.request(url, nil, headers))
    local deadline = computer.uptime() + timeout
    local function wait()
      assert(computer.uptime() < deadline, "Download timeout: " .. url)
      os.sleep(0.05)
    end
    while true do
      local connected, ready = pcall(handle.finishConnect)
      if not connected then
        -- OC can throw from finishConnect for HTTP errors even though response()
        -- already contains the status. Keep that status available to the user.
        local available, code = pcall(handle.response)
        if available and code and code ~= 200 then status_error(code, url, metadata) end
        error("Connection failed: " .. url, 0)
      end
      if ready then break end
      wait()
    end
    local code, _, response_headers = handle.response()
    if code ~= 200 then status_error(code, url, metadata) end
    local expected
    for name, item in pairs(response_headers or {}) do
      if type(name) == "string" and name:lower() == "content-length" then
        expected = tonumber(type(item) == "table" and item[1] or item)
        assert(expected and expected >= 0 and expected == math.floor(expected), "Invalid Content-Length")
        assert(expected <= budget, "Download exceeds the file budget")
      end
    end
    if destination then file = assert(io.open(destination, "wb")) end
    local parts, total = {}, 0
    while true do
      assert(computer.uptime() < deadline, "Download timeout: " .. url)
      local chunk, reason = handle.read(chunk_bytes)
      if chunk == nil then
        assert(not reason, "Download read failed: " .. tostring(reason))
        break
      end
      if #chunk == 0 then wait()
      else
        total = total + #chunk
        assert(total <= budget, "Download exceeds the file budget")
        if file then assert(file:write(chunk)) else parts[#parts + 1] = chunk end
      end
    end
    assert(total > 0, "Empty download: " .. url)
    assert(not expected or total == expected, "Incomplete download: Content-Length mismatch")
    if file then
      assert(file:flush()); close_file(file); file = nil
      assert(loadfile(destination, "t"), "Downloaded file is not valid Lua: " .. url)
    else
      return table.concat(parts)
    end
  end)
  if handle then pcall(handle.close) end
  if file then pcall(file.close, file) end
  if not ok then error(value, 0) end
  return value
end

local function copy(source_path, destination)
  local input, output
  local ok, reason = pcall(function()
    input = assert(io.open(source_path, "rb"))
    output = assert(io.open(destination, "wb"))
    while true do
      local chunk, err = input:read(chunk_bytes)
      if not chunk then assert(not err, err); break end
      assert(output:write(chunk))
    end
    assert(output:flush()); close_file(output); output = nil
    close_file(input); input = nil
  end)
  if input then pcall(input.close, input) end
  if output then pcall(output.close, output) end
  if not ok then error(reason, 0) end
end

local function remove(path)
  if exists(path) then assert(filesystem.remove(path), "Cannot remove " .. path) end
end

local function rollback()
  local failures = {}
  for index = #files, 1, -1 do
    local entry = files[index]
    local ok, reason = pcall(function()
      if entry.promoted then remove(entry[2]) end
      if entry.saved then assert(filesystem.rename(entry.backup, entry[2]), "Cannot restore " .. entry[2]) end
    end)
    if not ok then failures[#failures + 1] = tostring(reason) end
    if entry.staged then
      ok, reason = pcall(remove, entry.temporary)
      if not ok then failures[#failures + 1] = tostring(reason) end
    end
  end
  return failures
end

print("Installing AE Orbit. Stop any running bridge before updating.")
local ok, reason = pcall(function()
  assert(filesystem.makeDirectory(lock_path),
    "Installer lock exists or cannot be created: " .. lock_path .. "; see README recovery instructions")
  locked = true
  local base
  if revision then
    if #revision ~= 40 or not revision:match("^%x+$") then
      local encoded = revision:gsub("[^A-Za-z0-9_.~-]", function(char)
        return string.format("%%%02X", string.byte(char))
      end)
      revision = download("https://api.github.com/repos/" .. repository .. "/commits/" .. encoded, nil, 128, true)
      revision = revision:match("^%s*(.-)%s*$")
      assert(#revision == 40 and revision:match("^%x+$"), "GitHub returned an invalid commit SHA")
    end
    revision = revision:lower()
    base = "https://raw.githubusercontent.com/" .. repository .. "/" .. revision .. "/oc/"
    print("GitHub commit: " .. revision)
  else
    base = origin .. "/oc/"
  end

  if not exists("/etc/ae-orbit.cfg") then
    files[#files + 1] = {"config.example.lua", "/etc/ae-orbit.cfg", copy_from = "/etc/ae-orbit.cfg.example.ae-orbit-new"}
  end
  -- Stage next to each destination so promotion/rollback never crosses disks.
  -- Refuse leftover backups after a hard interruption instead of overwriting them.
  for _, entry in ipairs(files) do
    entry.temporary = entry[2] .. ".ae-orbit-new"
    entry.backup = entry[2] .. ".ae-orbit-old"
    assert(not exists(entry.temporary) and not exists(entry.backup),
      "Previous installation files remain beside " .. entry[2] .. "; see README recovery instructions")
    assert(not filesystem.isDirectory(entry[2]), "Destination is a directory: " .. entry[2])
  end
  for _, entry in ipairs(files) do
    local directory = filesystem.path(entry[2])
    if not exists(directory) then assert(filesystem.makeDirectory(directory)) end
    entry.staged = true
    if entry.copy_from then copy(entry.copy_from, entry.temporary)
    else download(base .. entry[1], entry.temporary, file_budget) end
    -- OC owns the garbage collector and does not expose collectgarbage in its
    -- sandbox. Yield between files so the host can reclaim compiled chunks.
    os.sleep(0)
  end
  -- No program is replaced until every download has passed validation.
  for _, entry in ipairs(files) do
    if exists(entry[2]) then
      assert(filesystem.rename(entry[2], entry.backup), "Cannot back up " .. entry[2])
      entry.saved = true
    end
    local moved, err = filesystem.rename(entry.temporary, entry[2])
    assert(moved, "Cannot rename staged file to " .. entry[2] .. ": " .. tostring(err))
    entry.promoted = true
  end
end)

if not ok then
  local failures = rollback()
  if locked then
    local released, err = pcall(remove, lock_path)
    if not released then failures[#failures + 1] = tostring(err) end
  end
  local detail = #failures > 0 and ("\nRecovery needed; keep .ae-orbit-old backups: " .. table.concat(failures, "; ")) or ""
  error(tostring(reason) .. detail, 0)
end
for _, entry in ipairs(files) do
  if entry.saved then
    local cleaned = pcall(remove, entry.backup)
    if not cleaned then print("Installed, but old backup needs cleanup: " .. entry.backup) end
  end
end
if not pcall(remove, lock_path) then print("Installed, but installer lock needs cleanup: " .. lock_path) end
print("Installed. Edit /etc/ae-orbit.cfg: url, token and me_address; then run ae-orbit.")
print("Next update: ae-orbit-install (GitHub), or ae-orbit-install https://your-ae-service")
