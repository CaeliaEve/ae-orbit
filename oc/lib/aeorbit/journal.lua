local json = require("aeorbit.json")
local quantity = require("aeorbit.quantity")
local cancel = require("aeorbit.cancel")
local M = {}
local function guarded(fn,...)
  local ok,result=pcall(fn,...)
  if ok then return result end
  cancel.rethrow(result)
  error({code="journal_failed",fatal=true,message="journal_failed"},0)
end
local function checksum(text)
  local a,b=1,0
  for index=1,#text do a=(a+text:byte(index))%65521;b=(b+a)%65521 end
  return b*65536+a
end
local function newer(a,b) return #a>#b or (#a==#b and a>b) end
local function new(fs,prefix,limit)
  local current,found=nil,false
  for slot=0,1 do
    local text=fs.read(prefix.."."..slot,limit+1)
    if text then
      found=true
      local ok,record=pcall(function()
        local record=json.decode(text,limit)
        quantity.validate(record.generation)
        assert(type(record.body)=="string" and checksum(record.body)==record.checksum,"bad checksum")
        record.data=json.decode(record.body,limit)
        return record
      end)
      if not ok then cancel.rethrow(record) end
      if ok and (not current or newer(record.generation,current.generation)) then current=record end
    end
  end
  assert(current or not found,"journal: no valid execution record")
  local journal={data=current and current.data or {attempts={},completed=json.array({})},generation=current and current.generation or "0"}
  function journal:save(data)
    return guarded(function()
      local generation=quantity.increment(self.generation)
      local body=json.encode(data,limit)
      local envelope=json.encode({generation=generation,body=body,checksum=checksum(body)},limit)
      local slot=tonumber(generation:sub(-1))%2
      assert(fs.write(prefix.."."..slot,envelope),"journal: write failed")
      self.data=data;self.generation=generation
    end)
  end
  return journal
end
function M.new(...) return guarded(new,...) end
return M
