local client=require("aeorbit.client")

local function failed_request(status)
  local closed=false
  local c=client.new({request=function()
    return {
      finishConnect=function() error("native HTTP stream failed",0) end,
      response=function()
        if not status then error("response unavailable",0) end
        return status,"test",{}
      end,
      read=function() error("a failed native stream cannot be read",0) end,
      close=function() closed=true end,
    }
  end},{url="http://127.0.0.1:18080",token=string.rep("t",32),timeout=2},
    {now=function() return 0 end,sleep=function() error("unexpected retry",0) end})
  local ok,err=pcall(c.post,c,"/bridge/v2/poll",{})
  assert(not ok,"an HTTP stream failure was accepted")
  assert(closed,"a failed request retained its native handle")
  return err
end

-- OC records the HTTP status before finishConnect raises for an error stream.
for _,status in ipairs({401,403,409}) do
  local err=failed_request(status)
  assert(err.code=="http_"..status and err.fatal==true,
    "a permanent HTTP rejection became a retryable transport failure")
end

for _,status in ipairs({429,500,502,503,504}) do
  local err=failed_request(status)
  assert(err.code=="http_"..status and err.fatal==false,
    "a temporary HTTP rejection lost its retry classification")
end

local err=failed_request()
assert(err.code=="transport_failed" and err.fatal==false,
  "a connection failure without an HTTP response became fatal")
