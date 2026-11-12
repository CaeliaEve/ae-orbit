local service = require("aeorbit.service")
local function checked(ok, result)
  -- rc treats any nonthrowing callback as success, ignoring returned errors.
  if not ok then error("AE Orbit: " .. result, 0) end
  if result.state == "blocked" then error("AE Orbit: " .. result.reason, 0) end
  return result
end
function start(path)
  checked(service.get():start(path or (type(args) == "table" and args[1] or nil), true))
end
function stop()
  checked(service.get():stop())
end
function restart(path)
  checked(service.get():restart(path or (type(args) == "table" and args[1] or nil)))
end
function status()
  print(service.describe(service.get():status()))
end
