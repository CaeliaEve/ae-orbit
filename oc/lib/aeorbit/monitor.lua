-- Reviewed read-only monitoring. No ME proxy, enumeration, subscription or control.
local json=require('aeorbit.json')
local cancel=require('aeorbit.cancel')
local sha=require('aeorbit.sha256')
local M={}
local function energy(s)
 return type(s)=='string' and #s>=1 and #s<=128 and s:match('^%d+$') and (s=='0' or s:sub(1,1)~='0')
end
local function compare(a,b) if #a~=#b then return #a<#b and -1 or 1 end;return a==b and 0 or a<b and -1 or 1 end
local function capacitorIdentity(proxy)
 if not proxy or not proxy.getName then return false end
 local ok,name=pcall(function()return proxy.getName()end)
 if not ok then cancel.rethrow(name) end
 return ok and name=='multimachine.supercapacitor'
end
function M.sample(proxy)
 if not proxy then return nil,nil,'component_missing' end
 -- Compound adapters can be named gt_machine by Computronics. Generic GT
 -- machines also expose energy methods, so recheck the controller identity.
 if proxy.type=='gt_machine' and not capacitorIdentity(proxy) then return nil,nil,'controller_mismatch' end
 if proxy.type and proxy.type~='gt_machine' and proxy.type~='lsc' and proxy.type~='LSC' then return nil,nil,'controller_mismatch' end
 if not proxy.getStoredEUString or not proxy.getEUCapacityString then return nil,nil,'interface_unsupported' end
 local ok,a,b=pcall(function()return proxy.getStoredEUString(),proxy.getEUCapacityString()end)
 if not ok then cancel.rethrow(a);return nil,nil,'read_failed' end
 if not energy(a) or not energy(b) or b=='0' or compare(a,b)>0 then return nil,nil,'invalid_energy' end
 return a,b
