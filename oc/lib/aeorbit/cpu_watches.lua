local cache=require('aeorbit.cache')
local cancel=require('aeorbit.cancel')
local signature=require('aeorbit.cpu_details').signature
local json=require('aeorbit.json')
local M={}
function M.new()
 local targets=cache.new(16,8192)
 local function call(fn)
  local ok,value=pcall(fn)
  if not ok then cancel.rethrow(value) end
  return ok,value
 end
 function targets:read(assignments,snapshots,now,check)
  local entry=self.last
  while entry do local previous=entry.previous;if entry.value.expires<=now then self:remove(entry.key)end;entry=previous end
  local results=json.array({})
  for _,a in ipairs(assignments) do
   if check then check()end
   local target=self:get(a.target_id)
   if not target then
    local source=snapshots:get(a.cpu_id)
    target={cpu=source and source.cpu,output=source and source.output_identity,id=a.cpu_id,status=source and 'busy' or 'missing'}
    -- Never evict an active watch to silently reacquire an enumeration slot.
    if self.count>=16 then target.status='missing' else self:put(a.target_id,target,512)end
   end
   target.expires=now+a.ttl_millis/1000
   if target.id~=a.cpu_id then target.status='missing' end
   if target.status=='busy' then
    local cpu=target.cpu
    if not cpu then target.status='missing'
    else
     local active,value=call(function()return cpu.isActive()end)
     if not active then target.status='missing'
     elseif value~=true then target.status='inactive'
     else
      local valid,busy=call(function()return cpu.isBusy()end)
      if not valid then target.status='missing'
      elseif busy~=true then target.status='idle'
      elseif target.output then
       local valid,output=call(function()
        local value=cpu.finalOutput()
        return type(value)=='table' and signature(value)==signature(target.output)
       end)
       if not valid then target.status='missing'
       elseif output~=true then target.status='changed'end
      end
     end
    end
   end
   results[#results+1]={target_id=a.target_id,status=target.status}
  end
  return results
 end
 return targets
end
return M
