local json=require("aeorbit.json")
local quantity=require("aeorbit.quantity")
local cache=require("aeorbit.cache")
local tracker=require("aeorbit.tracker")
local read_diagnostics=require("aeorbit.read_diagnostics")
local M={}
local read_cycle={"order","refresh","refresh","background"}
local read_sources={order="priority",refresh="refresh",background="recheck"}
local function fatal(code) error({code=code,message=code,fatal=true},0) end
local function integer(value,minimum,maximum)
  return type(value)=="number" and value%1==0 and value>=minimum and value<=maximum
end
local function token(value)
  return type(value)=="string" and #value==32 and not value:find("[^0-9a-f]")
end
local function transient(value) return type(value)=="table" and value.fatal==false end

function M.new(config,deps)
  assert(config.bridge_mode=="cooperative","cooperative: explicit cooperative mode required")
  local role=config.role or "primary"
  assert(role=="primary" or role=="quantity_worker","cooperative: invalid role")
  local primary=role=="primary"
  local memory=deps.free_memory()
  local task_bytes=math.max(256,math.min(16384,math.floor(memory/16)))
  local self={deps=deps,config=config,adapter=deps.adapter,client=deps.client,role=role,diagnostics={},diagnostic_samples=0,
    ids=cache.new(config.cache_entries or 256,math.max(256,math.min(config.cache_bytes or math.floor(memory/12),math.floor(memory/8)))),
    dirty=cache.new(16,task_bytes),tasks=cache.new(16,task_bytes),candidates=cache.new(16,task_bytes),
    attempts=cache.new(64,16384),
    grants=cache.new(64,16384),
    task_bytes=task_bytes,inventory_seq="0",poll_seq="0",observation="0",credits=0,poll_delay=1,
    uploaded=0,next_scan=0,next_cpu=0,next_recipe_check=0,task_turn=true,read_opportunity=1}
  function self:diagnose(code)
    if not self.diagnostics[code] then
      local count=0;for _ in pairs(self.diagnostics) do count=count+1 end
      if count>=16 then return end
      self.diagnostics[code]="0"
    end
    self.diagnostics[code]=quantity.increment(self.diagnostics[code])
  end
  function self:_read_failure(counter,stage,source,raw,item_id,error_value)
    self:diagnose(counter)
    if self.diagnostic_samples==0 then return end
    local code=stage=="recipe" and "recipe_lookup_failed" or "exact_lookup_failed"
    self.read_diagnostics:record(code,source,raw,item_id,error_value,stage)
  end
  function self:_memory_ready()
    return deps.free_memory()>=self.client.response_bytes*4+4096
  end
  function self:_discovery_capacity()
    -- Lossy event hints must leave real admission room to obtain drain credits.
    local slots=math.min(8,math.max(0,(self.limits and self.limits.read_slots or 16)-1))
    local bytes=math.min(self.task_bytes,math.floor(deps.free_memory()/16),
      self.limits and self.limits.read_bytes or self.task_bytes)
    return slots,math.floor(bytes/2)
  end
  function self:start()
    local nonce=self.client:post("/bridge/v2/boot-id",{})
    if type(nonce.boot_id)~="string" or #nonce.boot_id<1 then fatal("invalid_boot_identity") end
    local response=math.max(2048,math.min(config.max_response_bytes or 16384,math.floor(deps.free_memory()/16)))
    self.client.response_bytes=response
    local capabilities=self.adapter:capabilities()
    if not primary then
      capabilities={inventory=true,exact_quantities=true,quantity_refresh=true,craft=false,cpu_output=false}
    end
    local w=self.client:post("/bridge/v2/hello",{protocol=2,boot_id=nonce.boot_id,
      source=config.source or "oc",capabilities=capabilities,max_response_bytes=response})
    if w.protocol~=2 or type(w.session_id)~="string" or #w.session_id==0 or
      type(w.device_id)~="string" or #w.device_id==0 or w.role~=role or
      w.inventory_ack~="0" or w.poll_ack~="0" or type(w.limits)~="table" then fatal("invalid_welcome") end
    local l=w.limits
    if not integer(l.batch_items,1,512) or not integer(l.batch_bytes,512,1048576) or
      not integer(l.response_bytes,512,response) or not integer(l.read_slots,0,16) or
      not integer(l.read_bytes,0,16384) or not integer(l.cpus,1,64) or
      not integer(l.poll_millis or 1000,1,30000) then fatal("invalid_device_limits") end
    self.session=w.session_id;self.device_id=w.device_id;self.limits=l
    self.background_recipe_inconclusive=primary and l.background_recipe_inconclusive==true
    if integer(l.diagnostic_samples,1,4) then
      self.diagnostic_samples=l.diagnostic_samples
      self.read_diagnostics=read_diagnostics.new(deps.now)
    end
    self.client.response_bytes=l.response_bytes
    self.client.request_bytes=math.min(config.max_batch_bytes or 16384,l.batch_bytes)
    self.poll_delay=(l.poll_millis or 1000)/1000
    if primary then
      if next(deps.journal.data.attempts or {}) then self:diagnose("previous_attempts_need_review") end
      deps.journal:save({boot_id=nonce.boot_id,attempts={},completed=json.array({})})
      self.tracker=tracker.new(self.adapter,deps.journal,16)
    end
    self.started=true
  end
  function self:_health()
    local diagnostics=json.array({})
    local keys={};for key in pairs(self.diagnostics) do keys[#keys+1]=key end;table.sort(keys)
    for _,key in ipairs(keys) do diagnostics[#diagnostics+1]={code=key,count=self.diagnostics[key]} end
    return {free_memory=quantity.from_number(deps.free_memory()),diagnostics=diagnostics}
  end
  function self:_hint(raw)
    local key=self.adapter:key(raw)
    if self.dirty.entries[key] then return end
    local known=self.ids:get(key)
    local hint=known and {id=known.id} or {item=self.adapter:definition(raw)}
    local size=#json.encode(hint)*2+#key+128
    if self.dirty.count>=16 or self.dirty.bytes+size>self.task_bytes or not self.dirty:put(key,hint,size) then
      self:diagnose("hint_queue_full")
    end
  end
  function self:on_item_changed(raw)
    if not primary then return end
    local ok=pcall(function()
      local detail=self.adapter:descriptor(raw)
      local key=self.adapter:key(detail)
      if self.ids:get(key) then self:_hint(detail);return end
      if self.candidates.entries[key] then return end
      local size=#json.encode(self.adapter:definition(detail))*2+#key+256
      local budget=math.min(self.task_bytes,math.floor(deps.free_memory()/16))
      local slots,bytes=self:_discovery_capacity()
      if self.candidates.count>=slots or self.candidates.bytes+size>bytes or
        self.tasks.count+self.candidates.count>=16 or self.tasks.bytes+self.candidates.bytes+size>budget then
        self:diagnose("discovery_queue_full");return
      end
      self.candidates:put(key,{raw=detail,retry_at=0,failures=0},size)
    end)
    if not ok then self:diagnose("event_identity_unavailable") end
  end
  function self:_expire()
    local entry=self.tasks.last
    while entry do
      local previous=entry.previous
      if deps.now()>=entry.value.deadline then self.tasks:remove(entry.key) end
      entry=previous
    end
  end
  function self:_validate_read(read)
    if type(read)~="table" or not token(read.assignment_id) or
      (read.kind~="quantity" and read.kind~="quantity_recipe") or
      (read.purpose~="refresh" and read.purpose~="order" and read.purpose~="background") or
      not integer(read.ttl_millis,1,30000) or (not primary and read.kind~="quantity") then fatal("invalid_read_assignment") end
    local valid=pcall(quantity.validate,read.id)
    if not valid or read.id=="0" then fatal("invalid_read_identity") end
    local ok,raw=pcall(self.adapter.from_definition,self.adapter,read.item)
    if not ok then fatal("invalid_read_identity") end
    return raw
  end
  function self:_admit(read,raw,started)
    local id=read.assignment_id
    local previous=self.attempts:get(id)
    -- A replay may shorten an attempt but can never renew it or re-read a result.
    local deadline=started+read.ttl_millis/1000
    if previous then
      if previous.item_id~=read.id or previous.kind~=read.kind or previous.key~=self.adapter:key(raw) then fatal("assignment_identity_changed") end
      previous.deadline=math.min(previous.deadline,deadline)
      local queued=self.tasks.entries[id]
      if queued then queued.value.deadline=previous.deadline end
      return
    end
    local key=self.adapter:key(raw)
    local cost=#json.encode(read)*2+#key+256
    local fast=0
    for _,entry in pairs(self.tasks.entries) do if entry.value.purpose~="background" and not entry.value.in_flight then fast=fast+1 end end
    local budget=math.min(self.task_bytes,math.floor(deps.free_memory()/16),self.limits.read_bytes)
    local candidate=self.candidates.entries[key]
    local candidate_count=self.candidates.count-(candidate and 1 or 0)
    local candidate_bytes=self.candidates.bytes-(candidate and candidate.size or 0)
    if deadline<=deps.now() or self.tasks.count+candidate_count>=16 or self.tasks.bytes+candidate_bytes+cost>budget or
      (read.purpose~="background" and fast>=8) then self:diagnose("read_queue_full");return end
    if candidate then self.candidates:remove(key) end
    -- Replace only unread ownership. Frozen transport retains its own immutable token.
    local entry=self.tasks.last
    while entry do
      local next_entry=entry.previous
      if entry.value.key==key then self.tasks:remove(entry.key) end
      entry=next_entry
    end
    local attempt={deadline=deadline,item_id=read.id,kind=read.kind,key=key}
    self.attempts:put(id,attempt,160)
    local task={raw=raw,id=read.id,assignment_id=id,kind=read.kind,purpose=read.purpose,
      deadline=deadline,key=key,retry_at=0,failures=0}
    self.tasks:put(id,task,cost)
    self.ids:put(key,{id=read.id,label=raw.label,tag=raw.tag},#key+#read.id+#(raw.label or "")+#(raw.tag or "")+160)
  end
  function self:_control()
    self:_expire()
    if not self.pending_poll then
      local ready=self:_memory_ready()
      if ready then
        local slots,bytes=self:_discovery_capacity()
        while self.candidates.count>slots or self.candidates.bytes>bytes do
          self.candidates:remove(self.candidates.last.key)
          self:diagnose("discovery_queue_full")
        end
      end
      local remaining=math.max(0,math.min(self.task_bytes,math.floor(deps.free_memory()/16),self.limits.read_bytes)-self.tasks.bytes-self.candidates.bytes)
      local body={session_id=self.session,sequence=quantity.increment(self.poll_seq),health=self:_health(),
        read_slots=ready and math.max(0,self.limits.read_slots-self.tasks.count-self.candidates.count) or 0,read_bytes=ready and remaining or 0}
      local hints={}
      if primary then
        body.reports=json.array({})
        for _,report in ipairs(self.tracker:reports()) do
          body.reports[#body.reports+1]=report
          if #body.reports>16 or not pcall(json.encode,body,self.client.request_bytes) then body.reports[#body.reports]=nil;break end
        end
        if deps.now()>=self.next_cpu then
          self.next_cpu=deps.now()+(config.cpu_interval or 10)
          local ok,cpus,truncated=pcall(self.adapter.cpus,self.adapter)
          if ok then
            body.cpus=json.array({});body.cpus_truncated=truncated
            for _,cpu in ipairs(cpus) do
              body.cpus[#body.cpus+1]=cpu
              if #body.cpus>self.limits.cpus or not pcall(json.encode,body,self.client.request_bytes-64) then
                body.cpus[#body.cpus]=nil;body.cpus_truncated=true;break
              end
            end
          else self:diagnose("cpu_unavailable") end
        end
        if self.dirty.count>0 then
          body.hints=json.array({})
          local entry=self.dirty.last
          while entry and #hints<16 do
            body.hints[#body.hints+1]=entry.value
            if not pcall(json.encode,body,self.client.request_bytes) then body.hints[#body.hints]=nil;break end
            hints[#hints+1]={key=entry.key,value=entry.value};entry=entry.previous
          end
        end
      end
      local samples,diagnostic_token
      if self.read_diagnostics then samples,diagnostic_token=self.read_diagnostics:snapshot(self.diagnostic_samples) end
      if samples then
        body.health.read_failures=samples
        while #samples>0 and not pcall(json.encode,body,self.client.request_bytes) do samples[#samples]=nil end
        if #samples==0 then body.health.read_failures=nil;diagnostic_token=nil end
      end
      self.pending_poll={body=json.encode(body,self.client.request_bytes),sequence=body.sequence,
        reports=body.reports,hints=hints,diagnostic_token=diagnostic_token}
    end
    local pending=self.pending_poll
    local started=deps.now()
    local ack=self.client:send("/bridge/v2/poll",pending.body)
    local no_grant=ack.scan_budget==0 and (ack.grant_id==nil or ack.grant_id=="") and
      (ack.grant_ttl_millis==nil or ack.grant_ttl_millis==0)
    local valid_grant=type(ack.grant_id)=="string" and #ack.grant_id>=1 and #ack.grant_id<=128 and
      integer(ack.grant_ttl_millis,1,30000)
    if ack.ack~=pending.sequence or type(ack.commands)~="table" or #ack.commands>16 or
      type(ack.reads)~="table" or #ack.reads>16 or ack.verify~=nil or ack.refresh~=nil or
      not integer(ack.scan_budget,0,512) or not integer(ack.poll_millis,1,30000) or
      (not no_grant and not valid_grant) or (not primary and #ack.commands>0) then fatal("invalid_poll_confirmation") end
    local raw={}
    for index,read in ipairs(ack.reads) do raw[index]=self:_validate_read(read) end
    if primary then
      self.tracker:ack(pending.reports)
      for _,command in ipairs(ack.commands) do self.tracker:accept(command) end
      for _,hint in ipairs(pending.hints) do
        local current=self.dirty.entries[hint.key]
        if current and current.value==hint.value then self.dirty:remove(hint.key) end
      end
    end
    if not no_grant then
      for index,read in ipairs(ack.reads) do self:_admit(read,raw[index],started) end
      if self.grant_id~=ack.grant_id then
        self.credits=self.grants:get(ack.grant_id) and 0 or ack.scan_budget
        self.grants:put(ack.grant_id,true,#ack.grant_id+128)
        self.grant_id=ack.grant_id
        self.grant_deadline=started+ack.grant_ttl_millis/1000
      else
        self.grant_deadline=math.min(self.grant_deadline,started+ack.grant_ttl_millis/1000)
      end
    end
    self.poll_seq=ack.ack;self.pending_poll=nil;self.poll_delay=ack.poll_millis/1000
    if pending.diagnostic_token then self.read_diagnostics:ack(pending.diagnostic_token) end
    return started
  end
  function self:_available()
    if not self.grant_deadline or deps.now()>=self.grant_deadline then self.credits=0 end
    return self.credits
  end
  function self:_candidate(raw,task,inconclusive)
    local key=self.adapter:key(raw)
    local count=self.adapter:count(raw)
    self.observation=quantity.increment(self.observation)
    local craftable=raw.isCraftable
    if task and task.kind=="quantity" then craftable=nil end
    assert(craftable==nil or type(craftable)=="boolean","invalid recipe observation")
    local value={quantity=count,observation=self.observation,craftable=craftable==nil and json.null or craftable}
    if inconclusive then value.recipe_inconclusive=true end
    local entry={key=key,label=raw.label or raw.name,tag=raw.tag}
    if task then value.id=task.id;value.assignment_id=task.assignment_id;entry.assignment_id=task.assignment_id
    else value.item=self.adapter:definition(raw);entry.item=true end
    entry.encoded=json.encode(value,self.client.request_bytes)
    return entry
  end
  function self:_read_task()
    self:_expire()
    for _=1,#read_cycle do
      local purpose=read_cycle[self.read_opportunity]
      self.read_opportunity=self.read_opportunity%#read_cycle+1
      local entry=self.tasks.last
      while entry do
        local task=entry.value
        local cost=task.kind=="quantity_recipe" and 4 or 1
        if task.purpose==purpose and not task.in_flight and deps.now()>=task.retry_at and cost<=self:_available() and
          (task.kind~="quantity_recipe" or deps.now()>=self.next_recipe_check) then
          -- Rotate the lane before native work so failures cannot hold its next opportunity.
          self.tasks:get(entry.key)
          self.credits=self.credits-cost
          if task.kind=="quantity_recipe" then self.next_recipe_check=deps.now()+5 end
          local stage="quantity"
          local ok,result=pcall(function()
            local raw=self.adapter:read(task.raw)
            local inconclusive
            if task.kind=="quantity_recipe" then
              stage="recipe"
              local recipe_ok,craftable=pcall(self.adapter.recipe_status,self.adapter,raw)
              if recipe_ok then
                raw.isCraftable=craftable
              elseif self.background_recipe_inconclusive and task.purpose=="background" then
                self:_read_failure("read_failed","recipe",read_sources[task.purpose],task.raw,task.id,craftable)
                raw.isCraftable=nil
                inconclusive=true
              else
                error(craftable,0)
              end
            end
            if deps.now()>=task.deadline then return nil end
            stage="candidate"
            return self:_candidate(raw,task,inconclusive)
          end)
          if ok and result then task.in_flight=true;return result,true end
          if not ok then
            self:_read_failure("read_failed",stage,read_sources[task.purpose],task.raw,task.id,result)
            task.failures=task.failures+1
            task.retry_at=math.min(task.deadline,deps.now()+math.min(30,2^(math.min(task.failures,6)-1)))
          end
          return nil,true
        end
        entry=entry.previous
      end
    end
    return nil,false
  end
  function self:_discover()
    if not primary or self:_available()<1 then return nil,false end
    local candidate=self.candidates.last
    while candidate and deps.now()<candidate.value.retry_at do candidate=candidate.previous end
    local scan_ready=self.iterator or deps.now()>=self.next_scan
    if candidate and (not scan_ready or not self.discovery_scan_turn) then
      self.discovery_scan_turn=true
      self.credits=self.credits-1
      local detail=candidate.value
      self.candidates:get(candidate.key)
      local stage="quantity"
      local ok,result=pcall(function()
        local raw=self.adapter:read(detail.raw)
        stage="candidate"
        return self:_candidate(raw)
      end)
      if ok then self.candidates:remove(candidate.key);return result,true end
      detail.failures=detail.failures+1
      detail.retry_at=deps.now()+math.min(30,2^(math.min(detail.failures,6)-1))
      self:_read_failure("discovery_read_failed",stage,"event",detail.raw,nil,result)
      return nil,true
    end
    if not scan_ready then return nil,false end
    self.discovery_scan_turn=false
    self.credits=self.credits-1
    if not self.iterator then
      local ok,value=pcall(self.adapter.iterator,self.adapter)
      if not ok then self:diagnose("scan_unavailable");self.next_scan=deps.now()+10;return nil,true end
      self.iterator=value
    end
    local ok,raw=pcall(self.iterator)
    if not ok then self:diagnose("scan_failed");self.iterator=nil;self.next_scan=deps.now()+10;return nil,true end
    if not raw then
      self.iterator=nil;self.scan_completed=true;self.next_scan=deps.now()+(config.scan_interval or 60)
      return nil,true
    end
    local represented,result=pcall(function()
      local known=self.ids:get(self.adapter:key(raw))
      if known and known.label==(raw.label or raw.name) and (known.tag or "")==(raw.tag or "") then
        self:_hint(raw);return nil
      end
      return self:_candidate(raw)
    end)
    if not represented then self:diagnose("item_unrepresentable");return nil,true end
    return result,true
  end
  function self:_flush()
    local pending=self.pending_inventory
    if not pending then return end
    local ack=self.client:send("/bridge/v2/inventory",pending.body)
    if ack.ack~=pending.sequence or type(ack.bindings)~="table" then fatal("invalid_inventory_confirmation") end
    local seen={}
    for _,binding in ipairs(ack.bindings) do
      local index=binding.index
      if not integer(index,1,#pending.entries) or seen[index] or not pending.entries[index].item or
        not pcall(quantity.validate,binding.id) or binding.id=="0" then fatal("invalid_short_id_binding") end
      seen[index]=true
    end
    for index,entry in ipairs(pending.entries) do
      if entry.item and not seen[index] then fatal("missing_short_id_binding") end
    end
    for _,binding in ipairs(ack.bindings) do
      local entry=pending.entries[binding.index]
      self.ids:put(entry.key,{id=binding.id,label=entry.label,tag=entry.tag},
        #entry.key+#binding.id+#entry.label+#(entry.tag or "")+160)
    end
    for _,entry in ipairs(pending.entries) do
      if entry.assignment_id then self.tasks:remove(entry.assignment_id) end
    end
    self.inventory_seq=ack.ack;self.uploaded=self.uploaded+#pending.entries;self.pending_inventory=nil
  end
  function self:_upload(entry)
    local entries=json.array({})
    local updates=json.array({})
    if entry then entries[1]=entry;updates[1]=json.decode(entry.encoded) end
    local sequence=quantity.increment(self.inventory_seq)
    local body={session_id=self.session,sequence=sequence,updates=updates}
    if self.scan_completed then body.scan_completed=true end
    local ok,encoded=pcall(json.encode,body,math.min(self.client.request_bytes,math.max(512,math.floor(deps.free_memory()/8))))
    if not ok then
      self:diagnose("entry_exceeds_batch_budget")
      if entry and entry.assignment_id then
        local queued=self.tasks.entries[entry.assignment_id]
        if queued then queued.value.in_flight=nil;queued.value.retry_at=deps.now()+1 end
      end
      return
    end
    self.scan_completed=false
    self.pending_inventory={body=encoded,sequence=sequence,entries=entries}
    self:_flush()
  end
  function self:_inventory()
    self:_flush()
    if not self:_memory_ready() then self:diagnose("low_memory");return false end
    local started=deps.now()
    while self:_available()>0 do
      local entry,worked
      if self.task_turn then entry,worked=self:_read_task() else entry,worked=self:_discover() end
      self.task_turn=not self.task_turn
      if not worked then
        if self.task_turn then entry,worked=self:_read_task() else entry,worked=self:_discover() end
      end
      if entry or self.scan_completed then self:_upload(entry) end
      if not worked then return false end
      if deps.yield then deps.yield() end
      if deps.now()-started>=(config.scan_slice_seconds or 0.05) then break end
    end
    return self:_available()>0
  end
  function self:continue_inventory()
    if self:_available()<=0 and not self.pending_inventory then return false end
    local ok,result=pcall(self._inventory,self)
    if not ok then
      if not transient(result) then error(result,0) end
      self:diagnose("inventory_transport_failed");return false
    end
    return result==true
  end
  function self:tick()
    assert(self.started,"cooperative: start first")
    if self.tracker then self.tracker:pump() end
    local started=deps.now()
    local control_ok,result=pcall(self._control,self)
    if not control_ok and not transient(result) then error(result,0) end
    if control_ok then started=result else self:diagnose("control_transport_failed") end
    local completed=deps.now()
    local inventory_ok,more=pcall(self._inventory,self)
    if not inventory_ok and not transient(more) then error(more,0) end
    if not inventory_ok then self:diagnose("inventory_transport_failed") end
    return {control_ok=control_ok,inventory_ok=inventory_ok,control_started_at=started,control_completed_at=completed,
      inventory_work=control_ok and inventory_ok and more==true}
  end
  return self
end
return M
