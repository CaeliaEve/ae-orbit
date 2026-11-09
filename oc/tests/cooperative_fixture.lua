local adapter=require("aeorbit.adapter")
local client=require("aeorbit.client")
local journal=require("aeorbit.journal")
local json=require("aeorbit.json")
local available,cooperative=pcall(require,"aeorbit.cooperative")
assert(available,"cooperative v2 runtime is missing")

return function(options)
  options=options or {}
  local s={clock=0,memory=options.memory or 131072,role=options.role or "quantity_worker",polls={},uploads={},reads={},
    grant=options.grant or 4,grant_ttl=1000,reads_ack={},commands={},next_grant=0,files={},bindings={},stock={}}
  local function forbidden() error("worker crossed a primary-only hardware boundary") end
  local me={getItemInNetwork=function(detail)
    s.reads[#s.reads+1]={name=detail.name,at=s.clock}
    if s.fail_read and s.fail_read(detail) then error("hardware unavailable") end
    return {name=detail.name,damage=detail.damage,label=detail.label,size=s.stock[detail.name] or "42"}
  end}
  if s.role=="primary" then
    me.allItems=function()
      local index=0
      return function() index=index+1;return (s.catalog or {})[index] end
    end
    me.getCpus=function() s.cpu_reads=(s.cpu_reads or 0)+1;return {} end
    me.getCraftable=function(detail)
      s.recipe_reads=(s.recipe_reads or 0)+1
      return {getStack=function() return {name=detail.name,damage=0} end,
        request=function()
          s.crafts=(s.crafts or 0)+1
          assert(s.files["journal.1"] or s.files["journal.0"],"craft preceded durable intent")
          return {hasFailed=function() return false end,isComputing=function() return false end,
            isCanceled=function() return false end,isDone=function() return true end}
        end}
    end
  else
    me.allItems=nil
    me.getCpus=forbidden;me.getCraftable=forbidden;me.getCraftables=forbidden
    me.getItemsInNetwork=forbidden;me.setItemEventSubscription=forbidden
  end
  s.me=me
  function s:task(name,token,kind,purpose,ttl)
    return {id=name=="test:second" and "2" or "1",
      item={kind="item",name=name,damage=0,label=name,exact=true},
      assignment_id=token or string.rep("a",32),kind=kind or "quantity",purpose=purpose or "refresh",ttl_millis=ttl or 30000}
  end
  local internet={request=function(url,body,headers)
    assert(headers.Authorization=="Bearer "..string.rep("x",32))
    local path=url:match("https://fixture.test(.*)")
    local value=json.decode(body)
    local response
    if path=="/bridge/v2/boot-id" then response={boot_id="test-boot"}
    elseif path=="/bridge/v2/hello" then
      assert(value.protocol==2)
      s.hello=value
      response={protocol=2,session_id="session",device_id="device",role=options.welcome_role or s.role,
        inventory_ack="0",poll_ack="0",limits={batch_items=16,batch_bytes=4096,response_bytes=4096,
          cpus=2,poll_millis=1000,read_slots=16,read_bytes=16384}}
    elseif path=="/bridge/v2/poll" then
      s.polls[#s.polls+1]={value=value,body=body}
      s.clock=s.clock+(s.poll_rtt or 0)
      if s.fail_poll then s.fail_poll=false;error("lost poll") end
      s.next_grant=s.next_grant+1
      response={ack=value.sequence,commands=json.array(s.commands),reads=json.array(s.reads_ack),
        scan_budget=s.grant,poll_millis=1000,grant_id=s.grant_id or tostring(s.next_grant),grant_ttl_millis=s.grant_ttl}
      if s.mutate_poll_ack then s.mutate_poll_ack(response) end
      s.reads_ack={};s.commands={}
    elseif path=="/bridge/v2/inventory" then
      s.uploads[#s.uploads+1]={value=value,body=body}
      if s.fail_upload then s.fail_upload=false;error("lost inventory ack") end
      local bindings=json.array({})
      for index,update in ipairs(value.updates) do
        if update.item then
          local id=s.bindings[update.item.name] or "10"
          bindings[#bindings+1]={index=index,id=id}
        end
      end
      response={ack=value.sequence,revision=value.sequence,bindings=bindings}
      if s.mutate_ack then s.mutate_ack(response) end
    else error("unexpected v1 or unknown path: "..tostring(path)) end
    local text=json.encode(response)
    local offset=1
    return {finishConnect=function() return true end,
      response=function() return 200,nil,{["content-length"]=tostring(#text)} end,
      read=function(size) local part=text:sub(offset,offset+size-1);offset=offset+#part;return part end,
      close=function() end}
  end}
  local config={bridge_mode="cooperative",role=s.role,max_response_bytes=4096,max_batch_bytes=4096,
    scan_slice_seconds=options.slice or 0.05,scan_interval=60}
  for k,v in pairs(options.config or {}) do config[k]=v end
  local r=cooperative.new(config,{
    adapter=adapter.new(me,{role=s.role}),client=client.new(internet,{url="https://fixture.test",token=string.rep("x",32)},
      {now=function() return s.clock end,sleep=function(t) s.clock=s.clock+t end}),
    journal=journal.new({read=function(p) return s.files[p] end,write=function(p,v) s.files[p]=v;return true end},"journal",32768),
    now=function() return s.clock end,free_memory=function() return s.memory end,yield=function() end})
  s.runner=r
  function s:drain()
    for _=1,100 do if not r:continue_inventory() then return end end
    error("unbounded continuation")
  end
  function s:updates()
    local out={}
    for _,upload in ipairs(self.uploads) do for _,value in ipairs(upload.value.updates) do out[#out+1]=value end end
    return out
  end
  return s,r
end
