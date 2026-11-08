local json=require("aeorbit.json")
local quantity=require("aeorbit.quantity")
local M={}
local maximum=9007199254740991
local bounds={10,50,100,250,1000,5000,30000,math.huge}
local phases={"event_queue","recheck_queue","priority_queue","collection","inventory_http","control_http","batch_ack","scan"}
local allowed={};for _,name in ipairs(phases) do allowed[name]=true end
local function millis(seconds)
  return math.min(maximum,math.max(0,math.floor(seconds*1000+0.5)))
end

-- Fixed phases and bins retain no individual observations or item identities.
function M.new(now,origin)
  local self={values={},next_snapshot=0}
  function self:observe(phase,seconds,failed)
    assert(allowed[phase],"latency: invalid phase")
    local value=self.values[phase]
    if not value then
      value={samples=0,errors=0,total_millis=0,max_millis=0,buckets={0,0,0,0,0,0,0,0}}
      self.values[phase]=value
    end
    if value.samples==maximum then return end
    local duration=millis(seconds)
    value.samples=value.samples+1
    if failed then value.errors=value.errors+1 end
    value.total_millis=math.min(maximum,value.total_millis+duration)
    value.max_millis=math.max(value.max_millis,duration)
    for index,bound in ipairs(bounds) do
      if duration<=bound then value.buckets[index]=value.buckets[index]+1;break end
    end
  end
  function self:complete_scan(started)
    self:observe("scan",now()-started)
    if self.first_scan_ack_millis==nil then self.first_scan_ack_millis=millis(now()-origin) end
  end
  function self:due() return now()>=self.next_snapshot end
  function self:defer() self.next_snapshot=now()+30 end
  function self:snapshot()
    local result={captured_millis=quantity.from_number(millis(now()-origin)),stages={}}
    if self.first_scan_ack_millis~=nil then result.first_scan_ack_millis=quantity.from_number(self.first_scan_ack_millis) end
    for _,phase in ipairs(phases) do
      local value=self.values[phase]
      if value then
        local copy={buckets=json.array({})}
        for _,field in ipairs({"samples","errors","total_millis","max_millis"}) do copy[field]=quantity.from_number(value[field]) end
        for index,count in ipairs(value.buckets) do copy.buckets[index]=quantity.from_number(count) end
        result.stages[phase]=copy
      end
    end
    return result
  end
  return self
end
return M
