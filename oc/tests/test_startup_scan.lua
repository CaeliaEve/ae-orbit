local fixture=require("inventory_fixture")
local json=require("aeorbit.json")

local function finish_grant(r)
  for _=1,64 do if not r:continue_inventory() then return end end
  error("finite acquisition grant did not finish")
end

local function scanned(s)
  for _,request in ipairs(s.requests) do if request.value.scan_completed then return true end end
  return false
end

-- A full inherited catalog must not make ordinary rereads consume half of
-- initial discovery. The backend honors the real receiver capacity fields.
do
  local s,r=fixture({grant=32,delay=0.001,catalog_size=128})
  local definitions={}
  for index=1,128 do definitions[index]=s:verification("test:cold_"..index) end
  local next_recheck,lookups=1,0
  local read=s.me.getItemInNetwork
  s.me.getItemInNetwork=function(detail) lookups=lookups+1;return read(detail) end
  s.on_poll=function(_,request)
    s.verify={}
    local count=math.min(request.recheck_slots,math.floor(request.recheck_bytes/512),16)
    for _=1,count do
      s.verify[#s.verify+1]=definitions[next_recheck]
      next_recheck=next_recheck%#definitions+1
    end
  end
  for _=1,6 do
    s.clock=s.clock+1;r:tick();finish_grant(r)
    if scanned(s) then break end
  end
  assert(scanned(s),"inherited catalog rereads delayed initial discovery beyond six grants")
  for index=1,128 do assert(s.items["test:cold_"..index],"initial scan missed an inventory item") end
  assert(lookups>0 and lookups<=12,"initial discovery stopped ordinary rechecks or duplicated most of the catalog")
  assert(r.rechecks.count<=16 and r.rechecks.bytes<=r.verification_bytes,"startup exceeded the ordinary queue budget")
  s.clock=s.clock+1;r:tick()
  assert(s.polls[#s.polls].recheck_slots>=8,"ordinary rechecks did not resume after the completed scan was acknowledged")
end

-- The completion marker can reach the backend before its ACK reaches the OC.
-- Keep the smaller admission window until that exact batch is acknowledged.
do
  local s,r=fixture({grant=8,delay=0,catalog_size=0})
  local send=r.client.send
  local lost=false
  r.client.send=function(self,path,body)
    if path=="/bridge/v1/inventory" and json.decode(body).scan_completed and not lost then
      lost=true;s.fail_upload=true
    end
    return send(self,path,body)
  end
  r:tick()
  assert(lost and r.pending_inventory,"completion ACK loss was not exercised")
  s.clock=s.clock+1;r:tick()
  assert(s.polls[#s.polls].recheck_slots<=1,"completion ACK loss prematurely restored full reread traffic")
  assert(not r.pending_inventory,"completion batch did not resume with its original sequence")
  s.clock=s.clock+1;r:tick()
  assert(s.polls[#s.polls].recheck_slots==16,"successful completion retry did not restore normal admission")
  s.clock=70;r:tick()
  assert(s.polls[#s.polls].recheck_slots==16,"later scheduled scans were treated as a new boot")
end

-- A broken discovery source must not also suppress the only remaining stock
-- reads. Resume ordinary admission while waiting to retry the iterator.
for _,failure in ipairs({"open","next"}) do
  local s,r=fixture({grant=1,delay=0,catalog_size=8})
  s.me.allItems=function()
    if failure=="open" then error("AE unavailable") end
    return function() error("iterator invalidated") end
  end
  r:tick()
  assert(r.next_scan>s.clock,"failed discovery did not enter its retry delay")
  s.clock=s.clock+1;r:tick()
  assert(s.polls[#s.polls].recheck_slots==16,"failed discovery kept ordinary verification throttled")
end

-- Already-admitted work survives a full receiver and new priority requests
-- still pass while initial ordinary capacity is zero.
do
  local s,r=fixture({grant=0,delay=0.06,catalog_size=128})
  s.verify={s:verification("test:missed")};r:tick()
  local token=string.rep("a",32)
  s.verify={s:verification("test:priority",token)}
  s.grant=8;s.clock=s.clock+1;r:tick();finish_grant(r)
  assert(s.polls[#s.polls].recheck_slots==0,"queued initial reread did not exert backpressure")
  assert(s.items["test:priority"] and s.items["test:priority"].verification==token,"startup delayed an urgent quantity/recipe confirmation")
  assert(s.items["test:missed"],"startup dropped an already-admitted ordinary verification")
end
