-- Real bridge modules and real HTTP; only the internet card and AE are fixtures.
-- Match the OC sandbox and its callable component/handle methods.
collectgarbage=nil
local proxy = require("component_proxy")
local adapter = require("aeorbit.adapter")
local client = require("aeorbit.client")
local journal = require("aeorbit.journal")
local runner = require("aeorbit.runner")

fixture_stock, fixture_requests, fixture_continuations = 10, 0, 0
local job
local bridge
local function raw(index)
  if index == 1 then
    return {name="fixture:craftable",damage=0,label="Fixture craftable",size=fixture_stock,hasTag=false,isCraftable=true}
  end
  return {name="fixture:exact",damage=0,label="Fixture exact int64",size="9223372036854775807",hasTag=false,isCraftable=false}
end
local me = {}
function me.allItems()
  local index=0
  return proxy.callback(function() index=index+1;if index<=2 then return raw(index) end end)
end
function me.getItemInNetwork(detail)
  if detail.name=="fixture:craftable" then
    local value=raw(1);value.isCraftable=false;return value
  end
  if detail.name=="fixture:exact" then return raw(2) end
end
function me.getItemsInNetwork() error("full-array fallback is forbidden") end
function me.getCraftable(detail,kind)
  assert(detail.name=="fixture:craftable" and kind=="item")
  -- Real AE exact lookups omit its crafting inventory.
  return nil
end
function me.getCraftables(filter)
  assert(filter.name=="fixture:craftable" and filter.damage==0 and filter.hasTag==false)
  assert(filter.label==nil and filter.tag==nil and filter.size==nil)
  return {proxy.new({
    getStack=function() return raw(1) end,
    request=function(amount)
      fixture_requests=fixture_requests+1
      job={amount=amount,stage=0,done=false}
      return proxy.new({
        hasFailed=function() return false end,
        isComputing=function() job.stage=job.stage+1;return job.stage<2 end,
        isCanceled=function() return false end,
        isDone=function()
          if job.stage<3 then return false end
          if not job.done then
            fixture_stock=fixture_stock+job.amount;job.done=true
            local hint=raw(1);hint.size=-1
            bridge:on_item_changed(hint)
          end
          return true
        end
      })
    end
  })}
end
function me.getCpus()
  local busy=job~=nil and not job.done
  return {{name="Fixture CPU",storage=65536,coprocessors=4,busy=busy,cpu=proxy.new({
    finalOutput=function()
      local output=raw(1);output.size=job and job.amount or 0;return output
    end
  })}}
end
local internet={}
function internet.request(url,body,headers)
  local status,text=fixture_http(url,body,headers)
  local offset,closed=1,false
  return proxy.new({
    finishConnect=function() return true end,
    response=function() return status end,
    read=function(size)
      assert(not closed)
      if offset>#text then return nil end
      local part=text:sub(offset,offset+size-1);offset=offset+#part;return part
    end,
    close=function() closed=true end
  })
end
local fs={}
function fs.read(path,limit)
  local file=io.open(path,"rb")
  if not file then return nil end
  local text=file:read(limit);file:close();return text
end
function fs.write(path,text)
  local file=assert(io.open(path,"wb"))
  assert(file:write(text));assert(file:flush());assert(file:close());return true
end
bridge=runner.new({
  source="simulator",max_batch_bytes=2048,max_response_bytes=4096,
  cache_entries=2,dirty_entries=2,cache_bytes=1024,dirty_bytes=1024,
  -- One read per slice ensures the real HTTP checks exercise continuation.
  scan_interval=60,cpu_interval=0,scan_slice_seconds=0
},{
  adapter=adapter.new(proxy.new(me)),
  client=client.new(proxy.new(internet),{url=FIXTURE_URL,token=FIXTURE_TOKEN,response_bytes=4096,request_bytes=2048,timeout=5},
    {now=fixture_now,sleep=fixture_sleep}),
  journal=journal.new(fs,FIXTURE_JOURNAL,32768),
  now=fixture_now,free_memory=function() return 131072 end,yield=function() end
})
bridge:start()
function fixture_tick()
  local status=bridge:tick()
  local more,slices=status.inventory_work,0
  while more do
    slices=slices+1
    assert(slices<=64,"inventory continuation did not stop at its budget")
    fixture_continuations=fixture_continuations+1
    more=bridge:continue_inventory()
  end
  return status
end
