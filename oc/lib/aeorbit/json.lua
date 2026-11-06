local M = {}
local array_mt = {__json_array = true}
M.null = setmetatable({}, {__json_null = true})
function M.array(value) return setmetatable(value or {}, array_mt) end
local escapes = {['"']='\\"', ['\\']='\\\\', ['\n']='\\n', ['\r']='\\r', ['\t']='\\t', ['\b']='\\b', ['\f']='\\f'}
local function escape(value)
  if escapes[value] then return escapes[value] end
  local first,length=value:byte(),#value
  if length==1 and first<32 then return string.format("\\u%04x",first) end
  local code,minimum
  if length==2 and first>=0xc2 and first<=0xdf then code=first-0xc0;minimum=0x80
  elseif length==3 and first>=0xe0 and first<=0xef then code=first-0xe0;minimum=0x800
  elseif length==4 and first>=0xf0 and first<=0xf4 then code=first-0xf0;minimum=0x10000
  else error("json: invalid UTF-8") end
  for index=2,length do code=code*64+value:byte(index)-0x80 end
  assert(code>=minimum and code<=0x10ffff and (code<0xd800 or code>0xdfff),"json: invalid UTF-8")
  if code<0x10000 then return string.format("\\u%04x",code) end
  code=code-0x10000
  return string.format("\\u%04x\\u%04x",0xd800+math.floor(code/1024),0xdc00+code%1024)
end
local function quote(value,limit)
  local bytes=#value+2
  assert(bytes<=limit,"json: encoding budget exceeded")
  -- OC's HTTP writer uses the JVM default charset. Escape only special/Unicode
  -- characters; ASCII IDs and base64 data stay on string.gsub's fast path.
  return '"' .. value:gsub('[%z\1-\31\\"\128-\255][\128-\191]*',function(c)
    local encoded=escape(c)
    bytes=bytes+#encoded-#c
    assert(bytes<=limit,"json: encoding budget exceeded")
    return encoded
  end) .. '"'
