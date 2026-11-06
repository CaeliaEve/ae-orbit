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
    assert(not raw.hasTag or (type(raw.tag)=="string" and #raw.tag>0),"adapter: NBT identity unavailable")
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
    return {name=raw.name,damage=raw.damage or 0,tag=raw.tag,hasTag=raw.hasTag,label=raw.label}
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
    if value==nil then detail.size=0;detail.isCraftable=false;return detail end
    assert(self:key(value)==self:key(detail),"adapter: exact lookup returned another item")
    return value
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
  function adapter:craft(command)
    local ok,detail=pcall(self.from_definition,self,command.item)
    if not ok then return nil,"item identity unavailable",false end
    local parsed,amount=pcall(quantity.to_number,command.quantity)
    if not parsed or amount<1 then return nil,"quantity is not exactly representable",false end
    local available,craftable=pcall(me.getCraftable,detail,"item")
    if not available or not craftable then return nil,"recipe is unavailable",false end
    local matches,stack=pcall(craftable.getStack)
    if not matches then return nil,"recipe identity could not be verified",false end
    local identity_ok,key=pcall(self.key,self,stack)
    if not identity_ok or key~=self:key(detail) then return nil,"recipe output identity differs",false end
    local submitted,handle=pcall(craftable.request,amount)
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