end
local flowKeys={
 eu_in='input_eut',eu_out='output_eut',passive_loss='loss_eut',
 ['avg_eu_in.sec']='avg_input_eut',['avg_eu_out.sec']='avg_output_eut',
}
function M.parseFlow(lines)
 if type(lines)~='table' or #lines>64 then return nil,'invalid_format' end
 local flow={source='lsc_sensor_v1',window_seconds=5}
 local prefix='kekztech.infodata.lapotronic_super_capacitor.'
 local bytes=0
 for _,line in ipairs(lines) do
  if type(line)~='string' then return nil,'invalid_format' end
  bytes=bytes+#line;if bytes>16384 then return nil,'invalid_format' end
  if line:sub(1,#prefix)==prefix then
   local key,args=line:sub(#prefix+1):match('^(.-)\\\\(.*)$')
   local field=flowKeys[key]
   if field then
    if flow[field] then return nil,'invalid_format' end
    local value,window=args:match('^(.-)\\\\(.*)$')
    value=value or args
    if value:find(',',1,true) and not value:match('^%d%d?%d?,%d%d%d') then return nil,'invalid_format' end
    if value:find(',',1,true) then
     local first,rest=value:match('^(%d%d?%d?)(,.*)$')
     if not first or rest:gsub(',%d%d%d','')~='' then return nil,'invalid_format' end
    end
    value=value:gsub(',','')
    if not energy(value) then return nil,'invalid_format' end
    if key=='avg_eu_in.sec' or key=='avg_eu_out.sec' then
     if window~='5' then return nil,'invalid_format' end
    elseif window then return nil,'invalid_format' end
    flow[field]=value
   end
  end
 end
 for _,field in pairs(flowKeys) do if not flow[field] then return nil,'invalid_format' end end
 return flow
end
function M.flow(proxy)
 if not proxy or not proxy.getSensorInformation then return nil,'unsupported' end
 local ok,lines=pcall(function()return proxy.getSensorInformation()end)
 if not ok then cancel.rethrow(lines);return nil,'read_failed' end
 return M.parseFlow(lines)
end
function M.discover(component,check,yield)
 local out=json.array({})
 local seen,inspected={},0
 -- Adapter CompoundBlockDriver.cleanName lowercases the preferred "LSC"
 -- name to "lsc". Keep the wire type canonical and support direct drivers.
 -- Computronics' higher-priority name hides LSC on a compound adapter;
 -- its callbacks remain merged into that gt_machine component.
 for _,name in ipairs({'lsc','LSC','gt_machine'}) do
  for address in component.list(name,true) do
   if not seen[address] then
    if inspected>=32 then return out end
    inspected=inspected+1
    seen[address]=true
    if check then check() end
    local ok,p=pcall(component.proxy,address)
    if not ok then cancel.rethrow(p) end
    if name~='gt_machine' or ok and capacitorIdentity(p) then
     out[#out+1]={address=address,type='LSC',supported=ok and p.getStoredEUString~=nil and p.getEUCapacityString~=nil or false}
    end
    if yield then yield() end
   end
  end
 end
 return out
end
local function validWelcome(w)
 if type(w)~='table' or type(w.session_id)~='string' or not w.session_id:match('^[a-f0-9]+$') or #w.session_id~=32 or type(w.sequence)~='number' or w.sequence%1~=0 or w.sequence<0 or w.sequence>1000000000 or type(w.objects)~='table' or #w.objects>16 then return false end
 local seen={}
 for _,o in ipairs(w.objects) do
  if type(o)~='table' or o.plugin_id~='gtnh.lsc' or type(o.id)~='string' or #o.id~=32 or not o.id:match('^[a-f0-9]+$') or seen[o.id] or type(o.component_address)~='string' or #o.component_address~=36 or not o.component_address:match('^[a-f0-9%-]+$') or type(o.interval_seconds)~='number' or o.interval_seconds<10 or o.interval_seconds>3600 or type(o.revision)~='number' or o.revision%1~=0 or o.revision<1 or type(o.installation_revision)~='number' or o.installation_revision%1~=0 or o.installation_revision<1 then return false end
  seen[o.id]=true
 end
 return true
end
function M.new(config,deps)
 local self={session=nil,sequence=0,objects={},due={},pending=nil,next_discovery=0,uploaded=0,cursor=1}
 local components=M.discover(deps.component,deps.check,deps.yield)
 local hash=sha.new()
 hash:update((deps.address or 'monitor')..':'..tostring(deps.now())..':'..tostring(math.random())..':'..tostring(os.time()))
 local boot=hash:finish():sub(1,32)
 function self:start()
  local w=deps.client:send('/bridge/monitor/hello',json.encode({boot_id=boot,modules=json.array({'gtnh.lsc'}),components=components},8192))
  assert(validWelcome(w),'invalid monitor welcome')
  self.session,self.sequence,self.objects=w.session_id,w.sequence,w.objects
 end
 function self:tick()
  if not self.pending then
   local body={session_id=self.session,sequence=self.sequence+1,free_memory=deps.free_memory(),samples=json.array({})}
   local now=deps.now()
   if now>=self.next_discovery then components=M.discover(deps.component,deps.check,deps.yield);body.components=components;self.next_discovery=now+30 end
   local n=#self.objects
   for step=1,n do
    if #body.samples>=4 then break end
    local index=(self.cursor+step-2)%n+1
    local o=self.objects[index]
    if now>=(self.due[o.id] or 0) then
     if deps.check then deps.check() end
     local a,b,err,flow,flowError
     if body.free_memory<262144 then err='memory_low'
     else local ok,p=pcall(deps.component.proxy,o.component_address);if not ok then cancel.rethrow(p);p=nil end;a,b,err=M.sample(p);if not err then flow,flowError=M.flow(p) end end
     body.samples[#body.samples+1]={object_id=o.id,revision=o.revision,installation_revision=o.installation_revision,sample_millis=math.floor(deps.now()*1000),stored_eu=a,capacity_eu=b,error=err,flow=flow,flow_error=flowError}
     self.due[o.id]=now+o.interval_seconds
     self.next_cursor=index%n+1
     if deps.yield then deps.yield() end
    end
   end
   self.pending=json.encode(body,16384)
  end
  -- Retry the immutable bytes until receipt. Never queue an unbounded backlog.
  local w=deps.client:send('/bridge/monitor/poll',self.pending)
  assert(validWelcome(w) and w.session_id==self.session and w.sequence==self.sequence+1,'invalid monitor receipt')
  self.sequence,self.objects,self.pending=w.sequence,w.objects,nil;self.uploaded=self.uploaded+1
  self.cursor=self.next_cursor or self.cursor
  return w
 end
 return self
end
function M.run(context,path,report,mode)
 local foreground=mode=='foreground'
 local ok,err=xpcall(function()
  local component,computer=require('component'),require('computer')
  local config=assert(loadfile(path or '/etc/ae-orbit.cfg'))()
  assert(config.role=='monitor' and type(config.url)=='string' and type(config.token)=='string','monitor configuration is incomplete')
  local internet=config.internet_address and component.proxy(config.internet_address) or component.getPrimary('internet')
  assert(internet,'internet card required')
  local client=require('aeorbit.client').new(internet,{url=config.url,token=config.token,timeout=10,request_bytes=16384,response_bytes=8192},{now=computer.uptime,sleep=context.sleep,check=context.check})
  local bridge=M.new(config,{component=component,client=client,now=computer.uptime,free_memory=computer.freeMemory,address=computer.address(),check=context.check,yield=function()context.sleep(0)end})
  local function publish(state,code) if report then report(state,code,bridge.session) end end
  local function retry(fn)
   local backoff=1
   while true do
    context.check();local success,value=pcall(fn)
    if success then return value end
    cancel.rethrow(value)
    if type(value)~='table' or value.fatal~=false then error(value,0) end
    publish('backoff',value.code);context.sleep(backoff);backoff=math.min(30,backoff*2)
   end
  end
  retry(function()bridge:start()end);publish('running')
  if foreground then print('AE Orbit monitor connected. No ME connection required.') end
  while true do
   retry(function()bridge:tick()end);publish('running');context.sleep(5)
  end
 end,function(e)return e end)
 if ok or cancel.is_stop(err) then return {kind='stopped',code='interrupted'} end
 return {kind=type(err)=='table' and err.fatal==true and 'blocked' or 'crashed',code=type(err)=='table' and err.code or 'runtime_error'}
end
return M
