local json = require("aeorbit.json")
local quantity = require("aeorbit.quantity")
local cache = require("aeorbit.cache")
local tracker = require("aeorbit.tracker")
local read_diagnostics = require("aeorbit.read_diagnostics")
local M = {}
local function collect_step(amount)
  -- OC manages collection in its host; ordinary Lua also allows explicit steps.
  if collectgarbage then collectgarbage("step",amount) end
end
local function fatal(code) error({code=code,message=code,fatal=true},0) end
local function transient(error_value)
  return type(error_value)=="table" and error_value.fatal==false
end
local function account(self,name,amount)
  -- Telemetry saturates within Lua 5.2's exact integer range; item quantities
  -- and protocol sequences continue to use their separate decimal-string path.
  self.statistics[name]=math.min(9007199254740991,self.statistics[name]+amount)
end

function M.new(config,deps)
  local memory=deps.free_memory()
  local id_bytes=math.max(256,math.min(config.cache_bytes or math.floor(memory/12),math.floor(memory/8)))
  local dirty_bytes=math.max(256,math.min(config.dirty_bytes or math.floor(memory/12),math.floor(memory/8)))
  local dirty_entries=config.dirty_entries or 128
  local verification_bytes=math.max(256,math.min(16384,math.floor(memory/32)))
  local batch_delay=config.batch_interval_seconds or 0.25
  assert(type(batch_delay)=="number" and batch_delay>=0 and batch_delay<=1,"runner: invalid batch interval")
  local self={config=config,deps=deps,client=deps.client,adapter=deps.adapter,diagnostics={},diagnostic_samples=0,
    ids=cache.new(config.cache_entries or 256,id_bytes),dirty=cache.new(dirty_entries,dirty_bytes),
    verifications=cache.new(16,verification_bytes),verification_bytes=verification_bytes,
    rechecks=cache.new(16,verification_bytes),batch_delay=batch_delay,ordinary_turn=0,
    statistics={batches=0,updates=0,bytes=0,credits_used=0,collect_millis=0},
    dirty_bytes=dirty_bytes,dirty_entries=dirty_entries,inventory_seq="0",poll_seq="0",observation="0",
    credits=0,next_scan=0,next_cpu=0,next_recipe_check=0,poll_delay=1,uploaded=0}
  function self:diagnose(code)
    if not self.diagnostics[code] then
      local count=0;for _ in pairs(self.diagnostics) do count=count+1 end
      if count>=16 then return end
      self.diagnostics[code]="0"
    end
    self.diagnostics[code]=quantity.increment(self.diagnostics[code])
  end
  function self:_read_failure(code,source,key,raw,error_value)
    self:diagnose(code)
    if self.diagnostic_samples==0 then return end
    local known=self.ids.entries[key]
    self.read_diagnostics:record(code,source,raw,raw.diagnostic_item_id or (known and known.value.id),error_value)
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
    self.read_diagnostics=read_diagnostics.new(deps.now)
    local sample_limit=w.limits.diagnostic_samples
    self.diagnostic_samples=type(sample_limit)=="number" and sample_limit>=1 and sample_limit<=4 and sample_limit%1==0 and sample_limit or 0
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
  function self:on_item_changed(raw,verify_craftable)
    local ok,descriptor=pcall(self.adapter.descriptor,self.adapter,raw)
    if not ok then self:diagnose("event_identity_unavailable");return end
    local key=self.adapter:key(descriptor)
    local previous=self.dirty:get(key)
    descriptor.verify_craftable=verify_craftable==true or (previous and previous.verify_craftable) or nil
    local cost=#key*2+#(descriptor.tag or "")+#(descriptor.label or "")+256
    if not self.dirty.entries[key] and (self.dirty.count>=self.dirty_entries or self.dirty.bytes+cost>self.dirty_bytes) then self:diagnose("dirty_queue_evicted") end
    if not self.dirty:put(key,descriptor,cost) then self:diagnose("dirty_entry_too_large") end
  end
  function self:_health()
    local diagnostics=json.array({})
    local keys={};for code in pairs(self.diagnostics) do keys[#keys+1]=code end;table.sort(keys)
    for _,code in ipairs(keys) do diagnostics[#diagnostics+1]={code=code,count=self.diagnostics[code]} end
    local inventory={}
    for name,value in pairs(self.statistics) do inventory[name]=quantity.from_number(value) end
    return {free_memory=quantity.from_number(deps.free_memory()),diagnostics=diagnostics,inventory=inventory}
  end
  function self:_queue_recheck(raw)
    local key=self.adapter:key(raw)
    if self.verifications.entries[key] then return end
    local previous=self.rechecks.entries[key]
    if previous then
      previous.value.verify_craftable=previous.value.verify_craftable or raw.verify_craftable
      return
    end
    local cost=#key*2+#(raw.tag or "")+#(raw.label or "")+256
    if self.rechecks.count>=16 or self.rechecks.bytes+cost>self.verification_bytes then
      self:diagnose("recheck_queue_full");return
    end
    self.rechecks:put(key,raw,cost)
  end
  function self:_queue_verification(raw,token)
    local key=self.adapter:key(raw)
    self.rechecks:remove(key)
    local previous=self.verifications.entries[key]
    -- Retries do not move the oldest work to the back of the FIFO. A newer
    -- completion of the same item replaces only the fixed-size confirmation.
    if previous then previous.value.verification=token;return end
    local cost=#key*2+#(raw.tag or "")+#(raw.label or "")+#token+256
    if self.verifications.count>=16 or self.verifications.bytes+cost>self.verification_bytes then
      self:diagnose("verification_queue_full");return
    end
    raw.verification=token
    self.verifications:put(key,raw,cost)
  end
  function self:_control()
    if not self.pending_poll then
      local body={session_id=self.session,sequence=quantity.increment(self.poll_seq),reports=json.array({}),health=self:_health(),
        recheck_slots=16-self.rechecks.count,recheck_bytes=self.verification_bytes-self.rechecks.bytes}
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
      local samples,diagnostic_token=self.read_diagnostics:snapshot(self.diagnostic_samples)
      if samples then
        body.health.read_failures=samples
        while #samples>0 and not pcall(json.encode,body,limit) do samples[#samples]=nil end
        if #samples==0 then body.health.read_failures=nil;diagnostic_token=nil end
      end
      self.pending_poll={body=json.encode(body,limit),sequence=body.sequence,reports=body.reports,diagnostic_token=diagnostic_token}
    end
    local pending=self.pending_poll
    local ack=self.client:send("/bridge/v1/poll",pending.body)
    if ack.ack~=pending.sequence or type(ack.commands)~="table" or #ack.commands>16 or type(ack.verify)~="table" or #ack.verify>16 or
      type(ack.scan_budget)~="number" or ack.scan_budget<0 or ack.scan_budget>512 or ack.scan_budget%1~=0 or
      type(ack.poll_millis)~="number" or ack.poll_millis<1 or ack.poll_millis>30000 then fatal("invalid_poll_confirmation") end
    self.tracker:ack(pending.reports)
    for _,command in ipairs(ack.commands) do self.tracker:accept(command) end
    for _,verification in ipairs(ack.verify) do
      local token=verification.token
      if token~=nil and (type(token)~="string" or #token~=32 or token:find("[^0-9a-f]")) then fatal("invalid_verification_token") end
      local ok,raw=pcall(self.adapter.from_definition,self.adapter,verification.item)
      if ok then
        quantity.validate(verification.id)
        raw.diagnostic_item_id=verification.id
        local key=self.adapter:key(raw)
        self.ids:put(key,{id=verification.id,label=raw.label,tag=raw.tag},#key+#verification.id+#(raw.tag or "")+#(raw.label or "")+160)
        if token then self:_queue_verification(raw,token)
        else raw.verify_craftable=verification.craftable;self:_queue_recheck(raw) end
      else self:diagnose("verification_identity_unavailable") end
    end
    self.poll_seq=ack.ack;self.pending_poll=nil;self.credits=ack.scan_budget;self.poll_delay=ack.poll_millis/1000
    if pending.diagnostic_token then self.read_diagnostics:ack(pending.diagnostic_token) end
  end
  function self:_next_raw()
    local scan_ready=self.iterator~=nil or deps.now()>=self.next_scan
    local regular_ready=self.dirty.count>0 or self.rechecks.count>0 or scan_ready
    if self.verifications.count>0 and self.credits>=4 and deps.now()>=self.next_recipe_check and
      (not self.regular_turn or not regular_ready) then
      self.regular_turn=true;self.next_recipe_check=deps.now()+5
      local key,detail=self.verifications:pop()
      local ok,value=pcall(self.adapter.read,self.adapter,detail)
      if not ok then
        self:_read_failure("exact_lookup_failed","priority",key,detail,value);self:_queue_verification(detail,detail.verification);return nil,1
      end
      -- A new boot may still have the previous boot's catalog. Confirm the
      -- recipe as well as the count without copying the backend's old flag.
      local checked,craftable=pcall(self.adapter.recipe_status,self.adapter,value)
      if checked and type(craftable)=="boolean" then
        value.isCraftable=craftable;value.verification=detail.verification
      else
        self:_read_failure("recipe_lookup_failed","priority",key,detail,checked and "invalid recipe result" or craftable)
        self:_queue_verification(detail,detail.verification)
      end
      return value,4
    end
    self.regular_turn=false
    -- Retain round-robin position across slices and even one-credit grants.
    -- Hot hints, admitted cold reads and discovery each receive a turn.
    local lane
    for offset=0,2 do
      local candidate=(self.ordinary_turn+offset)%3
      if (candidate==0 and self.dirty.count>0) or (candidate==1 and self.rechecks.count>0) or (candidate==2 and scan_ready) then
        lane=candidate;self.ordinary_turn=(candidate+1)%3;break
      end
    end
    if lane==0 or lane==1 then
      local queue=lane==0 and self.dirty or self.rechecks
      local key,dirty=queue:pop()
      local ok,value=pcall(self.adapter.read,self.adapter,dirty)
      if not ok then self:_read_failure("exact_lookup_failed",lane==0 and "event" or "recheck",key,dirty,value);return nil,1 end
      local cost=1
      if dirty.verify_craftable and (value.size==0 or value.size=="0") then
        -- Removed zero-stock recipes disappear from enumeration. Recheck them
        -- explicitly, reserving four calls and at most one probe per 5 seconds.
        -- This quantity read may have crossed the cooldown boundary. Leave
        -- the newly available recipe opportunity to already queued priorities.
        if self.verifications.count==0 and self.credits>=4 and deps.now()>=self.next_recipe_check then
          self.next_recipe_check=deps.now()+5;cost=4
          local checked,craftable=pcall(self.adapter.recipe_status,self.adapter,value)
          if checked and type(craftable)=="boolean" then value.isCraftable=craftable
          else self:_read_failure("recipe_lookup_failed",lane==0 and "event" or "recheck",key,dirty,checked and "invalid recipe result" or craftable) end
        elseif lane==1 then self:_queue_recheck(dirty)
        else self:on_item_changed(dirty,true) end
      end
      return value,cost
    end
    if lane==nil then return nil,0 end
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
    local craftable=raw.isCraftable
    assert(craftable==nil or type(craftable)=="boolean","adapter: invalid recipe observation")
    if craftable==nil then craftable=json.null end
    local value={quantity=count,observation=self.observation,craftable=craftable,verification=raw.verification}
    local known=self.ids:get(key)
    if known and known.label==(raw.label or raw.name) and (known.tag or "")== (raw.tag or "") then value.id=known.id else value.item=self.adapter:definition(raw) end
    -- Only full definitions need their representation retained until binding.
    -- Short-ID rows must not hide a large raw tag behind a tiny wire record.
    return {key=key,label=raw.label or raw.name,tag=value.item and raw.tag or nil,item=value.item~=nil,verification=raw.verification,encoded=json.encode(value,self.client.request_bytes)}
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
      self.ids:put(entry.key,{id=binding.id,label=entry.label,tag=entry.tag},#entry.key+#binding.id+#entry.label+#(entry.tag or "")+160)
      seen[index]=true
    end
    for index,entry in ipairs(pending.entries) do
      if entry.item and not seen[index] then fatal("missing_short_id_binding") end
      local queued=self.verifications.entries[entry.key]
      if entry.verification and queued and queued.value.verification==entry.verification then self.verifications:remove(entry.key) end
    end
    account(self,"batches",1);account(self,"updates",#pending.entries);account(self,"bytes",#pending.body)
    self.inventory_seq=ack.ack;self.uploaded=self.uploaded+#pending.entries;self.pending_inventory=nil
  end
  function self:_memory_ready()
    local reserved=self.staged_inventory and self.staged_inventory.size*2 or 0
    if deps.free_memory()<self.client.response_bytes*4+4096+reserved then
      self:diagnose("low_memory");collect_step(32);return false
    end
    return true
  end
  function self:_send_staged()
    local batch=self.staged_inventory
    if not batch then return true end
    if #batch.entries==0 and not self.scan_completed then self.staged_inventory=nil;return true end
    if not self:_memory_ready() then return false end
    local seq=quantity.increment(self.inventory_seq)
    local body='{"session_id":'..json.encode(self.session)..',"sequence":'..json.encode(seq)..',"updates":['..table.concat(batch.encoded,",")..']'
    if self.scan_completed then body=body..',"scan_completed":true' end
    body=body.."}";self.scan_completed=false
    for _,entry in ipairs(batch.entries) do entry.encoded=nil end
    self.pending_inventory={body=body,sequence=seq,entries=batch.entries}
    self.staged_inventory=nil
    self:_flush()
    return true
  end
  function self:_inventory()
    -- Control traffic has already run even when this upload remains pending.
    local started=deps.now()
    local retrying=self.pending_inventory~=nil
    self:_flush()
    if not self:_memory_ready() then return false end
    if retrying and deps.now()-started>=(config.scan_slice_seconds or 0.05) then
      return self.credits>0 or self.carry~=nil or self.staged_inventory~=nil
    end
    if self.staged_inventory and (started>=self.staged_inventory.deadline or (self.credits<=0 and not self.carry)) then
      if not self:_send_staged() then return false end
      if deps.now()-started>=(config.scan_slice_seconds or 0.05) then
        return self.credits>0 or self.carry~=nil
      end
    end
    while self.credits>0 or self.carry do
      local limit=math.max(512,math.min(self.client.request_bytes,math.floor(deps.free_memory()/8)))
      if not self.staged_inventory then
        self.staged_inventory={entries={},encoded={},size=256,limit=limit,
          deadline=self.batch_delay>0 and (deps.now()+self.batch_delay) or math.huge}
      end
      local batch=self.staged_inventory
      local candidate=self.carry;self.carry=nil
      local idle,send=false,false
      if not candidate then
        local before=deps.now()
        local raw,cost=self:_next_raw();self.credits=self.credits-cost
        account(self,"credits_used",cost)
        account(self,"collect_millis",math.max(0,math.floor((deps.now()-before)*1000+0.5)))
        if raw then
          local ok,value=pcall(self._candidate,self,raw)
          if ok then candidate=value else self:diagnose("item_unrepresentable") end
        elseif cost==0 or self.scan_completed then idle=true end
      end
      if candidate then
        if #candidate.encoded+256>limit then self:diagnose("entry_exceeds_batch_budget")
        elseif batch.size+#candidate.encoded+1>batch.limit then self.carry=candidate;send=true
        else
          batch.entries[#batch.entries+1]=candidate;batch.encoded[#batch.encoded+1]=candidate.encoded
          batch.size=batch.size+#candidate.encoded+1
          send=candidate.verification~=nil
        end
      end
      send=send or idle or #batch.entries>=self.limits.batch_items or deps.now()>=batch.deadline or (self.credits<=0 and not self.carry)
      if send and not self:_send_staged() then return false end
      if idle then return false end
      if #batch.entries%4==0 and deps.yield then deps.yield() end
      if deps.now()-started>=(config.scan_slice_seconds or 0.05) then
        if self.batch_delay==0 and not self:_send_staged() then return false end
        break
      end
    end
    return self.credits>0 or self.carry~=nil or self.staged_inventory~=nil
  end
  function self:continue_inventory()
    if self.credits<=0 and not self.carry and not self.staged_inventory then return false end
    local ok,result=pcall(self._inventory,self)
    if not ok then
      if not transient(result) then error(result,0) end
      self:diagnose("inventory_transport_failed")
      return false
    end
    return result==true
  end
  function self:tick()
    assert(self.tracker,"runner: start first")
    self.tracker:pump()
    -- Grants span processing slices, but expire before the next control cycle.
    self.credits=0
    local control_ok,control_error=pcall(self._control,self)
    if not control_ok and not transient(control_error) then error(control_error,0) end
    local control_completed_at=deps.now()
    local inventory_ok,inventory_result=pcall(self._inventory,self)
    if not inventory_ok and not transient(inventory_result) then error(inventory_result,0) end
    if not control_ok then self:diagnose("control_transport_failed") end
    if not inventory_ok then self:diagnose("inventory_transport_failed") end
    return {control_ok=control_ok,inventory_ok=inventory_ok,control_completed_at=control_completed_at,
      inventory_work=control_ok and inventory_ok and inventory_result==true}
  end
  return self
end
return M