end
function M.encode(value, limit)
  limit = limit or 131072
  local chunks, bytes, seen = {}, 0, {}
  local function add(chunk)
    bytes = bytes + #chunk
    assert(bytes <= limit, "json: encoding budget exceeded")
    chunks[#chunks+1] = chunk
  end
  local encode
  encode = function(v, depth)
    assert(depth <= 24, "json: nesting limit")
    if v == M.null or v == nil then add("null")
    elseif type(v) == "boolean" then add(v and "true" or "false")
    elseif type(v) == "string" then
      assert(#v <= limit, "json: string budget exceeded")
      add(quote(v,limit-bytes))
    elseif type(v) == "number" then
      assert(v == v and math.abs(v) ~= math.huge, "json: non-finite number")
      if math.type and math.type(v) == "integer" then add(string.format("%d", v))
      else
        assert(math.abs(v) <= 9007199254740991, "json: unsafe numeric value")
        add(string.format("%.17g", v))
      end
    elseif type(v) == "table" then
      assert(not seen[v], "json: cyclic table"); seen[v] = true
      local mt = getmetatable(v)
      if mt and mt.__json_array then
        add("[")
        for index = 1, #v do if index > 1 then add(",") end; encode(v[index], depth+1) end
        for key in pairs(v) do assert(type(key)=="number" and key%1==0 and key>=1 and key<=#v, "json: invalid array key") end
        add("]")
      else
        local keys = {}
        for key in pairs(v) do assert(type(key)=="string", "json: object keys must be strings"); keys[#keys+1]=key end
        table.sort(keys); add("{")
        for index,key in ipairs(keys) do if index>1 then add(",") end; add(quote(key,limit-bytes)); add(":"); encode(v[key], depth+1) end
        add("}")
      end
      seen[v] = nil
    else error("json: unsupported value type") end
  end
  encode(value, 0)
  return table.concat(chunks)
end

local function utf8(code)
  if code<128 then return string.char(code) end
  if code<2048 then return string.char(192+math.floor(code/64),128+code%64) end
  if code<65536 then return string.char(224+math.floor(code/4096),128+math.floor(code/64)%64,128+code%64) end
  return string.char(240+math.floor(code/262144),128+math.floor(code/4096)%64,128+math.floor(code/64)%64,128+code%64)
end
function M.decode(text, limit)
  assert(type(text)=="string" and #text<=(limit or 131072), "json: input budget exceeded")
  local position,nodes = 1,0
  local max_nodes=math.max(64,math.floor((limit or 131072)/16))
  local function skip() local _,last=text:find("^[ \t\r\n]*",position); position=(last or position-1)+1 end
  local function unicode()
    local value=text:sub(position,position+3)
    assert(#value==4 and value:match("^%x%x%x%x$"), "json: invalid unicode escape")
    position=position+4
    return tonumber(value,16)
  end
  local function parse_string()
    assert(text:sub(position,position)=='"', "json: string expected");position=position+1
    local chunks,start={},position
    while position<=#text do
      local c=text:sub(position,position)
      if c=='"' then chunks[#chunks+1]=text:sub(start,position-1);position=position+1;return table.concat(chunks) end
      if c=='\\' then
        chunks[#chunks+1]=text:sub(start,position-1);position=position+1
        local escaped=text:sub(position,position);position=position+1
        if escaped=="u" then
          local code=unicode()
          if code>=0xd800 and code<=0xdbff then
            assert(text:sub(position,position+1)=="\\u", "json: unpaired surrogate");position=position+2
            local low=unicode();assert(low>=0xdc00 and low<=0xdfff, "json: invalid surrogate")
            code=0x10000+(code-0xd800)*1024+low-0xdc00
          else assert(code<0xdc00 or code>0xdfff, "json: unpaired surrogate") end
          chunks[#chunks+1]=utf8(code)
        else
          local map={['"']='"',['\\']='\\',['/']='/',b='\b',f='\f',n='\n',r='\r',t='\t'}
          assert(map[escaped], "json: invalid escape");chunks[#chunks+1]=map[escaped]
        end
        start=position
      else assert(c:byte()>=32, "json: unescaped control character");position=position+1 end
    end
    error("json: unterminated string")
  end
  local parse
  parse=function(depth)
    nodes=nodes+1;assert(nodes<=max_nodes,"json: decoded node budget exceeded")
    assert(depth<=24,"json: nesting limit");skip()
    local c=text:sub(position,position)
    if c=='"' then return parse_string()
    elseif c=="{" then
      position=position+1;skip();local value={}
      if text:sub(position,position)=="}" then position=position+1;return value end
      while true do
        skip();local key=parse_string();assert(value[key]==nil,"json: duplicate key");skip()
        assert(text:sub(position,position)==":","json: colon expected");position=position+1
        value[key]=parse(depth+1);skip();local separator=text:sub(position,position);position=position+1
        if separator=="}" then return value end;assert(separator==",","json: comma expected")
      end
    elseif c=="[" then
      position=position+1;skip();local value=M.array({})
      if text:sub(position,position)=="]" then position=position+1;return value end
      while true do
        value[#value+1]=parse(depth+1);skip();local separator=text:sub(position,position);position=position+1
        if separator=="]" then return value end;assert(separator==",","json: comma expected")
      end
    elseif text:sub(position,position+3)=="true" then position=position+4;return true
    elseif text:sub(position,position+4)=="false" then position=position+5;return false
    elseif text:sub(position,position+3)=="null" then position=position+4;return M.null
    else
      local start=position
      if c=="-" then position=position+1 end
      local digit=text:sub(position,position);assert(digit:match("%d"),"json: value expected")
      if digit=="0" then position=position+1
      else local _,last=text:find("^%d+",position);position=last+1 end
      if text:sub(position,position)=="." then position=position+1;local _,last=text:find("^%d+",position);assert(last,"json: fraction expected");position=last+1 end
      if text:sub(position,position):match("[eE]") then
        position=position+1;if text:sub(position,position):match("[+-]") then position=position+1 end
        local _,last=text:find("^%d+",position);assert(last,"json: exponent expected");position=last+1
      end
      local number=tonumber(text:sub(start,position-1))
      assert(number and number==number and math.abs(number)~=math.huge,"json: invalid number")
      if not(math.type and math.type(number)=="integer") then assert(math.abs(number)<=9007199254740991,"json: unsafe number") end
      return number
    end
  end
  local result=parse(0);skip();assert(position>#text,"json: trailing data");return result
end
return M
