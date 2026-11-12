local cancel = require("aeorbit.cancel")
local M = {}
local default
local codes = {}
for word in ("interrupted manual_stop not_started starting connected reconnecting " ..
  "runtime_error supervisor_failed protocol_error unauthorized forbidden stale_session " ..
  "journal_failed cleanup_failed config_error component_error component_failed setup_failed transport_failed timeout " ..
  "connection_failed incomplete_http_response crash_limit service_start_failed " ..
  "invalid_token invalid_credentials sequence_conflict bridge_error"):gmatch("%S+") do codes[word] = true end
local function code(value)
  return type(value) == "string" and (codes[value] or value:match("^http_%d%d%d$")) and value or "bridge_error"
end
local function copy(value)
  local result = {}
  for key, item in pairs(value) do result[key] = item end
  return result
end

function M.new(deps)
  local self = {snapshot = {state = "stopped", reason = "not_started", restarts = 0}}
  local function publish(state, reason, session)
    local s = self.snapshot
    reason = code(reason or (state == "running" and "connected" or state == "starting" and "starting"))
    if state == "backoff" then s.last_error = reason end
    if s.state == state and s.reason == reason and s.session == session then return end
    s.state, s.reason, s.session, s.updated_uptime = state, reason, session, deps.now()
    if deps.persist then
      local ok, saved = pcall(deps.persist, copy(s))
      if not ok or not saved then s.status_warning = "status_write_failed" else s.status_warning = nil end
    end
  end
  local function reap()
    local owner = self.owner
    -- Body completion precedes native process teardown. Do not trust join(), which
    -- OpenOS implements by killing suspended threads as well as dead ones.
    if owner and owner.finalized and not owner.unsafe and owner.thread and deps.dead(owner.thread) then
      if not owner.desired and self.snapshot.state == "stopping" then
        publish("stopped", owner.stop_reason or "manual_stop")
      end
      self.owner = nil
    end
  end
  local function supervise(owner)
    local crashes = {}
    local ok, err = pcall(function()
      while owner.desired do
        owner.context.check()
        publish("starting", "starting")
        local ran, result = pcall(deps.run, owner.context, owner.path, function(state, reason, session)
          if owner.desired then publish(state, reason, session) end
        end, owner.mode)
        if not ran then
          if cancel.is_stop(result) then cancel.rethrow(result) end
          -- An uncontained runtime exception cannot prove resource cleanup.
          result = {kind = "blocked", code = "cleanup_failed"}
        end
        if type(result) ~= "table" then result = {kind = "blocked", code = "protocol_error"} end
        if result.code == "cleanup_failed" then
          owner.unsafe = true
          publish("blocked", "cleanup_failed")
          break
        end
        if not owner.desired then break end
        if result.kind == "stopped" then
          owner.desired = false
          owner.stop_reason = code(result.code)
          break
        elseif result.kind == "blocked" then
          self.snapshot.last_error = code(result.code)
          publish("blocked", result.code)
          break
        elseif result.kind ~= "crashed" then
          publish("blocked", "protocol_error")
          break
        end
        self.snapshot.last_error = code(result.code)
        local now = deps.now()
        while crashes[1] and now - crashes[1] >= 600 do table.remove(crashes, 1) end
        if #crashes >= 3 then publish("blocked", "crash_limit"); break end
        crashes[#crashes + 1] = now
        self.snapshot.restarts = self.snapshot.restarts + 1
        publish("restarting", result.code)
        owner.context.sleep(math.min(2 ^ #crashes, 30) * (1 + (deps.random or math.random)() * 0.2))
      end
    end)
    if not ok then
      if cancel.is_stop(err) then
        owner.desired = false
        owner.stop_reason = owner.stop_reason or "interrupted"
      else publish("blocked", "supervisor_failed") end
    end
    if owner.listener then
      local removed, result = pcall(deps.ignore, "interrupted", owner.listener)
      if not removed or result == false then owner.unsafe = true; publish("blocked", "cleanup_failed") end
    end
    if not owner.desired and not owner.unsafe then publish("stopped", owner.stop_reason or "manual_stop") end
    owner.finalized = true
  end
  function self:status()
    reap()
    return copy(self.snapshot)
  end
  function self:start(path, background)
    reap()
    if self.owner then return false, self.owner.unsafe and "cleanup_failed" or
      (not self.owner.desired and "stopping" or "already_running") end
    path = path or "/etc/ae-orbit.cfg"
    local owner = {desired = true, path = path, mode = background and "background" or "foreground"}
    owner.context = cancel.new({now = deps.now, sleep = deps.sleep})
    self.owner, self.last_path = owner, path
    self.snapshot = {state = "stopped", reason = "not_started", restarts = 0, mode = owner.mode}
    publish("starting", "starting")
    if background then
      -- OpenOS thread.create runs the body immediately until its first yield.
      local ok, handle = pcall(deps.spawn, function() supervise(owner) end)
      if not ok or not handle then
        owner.unsafe = true
        publish("blocked", "service_start_failed")
        return false, "service_start_failed"
      end
      owner.thread = handle
    else
      owner.listener = function()
        owner.desired, owner.stop_reason = false, "interrupted"
        owner.context.request_stop("interrupted")
      end
      local ok, result = pcall(deps.listen, "interrupted", owner.listener)
      if not ok or result == false then
        owner.unsafe = true
        publish("blocked", "service_start_failed")
        return false, "service_start_failed"
      end
      supervise(owner)
      if not owner.unsafe then self.owner = nil end
    end
    return true, self:status()
  end
  function self:stop(timeout)
    reap()
    local owner = self.owner
    if not owner then return true, self:status() end
    if owner.unsafe then return false, "cleanup_failed" end
    owner.desired, owner.stop_reason = false, "manual_stop"
    owner.context.request_stop("manual_stop")
    publish("stopping", "manual_stop")
    local deadline = deps.now() + (timeout or 5)
    repeat
      reap()
      if self.owner ~= owner then return true, self:status() end
      if deps.now() >= deadline then break end
      local ok = pcall(deps.sleep, math.min(0.1, deadline - deps.now()))
      if not ok then break end
    until false
    return false, "stopping"
  end
  function self:restart(path, timeout)
    path = path or self.last_path
    local stopped, reason = self:stop(timeout)
    if not stopped then return false, reason end
    return self:start(path, true)
  end
  return self
end

function M.describe(snapshot)
  local result = "AE Orbit: " .. snapshot.state .. "; reason=" .. snapshot.reason ..
    "; restarts=" .. snapshot.restarts
  if snapshot.last_error then result = result .. "; last_error=" .. snapshot.last_error end
  if snapshot.status_warning then result = result .. "; " .. snapshot.status_warning end
  if snapshot.previous_reason then result = result .. "; previous_reason=" .. snapshot.previous_reason end
  return result
end

function M.get()
  if default then return default end
  local computer, event = require("computer"), require("event")
  local filesystem, json = require("filesystem"), require("aeorbit.json")
  local status_path = "/var/lib/ae-orbit/service-status.json"
  default = M.new({
    now = computer.uptime, sleep = os.sleep, listen = event.listen, ignore = event.ignore,
    run = function(...) return require("aeorbit.runtime").run(...) end,
    spawn = function(fn)
      local thread = require("thread")
      local t = thread.create(function()
        local data = require("process").info().data
        local inherited = data.signal
        data.signal = function(name, ...)
          if name ~= "interrupted" and inherited then return inherited(name, ...) end
        end
        fn()
      end)
      assert(t:detach())
      return t
    end,
    dead = function(t) return t.pco and coroutine.status(t.pco.root) == "dead" end,
    persist = function(snapshot)
      if not filesystem.exists("/var/lib/ae-orbit") then assert(filesystem.makeDirectory("/var/lib/ae-orbit")) end
      local file = assert(io.open(status_path, "wb"))
      local ok, err = pcall(function()
        assert(file:write(json.encode(snapshot, 1024)))
        assert(file:flush())
      end)
      local closed, result, why = pcall(file.close, file)
      assert(ok and closed and result ~= false and why == nil, "status_write_failed")
      return true
    end,
  })
  local file = io.open(status_path, "rb")
  if file then
    local ok, old = pcall(function() return json.decode(file:read(1025), 1024) end)
    pcall(file.close, file)
    if ok and type(old) == "table" then
      default.snapshot.previous_reason = code(old.reason)
      default.snapshot.last_error = old.last_error and code(old.last_error) or nil
    end
  end
  return default
end
return M
