local quantity = require("aeorbit.quantity")
local base64 = require("aeorbit.base64")
local json = require("aeorbit.json")
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
  assert(M.has_method(me,"allItems"),"adapter: missing allItems; select an AE controller or full ME interface")
  assert(M.has_method(me,"getItemInNetwork"),"adapter: missing getItemInNetwork; requires OC 1.12.47-GTNH+ (table/NBT API)")
  local adapter={me=me,identity_bytes=config.identity_bytes or 4096,cpu_limit=config.cpu_limit or 16,cpu_generation="0"}
  function adapter:capabilities()
    return {inventory=true,craft=M.has_method(me,"getCraftable"),cpu_output=M.has_method(me,"getCpus"),
      exact_quantities=true,max_craft_quantity=quantity.maximum(),enumeration="volatile_iterator"}
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
    return raw.name.."\0"..quantity.from_number(damage).."\0"..tag
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
    local value=me.getItemInNetwork(detail)
    if value~=nil then
      assert(self:key(value)==self:key(detail),"adapter: exact lookup returned another item")
      detail.size=value.size;detail.label=value.label or detail.label
    else detail.size=0 end
    -- AE's exact stock lookup does not include its crafting inventory. Even a
    -- returned false (or no stored stack) says nothing about available recipes.
    return detail
  end
  function adapter:iterator()
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
    if not available then return nil,"recipe lookup failed" end
    if craftable==nil then
      if not M.has_method(me,"getCraftables") then return nil,"recipe lookup unavailable" end
      -- Filter inside OC before results enter Lua; never request the full
      -- recipe array. The native method may still walk the AE storage list.
      local filter={name=detail.name,damage=detail.damage,hasTag=detail.hasTag,tag=detail.tag}
      local ok,recipes=pcall(me.getCraftables,filter)
      if not ok or type(recipes)~="table" then return nil,"recipe lookup failed" end
      if #recipes==0 then return nil end
      if #recipes~=1 then return nil,"recipe lookup is ambiguous" end
      craftable=recipes[1]
    end
    local matches,stack=pcall(function() return craftable.getStack() end)
    if not matches then return nil,"recipe identity could not be verified" end
    local identity_ok,key=pcall(self.key,self,stack)
    if not identity_ok or key~=self:key(detail) then return nil,"recipe output identity differs" end
    return craftable
  end
  function adapter:recipe_status(raw)
    local recipe,reason=self:_recipe(self:descriptor(raw))
    assert(reason==nil,reason)
    return recipe~=nil
  end
  function adapter:craft(command)
    local ok,detail=pcall(self.from_definition,self,command.item)
    if not ok then return nil,"item identity unavailable",false end
    local parsed,amount=pcall(quantity.to_number,command.quantity)
    if not parsed or amount<1 then return nil,"quantity is not exactly representable",false end
    local available,craftable,reason=pcall(self._recipe,self,detail)
    if not available then return nil,"recipe lookup failed",false end
    if not craftable then return nil,reason or "recipe is unavailable",false end
    local submitted,handle=pcall(function() return craftable.request(amount) end)
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
        if ok and type(output)=="table" then
          local precise,count=pcall(self.count,self,output)
          if precise then cpu.output={name=output.name or "",label=output.label or "",quantity=count}
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
