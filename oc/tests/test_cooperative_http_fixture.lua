local json=require("aeorbit.json")

-- The shared real-HTTP harness must select v2 for both roles, not silently run v1.
for _,role in ipairs({"quantity_worker","primary"}) do
  FIXTURE_BRIDGE_MODE="cooperative";FIXTURE_ROLE=role
  FIXTURE_URL="https://fixture.test";FIXTURE_TOKEN=string.rep("t",32)
  FIXTURE_JOURNAL=os.tmpname()
  local clock,polls,uploads=0,0,{}
  fixture_now=function() return clock end
  fixture_sleep=function(value) clock=clock+value end
  fixture_http=function(url,body)
    local value=json.decode(body)
    local path=url:match("^https://fixture.test(.*)")
    assert(path and path:match("^/bridge/v2/"),"cooperative HTTP fixture used v1")
    local response
    if path=="/bridge/v2/boot-id" then response={boot_id="fixture-boot"}
    elseif path=="/bridge/v2/hello" then
      assert(value.protocol==2 and value.capabilities.craft==(role=="primary"))
      response={protocol=2,session_id="session",device_id="device",role=role,inventory_ack="0",poll_ack="0",
        limits={batch_items=16,batch_bytes=2048,response_bytes=4096,cpus=4,poll_millis=1000,read_slots=16,read_bytes=16384}}
    elseif path=="/bridge/v2/poll" then
      polls=polls+1
      local reads=json.array({})
      local commands=json.array({})
      if polls==1 then
        local primary=role=="primary"
        reads[1]={id=primary and "1" or "2",assignment_id=string.rep("a",32),ttl_millis=30000,
          kind=primary and "quantity_recipe" or "quantity",purpose=primary and "order" or "refresh",
          item={kind="item",name=primary and "fixture:craftable" or "fixture:exact",damage=0,exact=true}}
        if primary then
          commands[1]={order_id="order",attempt_id="attempt",quantity="3",
            item={kind="item",name="fixture:craftable",damage=0,exact=true}}
        end
      end
      if role=="quantity_worker" then assert(value.cpus==nil and value.reports==nil and value.hints==nil) end
      response={ack=value.sequence,commands=commands,reads=reads,scan_budget=8,poll_millis=1000,
        grant_id=tostring(polls),grant_ttl_millis=1000}
    elseif path=="/bridge/v2/inventory" then
      local bindings=json.array({})
      for index,update in ipairs(value.updates) do
        uploads[#uploads+1]=update
        if update.item then bindings[#bindings+1]={index=index,id=update.item.name=="fixture:exact" and "2" or "1"} end
      end
      response={ack=value.sequence,revision=value.sequence,bindings=bindings}
    else error("unexpected fixture path") end
    local text=json.encode(response)
    return 200,text,{["content-length"]={tostring(#text)}}
  end
  local loaded,err=pcall(assert(loadfile(TEST_ROOT.."/scripts/fixtures/oc_http.lua")))
  assert(loaded,type(err)=="table" and err.code or tostring(err))
  for _=1,4 do fixture_tick();clock=clock+1 end
  assert(#uploads>=1)
  local receipt=uploads[1]
  assert(receipt.assignment_id==string.rep("a",32))
  if role=="quantity_worker" then
    assert(receipt.id=="2" and receipt.quantity=="9223372036854775807" and receipt.craftable==json.null)
    assert(fixture_requests==0)
  else
    assert(receipt.id=="1" and receipt.craftable==true and fixture_requests==1)
  end
  os.remove(FIXTURE_JOURNAL);os.remove(FIXTURE_JOURNAL..".0");os.remove(FIXTURE_JOURNAL..".1")
end
