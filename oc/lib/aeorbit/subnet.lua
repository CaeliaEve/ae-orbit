-- Dedicated subnet monitoring. Selected stock uses exact reads; explicitly
-- isolated small subnets may discover stock with bounded low-frequency lists.
local json=require('aeorbit.json')
local cancel=require('aeorbit.cancel')
local adapter=require('aeorbit.adapter')
local sha=require('aeorbit.sha256')
local M={}
local function token(v)return type(v)=='string' and #v==32 and not v:find('[^a-f0-9]')end
function M.discover(component,check)
 local out=json.array({});local seen={}
 for _,kind in ipairs({'me_interface','me_controller'})do
  for address in component.list(kind,true)do
   if not seen[address]then
    if #out>=8 then return out end;seen[address]=true;if check then check()end
    local ok,p=pcall(component.proxy,address);if not ok then cancel.rethrow(p)end
    out[#out+1]={address=address,type=kind,supported=ok and adapter.has_method(p,'getItemInNetwork') or false}
   end
  end
 end
 return out
end
local function welcome(w)
 assert(type(w)=='table' and token(w.session_id) and token(w.subnet_id) and type(w.sequence)=='number' and w.sequence%1==0 and w.sequence>=0,'invalid subnet welcome')
 assert(type(w.component_address)=='string' and (w.component_address=='' or #w.component_address==36) and type(w.revision)=='number' and w.revision>=1 and w.revision%1==0,'invalid subnet binding')
 assert(type(w.poll_millis)=='number' and w.poll_millis>=250 and w.poll_millis<=30000 and type(w.interval_seconds)=='number' and w.interval_seconds>=1 and w.interval_seconds<=3600,'invalid subnet timing')
 assert(type(w.item_count)=='number' and w.item_count>=0 and w.item_count<=64 and type(w.reads)=='table' and #w.reads<=16,'invalid subnet reads')
 assert(type(w.budget)=='number' and w.budget>=0 and w.budget<=16 and w.budget%1==0 and #w.reads<=w.budget,'invalid subnet grant')
 if #w.reads>0 then assert(token(w.grant_id) and type(w.grant_ttl_millis)=='number' and w.grant_ttl_millis>=1 and w.grant_ttl_millis<=30000,'invalid subnet grant')end
 local seen={}
 for _,r in ipairs(w.reads)do
  assert(token(r.item_id) and token(r.assignment_id) and not seen[r.item_id] and r.revision==w.revision and type(r.ttl_millis)=='number' and r.ttl_millis>=1 and r.ttl_millis<=30000,'invalid subnet assignment')
  assert(type(r.item)=='table' and (r.item.kind=='item' or r.item.kind=='fluid') and r.item.exact==true,'invalid subnet identity')
  seen[r.item_id]=true
 end
 if w.snapshot then
  local a=w.snapshot;assert(token(a.id) and (a.kind=='item' or a.kind=='fluid') and a.revision==w.revision and a.max_items==128 and a.ttl_millis==60000,'invalid subnet snapshot assignment')
 end
end
function M.snapshot(proxy,a,check,free)
 local r={id=a.id,kind=a.kind,revision=a.revision,items=json.array({})}
 local function reject(code)r.items=json.array({});r.error=code;return r end
 if free and free()<524288 then return reject('memory_low')end
 local method=a.kind=='item' and 'getItemsInNetwork' or 'getFluidsInNetwork'
 if not adapter.has_method(proxy,method)then return reject('unsupported')end
 local ok,value=pcall(function()
  if check then check()end
  local rows,reason=proxy[method]();assert(type(rows)=='table' and reason==nil,'list unavailable')
  if #rows>128 then return 'too_many_items'end
  local count=0;for k in pairs(rows)do count=count+1;assert(type(k)=='number' and k>=1 and k<=#rows and k%1==0,'invalid list')end
  assert(count==#rows,'sparse list')
  local reader=adapter.new(proxy,{inventory_mode='exact_only',role='quantity_worker'});local seen={}
  for _,raw in ipairs(rows)do
   if check then check()end
   assert(type(raw)=='table','invalid stack')
   if a.kind=='fluid' then raw={kind='fluid',name=raw.name,size=raw.size or raw.amount,label=raw.label,damage=0,hasTag=raw.hasTag,tag=raw.tag}end
   local quantity=reader:count(raw)
   if quantity~='0'then
    local key=reader:key(raw);assert(not seen[key],'duplicate identity');seen[key]=true
    r.items[#r.items+1]={item=reader:definition(raw),quantity=quantity}
   end
  end
  return nil
 end)
 if not ok then cancel.rethrow(value);return reject('read_failed')end
 if value then return reject(value)end
 local encoded,reason=pcall(json.encode,r,45000)
 if not encoded then cancel.rethrow(reason);return reject('payload_large')end
 return r
end
function M.new(config,deps)
 local hash=sha.new();hash:update((deps.address or 'subnet')..':'..tostring(deps.now())..':'..tostring(math.random())..':'..tostring(os.time()))
 local self={boot=hash:finish():sub(1,32),sequence=0,session=nil,pending=nil,samples=json.array({}),next_discovery=0,cycle_seen={},cycle_start=deps.now(),cycle_millis=0,reads=0}
 function self:start()
  local w=deps.client:send('/bridge/subnet/hello',json.encode({boot_id=self.boot,components=M.discover(deps.component,deps.check),inventory_discovery=true,inventory_snapshot=true},4096));welcome(w)
  self.session,self.sequence,self.w=w.session_id,w.sequence,w
 end
 function self:tick()
  local started=deps.now()
  if not self.pending then
   local body={session_id=self.session,sequence=self.sequence+1,free_memory=deps.free_memory(),samples=self.samples,cycle_millis=self.cycle_millis,snapshot=self.snapshot}
   if started>=self.next_discovery then body.components=M.discover(deps.component,deps.check);self.next_discovery=started+30 end
   self.pending=json.encode(body,65536)
  end
  local w=deps.client:send('/bridge/subnet/poll',self.pending);welcome(w)
  assert(w.session_id==self.session and w.sequence==self.sequence+1,'invalid subnet receipt')
  self.pending=nil;self.sequence=w.sequence;self.samples=json.array({});self.snapshot=nil
  if self.w.revision~=w.revision or self.w.component_address~=w.component_address or self.w.item_count~=w.item_count then
   self.reader=nil;self.cycle_seen={};self.cycle_start=deps.now();self.cycle_millis=0
  end
  self.w=w
  if #w.reads==0 and not w.snapshot then return w.poll_millis/1000 end
  local function getReader()
   if not self.reader then
    local proxy=assert(deps.component.proxy(w.component_address),'component_missing')
    assert(adapter.has_method(proxy,'getItemInNetwork'),'interface_unsupported')
    for _,name in ipairs({'setItemEventSubscription','setFluidEventSubscription','setEssentiaEventSubscription'})do
     if adapter.has_method(proxy,name)then assert(proxy[name](false)~=false,'subscription_disable_failed')end
    end
    self.reader=adapter.new(proxy,{inventory_mode='exact_only',role='quantity_worker'})
   end
   return self.reader
  end
  local deadline=started+(w.grant_ttl_millis or 0)/1000
  for _,r in ipairs(w.reads)do
   if deps.now()>=deadline then break end
   if deps.check then deps.check()end
   local result={item_id=r.item_id,assignment_id=r.assignment_id,revision=r.revision,sample_millis=math.floor(deps.now()*1000)}
   if deps.free_memory()<262144 then result.error='memory_low'
   else
    local ok,value=pcall(function()
     local reader=getReader();local raw=reader:from_definition(r.item)
     return reader:count(reader:read(raw))
    end)
    if not ok then cancel.rethrow(value);result.error='read_failed';self.reader=nil else result.quantity=value;self.reads=self.reads+1 end
   end
   result.sample_millis=math.floor(deps.now()*1000)
   self.samples[#self.samples+1]=result
   if r.purpose~='discovery' then self.cycle_seen[r.item_id]=true end
   local count=0;for _ in pairs(self.cycle_seen)do count=count+1 end
   if w.item_count>0 and count>=w.item_count then self.cycle_millis=math.max(1,math.floor((deps.now()-self.cycle_start)*1000));self.cycle_seen={};self.cycle_start=deps.now()end
   if deps.yield then deps.yield()end
  end
  if w.snapshot then
   local ok,value=pcall(function()return M.snapshot(getReader().me,w.snapshot,deps.check,deps.free_memory)end)
   if not ok then cancel.rethrow(value);self.reader=nil;value={id=w.snapshot.id,kind=w.snapshot.kind,revision=w.snapshot.revision,items=json.array({}),error='read_failed'}end
   self.snapshot=value
  end
  return 0 -- Upload results promptly; never wait for a whole scan.
 end
 return self
end
function M.run(context,path,report,mode)
 local ok,err=xpcall(function()
  local component,computer=require('component'),require('computer')
  local config=assert(loadfile(path or '/etc/ae-orbit.cfg'))()
  assert(config.role=='subnet_monitor','subnet configuration required')
  local internet=assert(component.getPrimary('internet'))
  local client=require('aeorbit.client').new(internet,{url=config.url,token=config.token,response_bytes=32768,request_bytes=65536},{now=computer.uptime,sleep=context.sleep,check=context.check})
  local bridge=M.new(config,{component=component,client=client,address=computer.address(),now=computer.uptime,free_memory=computer.freeMemory,check=context.check,yield=function()context.sleep(0)end})
  bridge:start();local backoff,next_status=1,0
  if report then report('running','connected',bridge.session)end
  while true do
   context.check();local ticked,delay=pcall(bridge.tick,bridge)
   if not ticked then cancel.rethrow(delay);if type(delay)~='table' or delay.fatal~=false then error(delay,0)end;backoff=math.min(backoff*2,30);delay=backoff;if report then report('backoff','transport_failed',bridge.session)end
   else backoff=1;if report then report('running','connected',bridge.session)end end
   if mode=='foreground' and computer.uptime()>=next_status then print(string.format('Subnet: reads=%d cycle=%.2fs free=%d KiB',bridge.reads,bridge.cycle_millis/1000,math.floor(computer.freeMemory()/1024)));next_status=computer.uptime()+60 end
   context.sleep(math.max(0,delay))
  end
 end,function(e)return e end)
 if ok or cancel.is_stop(err)then return {kind='stopped',code='interrupted'}end
 return {kind=type(err)=='table' and err.fatal==true and 'blocked' or 'crashed',code=type(err)=='table' and err.code or 'runtime_error'}
end
return M
