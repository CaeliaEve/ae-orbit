local diagnostics=require("aeorbit.read_diagnostics")
local json=require("aeorbit.json")

local clock=0
local d=diagnostics.new(function() return clock end)
local item={name="test:iron_plate",damage=7,tag="a",hasTag=true}
d:record("exact_lookup_failed","recheck",item,"9007199254740993","lookup failed")
clock=1
d:record("exact_lookup_failed","recheck",item,"9007199254740993","lookup failed")
local samples,token=d:snapshot(4)
assert(#samples==1 and samples[1].occurrences=="2","repeat failures filled the sample buffer")
assert(samples[1].item_id=="9007199254740993" and samples[1].last_seen_millis=="1000")
assert(samples[1].nbt_bytes==1 and samples[1].nbt_adler32=="00620062")
assert(not samples[1].tag and not samples[1].nbt,"diagnostic retained the full NBT")
item.name="test:mutated";item.tag="private NBT"
assert(samples[1].name=="test:iron_plate" and samples[1].nbt_bytes==1,"sample retained the source item table")

-- ACK of a frozen snapshot cannot hide an error recorded afterward.
d:record("exact_lookup_failed","event",{name="test:second",damage=0},nil,"another failure")
d:ack(token)
assert(not d:snapshot(4),"snapshot updates ignored their transmission interval")
clock=31
samples,token=d:snapshot(4)
assert(#samples==2 and samples[1].name=="test:second")
d:ack(token);clock=62
assert(not d:snapshot(4),"unchanged samples were resent")

-- A finite recent window replaces old samples; equal-length NBT can differ.
do
  local b=diagnostics.new(function() return clock end)
  for _,tag in ipairs({"a","b"}) do
    b:record("exact_lookup_failed","event",{name="test:tagged",damage=0,tag=tag},nil,"lookup failed")
  end
  local got=b:snapshot(4)
  assert(#got==2 and got[1].nbt_adler32=="00630063" and got[2].nbt_adler32=="00620062")
  for index=1,100 do
    b:record("exact_lookup_failed","event",{name="test:item_"..index,damage=0},nil,"lookup failed")
    local current=b:snapshot(4)
    assert(#current<=4 and #json.encode(current)<=2048,"diagnostic window grew without bound")
  end
  got=b:snapshot(4)
  assert(#got==4 and got[1].name=="test:item_100" and got[4].name=="test:item_97","old samples starved recent failures")
  local limited=b:snapshot(1)
  assert(#limited==1 and limited[1].name=="test:item_100","receiver capacity was ignored")
end

-- Long Unicode and invalid bytes cannot poison the next normal JSON poll.
do
  local b=diagnostics.new(function() return clock end)
  for index=1,20 do
    b:record("exact_lookup_failed","event",{name=string.rep("名",80)..index,damage=0},nil,string.rep("错",3000))
  end
  local got=b:snapshot(4)
  assert(#got>0 and #got<=4 and #json.encode(got)<=2048)
  assert(got[1].truncated and #got[1].message<=160 and #got[1].name<=256)
  assert(json.decode(json.encode(got))[1].message==got[1].message,"truncation split a Unicode code point")
  b:record("exact_lookup_failed","event",{name="test:invalid",damage=0},nil,"bad\0\n\255 tail")
  got=b:snapshot(4)
  assert(got[1].message=="bad  ? tail" and got[1].truncated)
  assert(pcall(json.encode,got,2048))
  local error_value=setmetatable({message="private",tag=string.rep("secret",2000)},
    {__tostring=function() error("must not stringify an error object") end})
  b:record("exact_lookup_failed","event",{name="test:object",damage=0},nil,error_value)
  got=b:snapshot(4)
  assert(got[1].message=="non-string error (table)" and not json.encode(got):find("private",1,true))
  for _,value in ipairs({"\192\175","\237\160\128","\244\144\128\128","\226\130",
    "control \194\128 end",string.rep("😀",41)}) do
    b:record("exact_lookup_failed","event",{name="test:utf8",damage=0},nil,value)
    local sample=b:snapshot(4)[1]
    assert(not sample.message:find("\194\128",1,true),"C1 control leaked into diagnostic text")
    assert(pcall(json.encode,sample,2048) and #sample.message<=160)
  end
  clock=1e14
  b:record("exact_lookup_failed","event",{name="test:late",damage=0},nil,"late failure")
  assert(b:snapshot(4)[1].last_seen_millis=="9007199254740991","elapsed time exceeded exact Lua telemetry")
end
