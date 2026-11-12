local json = require("aeorbit.json")
local quantity = require("aeorbit.quantity")
local cache = require("aeorbit.cache")
local tracker = require("aeorbit.tracker")
local read_diagnostics = require("aeorbit.read_diagnostics")
local latency = require("aeorbit.latency")
local cancel = require("aeorbit.cancel")
local native_pcall=pcall
local function preserve_stop(ok,...)
  if not ok then cancel.rethrow((...)) end
  return ok,...
end
-- Recovery handlers must never reinterpret an intentional stop as read failure.
local function pcall(...) return preserve_stop(native_pcall(...)) end
local M = {}
local function collect_step(amount)
  -- OC manages collection in its host; ordinary Lua also allows explicit steps.
  if collectgarbage then collectgarbage("step",amount) end
end
local function fatal(code) error({code=code,message=code,fatal=true},0) end
local function integer(value,minimum,maximum)
  return type(value)=="number" and value%1==0 and value>=minimum and value<=maximum
end
local function list(value,maximum)
  if type(value)~="table" or value==json.null or #value>maximum then return false end
  for key in pairs(value) do if not integer(key,1,#value) then return false end end
  return true
end
local function definition(adapter,item,code)
  if type(item)~="table" or (item.label~=nil and type(item.label)~="string") then fatal(code) end
  local ok,raw=pcall(adapter.from_definition,adapter,item)
  if not ok then fatal(code) end
  return raw
end
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
    refreshes=cache.new(8,verification_bytes),refresh_bytes=verification_bytes,refresh_reads=0,
    rechecks=cache.new(16,verification_bytes),batch_delay=batch_delay,ordinary_turn=0,
    statistics={batches=0,updates=0,bytes=0,credits_used=0,collect_millis=0},
    dirty_bytes=dirty_bytes,dirty_entries=dirty_entries,inventory_seq="0",poll_seq="0",observation="0",
    credits=0,next_scan=0,next_cpu=0,next_recipe_check=0,poll_delay=1,uploaded=0,first_scan_acked=false}
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
    if self.started then return end
    self.started_at=self.started_at or deps.now()
    if not self.boot_id then
      local nonce=self.client:post("/bridge/v1/boot-id",{})
      if type(nonce)~="table" or type(nonce.boot_id)~="string" or #nonce.boot_id<1 then fatal("invalid_boot_identity") end
      self.boot_id=nonce.boot_id
    end
    if not self.hello_body then
      local free=deps.free_memory()
      self.hello_response_bytes=math.max(2048,math.min(config.max_response_bytes or 16384,math.floor(free/16)))
      self.hello_body=json.encode({protocol=1,boot_id=self.boot_id,source=config.source or "oc",
        capabilities=self.adapter:capabilities(),max_response_bytes=self.hello_response_bytes},self.client.request_bytes)
    end
    local response=self.hello_response_bytes
    self.client.response_bytes=response
    local w=self.client:send("/bridge/v1/hello",self.hello_body)
    if type(w)~="table" or w.protocol~=1 or type(w.session_id)~="string" or #w.session_id==0 or type(w.limits)~="table" or w.inventory_ack~="0" or w.poll_ack~="0" then fatal("invalid_welcome") end
    if not integer(w.limits.batch_items,1,512) or not integer(w.limits.batch_bytes,512,1048576) or
      not integer(w.limits.cpus,1,64) or not integer(w.limits.response_bytes,512,response) or
      (w.limits.poll_millis~=nil and not integer(w.limits.poll_millis,1,30000)) then fatal("invalid_device_limits") end
    self.session=w.session_id;self.limits=w.limits
    if w.limits.sync_telemetry==true then self.latency=latency.new(deps.now,self.started_at) end
    self.read_diagnostics=read_diagnostics.new(deps.now)
    local sample_limit=w.limits.diagnostic_samples
    self.diagnostic_samples=type(sample_limit)=="number" and sample_limit>=1 and sample_limit<=4 and sample_limit%1==0 and sample_limit or 0
    self.client.response_bytes=w.limits.response_bytes
    self.client.request_bytes=math.min(config.max_batch_bytes or 16384,w.limits.batch_bytes)
    local recovered=0;for _ in pairs(deps.journal.data.attempts or {}) do recovered=recovered+1 end
    if recovered>0 then self:diagnose("previous_attempts_need_review") end
    -- A new server-issued boot identity retires the old backend session. Never
    -- reconstruct native tracking userdata or execute an old disk record.
    deps.journal:save({boot_id=self.boot_id,attempts={},completed=json.array({})})
    self.tracker=tracker.new(self.adapter,deps.journal,16)
    self.poll_delay=(w.limits.poll_millis or 1000)/1000
    self.started=true
  end
  function self:on_item_changed(raw,verify_craftable)
    local ok,descriptor=pcall(self.adapter.descriptor,self.adapter,raw)
    if not ok then self:diagnose("event_identity_unavailable");return end
    local key=self.adapter:key(descriptor)
    local previous=self.dirty.entries[key]
    if previous then
      -- Keep the first hint's queue position and bounded identity. Repeated
      -- changes must not postpone the item; the read supplies fresh metadata.
      previous.value.verify_craftable=verify_craftable==true or previous.value.verify_craftable or nil
      return
    end
    descriptor.verify_craftable=verify_craftable==true or nil
    if self.latency then descriptor.queued_at=deps.now() end
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
  function self:_send(path,body,phase)
    if not self.latency then return self.client:send(path,body) end
    local started=deps.now()
    local ok,result=pcall(self.client.send,self.client,path,body)
    self.latency:observe(phase,deps.now()-started,not ok)
    if not ok then error(result,0) end
    return result
  end
  function self:_wait(phase,entry)
    if self.latency and entry.queued_at then self.latency:observe(phase,deps.now()-entry.queued_at) end
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
    if self.latency then raw.queued_at=deps.now() end
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
    if self.latency then raw.queued_at=deps.now() end
    self.verifications:put(key,raw,cost)
  end
  function self:_expire_refreshes()
    local entry=self.refreshes.last
    while entry do
      local previous=entry.previous
      if deps.now()>=entry.value.expires_at then self.refreshes:remove(entry.key) end
      entry=previous
    end
  end
  function self:_queue_refresh(raw,token,ttl_millis)
    local key=self.adapter:key(raw)
    local previous=self.refreshes.entries[key]
    if previous then
      if previous.value.refresh~=token then
        previous.value.refresh=token;previous.value.in_flight=nil
        previous.value.expires_at=deps.now()+ttl_millis/1000
      end
      return
    end
    local cost=#key*2+#(raw.tag or "")+#(raw.label or "")+#token+256
    if self.refreshes.count>=8 or self.refreshes.bytes+cost>self.refresh_bytes then
      self:diagnose("refresh_queue_full");return
    end
    raw.refresh=token
    raw.expires_at=deps.now()+ttl_millis/1000
    self.refreshes:put(key,raw,cost)
  end
  function self:_control()
    self:_expire_refreshes()
    if not self.pending_poll then
      -- An inherited catalog is already covered by initial discovery. Keep a
      -- small ordinary reread trickle until the completed scan is acknowledged;
      -- event reads and the separate priority queue keep their normal turns.
      -- If discovery fails, use the retry delay for ordinary verification.
      local recheck_limit=16
      if not self.first_scan_acked and (self.iterator~=nil or deps.now()>=self.next_scan or
        self.scan_completed or (self.pending_inventory and self.pending_inventory.scan_completed)) then
        recheck_limit=1
      end
      local body={session_id=self.session,sequence=quantity.increment(self.poll_seq),reports=json.array({}),health=self:_health(),
        recheck_slots=math.max(0,recheck_limit-self.rechecks.count),recheck_bytes=self.verification_bytes-self.rechecks.bytes}
      if self.limits.quantity_refresh==true then
        body.refresh_slots=8-self.refreshes.count
        body.refresh_bytes=self.refresh_bytes-self.refreshes.bytes
      end
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
      local sync_snapshot=false
      if self.latency and self.latency:due() and deps.free_memory()>=32768 then
        body.health.sync=self.latency:snapshot()
        if pcall(json.encode,body,limit) then sync_snapshot=true
        else body.health.sync=nil;self.latency:defer() end
      end
      self.pending_poll={body=json.encode(body,limit),sequence=body.sequence,reports=body.reports,diagnostic_token=diagnostic_token,sync_snapshot=sync_snapshot}
    end
    local pending=self.pending_poll
    local started=deps.now()
    local ack=self:_send("/bridge/v1/poll",pending.body,"control_http")
    if type(ack)~="table" or ack.ack~=pending.sequence or not list(ack.commands,16) or not list(ack.verify,16) or
      not integer(ack.scan_budget,0,512) or not integer(ack.poll_millis,1,30000) then fatal("invalid_poll_confirmation") end
    self.tracker:validate_commands(ack.commands)
    local verified,refreshed={},{}
    for index,verification in ipairs(ack.verify) do
      if type(verification)~="table" or not pcall(quantity.validate,verification.id) or verification.id=="0" or
        (verification.craftable~=nil and type(verification.craftable)~="boolean") then fatal("invalid_verification_identity") end
      local token=verification.token
      if token~=nil and (type(token)~="string" or #token~=32 or token:find("[^0-9a-f]")) then fatal("invalid_verification_token") end
      verified[index]=definition(self.adapter,verification.item,"invalid_verification_identity")
    end
    if self.limits.quantity_refresh==true and ack.refresh~=nil then
      if not list(ack.refresh,4) then fatal("invalid_refresh_confirmation") end
      for index,refresh in ipairs(ack.refresh) do
        if type(refresh)~="table" then fatal("invalid_refresh_identity") end
        local token=refresh.token
        if type(token)~="string" or #token~=32 or token:find("[^0-9a-f]") then fatal("invalid_refresh_token") end
        local ttl=refresh.ttl_millis
        if not integer(ttl,1,60000) then fatal("invalid_refresh_ttl") end
        if not pcall(quantity.validate,refresh.id) or refresh.id=="0" then fatal("invalid_refresh_identity") end
        refreshed[index]=definition(self.adapter,refresh.item,"invalid_refresh_identity")
      end
    end
    self.tracker:ack(pending.reports)
    for _,command in ipairs(ack.commands) do self.tracker:accept(command) end
    for index,raw in ipairs(verified) do
      local verification=ack.verify[index]
      raw.diagnostic_item_id=verification.id
      local key=self.adapter:key(raw)
      self.ids:put(key,{id=verification.id,label=raw.label,tag=raw.tag},#key+#verification.id+#(raw.tag or "")+#(raw.label or "")+160)
      if verification.token then self:_queue_verification(raw,verification.token)
      else raw.verify_craftable=verification.craftable;self:_queue_recheck(raw) end
    end
    for index,raw in ipairs(refreshed) do
      local refresh=ack.refresh[index]
      raw.diagnostic_item_id=refresh.id
      local key=self.adapter:key(raw)
      self.ids:put(key,{id=refresh.id,label=raw.label,tag=raw.tag},#key+#refresh.id+#(raw.tag or "")+#(raw.label or "")+160)
      self:_queue_refresh(raw,refresh.token,refresh.ttl_millis)
    end
    self.poll_seq=ack.ack;self.pending_poll=nil;self.credits=ack.scan_budget;self.poll_delay=ack.poll_millis/1000
    self.refresh_reads=0
    if pending.diagnostic_token then self.read_diagnostics:ack(pending.diagnostic_token) end
    if pending.sync_snapshot then self.latency:defer() end
    return started
  end
  function self:_next_raw()
    self:_expire_refreshes()
    local scan_ready=self.iterator~=nil or deps.now()>=self.next_scan
    local regular_ready=self.dirty.count>0 or self.rechecks.count>0 or scan_ready
    if self.verifications.count>0 and self.credits>=4 and deps.now()>=self.next_recipe_check and
      (not self.regular_turn or not regular_ready) then
      self.regular_turn=true;self.next_recipe_check=deps.now()+5
      local key,detail=self.verifications:pop()
      self:_wait("priority_queue",detail)
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
    local refresh=self.refreshes.entries[self.refresh_cursor] or self.refreshes.last
    local remaining=self.refreshes.count
    while refresh and remaining>0 and (refresh.value.in_flight or deps.now()<(refresh.value.retry_at or 0)) do
      refresh=refresh.previous or self.refreshes.last;remaining=remaining-1
    end
    if remaining==0 then refresh=nil end
    if refresh and self.refresh_reads<2 and (not self.refresh_turn or not regular_ready) then
      self.refresh_turn=true;self.refresh_reads=self.refresh_reads+1
      -- Advance independently of admission order, including after failures.
      -- Permanently failing oldest entries must not monopolize small grants.
      self.refresh_cursor=(refresh.previous or self.refreshes.last).key
      local detail=refresh.value
      local ok,value=pcall(function()
        local result=self.adapter:read(detail)
        self.adapter:count(result)
        return result
      end)
      if not ok then
        detail.retry_at=deps.now()+2
        self:_read_failure("exact_lookup_failed","refresh",refresh.key,detail,value)
        return nil,1
      end
      detail.in_flight=detail.refresh
      value.refresh=detail.refresh
      value.isCraftable=nil
      return value,1
    end
    self.refresh_turn=false
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
      self:_wait(lane==0 and "event_queue" or "recheck_queue",dirty)
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
      local started=deps.now()
      local ok,iterator=pcall(self.adapter.iterator,self.adapter)
      if not ok then self:diagnose("scan_unavailable");self.next_scan=deps.now()+10;return nil,1 end
      self.iterator=iterator;self.scan_started_at=started
    end
    local ok,raw=pcall(self.iterator)
    if not ok then self:diagnose("scan_failed");self.iterator=nil;self.scan_started_at=nil;self.next_scan=deps.now()+10;return nil,1 end
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
    local value={quantity=count,observation=self.observation,craftable=craftable,verification=raw.verification,refresh=raw.refresh}
    local known=self.ids:get(key)
    if known and known.label==(raw.label or raw.name) and (known.tag or "")== (raw.tag or "") then value.id=known.id else value.item=self.adapter:definition(raw) end
    -- Only full definitions need their representation retained until binding.
    -- Short-ID rows must not hide a large raw tag behind a tiny wire record.
    return {key=key,label=raw.label or raw.name,tag=value.item and raw.tag or nil,item=value.item~=nil,verification=raw.verification,refresh=raw.refresh,
      sampled_at=self.latency and deps.now() or nil,encoded=json.encode(value,self.client.request_bytes)}
  end
  function self:_flush()
    local pending=self.pending_inventory
    if not pending then return end
    local ack=self:_send("/bridge/v1/inventory",pending.body,"inventory_http")
    if type(ack)~="table" or ack.ack~=pending.sequence or not list(ack.bindings,#pending.entries) then fatal("invalid_inventory_confirmation") end
    local seen={}
    for _,binding in ipairs(ack.bindings) do
      if type(binding)~="table" then fatal("invalid_short_id_binding") end
      local index=binding.index
      if type(index)~="number" or index%1~=0 or index<1 or index>#pending.entries or seen[index] or not pending.entries[index].item then fatal("invalid_short_id_binding") end
      if not pcall(quantity.validate,binding.id) or binding.id=="0" then fatal("invalid_short_id_binding") end
      seen[index]=true
    end
    for index,entry in ipairs(pending.entries) do
      if entry.item and not seen[index] then fatal("missing_short_id_binding") end
    end
    for _,binding in ipairs(ack.bindings) do
      local entry=pending.entries[binding.index]
      self.ids:put(entry.key,{id=binding.id,label=entry.label,tag=entry.tag},#entry.key+#binding.id+#entry.label+#(entry.tag or "")+160)
    end
    for _,entry in ipairs(pending.entries) do
      local queued=self.verifications.entries[entry.key]
      if entry.verification and queued and queued.value.verification==entry.verification then self.verifications:remove(entry.key) end
      local refreshed=self.refreshes.entries[entry.key]
      if entry.refresh and refreshed and refreshed.value.refresh==entry.refresh then self.refreshes:remove(entry.key) end
    end
    account(self,"batches",1);account(self,"updates",#pending.entries);account(self,"bytes",#pending.body)
    if self.latency then
      if pending.sampled_at then self.latency:observe("batch_ack",deps.now()-pending.sampled_at) end
      if pending.scan_completed then self.latency:complete_scan(pending.scan_started_at) end
    end
    if pending.scan_completed then self.first_scan_acked=true end
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
    local completed=self.scan_completed
    if completed then body=body..',"scan_completed":true' end
    body=body.."}";self.scan_completed=false
    for _,entry in ipairs(batch.entries) do entry.encoded=nil end
    self.pending_inventory={body=body,sequence=seq,entries=batch.entries,scan_completed=completed,
      sampled_at=batch.sampled_at,scan_started_at=completed and self.scan_started_at or nil}
    if completed then self.scan_started_at=nil end
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
        if self.latency and cost>0 then self.latency:observe("collection",deps.now()-before) end
        if raw then
          local ok,value=pcall(self._candidate,self,raw)
          if ok then candidate=value
          else
            self:diagnose("item_unrepresentable")
            if raw.refresh then
              local queued=self.refreshes.entries[self.adapter:key(raw)]
              if queued and queued.value.refresh==raw.refresh then queued.value.in_flight=nil;queued.value.retry_at=deps.now()+2 end
            end
          end
        elseif cost==0 or self.scan_completed then idle=true end
      end
      if candidate then
        if #candidate.encoded+256>limit then
          self:diagnose("entry_exceeds_batch_budget")
          local queued=self.refreshes.entries[candidate.key]
          if candidate.refresh and queued and queued.value.refresh==candidate.refresh then queued.value.in_flight=nil;queued.value.retry_at=deps.now()+2 end
        elseif batch.size+#candidate.encoded+1>batch.limit then self.carry=candidate;send=true
        else
          batch.entries[#batch.entries+1]=candidate;batch.encoded[#batch.encoded+1]=candidate.encoded
          if not batch.sampled_at then batch.sampled_at=candidate.sampled_at end
          batch.size=batch.size+#candidate.encoded+1
          send=candidate.verification~=nil or candidate.refresh~=nil
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
    local control_started_at=deps.now()
    local control_ok,control_result=pcall(self._control,self)
    if not control_ok and not transient(control_result) then error(control_result,0) end
    if control_ok then control_started_at=control_result end
    local control_completed_at=deps.now()
    local inventory_ok,inventory_result=pcall(self._inventory,self)
    if not inventory_ok and not transient(inventory_result) then error(inventory_result,0) end
    if not control_ok then self:diagnose("control_transport_failed") end
    if not inventory_ok then self:diagnose("inventory_transport_failed") end
    return {control_ok=control_ok,inventory_ok=inventory_ok,control_started_at=control_started_at,control_completed_at=control_completed_at,
      inventory_work=control_ok and inventory_ok and inventory_result==true}
  end
  return self
end
return M
