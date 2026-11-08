local json = require("aeorbit.json")
local M = {}
local function problem(code,fatal) return {code=code,fatal=fatal,message=code} end
local function response_length(headers)
  if type(headers)~="table" then return nil end
  local length,found
  for name,value in pairs(headers) do
    if type(name)=="string" then
      name=name:lower()
      -- Transfer/content encodings can change which bytes reach this API.
      -- Ambiguous framing keeps the bounded EOF path.
      if name=="transfer-encoding" or name=="content-encoding" then return nil end
      if name=="content-length" then
        if found then return nil end
        found=true
        if type(value)=="table" then
          if #value~=1 then return nil end
          value=value[1]
        end
        if type(value)~="string" then return nil end
        local digits=value:match("^[ \t]*(%d+)[ \t]*$")
        if not digits then return nil end
        length=tonumber(digits)
      end
    end
  end
  return length
end
function M.new(internet,config,clock)
  local url=assert(config.url):gsub("/+$","")
  local secure=url:match("^https://[A-Za-z0-9%.%-%[%]:]+$")
  local local_http=url:match("^http://127%.0%.0%.1:%d+$") or url:match("^http://localhost:%d+$")
  assert(secure or local_http,"client: HTTPS origin required")
  assert(type(config.token)=="string" and #config.token>=32 and #config.token<=256,"client: invalid token")
  local client={response_bytes=config.response_bytes or 16384,request_bytes=config.request_bytes or 16384,timeout=config.timeout or 10}
  function client:send(path,body)
    assert(type(path)=="string" and path:match("^/bridge/v1/[a-z%-]+$"),"client: invalid path")
    assert(type(body)=="string" and #body<=self.request_bytes,"client: request budget exceeded")
    local handle
    local ok,result=pcall(function()
      handle=internet.request(url..path,body,{["Content-Type"]="application/json",Authorization="Bearer "..config.token})
      if not handle then error(problem("connection_failed",false),0) end
      local deadline=clock.now()+self.timeout
      local function wait()
        if clock.now()>=deadline then error(problem("timeout",false),0) end
        clock.sleep(0.05)
      end
      while not handle.finishConnect() do wait() end
      local status,_,headers=handle.response()
      local expected=response_length(headers)
      if expected and expected>self.response_bytes then error(problem("response_too_large",true),0) end
      local chunks,total={},0
      while true do
        if clock.now()>=deadline then error(problem("timeout",false),0) end
        -- Avoid an extra synchronized OC read once a framed body is complete.
        if expected and total==expected then break end
        local size=math.min(4096,expected and (expected-total) or (self.response_bytes-total+1))
        local data=handle.read(size)
        if data==nil then
          if expected and total<expected then error(problem("incomplete_http_response",false),0) end
          break
        end
        if #data==0 then wait()
        else
          total=total+#data
          if total>self.response_bytes then error(problem("response_too_large",true),0) end
          if #data>size then error(problem("invalid_http_response",true),0) end
          chunks[#chunks+1]=data
        end
      end
      local text=table.concat(chunks)
      if type(status)~="number" then error(problem("invalid_http_response",true),0) end
      if status<200 or status>=300 then
        local code="http_"..status
        local parsed,value=pcall(json.decode,text,self.response_bytes)
        if parsed and type(value)=="table" and type(value.error)=="table" and type(value.error.code)=="string" then code=value.error.code end
        error(problem(code,status~=429 and status~=502 and status~=503 and status~=504),0)
      end
      local parsed,value=pcall(json.decode,text,self.response_bytes)
      if not parsed or type(value)~="table" then error(problem("invalid_json_response",true),0) end
      return value
    end)
    if handle then pcall(handle.close) end
    if not ok then
      if type(result)=="table" and result.code then error(result,0) end
      error(problem("transport_failed",false),0)
    end
    return result
  end
  function client:post(path,value) return self:send(path,json.encode(value,self.request_bytes)) end
  return client
end
return M
