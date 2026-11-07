local json=require("aeorbit.json")
local quantity=require("aeorbit.quantity")
local M={}
local maximum=9007199254740991

-- Sanitize only a bounded prefix, retaining complete UTF-8 code points.
-- Lua 5.2 has no standard utf8 module, and an error object may have a hostile
-- __tostring method. Never stringify or keep the original exception object.
local function text(value,limit)
  local chunks,used,position,changed={},0,1,false
  while position<=#value and used<limit do
    local first=value:byte(position)
    local width,code,minimum=1,first,0
    if first>=0xc2 and first<=0xdf then width=2;code=first-0xc0;minimum=0x80
    elseif first>=0xe0 and first<=0xef then width=3;code=first-0xe0;minimum=0x800
    elseif first>=0xf0 and first<=0xf4 then width=4;code=first-0xf0;minimum=0x10000
    elseif first>=128 then width=0 end
    if width>1 then
      for offset=1,width-1 do
        local byte=value:byte(position+offset)
        if not byte or byte<0x80 or byte>0xbf then width=0;break end
        code=code*64+byte-0x80
      end
      if code<minimum or code>0x10ffff or (code>=0xd800 and code<=0xdfff) then width=0 end
    end
    local part
    if width==0 then width=1;part="?";changed=true
    elseif code<32 or (code>=127 and code<=159) then part=" ";changed=true
    else part=value:sub(position,position+width-1) end
    if used+#part>limit then changed=true;break end
    chunks[#chunks+1]=part;used=used+#part;position=position+width
  end
  if position<=#value then changed=true end
  return table.concat(chunks),changed
end

local function adler32(tag,length)
  local a,b=1,0
  for index=1,length do a=(a+tag:byte(index))%65521;b=(b+a)%65521 end
  return string.format("%08x",b*65536+a)
end

local function same(a,b)
  return a.code==b.code and a.source==b.source and a.item_id==b.item_id and
    a.name==b.name and a.damage==b.damage and a.nbt_bytes==b.nbt_bytes and
    a.nbt_adler32==b.nbt_adler32 and a.message==b.message
end

function M.new(now)
  local entries,bytes={},2
  local started,next_send=now(),0
  local generation,acknowledged
  local self={}
  local function remove(index)
    local entry=table.remove(entries,index)
    bytes=bytes-entry.bytes-(#entries>0 and 1 or 0)
  end
  function self:record(code,source,raw,item_id,error_value)
    local name,name_changed=text(type(raw.name)=="string" and raw.name or "(unknown)",256)
    local message,message_changed
    if type(error_value)=="string" then message,message_changed=text(error_value,160)
    else message="non-string error ("..type(error_value)..")";message_changed=true end
    if #message==0 then message="(empty error)";message_changed=true end
    if #name==0 then name="(unknown)";name_changed=true end
    local tag=type(raw.tag)=="string" and raw.tag or ""
    local length=math.min(#tag,16384)
    local sample={code=code,source=source,item_id=item_id,name=name,damage=raw.damage or 0,
      nbt_bytes=length,nbt_adler32=length>0 and adler32(tag,length) or nil,message=message,
      occurrences="1",last_seen_millis=quantity.from_number(math.min(maximum,math.floor(math.max(0,now()-started)*1000))),
      truncated=(name_changed or message_changed or length<#tag) or nil}
    for index,entry in ipairs(entries) do
      if same(entry.value,sample) then
        sample.occurrences=quantity.from_number(math.min(maximum,tonumber(entry.value.occurrences)+1))
        remove(index);break
      end
    end
    -- Only sanitized fields survive this call. Neither raw, error_value nor a
    -- full identity key is retained; size includes JSON escaping and commas.
    local size=#json.encode(sample,2048)
    bytes=bytes+size+(#entries>0 and 1 or 0)
    table.insert(entries,1,{value=sample,bytes=size})
    while #entries>4 or bytes>2048 do remove(#entries) end
    generation={}
  end
  function self:snapshot(limit)
    if type(limit)~="number" or limit<1 or limit>4 or limit%1~=0 or
      not generation or generation==acknowledged or now()<next_send then return nil end
    local result=json.array({})
    for index=1,math.min(limit,#entries) do result[index]=entries[index].value end
    return result,generation
  end
  function self:ack(token)
    acknowledged=token;next_send=now()+30
  end
  return self
end
return M
