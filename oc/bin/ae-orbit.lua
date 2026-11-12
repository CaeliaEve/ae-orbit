local service = require("aeorbit.service")
local owner = service.get()
local args = {...}
local command = args[1]
local ok, result
if command == "status" then
  print(service.describe(owner:status()))
  return
elseif command == "start" then ok, result = owner:start(args[2], true)
elseif command == "stop" then ok, result = owner:stop()
elseif command == "restart" then ok, result = owner:restart(args[2])
elseif command == "run" then ok, result = owner:start(args[2], false)
else ok, result = owner:start(command, false) end
if not ok then
  io.stderr:write("AE Orbit: " .. result .. "\n")
  return 1
end
print(service.describe(result))
return result.state == "blocked" and 1 or 0
