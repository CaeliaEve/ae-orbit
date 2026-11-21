local json=require('aeorbit.json')
local cancel=require('aeorbit.cancel')
local sha=require('aeorbit.sha256')
local nbt=require('aeorbit.nbt')
local quantity=require('aeorbit.quantity')
local M={}
local methods={active='activeItems',pending='pendingItems',stored='storedItems'}
local function digest(value)local h=sha.new();h:update(value);return h:finish()end
local function signature(output)
 if not output then return '' end
 return digest(json.encode(json.array({output.name,output.damage or 0,output.kind or 'item',output.hasTag==true or output.hasTag==1,output.label or ''})))
end
M.signature=signature
function M.read(target,assignment,check)
 local result={assignment_id=assignment.assignment_id,cpu_id=assignment.cpu_id,kind=assignment.kind,total=0,truncated=false}
 local function failed(reason)result.items=nil;result.total=0;result.truncated=false;result.error=reason;return result end
 if not target then return failed('target_expired') end
 local cpu=target.cpu
 if not cpu then return failed('unsupported') end
 local ok,busy=pcall(function()return cpu.isBusy()end)
 if not ok then cancel.rethrow(busy) end
 if not ok or not busy then return failed('target_changed') end
 local before
 if target.output_signature then
  local success,value=pcall(function()return cpu.finalOutput()end)
  if not success then cancel.rethrow(value) end
  if not success or type(value)~='table' or signature(value)~=target.output_signature then return failed('target_changed') end
  before=signature(value)
 end
 if check then check() end
 local method=methods[assignment.kind]
 if not method or not cpu[method] then return failed('unsupported') end
 -- Native OC callbacks build the whole CPU list before Lua receives it.
 -- These limits bound encoding and retained memory, not that native work.
 local success,items=pcall(function()return cpu[method]()end)
 if not success then cancel.rethrow(items);return failed('read_failed') end
 if type(items)~='table' or #items>1000000 then return failed('invalid_items') end
 local length=#items
 for key in pairs(items) do
  if type(key)~='number' or key%1~=0 or key<1 or key>length then return failed('invalid_items') end
 end
 if check then check() end
 if before then
  local valid,after=pcall(function()return cpu.finalOutput()end)
  if not valid then cancel.rethrow(after) end
  if not valid or type(after)~='table' or signature(after)~=before then return failed('target_changed') end
 end
 local valid,afterBusy=pcall(function()return cpu.isBusy()end)
 if not valid then cancel.rethrow(afterBusy) end
 if not valid or not afterBusy then return failed('target_changed') end
 result.items=json.array({});result.total=#items
 for index=1,math.min(#items,64) do
  local item=items[index]
  if type(item)~='table' or type(item.name)~='string' or #item.name<1 or #item.name>256 then return failed('invalid_items') end
  local countOK,count=pcall(function()return type(item.size or item.amount)=='string' and quantity.validate(item.size or item.amount) or quantity.from_number(item.size or item.amount)end)
  if not countOK then cancel.rethrow(count);result.items=nil;result.total=0;return failed('invalid_items') end
  local kind=item.kind or (item.damage~=nil and 'item' or item.amount~=nil and 'fluid' or 'item')
  local damage=item.damage or 0
  if not methods[assignment.kind] or kind~='item' and kind~='fluid' and kind~='essentia' or type(damage)~='number' or damage%1~=0 or damage<0 or damage>2147483647 then result.items=nil;result.total=0;return failed('invalid_items') end
  local label=item.label or item.name
  if type(label)~='string' or #label>512 then result.items=nil;result.total=0;return failed('invalid_items') end
  local tagged=item.hasTag==true or item.hasTag==1 or item.tag~=nil
  local hash
  if tagged and type(item.tag)=='string' and #item.tag<=4096 then
   local parsed,value=pcall(nbt.digest,item.tag);if not parsed then cancel.rethrow(value) else hash=value end
  end
  local row={row_id=digest(json.encode(json.array({kind,item.name,damage,hash or '',label,tagged,index}))),kind=kind,name=item.name,damage=damage,label=label,has_tag=tagged,variant_hash=hash,quantity=count}
  result.items[#result.items+1]=row
  result.truncated=result.total>#result.items
  if not pcall(json.encode,result,10000) then result.items[#result.items]=nil;result.truncated=true;break end
 end
 result.truncated=result.total>#result.items
 return result
end
return M
