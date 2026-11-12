local path = TEST_ROOT .. "/oc/rc.d/ae-orbit.lua"
local loadable = loadfile(path)
assert(loadable, "OpenOS rc service is missing")
local calls, stopped = {}, false
local owner = {
  start = function(_, config, background)
    calls[#calls + 1] = {"start", config, background}
    return false, "already_running"
  end,
  stop = function() return false, "stopping" end,
  restart = function() return false, "stopping" end,
  status = function() return {state = "reconnecting", reason = "timeout", restarts = 2} end,
}
local module = require("aeorbit.service")
local old_get = module.get
module.get = function() return owner end
local output = {}
local env = setmetatable({args = {"/etc/worker.cfg"}, print = function(text) output[#output + 1] = text end}, {__index = _G})
assert(loadfile(path, "t", env))()
local ok, reason = pcall(env.start)
assert(not ok and tostring(reason):find("already_running", 1, true), "rc swallowed start failure")
assert(calls[1][2] == "/etc/worker.cfg" and calls[1][3] == true)
ok, reason = pcall(env.restart)
assert(not ok and tostring(reason):find("stopping", 1, true), "rc restart ignored old owner")
assert(#calls == 1, "rc restart bypassed the service owner")
owner.start = function() return true, {state = "blocked", reason = "config_error"} end
ok, reason = pcall(env.start)
assert(not ok and tostring(reason):find("config_error", 1, true), "rc hid an immediate startup failure")
env.status()
assert(output[1]:find("reconnecting", 1, true) and output[1]:find("timeout", 1, true))
module.get = old_get
