-- Hardware callbacks in OC are callable tables, including methods on handles.
local M = {}
function M.callback(callback)
  return setmetatable({}, {__call=function(_, ...) return callback(...) end})
end
function M.new(methods)
  local proxy={}
  for name,callback in pairs(methods) do proxy[name]=M.callback(callback) end
  return proxy
end
return M
