local available, service = pcall(require, "aeorbit.service")
assert(available, "the shared lifecycle service is missing")

local function fixture(run)
  local now, calls, listeners, handles, snapshots = 0, 0, {}, {}, {}
  local s
  local dependencies = {
    now = function() return now end,
    sleep = function(seconds) now = now + math.max(seconds, 0.01) end,
    random = function() return 0 end,
    run = function(context, path, report, mode)
      calls = calls + 1
      return run(s, context, path, report, mode, calls)
    end,
    listen = function(name, fn) listeners[name] = fn; return true end,
    ignore = function(name, fn) assert(listeners[name] == fn); listeners[name] = nil; return true end,
    spawn = function(fn)
      local handle = {run = fn, dead = false}
      handles[#handles + 1] = handle
      return handle
    end,
    dead = function(handle) return handle.dead end,
    persist = function(snapshot) snapshots[#snapshots + 1] = snapshot; return true end,
  }
  s = service.new(dependencies)
  return s, handles, listeners, snapshots, function() return calls, now end
end

-- A stop request must win over an error returned by the exiting bridge.
do
  local s, _, listeners, _, counters
  s, _, listeners, _, counters = fixture(function(owner, context)
    assert(listeners.interrupted)
    listeners.interrupted("interrupted")
    local ok, err = pcall(context.check)
    assert(not ok and err.stop)
    return {kind = "crashed", code = "runtime_error"}
  end)
  assert(s:start("/etc/custom.cfg", false))
  assert(s:status().state == "stopped" and s:status().reason == "interrupted")
  assert(counters() == 1 and listeners.interrupted == nil)
end

-- Crash budget is bounded; no fifth execution and no raw errors in snapshots.
do
  local s, _, _, snapshots, counters = fixture(function()
    return {kind = "crashed", code = "runtime_error"}
  end)
  s:start(nil, false)
  assert(counters() == 4, "watchdog restarted beyond its rolling crash budget")
  local status = s:status()
  assert(status.state == "blocked" and status.reason == "crash_limit" and status.restarts == 3)
  assert(#snapshots > 0)
end

-- Successful status must not invent an error; recovery retains the last fault.
do
  local s = fixture(function(owner, _, _, report)
    report("backoff", "timeout")
    report("running", nil, "session-1")
    local status = owner:status()
    assert(status.reason == "connected" and status.last_error == "timeout")
    return {kind = "stopped", code = "interrupted"}
  end)
  s:start(nil, false)
  assert(s:status().state == "stopped", "status reporting was treated as a crash")
end

-- Stopping an already finalized worker still waits for native teardown and ends
-- at stopped, not a permanent stopping status after its owner has disappeared.
do
  local s, handles = fixture(function() return {kind = "blocked", code = "unauthorized"} end)
  s:start(nil, true)
  handles[1].run()
  assert(not s:stop(0))
  handles[1].dead = true
  assert(s:status().state == "stopped")
end

-- Never put unexpected exception/response content into human-readable status.
do
  local s, _, _, snapshots = fixture(function()
    return {kind = "blocked", code = "private-token-1234567890"}
  end)
  s:start(nil, false)
  for _, snapshot in ipairs(snapshots) do
    assert(not service.describe(snapshot):find("private-token", 1, true))
  end
end

for _, code in ipairs({"stale_session", "journal_failed", "unauthorized", "protocol_error"}) do
  local s, _, _, _, counters = fixture(function() return {kind = "blocked", code = code} end)
  s:start(nil, false)
  assert(counters() == 1 and s:status().reason == code, "fatal exit was retried")
end

-- Returning from a worker body is not sufficient: native teardown must finish.
do
  local s, handles, listeners = fixture(function() return {kind = "stopped", code = "manual_stop"} end)
  assert(s:start(nil, true))
  assert(not s:start(nil, true), "a duplicate owner was admitted")
  assert(listeners.interrupted == nil, "background service captured shell Ctrl+C")
  handles[1].run()
  assert(not s:start(nil, true), "worker admitted a replacement before teardown")
  handles[1].dead = true
  assert(s:start(nil, true), "finalized dead owner could not be reaped")
  assert(#handles == 2)
end

-- Stop during startup reserves the owner until the old worker acknowledges it.
do
  local s, handles, _, _, counters = fixture(function() error("stopped worker must not execute") end)
  assert(s:start(nil, true))
  local ok, reason = s:stop(0)
  assert(not ok and reason == "stopping")
  assert(not s:restart(nil, 0), "restart overlapped an unacknowledged stop")
  handles[1].run()
  handles[1].dead = true
  assert(s:stop(0))
  assert(counters() == 0 and #handles == 1)
end

-- Cleanup failure keeps ownership even after the thread dies.
do
  local s, handles = fixture(function() return {kind = "blocked", code = "cleanup_failed"} end)
  assert(s:start(nil, true))
  handles[1].run(); handles[1].dead = true
  assert(not s:start(nil, true), "unclean resources were silently replaced")
  assert(s:status().reason == "cleanup_failed")
end

-- thread.create may execute the entire worker synchronously before returning.
do
  local s = service.new({
    now = function() return 0 end, sleep = function() end,
    run = function() return {kind = "blocked", code = "unauthorized"} end,
    spawn = function(fn) fn(); return {} end, dead = function() return true end,
    persist = function() return true end,
  })
  assert(s:start(nil, true))
  assert(s:status().state == "blocked")
  assert(s:start(nil, true))
end
