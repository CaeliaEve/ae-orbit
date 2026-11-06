local json = require("aeorbit.json")
local quantity = require("aeorbit.quantity")
local cache = require("aeorbit.cache")
local tracker = require("aeorbit.tracker")
local M = {}
local function collect_step(amount)
  -- OC manages collection in its host; ordinary Lua also allows explicit steps.
  if collectgarbage then collectgarbage("step",amount) end
end
local function fatal(code) error({code=code,message=code,fatal=true},0) end
local function transient(error_value)
  return type(error_value)=="table" and error_value.fatal==false
end

function M.new(config,deps)
  local memory=deps.free_memory()
  local id_bytes=math.max(256,math.min(config.cache_bytes or math.floor(memory/12),math.floor(memory/8)))
  local dirty_bytes=math.max(256,math.min(config.dirty_bytes or math.floor(memory/12),math.floor(memory/8)))
  local dirty_entries=config.dirty_entries or 128
  local self={config=config,deps=deps,client=deps.client,adapter=deps.adapter,diagnostics={},
    ids=cache.new(config.cache_entries or 256,id_bytes),dirty=cache.new(dirty_entries,dirty_bytes),
    dirty_bytes=dirty_bytes,dirty_entries=dirty_entries,inventory_seq="0",poll_seq="0",observation="0",
    credits=0,next_scan=0,next_cpu=0,poll_delay=1,uploaded=0}
  function self:diagnose(code)
    if not self.diagnostics[code] then
      local count=0;for _ in pairs(self.diagnostics) do count=count+1 end
      if count>=16 then return end
      self.diagnostics[code]="0"
    end
    self.diagnostics[code]=quantity.increment(self.diagnostics[code])
  end
  function self:start()
    local nonce=self.client:post("/bridge/v1/boot-id",{})
    if type(nonce.boot_id)~="string" or #nonce.boot_id<1 then fatal("invalid_boot_identity") end
    local free=deps.free_memory()
    local response=math.max(2048,math.min(config.max_response_bytes or 16384,math.floor(free/16)))
    self.client.response_bytes=response
    local w=self.client:post("/bridge/v1/hello",{protocol=1,boot_id=nonce.boot_id,source=config.source or "oc",
      capabilities=self.adapter:capabilities(),max_response_bytes=response})
    if w.protocol~=1 or type(w.session_id)~="string" or type(w.limits)~="table" or w.inventory_ack~="0" or w.poll_ack~="0" then fatal("invalid_welcome") end
    if type(w.limits.batch_items)~="number" or w.limits.batch_items<1 or w.limits.batch_items>512 or
      type(w.limits.batch_bytes)~="number" or w.limits.batch_bytes<512 or
      type(w.limits.cpus)~="number" or w.limits.cpus<1 or w.limits.cpus>64 or w.limits.cpus%1~=0 or
      type(w.limits.response_bytes)~="number" or w.limits.response_bytes>response then fatal("invalid_device_limits") end
    self.session=w.session_id;self.limits=w.limits
    self.client.response_bytes=w.limits.response_bytes
    self.client.request_bytes=math.min(config.max_batch_bytes or 16384,w.limits.batch_bytes)
    local recovered=0;for _ in pairs(deps.journal.data.attempts or {}) do recovered=recovered+1 end
    if recovered>0 then self:diagnose("previous_attempts_need_review") end
    -- A new server-issued boot identity retires the old backend session. Never
    -- reconstruct native tracking userdata or execute an old disk record.
    deps.journal:save({boot_id=nonce.boot_id,attempts={},completed=json.array({})})
    self.tracker=tracker.new(self.adapter,deps.journal,16)
    self.poll_delay=(w.limits.poll_millis or 1000)/1000
  end
  function self:on_item_changed(raw)
    local ok,descriptor=pcall(self.adapter.descriptor,self.adapter,raw)
    if not ok then self:diagnose("event_identity_unavailable");return end
    local key=self.adapter:key(descriptor)
    local cost=#key*2+#(descriptor.label or "")+256
    if not self.dirty.entries[key] and (self.dirty.count>=self.dirty_entries or self.dirty.bytes+cost>self.dirty_bytes) then self:diagnose("dirty_queue_evicted") end
    if not self.dirty:put(key,descriptor,cost) then self:diagnose("dirty_entry_too_large") end
  end
  function self:_health()
    local diagnostics=json.array({})
    local keys={};for code in pairs(self.diagnostics) do keys[#keys+1]=code end;table.sort(keys)
    for _,code in ipairs(keys) do diagnostics[#diagnostics+1]={code=code,count=self.diagnostics[code]} end
    return {free_memory=quantity.from_number(deps.free_memory()),diagnostics=diagnostics}
  end
  function self:_control()
    if not self.pending_poll then
      local body={session_id=self.session,sequence=quantity.increment(self.poll_seq),reports=json.array({}),health=self:_health()}
      local limit=self.client.request_bytes
      local reports=self.tracker:reports()
      for _,report in ipairs(reports) do
        body.reports[#body.reports+1]=report
        if #body.reports>16 or not pcall(json.encode,body,limit) then body.reports[#body.reports]=nil;break end
      end
      if deps.now()>=self.next_cpu then
        local ok,cpus,truncated=pcall(self.adapter.cpus,self.adapter)
        self.next_cpu=deps.now()+(config.cpu_interval or 10)
        if ok then
          body.cpus=json.array({});body.cpus_truncated=truncated
          for _,cpu in ipairs(cpus) do
            if #body.cpus>=self.limits.cpus then body.cpus_truncated=true;break end
            body.cpus[#body.cpus+1]=cpu
            if not pcall(json.encode,body,limit-64) then body.cpus[#body.cpus]=nil;body.cpus_truncated=true;break end
          end
        else self:diagnose("cpu_unavailable") end
      end
      self.pending_poll={body=json.encode(body,limit),sequence=body.sequence,reports=body.reports}
    end
    local pending=self.pending_poll
    local ack=self.client:send("/bridge/v1/poll",pending.body)
    if ack.ack~=pending.sequence or type(ack.commands)~="table" or #ack.commands>16 or type(ack.verify)~="table" or #ack.verify>16 or
      type(ack.scan_budget)~="number" or ack.scan_budget<0 or ack.scan_budget>512 or ack.scan_budget%1~=0 or
      type(ack.poll_millis)~="number" or ack.poll_millis<1 or ack.poll_millis>30000 then fatal("invalid_poll_confirmation") end
    self.tracker:ack(pending.reports)
    for _,command in ipairs(ack.commands) do self.tracker:accept(command) end
    for _,verification in ipairs(ack.verify) do
      local ok,raw=pcall(self.adapter.from_definition,self.adapter,verification.item)
      if ok then
        quantity.validate(verification.id)
        local key=self.adapter:key(raw)
        self.ids:put(key,{id=verification.id,label=raw.label},#key+#verification.id+#(raw.label or "")+160)
        self:on_item_changed(raw)
      else self:diagnose("verification_identity_unavailable") end
    end
    self.poll_seq=ack.ack;self.pending_poll=nil;self.credits=ack.scan_budget;self.poll_delay=ack.poll_millis/1000
  end
  function self:_next_raw()
    local scan_ready=self.iterator~=nil or deps.now()>=self.next_scan
    -- Keep the turn across time slices and one-item grants. A busy event queue
    -- must not prevent cold items from being discovered or refreshed.
    if self.dirty.count>0 and (not self.scan_turn or not scan_ready) then
      local _,dirty=self.dirty:pop()
      self.scan_turn=true
      local ok,value=pcall(self.adapter.read,self.adapter,dirty)
      if not ok then self:diagnose("exact_lookup_failed");return nil,1 end
      return value,1
    end
    self.scan_turn=false
    if not self.iterator then
      if deps.now()<self.next_scan then return nil,0 end
      local ok,iterator=pcall(self.adapter.iterator,self.adapter)
      if not ok then self:diagnose("scan_unavailable");self.next_scan=deps.now()+10;return nil,1 end
      self.iterator=iterator
    end
    local ok,raw=pcall(self.iterator)
    if not ok then self:diagnose("scan_failed");self.iterator=nil;self.next_scan=deps.now()+10;return nil,1 end
    if raw==nil then
      self.iterator=nil;self.scan_completed=true;self.next_scan=deps.now()+(config.scan_interval or 60)
      collect_step(16)
    end
    return raw,1
  end
  function self:_candidate(raw)
    local key=self.adapter:key(raw)
    local count=self.adapter:count(raw)
    self.observation=quantity.increment(self.observation)
    local value={quantity=count,observation=self.observation,craftable=raw.isCraftable==true}
    local known=self.ids:get(key)
    if known and known.label==(raw.label or raw.name) then value.id=known.id else value.item=self.adapter:definition(raw) end
    return {key=key,label=raw.label or raw.name,item=value.item~=nil,encoded=json.encode(value,self.client.request_bytes)}
  end
  function self:_flush()
    local pending=self.pending_inventory
    if not pending then return end
    local ack=self.client:send("/bridge/v1/inventory",pending.body)
    if ack.ack~=pending.sequence or type(ack.bindings)~="table" then fatal("invalid_inventory_confirmation") end
    local seen={}
    for _,binding in ipairs(ack.bindings) do
      local index=binding.index
      if type(index)~="number" or index%1~=0 or index<1 or index>#pending.entries or seen[index] or not pending.entries[index].item then fatal("invalid_short_id_binding") end
      quantity.validate(binding.id);if binding.id=="0" then fatal("invalid_short_id_binding") end
      local entry=pending.entries[index]
      self.ids:put(entry.key,{id=binding.id,label=entry.label},#entry.key+#binding.id+#entry.label+160)
      seen[index]=true
    end
    for index,entry in ipairs(pending.entries) do if entry.item and not seen[index] then fatal("missing_short_id_binding") end end
    self.inventory_seq=ack.ack;self.uploaded=self.uploaded+#pending.entries;self.pending_inventory=nil
  end
  function self:_inventory()
    -- Control traffic has already run even when this upload remains pending.
    self:_flush()
    if deps.free_memory()<self.client.response_bytes*4+4096 then self:diagnose("low_memory");collect_step(32);return end
    local started=deps.now()
    while self.credits>0 or self.carry do
      local limit=math.max(512,math.min(self.client.request_bytes,math.floor(deps.free_memory()/8)))
      local entries,encoded,size={}, {},256
      while #entries<self.limits.batch_items and (self.credits>0 or self.carry) do
        local candidate=self.carry;self.carry=nil
        if not candidate then
          local raw,cost=self:_next_raw();self.credits=self.credits-cost
          if raw then
            local ok,value=pcall(self._candidate,self,raw)
            if ok then candidate=value else self:diagnose("item_unrepresentable") end
          elseif cost==0 or self.scan_completed then break end
        end
        if candidate then
          if #candidate.encoded+256>limit then self:diagnose("entry_exceeds_batch_budget")
          elseif size+#candidate.encoded+1>limit then self.carry=candidate;break
          else entries[#entries+1]=candidate;encoded[#encoded+1]=candidate.encoded;size=size+#candidate.encoded+1 end
        end
        if #entries%4==0 and deps.yield then deps.yield() end
        if deps.now()-started>=(config.scan_slice_seconds or 0.05) then break end
      end
      if #entries>0 or self.scan_completed then
        local seq=quantity.increment(self.inventory_seq)
        local body='{"session_id":'..json.encode(self.session)..',"sequence":'..json.encode(seq)..',"updates":['..table.concat(encoded,",")..']'
        if self.scan_completed then body=body..',"scan_completed":true' end
        body=body.."}";self.scan_completed=false
        for _,entry in ipairs(entries) do entry.encoded=nil end
        self.pending_inventory={body=body,sequence=seq,entries=entries}
        self:_flush()
      else break end
      if deps.now()-started>=(config.scan_slice_seconds or 0.05) then break end
    end
    -- Unused acquisition credit expires at this control cycle; never accumulate a burst.
    self.credits=0
  end
  function self:tick()
    assert(self.tracker,"runner: start first")
    self.tracker:pump()
    self.credits=0
    local control_ok,control_error=pcall(self._control,self)
    if not control_ok and not transient(control_error) then error(control_error,0) end
    local inventory_ok,inventory_error=pcall(self._inventory,self)
    if not inventory_ok and not transient(inventory_error) then error(inventory_error,0) end
    if not control_ok then self:diagnose("control_transport_failed") end
    if not inventory_ok then self:diagnose("inventory_transport_failed") end
    return {control_ok=control_ok,inventory_ok=inventory_ok}
  end
  return self
end
return M
