local M = {}
local function stop(reason)
  return {code="interrupted",stop=true,fatal=true,reason=reason}
end
function M.is_stop(err)
  return err=="interrupted" or (type(err)=="table" and err.stop==true)
end
function M.rethrow(err)
  if err=="interrupted" then error(stop("interrupted"),0) end
  if M.is_stop(err) then error(err,0) end
end
function M.new(clock)
  local requested
  local ctx={}
  function ctx.request_stop(reason)
    if not requested then requested=stop(reason or "requested") end
  end
  function ctx.check()
    if requested then error(requested,0) end
  end
  function ctx.sleep(seconds)
    ctx.check()
    local deadline=clock.now()+seconds
    repeat
      local ok,err=pcall(clock.sleep,math.max(0,math.min(0.1,deadline-clock.now())))
      if not ok then M.rethrow(err);error(err,0) end
      ctx.check()
    until clock.now()>=deadline
  end
  return ctx
end
return M
