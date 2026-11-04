local client = require("aeorbit.client")
local clock,closed,reads=0,false,0
local card={request=function(url,body,headers)
  assert(url=="https://example.test/bridge/v1/inventory")
  assert(headers.Authorization=="Bearer "..string.rep("t",32))
  assert(body=='{"quantity":"9007199254740993"}')
  return {
    finishConnect=function() return clock>0 end,
    response=function() return 200 end,
    read=function()
      reads=reads+1
      if reads==1 then return "" elseif reads==2 then return '{"ack":"9007199254740993"}' end
      return nil
    end,
    close=function() closed=true end
  }
end}
local c=client.new(card,{url="https://example.test",token=string.rep("t",32),timeout=2,response_bytes=2048,request_bytes=2048},
  {now=function() return clock end,sleep=function(n) clock=clock+n end})
assert(c:send("/bridge/v1/inventory",'{"quantity":"9007199254740993"}').ack=="9007199254740993" and closed)

closed=false
card.request=function() return {finishConnect=function() return false end,close=function() closed=true end} end
local ok,err=pcall(function() c:post("/bridge/v1/poll",{}) end)
assert(not ok and err.code=="timeout" and closed, "hung request retained its handle")
closed=false
card.request=function() return {finishConnect=function() return true end,response=function() return 200 end,
  read=function() return string.rep("x",2049) end,close=function() closed=true end} end
ok,err=pcall(function() c:post("/bridge/v1/poll",{}) end)
assert(not ok and err.code=="response_too_large" and closed)
