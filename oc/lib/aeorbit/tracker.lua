local json = require("aeorbit.json")
local M = {}
local function terminal(state) return state=="done" or state=="failed" or state=="canceled" or state=="needs_review" end
function M.new(adapter,journal,max_active)
  local data=journal.data
  assert(type(data.attempts)=="table" and type(data.completed)=="table","journal: invalid tracking record")
  local tracker={handles={},acked={},data=data,completed={}}
  for _,id in ipairs(data.completed) do tracker.completed[id]=true end
  local recovered=false
  for _,record in pairs(data.attempts) do
    if not terminal(record.state) then record.state="needs_review";record.reason="tracking handle lost during process restart";recovered=true end
  end
  if recovered then journal:save(data) end
  function tracker:accept(command)
    assert(type(command.order_id)=="string" and type(command.attempt_id)=="string","tracker: invalid command")
    local id=command.attempt_id
    if self.data.attempts[id] or self.completed[id] then return end
    local active=0;for _ in pairs(self.data.attempts) do active=active+1 end
    assert(active<max_active,"tracker: active window exceeded")
    local record={order_id=command.order_id,attempt_id=id,state="intent",reason=""}
    self.data.attempts[id]=record
    journal:save(self.data)
    local ok,handle,reason,submitted=pcall(adapter.craft,adapter,command)
    if not ok then record.state="needs_review";record.reason="craft call failed at an unknown point"
    elseif not handle then record.state=submitted and "needs_review" or "failed";record.reason=tostring(reason or "craft failed"):sub(1,160)
    else self.handles[id]=handle;record.state="computing" end
    journal:save(self.data)
  end
  function tracker:pump()
    local changed=false
    for id,handle in pairs(self.handles) do
      local record=self.data.attempts[id]
      local ok,state,reason=pcall(function()
        local failed,why=handle.hasFailed()
        if failed then return "failed",why end
        if handle.isComputing() then return "computing","" end
        if handle.isCanceled() then return "canceled","" end
        if handle.isDone() then return "done","" end
        return "running",""
      end)
      if not ok then state,reason="needs_review","tracking call failed; outcome unknown" end
      if state~=record.state then record.state=state;record.reason=tostring(reason or ""):sub(1,160);changed=true end
      if terminal(state) then self.handles[id]=nil end
    end
    if changed then journal:save(self.data) end
  end
  function tracker:reports()
    local result=json.array({})
    for id,record in pairs(self.data.attempts) do
      if self.acked[id]~=record.state then result[#result+1]={order_id=record.order_id,attempt_id=id,state=record.state,reason=record.reason} end
    end
    table.sort(result,function(a,b) if terminal(a.state)~=terminal(b.state) then return terminal(a.state) end;return a.attempt_id<b.attempt_id end)
    return result
  end
  function tracker:ack(reports)
    local changed=false
    for _,report in ipairs(reports) do
      local record=self.data.attempts[report.attempt_id]
      if record then
        self.acked[report.attempt_id]=report.state
        if terminal(report.state) and report.state==record.state then
          self.data.attempts[report.attempt_id]=nil;self.handles[report.attempt_id]=nil;self.acked[report.attempt_id]=nil
          self.data.completed[#self.data.completed+1]=report.attempt_id;self.completed[report.attempt_id]=true
          while #self.data.completed>64 do self.completed[table.remove(self.data.completed,1)]=nil end
          self.data.completed=json.array(self.data.completed);changed=true
        end
      end
    end
    if changed then journal:save(self.data) end
  end
  return tracker
end
return M
