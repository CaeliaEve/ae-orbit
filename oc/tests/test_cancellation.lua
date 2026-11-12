local failures={}
local function test(name,fn)
  local ok,err=pcall(fn)
  if not ok then failures[#failures+1]=name..": "..tostring(err) end
end
local function stopped(err)
  return type(err)=="table" and err.stop==true and err.code=="interrupted" and err.fatal==true
end
test("bounded waits stop immediately",function()
  local cancel=require("aeorbit.cancel")
  local time,calls,ctx=0,0
  ctx=cancel.new({now=function() return time end,sleep=function(n)
    assert(n<=0.1);time=time+n;calls=calls+1;ctx.request_stop("manual")
  end})
  local ok,err=pcall(ctx.sleep,5)
  assert(not ok and stopped(err) and calls==1)
  local again,next_err=pcall(ctx.check)
  assert(not again and next_err==err)
  assert(not cancel.is_stop("network interrupted"))
  assert(pcall(cancel.rethrow,"other failure"))
end)
test("zero wait yields and observes stop",function()
  local calls,ctx=0
  ctx=require("aeorbit.cancel").new({now=function() return 0 end,sleep=function(seconds)
    assert(seconds==0);calls=calls+1;ctx.request_stop("manual")
  end})
  local ok,err=pcall(ctx.sleep,0)
  assert(calls==1,"zero wait did not yield")
  assert(not ok and stopped(err),"zero wait did not observe stop")
end)
test("HTTP stop closes without response probing",function()
  for _,stage in ipairs({"connect","read","cooperative"}) do
    local closed,probes,requests=false,0,0
    local c=require("aeorbit.client").new({request=function()
      requests=requests+1
      return {finishConnect=function() if stage=="connect" then error("interrupted",0) end;return true end,
        response=function() probes=probes+1;return 500 end,
        read=function() error("interrupted",0) end,close=function() closed=true end}
    end},{url="https://example.test",token=string.rep("t",32)},
      {now=function() return 0 end,sleep=function() end,check=function()
        if stage=="cooperative" and requests>0 then error({code="interrupted",stop=true,fatal=true},0) end
      end})
    local ok,err=pcall(c.post,c,"/bridge/v2/poll",{})
    assert(not ok and stopped(err) and closed and requests==1,"stop was swallowed or handle leaked: "..stage)
    assert(probes==(stage=="read" and 1 or 0),"stop probed response: "..stage)
  end
end)
test("HTTP 500 is retryable",function()
  local c=require("aeorbit.client").new({request=function() return {
    finishConnect=function() return true end,response=function() return 500 end,
    read=function() return nil end,close=function() end}
  end},{url="https://example.test",token=string.rep("t",32)},
    {now=function() return 0 end,sleep=function() end})
  local ok,err=pcall(c.post,c,"/bridge/v2/poll",{})
  assert(not ok and err.code=="http_500" and err.fatal==false)
end)
test("HTTP unsafe cleanup blocks even after stop",function()
  local c=require("aeorbit.client").new({request=function() return {
    finishConnect=function() error("interrupted",0) end,
    close=function() error("close failed",0) end}
  end},{url="https://example.test",token=string.rep("t",32)},
    {now=function() return 0 end,sleep=function() end})
  local ok,err=pcall(c.post,c,"/bridge/v2/poll",{})
  assert(not ok and err.code=="cleanup_failed" and err.fatal==true and not err.stop)
end)
for _,mode in ipairs({"false","nil_error","true_error","nil","true"}) do
  test("HTTP cleanup recognizes returned "..mode,function()
    local c=require("aeorbit.client").new({request=function() return {
      finishConnect=function() return true end,response=function() return 200,"OK",{["Content-Length"]="2"} end,
      read=function() return "{}" end,close=function()
        if mode=="false" then return false,"failure" end
        if mode=="nil_error" then return nil,"failure" end
        if mode=="true_error" then return true,"failure" end
        if mode=="true" then return true end
      end}
    end},{url="https://example.test",token=string.rep("t",32)},
      {now=function() return 0 end,sleep=function() end})
    local ok,err=pcall(c.post,c,"/bridge/v2/poll",{})
    if mode=="false" or mode=="nil_error" or mode=="true_error" then
      assert(not ok and err.code=="cleanup_failed" and err.fatal==true,"cleanup accepted "..mode)
    else assert(ok,"cleanup rejected normal "..mode) end
  end)
end
local function adapter(stage,on_request)
  return require("aeorbit.adapter").new({allItems=function() end,getItemInNetwork=function() end,
    getCraftable=function() if stage=="lookup" then error("interrupted",0) end;return nil end,
    getCraftables=function()
      if stage=="fallback" then error("interrupted",0) end
      return {{getStack=function() return {name="test:item",damage=0} end,
        request=function() if on_request then on_request() end;error("interrupted",0) end}}
    end})
end
local command={order_id="o",attempt_id="a",quantity="1",item={kind="item",name="test:item",damage=0,exact=true}}
test("client rejects invalid setup limits",function()
  for _,case in ipairs({{"timeout","bad"},{"timeout",0},{"timeout",math.huge},{"timeout",0/0},
    {"request_bytes",1.5},{"response_bytes",0},{"request_bytes",false}}) do
    local config={url="https://example.test",token=string.rep("t",32)}
    config[case[1]]=case[2]
    assert(not pcall(require("aeorbit.client").new,{},config,{}),"invalid client setting accepted: "..case[1])
  end
end)
test("tracker validates batch before side effects",function()
  local calls=0
  local t=require("aeorbit.tracker").new({craft=function() calls=calls+1 end},
    {data={attempts={},completed={}},save=function() calls=calls+1 end},1)
  local ok,err=pcall(function() t:validate_commands({command,{order_id="o",attempt_id="bad"}}) end)
  assert(not ok and type(err)=="table" and err.fatal and err.code=="invalid_command")
  assert(calls==0 and next(t.data.attempts)==nil)
  assert(pcall(function() t:validate_commands({command}) end))
  assert(pcall(function() t:validate_commands({{order_id="o",attempt_id="unsupported",quantity="0",
    item={kind="fluid",name="test:fluid",exact=false}}}) end),"unsupported semantics became malformed schema")
  local sparse={[1]=command,[3]=command}
  ok,err=pcall(function() t:validate_commands(sparse) end)
  assert(not ok and type(err)=="table" and err.fatal)
end)
test("tracker capacity and malformed commands fail fatally",function()
  local t=require("aeorbit.tracker").new({craft=function() error("unexpected craft") end},
    {data={attempts={},completed={}},save=function() error("unexpected save") end},0)
  local ok,err=pcall(t.accept,t,command)
  assert(not ok and type(err)=="table" and err.fatal and err.code=="command_window_exceeded")
  ok,err=pcall(t.accept,t,{})
  assert(not ok and type(err)=="table" and err.fatal and err.code=="invalid_command")
end)
test("adapter lookup preserves stop",function()
  for _,stage in ipairs({"lookup","fallback"}) do
    local a=adapter(stage)
    local ok,err=pcall(a.recipe_status,a,{name="test:item",damage=0})
    assert(not ok and stopped(err),"adapter swallowed "..stage)
  end
end)
test("interrupted craft is durable and never resubmitted",function()
  local files={}
  local fs={read=function(path) return files[path] end,write=function(path,value) files[path]=value;return true end}
  local journal=require("aeorbit.journal")
  local j=journal.new(fs,"j",8192)
  local submissions=0
  local t=require("aeorbit.tracker").new(adapter("request",function() submissions=submissions+1 end),j,4)
  local ok,err=pcall(t.accept,t,command)
  assert(not ok and stopped(err),"craft stop was swallowed")
  assert(journal.new(fs,"j",8192).data.attempts.a.state=="needs_review")
  t:accept(command)
  local recovered=require("aeorbit.tracker").new(adapter("request",function() submissions=submissions+1 end),journal.new(fs,"j",8192),4)
  recovered:accept(command)
  assert(t.data.attempts.a.state=="needs_review" and submissions==1)
end)
test("interrupted monitor durably records uncertainty",function()
  local files={}
  local fs={read=function(path) return files[path] end,write=function(path,value) files[path]=value;return true end}
  local journal=require("aeorbit.journal")
  local t=require("aeorbit.tracker").new({craft=function() return {
    hasFailed=function() error("interrupted",0) end}
  end},journal.new(fs,"monitor",8192),4)
  t:accept(command)
  local ok,err=pcall(t.pump,t)
  assert(not ok and stopped(err))
  assert(journal.new(fs,"monitor",8192).data.attempts.a.state=="needs_review")
end)
test("journal write failure is fatal",function()
  local j=require("aeorbit.journal").new({read=function() end,write=function() error("disk full") end},"j",8192)
  local ok,err=pcall(j.save,j,{attempts={},completed={}})
  assert(not ok and type(err)=="table" and err.fatal==true and err.code=="journal_failed")
end)
assert(#failures==0,table.concat(failures,"\n"))
