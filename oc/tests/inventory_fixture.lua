local runner=require("aeorbit.runner")
local adapter=require("aeorbit.adapter")
local json=require("aeorbit.json")
local journal=require("aeorbit.journal")

return function(options)
  options=options or {}
  local s={clock=0,memory=131072,grant=options.grant or 16,catalog_size=options.catalog_size or 16,
    delay=options.delay or 0.06,http_delay=options.http_delay or 0,reads=0,scans=0,polls={},requests={},items={},
    ids={},names={},next_id=0,acknowledged={},accepted_batches=0,accepted_updates=0,accepted_bytes=0}
  local function raw(name)
    return {name=name,damage=0,label=name,hasTag=false,size=42,isCraftable=false}
  end
  local me={
    allItems=function()
      s.scans=s.scans+1
      local index=0
      return function()
        s.clock=s.clock+s.delay;s.reads=s.reads+1;index=index+1
        if index<=s.catalog_size then return raw("test:cold_"..index) end
      end
    end,
    getItemsInNetwork=function() error("unbounded inventory array") end,
    getItemInNetwork=function(detail)
      s.clock=s.clock+s.delay;s.reads=s.reads+1
      if s.on_read then s.on_read(detail) end
      return raw(detail.name)
    end,
    getCraftable=function() return nil end,
    getCraftables=function(filter) return {{getStack=function() return raw(filter.name) end}} end,
  }
  s.me=me
  local client={request_bytes=options.bytes or 4096,response_bytes=4096}
  function client:post(path)
    if path=="/bridge/v1/boot-id" then return {boot_id="inventory-fixture"} end
    return {protocol=1,session_id="s",inventory_ack="0",poll_ack="0",
      limits={batch_items=options.batch_items or 32,batch_bytes=self.request_bytes,response_bytes=4096,cpus=16,poll_millis=1000,
        diagnostic_samples=options.diagnostic_samples,sync_telemetry=options.sync_telemetry}}
  end
  function client:send(path,body)
    assert(#body<=self.request_bytes,"wire request exceeded the negotiated byte limit")
    s.clock=s.clock+s.http_delay
    local value=json.decode(body)
    if path=="/bridge/v1/poll" then
      if s.on_poll then s.on_poll(body,value) end
      s.polls[#s.polls+1]=value
      local verify=s.verify or {};s.verify=nil
      return {ack=value.sequence,scan_budget=s.grant,poll_millis=1000,commands=json.array({}),verify=json.array(verify)}
    end
    assert(path=="/bridge/v1/inventory")
    s.requests[#s.requests+1]={body=body,at=s.clock,value=value}
    local existing=s.acknowledged[value.sequence]
    if existing then
      assert(existing.body==body,"retry changed already transmitted observations")
      return existing.ack
    end
    local bindings=json.array({})
    for index,update in ipairs(value.updates) do
      local id=update.id
      if update.item then
        local name=update.item.name
        if not s.ids[name] then s.next_id=s.next_id+1;s.ids[name]=tostring(s.next_id);s.names[tostring(s.next_id)]=name end
        id=s.ids[name];bindings[#bindings+1]={index=index,id=id}
      end
      s.items[assert(s.names[id])]=update
    end
    local ack={ack=value.sequence,bindings=bindings}
    s.acknowledged[value.sequence]={body=body,ack=ack}
    s.accepted_batches=s.accepted_batches+1;s.accepted_updates=s.accepted_updates+#value.updates;s.accepted_bytes=s.accepted_bytes+#body
    if s.fail_upload then s.fail_upload=false;error({fatal=false,code="transport_failed"}) end
    return ack
  end
  local files={}
  local log=journal.new({read=function(path) return files[path] end,write=function(path,value) files[path]=value;return true end},"inventory",32768)
  local config={cache_entries=64,dirty_entries=4,dirty_bytes=2048,max_batch_bytes=client.request_bytes,scan_slice_seconds=0.05}
  for key,value in pairs(options.config or {}) do config[key]=value end
  local r=runner.new(config,{client=client,adapter=adapter.new(me),journal=log,now=function() return s.clock end,
    free_memory=function() return s.memory end,yield=function() end})
  r:start()
  function s:verification(name,token)
    if not self.ids[name] then self.next_id=self.next_id+1;self.ids[name]=tostring(self.next_id);self.names[tostring(self.next_id)]=name end
    return {id=self.ids[name],item={kind="item",name=name,damage=0,label=name,exact=true},craftable=false,token=token}
  end
  function s:flood()
    for index=1,32 do r:on_item_changed(raw("test:hot_"..index)) end
  end
  return s,r,raw
end
