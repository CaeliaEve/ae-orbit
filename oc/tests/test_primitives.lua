local json = require("aeorbit.json")
local quantity = require("aeorbit.quantity")
local base64 = require("aeorbit.base64")
local cache = require("aeorbit.cache")

local function rejects(f)
  local ok = pcall(f)
  assert(not ok, "invalid input was accepted")
end

local value = {quantity = "9223372036854775807", label = "石头\n\"test\"", rows = json.array({1, 2}), empty = json.array({})}
local decoded = json.decode(json.encode(value))
assert(decoded.quantity == "9223372036854775807" and decoded.label == value.label)
assert(#decoded.rows == 2 and json.encode(decoded.empty) == "[]")
assert(json.decode('"\\u77f3\\u5934"') == "石头")
assert(json.decode('"\\ud83d\\ude80"') == "🚀")
rejects(function() json.decode('"\\ud800"') end)
rejects(function() json.decode('{"a":1,"a":2}') end)
rejects(function() json.decode("{} []") end)
rejects(function() json.encode({x=string.rep("x", 1000)}, 100) end)
rejects(function() json.decode("["..string.rep("[],",2048).."[]]",16384) end)
assert(json.encode({control="a\0b"}) == '{"control":"a\\u0000b"}')

assert(quantity.from_number(12345) == "12345")
assert(quantity.increment("9007199254740992") == "9007199254740993")
assert(quantity.increment("999") == "1000")
rejects(function() quantity.to_number("01") end)
rejects(function() quantity.from_number(-1) end)
rejects(function() quantity.to_number("9223372036854775808") end)
if math.type then
  assert(quantity.from_number(math.maxinteger) == "9223372036854775807")
  assert(quantity.to_number("9007199254740993") == 9007199254740993)
else
  rejects(function() quantity.from_number(9007199254740993) end)
  rejects(function() quantity.to_number("9007199254740993") end)
end

local binary = "\0\255\128abc"
assert(base64.encode(binary) == "AP+AYWJj")
assert(base64.decode("AP+AYWJj") == binary)
assert(base64.decode("YQ==") == "a")
rejects(function() base64.decode("YQ==x") end)
rejects(function() base64.decode("YR==") end)

local c = cache.new(2, 200)
assert(c:put("a",1,50)); assert(c:put("b",2,50)); assert(c:get("a")==1)
assert(c:put("c",3,50)); assert(c:get("b")==nil and c:get("a")==1)
assert(not c:put("big",9,201)); assert(c.count<=2 and c.bytes<=200)
c:remove("a"); assert(c.count==1)
local key,val = c:pop(); assert(key=="c" and val==3 and c.count==0)
