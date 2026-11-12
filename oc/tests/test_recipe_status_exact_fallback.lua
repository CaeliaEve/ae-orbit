local adapter=require("aeorbit.adapter")
local proxy=require("component_proxy")
local tag=require("aeorbit.base64").decode("H4sIAAAAAAAAA+NiYGAAAHg/+U4EAAAA")
local unknown="recipe NBT encoding could not be verified"

local function exercise(wrap)
  local function item(flag)
    return {name="gregtech:gt.blockmachines",damage=282,hasTag=true,tag=tag,size=4,isCraftable=flag}
  end
  local raw=item(false)
  local rows={item(false)}
  local mode="miss"
  local scans,requests,calls=0,0,0
  local output=item(true)
  local recipe=wrap({getStack=function()
    calls=calls+1
    if mode=="stack_error" then error("unreadable output") end
    return output
  end,request=function() requests=requests+1;return {} end})
  local methods={
    allItems=function() error("unexpected enumeration") end,
    getItemInNetwork=function() calls=calls+1;return raw end,
    getCraftable=function()
      calls=calls+1
      if mode=="lookup_error" then error("offline") end
      if mode=="direct" or mode=="stack_error" or mode=="mismatch" then return recipe end
    end,
    getCraftables=function()
      calls=calls+1
      if mode=="filter_error" then error("offline") end
      if mode=="filtered" then return {recipe} end
      if mode=="ambiguous" then return {recipe,recipe} end
      return {}
    end,
    getItemsInNetwork=function(filter)
      scans=scans+1;calls=calls+1
      assert(filter.name=="gregtech:gt.blockmachines" and filter.damage==282,"storage filter broadened item identity")
      assert(filter.hasTag==true and filter.tag==tag,"storage filter changed binary NBT")
      local count=0
      for _ in pairs(filter) do count=count+1 end
      assert(count==4,"unexpected storage filter fields")
      if rows=="error" then error("offline") end
      return rows
    end,
  }
  local a=adapter.new(wrap(methods))
  for _,flag in ipairs({false,true}) do
    rows={item(flag)};calls=0
    local fresh=a:read(raw)
    local ok,status=pcall(a.recipe_status,a,fresh)
    assert(ok and status==flag,"exact storage observation did not resolve tagged recipe status")
    assert(calls==4,"recipe status exceeded the four-call acquisition reservation")
  end
  local function inconclusive(value)
    rows=value
    local ok,reason=pcall(a.recipe_status,a,raw)
    assert(not ok and tostring(reason):find(unknown,1,true),"invalid fallback replaced the original uncertainty")
  end
  inconclusive({})
  inconclusive(nil)
  inconclusive(false)
  inconclusive("error")
  inconclusive({item(false),item(true)})
  inconclusive({false})
  inconclusive({item(nil)})
  inconclusive({item(0)})
  inconclusive({item(1)})
  inconclusive({item("false")})
  local bad=item(false);bad.damage=283;inconclusive({bad})
  bad=item(false);bad.tag=nil;inconclusive({bad})
  bad=item(false);bad.tag="invalid";inconclusive({bad})
  bad=item(false);bad.name="another:item";inconclusive({bad})
  inconclusive({[1]=item(false),[3]=item(false)})
  inconclusive({[1]=item(false),extra=true})
  rows={item(true)}
  local before=scans
  for _,next_mode in ipairs({"direct","filtered"}) do
    mode=next_mode
    assert(a:recipe_status(raw)==true,"normal recipe lookup regressed")
  end
  for _,next_mode in ipairs({"lookup_error","filter_error","ambiguous","stack_error","mismatch"}) do
    mode=next_mode
    output.damage=mode=="mismatch" and 283 or 282
    assert(not pcall(a.recipe_status,a,raw),"invalid recipe handle accepted")
  end
  assert(scans==before,"fallback ran outside an exact tagged miss")
  mode="miss";output.damage=282
  local untagged={name=raw.name,damage=282,hasTag=false}
  assert(a:recipe_status(untagged)==false and scans==before,"untagged absence triggered fallback")
  local handle,reason,submitted=a:craft({item=a:definition(raw),quantity="1"})
  assert(handle==nil and reason==unknown and not submitted and requests==0 and scans==before,
    "crafting used status fallback or submitted without a verified recipe handle")
  methods.getItemsInNetwork=nil
  local missing=adapter.new(wrap(methods))
  local ok,reason=pcall(missing.recipe_status,missing,raw)
  assert(not ok and tostring(reason):find(unknown,1,true),"missing fallback method lost original uncertainty")
end

exercise(function(methods) return methods end)
exercise(proxy.new)
