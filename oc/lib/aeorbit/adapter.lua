local quantity = require("aeorbit.quantity")
local base64 = require("aeorbit.base64")
local json = require("aeorbit.json")
local nbt = require("aeorbit.nbt")
local cache = require("aeorbit.cache")
local cancel = require("aeorbit.cancel")
local M = {}
function M.has_method(proxy,name)
  local value=proxy[name]
  if type(value)=="function" then return true end
  -- OC component and userdata methods are tables with an __call metamethod.
  local meta=type(value)=="table" and getmetatable(value)
  return type(meta)=="table" and type(meta.__call)=="function"
end
function M.new(me,config)
  config=config or {}
  assert(config.inventory_mode==nil or config.inventory_mode=="iterator" or config.inventory_mode=="exact_only",
    "adapter: invalid inventory mode")
  local exact_only=config.inventory_mode=="exact_only"
  local worker=config.role=="quantity_worker"
  if not worker and not exact_only then
    assert(M.has_method(me,"allItems"),"adapter: missing allItems; select an AE controller or full ME interface")
  end
  assert(M.has_method(me,"getItemInNetwork"),"adapter: missing getItemInNetwork; requires OC 1.12.47-GTNH+ (table/NBT API)")
  local cpu_limit=config.cpu_limit or 64
  assert(type(cpu_limit)=="number" and cpu_limit%1==0 and cpu_limit>=1 and cpu_limit<=64,"adapter: CPU limit must be 1..64")
  local adapter={me=me,identity_bytes=config.identity_bytes or 4096,cpu_limit=cpu_limit,cpu_generation="0",
    nbt_cache=cache.new(64,32768)}
  local function cpu_name(raw)
    return tostring(raw and raw.name or ""):sub(1,256)
  end
  local function cpu_snapshot()
    if not M.has_method(me,"getCpus") then return nil,"CPU selection is unavailable" end
    local ok,values=pcall(me.getCpus)
    if not ok then cancel.rethrow(values) end
    if type(values)~="table" then return nil,"CPU snapshot unavailable" end
    return values
  end
  local function wait_for_cpu(seconds)
    local wait=config.wait
    if type(wait)~="function" then wait=config.sleep end
    if type(wait)~="function" then return false,"CPU is busy and waiting is unavailable" end
    local ok,reason=pcall(wait,seconds)
    if not ok then cancel.rethrow(reason) end
    if not ok then return false,tostring(reason or "CPU wait failed") end
    return true
  end
  function adapter:capabilities()
    if worker then
      return {inventory=true,craft=false,cpu_output=false,cpu_selection=false,exact_quantities=true,quantity_refresh=true}
    end
    return {inventory=true,craft=M.has_method(me,"getCraftable"),cpu_output=M.has_method(me,"getCpus"),
      cpu_selection=M.has_method(me,"getCraftable") and M.has_method(me,"getCpus"),
      exact_quantities=true,quantity_refresh=true,max_craft_quantity=quantity.maximum(),
      enumeration=exact_only and "exact_only" or "volatile_iterator"}
  end
  function adapter:key(raw)
    assert(type(raw)=="table" and type(raw.name)=="string" and #raw.name>0 and #raw.name<=256,"adapter: invalid registry name")
    local damage=raw.damage or 0
    assert(type(damage)=="number" and damage>=0 and damage%1==0 and damage<=2147483647,"adapter: invalid metadata")
    -- OC serializes event booleans through NBT, returning numeric 0/1 in Lua.
    local flag=raw.hasTag
    assert(flag==nil or type(flag)=="boolean" or flag==0 or flag==1,"adapter: invalid NBT flag")
    assert((flag~=true and flag~=1) or (type(raw.tag)=="string" and #raw.tag>0),"adapter: NBT identity unavailable")
    assert(raw.tag==nil or type(raw.tag)=="string","adapter: unsupported NBT representation")
    local tag=raw.tag or ""
    assert(#tag<=self.identity_bytes,"adapter: NBT identity exceeds budget")
    local identity=""
    if #tag>0 then
      identity=self.nbt_cache:get(tag)
      if not identity then
        identity=nbt.digest(tag)
        self.nbt_cache:put(tag,identity,#tag+#identity+160)
      end
    end
    return raw.name.."\0"..quantity.from_number(damage).."\0"..identity
  end
  function adapter:definition(raw)
    self:key(raw)
    local label=type(raw.label)=="string" and raw.label or raw.name
    assert(#label<=512,"adapter: label exceeds budget")
    return {kind="item",name=raw.name,damage=raw.damage or 0,label=label,exact=true,nbt=raw.tag and base64.encode(raw.tag) or nil}
  end
  function adapter:descriptor(raw)
    self:key(raw)
    return {name=raw.name,damage=raw.damage or 0,tag=raw.tag,hasTag=raw.hasTag==true or raw.hasTag==1,label=raw.label}
  end
  function adapter:from_definition(definition)
    assert(definition.kind=="item" and definition.exact==true,"adapter: unsupported or ambiguous resource")
    local tag=definition.nbt and definition.nbt~="" and base64.decode(definition.nbt) or nil
    local raw={name=definition.name,damage=definition.damage or 0,label=definition.label,tag=tag,hasTag=tag~=nil}
    self:key(raw);return raw
  end
  function adapter:read(raw)
    local detail=self:descriptor(raw)
    local value,reason=me.getItemInNetwork(detail)
    assert(reason==nil,"adapter: exact lookup failed")
    if value~=nil then
      assert(self:key(value)==self:key(detail),"adapter: exact lookup returned another item")
      detail.size=value.size;detail.label=value.label or detail.label
      detail.tag=value.tag;detail.hasTag=value.hasTag==true or value.hasTag==1 or (value.tag~=nil and #value.tag>0)
    else detail.size=0 end
    -- AE's exact stock lookup does not include its crafting inventory. Even a
    -- returned false (or no stored stack) says nothing about available recipes.
    return detail
  end
  function adapter:iterator()
    assert(not exact_only,"adapter: native enumeration is disabled in exact-only mode")
    local iterator=me.allItems()
    assert(iterator~=nil,"adapter: inventory iterator unavailable")
    return function() return iterator() end
  end
  function adapter:count(raw)
    if type(raw.size)=="string" then return quantity.validate(raw.size) end
    return quantity.from_number(raw.size)
  end
  function adapter:_recipe(detail)
    local available,craftable=pcall(me.getCraftable,detail,"item")
    if not available then cancel.rethrow(craftable) end
    if not available then return nil,"recipe lookup failed" end
    if craftable==nil then
      -- An empty exact lookup does not establish that a recipe is absent.
      -- Both filtered fallbacks can rebuild the full native storage list.
      if exact_only then return nil,"recipe could not be confirmed in exact-only mode" end
      if not M.has_method(me,"getCraftables") then return nil,"recipe lookup unavailable" end
      -- Filter inside OC before results enter Lua; never request the full
      -- recipe array. The native method may still walk the AE storage list.
      local filter={name=detail.name,damage=detail.damage,hasTag=detail.hasTag,tag=detail.tag}
      local ok,recipes=pcall(me.getCraftables,filter)
      if not ok then cancel.rethrow(recipes) end
      if not ok or type(recipes)~="table" then return nil,"recipe lookup failed" end
      if #recipes==0 then
        -- This fallback compares compressed bytes inside OC. A tagged miss
        -- is inconclusive; do not turn a representation change into a false
        -- recipe observation or issue a broader, unbounded native query.
        if detail.tag and #detail.tag>0 then return nil,"recipe NBT encoding could not be verified" end
        return nil
      end
      if #recipes~=1 then return nil,"recipe lookup is ambiguous" end
      craftable=recipes[1]
    end
    local matches,stack=pcall(function() return craftable.getStack() end)
    if not matches then cancel.rethrow(stack) end
    if not matches then return nil,"recipe identity could not be verified" end
    local identity_ok,key=pcall(self.key,self,stack)
    if not identity_ok then cancel.rethrow(key) end
    if not identity_ok or key~=self:key(detail) then return nil,"recipe output identity differs" end
    return craftable
  end
  function adapter:recipe_status(raw)
    local detail=self:descriptor(raw)
    local recipe,reason=self:_recipe(detail)
    if reason=="recipe NBT encoding could not be verified" and M.has_method(me,"getItemsInNetwork") then
      -- The storage list retains recipe flags independently of stock lookup.
      -- Filter natively by the complete identity; an empty result proves nothing.
      local checked,flag=pcall(function()
        local rows=me.getItemsInNetwork({name=detail.name,damage=detail.damage,hasTag=detail.hasTag,tag=detail.tag})
        if type(rows)~="table" or type(rows[1])~="table" then return nil end
        for index in pairs(rows) do if index~=1 then return nil end end
        local row=rows[1]
        if type(row.isCraftable)=="boolean" and self:key(row)==self:key(detail) then return row.isCraftable end
      end)
      if not checked then cancel.rethrow(flag) end
      if checked and type(flag)=="boolean" then return flag end
    end
    assert(reason==nil,reason)
    return recipe~=nil
  end
  function adapter:craft(command)
    if command.cpu_name~=nil and type(command.cpu_name)~="string" then
      return nil,"invalid CPU target",false
    end
    if command.wait_for_cpu~=nil and type(command.wait_for_cpu)~="boolean" then
      return nil,"invalid CPU wait flag",false
    end
    local target=command.cpu_name
    local waiting=command.wait_for_cpu==true
    if target=="" then target=nil end
    if waiting and not target then
      return nil,"wait_for_cpu requires cpu_name",false
    end
    if target then
      local deadline
      if config.cpu_wait_timeout~=nil then
        assert(type(config.cpu_wait_timeout)=="number" and config.cpu_wait_timeout>=0,
          "adapter: invalid CPU wait timeout")
        local now=config.now
        deadline=type(now)=="function" and now()+config.cpu_wait_timeout or nil
      end
      while true do
        local values,reason=cpu_snapshot()
        if not values then return nil,reason,false end
        local matches,busy=0,false
        for _,raw in ipairs(values) do
          if cpu_name(raw)==target then
            matches=matches+1
            busy=busy or raw.busy==true
          end
        end
        if matches==0 then return nil,"requested CPU not found",false end
        if matches>1 then return nil,"requested CPU is ambiguous",false end
        if not busy then break end
        if not waiting then return nil,"requested CPU is busy",false end
        if deadline and type(config.now)=="function" and config.now()>=deadline then
          return nil,"timed out waiting for requested CPU",false
        end
        local waited,wait_reason=wait_for_cpu(config.cpu_wait_interval or 0.1)
        if not waited then return nil,wait_reason,false end
      end
    end
    local ok,detail=pcall(self.from_definition,self,command.item)
    if not ok then cancel.rethrow(detail) end
    if not ok then return nil,"item identity unavailable",false end
    local parsed,amount=pcall(quantity.to_number,command.quantity)
    if not parsed then cancel.rethrow(amount) end
    if not parsed or amount<1 then return nil,"quantity is not exactly representable",false end
    local available,craftable,reason=pcall(self._recipe,self,detail)
    if not available then cancel.rethrow(craftable) end
    if not available then return nil,"recipe lookup failed",false end
    if not craftable then return nil,reason or "recipe is unavailable",false end
    local submitted,handle=pcall(function()
      if target then return craftable.request(amount,true,target) end
      return craftable.request(amount)
    end)
    if not submitted then cancel.rethrow(handle) end
    if not submitted or not handle then return nil,"request returned no reliable tracking handle",true end
    return handle,nil,true
  end
  function adapter:cpus()
    if not M.has_method(me,"getCpus") then return json.array({}),false end
    local values=me.getCpus()
    assert(type(values)=="table","adapter: CPU snapshot unavailable")
    self.cpu_generation=quantity.increment(self.cpu_generation)
    local result=json.array({})
    for index=1,math.min(#values,self.cpu_limit) do
      local raw=values[index]
      local cpu={id=self.cpu_generation..":"..index,name=tostring(raw.name or ""):sub(1,256),busy=raw.busy==true,
        storage=quantity.from_number(raw.storage or 0),coprocessors=raw.coprocessors or 0}
      if cpu.busy and raw.cpu then
        local ok,output,reason=pcall(function() return raw.cpu.finalOutput() end)
        if not ok then cancel.rethrow(output) end
        if ok and type(output)=="table" then
          local precise,count=pcall(self.count,self,output)
          if not precise then cancel.rethrow(count) end
          if precise then cpu.output={name=tostring(output.name or ""):sub(1,256),label=tostring(output.label or ""):sub(1,512),quantity=count}
          else cpu.output_unavailable="quantity precision unavailable" end
        else cpu.output_unavailable=ok and tostring(reason or "output unavailable"):sub(1,256) or "monitor unavailable" end
      elseif cpu.busy then cpu.output_unavailable="monitor unavailable" end
      result[#result+1]=cpu
    end
    return result,#values>self.cpu_limit
  end
  return adapter
end
return M
