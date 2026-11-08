local adapter = require("aeorbit.adapter")
local component_proxy = require("component_proxy")

local function exercise(proxy)
  local requests,lookups,filtered=0,0,0
  local stored={name="gregtech:gt.metaitem.01",damage=17032,label="Iron plate",size=30795,hasTag=false,isCraftable=false}
  local output={name=stored.name,damage=stored.damage,label="Iron plate",size=1,hasTag=false}
  local handle={}
  local recipe=proxy({getStack=function() return output end,request=function(amount)
    assert(amount==1);requests=requests+1;return handle
  end})
  local results={recipe}
  local mode="missing_flag"
  local me=proxy({
    allItems=function() return function() end end,
    getItemsInNetwork=function() error("full inventory array fallback is forbidden") end,
    getItemInNetwork=function() lookups=lookups+1;return stored end,
    getCraftable=function()
      if mode=="error" then error("component unavailable") end
      if mode=="working" then return recipe end
      return nil
    end,
    getCraftables=function(filter)
      filtered=filtered+1
      assert(filter.name==stored.name and filter.damage==stored.damage,"recipe filter lost identity")
      assert(filter.hasTag==stored.hasTag and filter.tag==stored.tag,"recipe filter lost NBT")
      assert(filter.label==nil and filter.size==nil and filter.isCraftable==nil,"display/count leaked into recipe identity")
      return results
    end,
  })
  local a=adapter.new(me)
  local command={item=a:definition(stored),quantity="1"}
  local got,reason,submitted=a:craft(command)
  assert(got==handle and submitted and requests==1 and filtered==1,"single-recipe lookup lost craftability and prevented a valid order")

  mode="working"
  got=a:craft(command)
  assert(got==handle and requests==2 and filtered==1,"working native lookup unnecessarily scanned recipes")
  mode="error"
  got,reason,submitted=a:craft(command)
  assert(got==nil and not submitted and filtered==1 and requests==2,"lookup error triggered a different request path")
  mode="missing_flag"
  results={}
  got,reason,submitted=a:craft(command)
  assert(got==nil and not submitted and requests==2,"removed recipe was submitted")
  results={recipe,recipe}
  got,reason,submitted=a:craft(command)
  assert(got==nil and not submitted and requests==2,"ambiguous filtered results were submitted")
  results={recipe}
  output.damage=17033
  got,reason,submitted=a:craft(command)
  assert(got==nil and not submitted and requests==2,"wrong metadata recipe was submitted")
  output.damage=stored.damage

  stored.hasTag=true;stored.tag=require("aeorbit.base64").decode("H4sIAAAAAAAAA+NiYGAAAHg/+U4EAAAA")
  command.item=a:definition(stored)
  got,reason,submitted=a:craft(command)
  assert(got==nil and not submitted and requests==2,"missing NBT in recipe output was accepted")
  output.hasTag=true;output.tag=stored.tag
  got,reason,submitted=a:craft(command)
  assert(got==handle and submitted and requests==3,"exact NBT recipe could not be submitted")

  recipe.request=component_proxy.callback(function() requests=requests+1;error("response lost after submission") end)
  local before=filtered
  got,reason,submitted=a:craft(command)
  assert(got==nil and submitted and requests==4 and filtered==before+1,"uncertain submission was repeated or marked safe to retry")
  assert(lookups==0,"craft lookup unnecessarily read the entire inventory")
end

exercise(function(methods) return methods end)
exercise(component_proxy.new)
