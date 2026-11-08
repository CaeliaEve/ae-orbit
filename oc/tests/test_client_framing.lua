local client=require("aeorbit.client")

local function request(options)
  local s={clock=0,reads=0,sizes={},closed=false,position=1,empty=false}
  local body=options.body or '{"ack":"9007199254740993"}'
  local handle={
    finishConnect=function() return true end,
    response=function() return options.status or 200,"test",options.headers end,
    read=function(size)
      s.reads=s.reads+1;s.sizes[#s.sizes+1]=size
      if options.never_ready then return "" end
      if options.empty_between and not s.empty then s.empty=true;return "" end
      s.empty=false
      if s.position>#body then return nil end
      local count=options.ignore_size and #body or math.min(size,options.chunk or #body)
      local value=body:sub(s.position,s.position+count-1)
      s.position=s.position+#value
      return value
    end,
    close=function() s.closed=true end,
  }
  local c=client.new({request=function() return handle end},
    {url="https://example.test",token=string.rep("t",32),timeout=options.timeout or 2,
      response_bytes=options.limit or 16384,request_bytes=2048},
    {now=function() return s.clock end,sleep=function(delay) s.clock=s.clock+delay end})
  s.ok,s.result=pcall(c.send,c,"/bridge/v1/inventory","{}")
  assert(s.closed,"HTTP completion or failure retained its native handle")
  return s
end

local body='{"ack":"9007199254740993"}'

-- OpenComputers exposes Java header lists as Lua arrays. Once the declared
-- bytes are received, probing the native stream again adds no information.
do
  local s=request({body=body,headers={["Content-Length"]={tostring(#body)}}})
  assert(s.ok and s.result.ack=="9007199254740993")
  assert(s.reads==1,"a complete length-delimited response still probed for EOF")
end

-- Header names are case-insensitive; direct string values are also supported.
do
  local s=request({body=body,headers={["cOnTeNt-LeNgTh"]=" \t"..#body.."\t "},chunk=7,empty_between=true})
  assert(s.ok and s.result.ack=="9007199254740993")
  assert(s.reads==2*math.ceil(#body/7),"partial or temporarily empty reads lost the framing boundary")
end

do
  local unicode='{"ack":"1","label":"铁板"}'
  local s=request({body=unicode,headers={["Content-Length"]={tostring(#unicode)}},chunk=5})
  assert(s.ok and s.result.label=="铁板","framing counted characters instead of UTF-8 bytes")
end

-- A larger valid reply should use bounded chunks without a 1 KiB callback
-- for every fragment. Limits continue to include the whole response.
do
  local large='{"ack":"1","padding":"'..string.rep("x",10000)..'"}'
  local s=request({body=large,headers={["Content-Length"]={tostring(#large)}}})
  assert(s.ok and #s.result.padding==10000)
  assert(s.reads<=3,"a larger response was fragmented into excessive native reads")
  for _,size in ipairs(s.sizes) do assert(size<=4096,"a single native read grew without a chunk limit") end
end

-- Missing or ambiguous framing must preserve EOF semantics. Do not trust a
-- smaller conflicting length and silently accept a truncated JSON prefix.
for _,headers in ipairs({
  {},
  {["Content-Length"]={"+"..#body}},
  {["Content-Length"]={"-1"}},
  {["Content-Length"]={tostring(#body)..".0"}},
  {["Content-Length"]={"invalid"}},
  {["Content-Length"]={}},
  {["Content-Length"]={"1",tostring(#body)}},
  {["Content-Length"]={tostring(#body)},["content-length"]={tostring(#body)}},
  {["Content-Length"]={"1"},["Transfer-Encoding"]={"chunked"}},
  {["Content-Length"]={"1"},["transfer-encoding"]="chunked"},
  {["Content-Length"]={"1"},["Content-Encoding"]={"gzip"}},
}) do
  local s=request({body=body,headers=headers})
  assert(s.ok and s.result.ack=="9007199254740993","ambiguous framing truncated a valid response")
  assert(s.reads==2,"an ambiguous response skipped its EOF check")
end

-- Premature EOF is a transport failure even if the received prefix happens
-- to be valid JSON. A frozen batch can be retried instead of being confirmed.
do
  local s=request({body=body,headers={["Content-Length"]={tostring(#body+10)}}})
  assert(not s.ok and s.result.code=="incomplete_http_response" and s.result.fatal==false,"premature EOF was accepted as an ACK")
end

-- Do not allocate or read the advertised body when it exceeds the budget.
do
  local s=request({body=body,headers={["Content-Length"]={tostring(#body-1)}},ignore_size=true})
  assert(not s.ok and s.result.code=="invalid_http_response" and s.result.fatal==true,"a native read overran the declared body boundary")
end

for _,length in ipairs({"2049","999999999999999999999999999999999999999"}) do
  local s=request({body=body,limit=2048,headers={["Content-Length"]={length}}})
  assert(not s.ok and s.result.code=="response_too_large" and s.result.fatal==true)
  assert(s.reads==0,"an oversized declared response was read before rejecting its size")
end

do
  local s=request({body=string.rep("x",2049),limit=2048})
  assert(not s.ok and s.result.code=="response_too_large" and s.result.fatal==true,"EOF fallback lost its total byte limit")
  for _,size in ipairs(s.sizes) do assert(size<=2049) end
end

do
  local s=request({body="",headers={["Content-Length"]={"0"}}})
  assert(not s.ok and s.result.code=="invalid_json_response" and s.reads==0,"empty framed JSON bypassed validation")
end

do
  local s=request({headers={["Content-Length"]={tostring(#body)}},never_ready=true,timeout=0.2})
  assert(not s.ok and s.result.code=="timeout" and s.result.fatal==false,"declared length disabled the read deadline")
end

do
  local error_body='{"error":{"code":"capacity_exceeded"}}'
  local s=request({body=error_body,status=429,headers={["Content-Length"]={tostring(#error_body)}}})
  assert(not s.ok and s.result.code=="capacity_exceeded" and s.result.fatal==false,"framing changed retryable HTTP errors")
end
