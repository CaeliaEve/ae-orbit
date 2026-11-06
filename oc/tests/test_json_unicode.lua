local json = require("aeorbit.json")

local function rejects(callback)
  assert(not pcall(callback),"invalid Unicode or an over-budget string was accepted")
end

-- OC's HTTP writer uses the JVM's default charset. ASCII JSON preserves these
-- names on GB18030/Windows-1252 hosts without changing the game's JVM settings.
local expected='{"label":"\\u77f3\\u5934","\\u540d\\u79f0":"\\ud83d\\ude80"}'
local encoded=json.encode({label="石头",["名称"]="🚀"},#expected)
assert(encoded==expected,"JSON depends on the HTTP writer's default charset")
assert(not encoded:find("[\128-\255]"),"non-ASCII bytes reached the HTTP body")
local decoded=json.decode(encoded)
assert(decoded.label=="石头" and decoded["名称"]=="🚀")
rejects(function() json.encode({label="石头",["名称"]="🚀"},#expected-1) end)
assert(json.encode({label="石头"},24)=='{"label":"\\u77f3\\u5934"}')
rejects(function() json.encode({label="石头"},23) end)
assert(json.encode("\0\n\t\"\\")=='"\\u0000\\n\\t\\\"\\\\"')

for _,case in ipairs({
  {bytes={0xc2,0x80},escaped="\\u0080"},
  {bytes={0xdf,0xbf},escaped="\\u07ff"},
  {bytes={0xe0,0xa0,0x80},escaped="\\u0800"},
  {bytes={0xed,0x9f,0xbf},escaped="\\ud7ff"},
  {bytes={0xee,0x80,0x80},escaped="\\ue000"},
  {bytes={0xef,0xbf,0xbf},escaped="\\uffff"},
  {bytes={0xf0,0x90,0x80,0x80},escaped="\\ud800\\udc00"},
  {bytes={0xf4,0x8f,0xbf,0xbf},escaped="\\udbff\\udfff"},
}) do
  local raw=string.char(table.unpack(case.bytes))
  local quoted=json.encode(raw)
  assert(quoted=='"'..case.escaped..'"',"Unicode scalar was encoded incorrectly")
  assert(json.decode(quoted)==raw,"Unicode scalar did not round trip")
end

for _,bytes in ipairs({
  {0x80},{0xff},{0xc2},{0xe1,0x80},{0xf1,0x80,0x80},
  {0xc0,0x80},{0xe0,0x80,0x80},{0xf0,0x80,0x80,0x80},
  {0xed,0xa0,0x80},{0xf4,0x90,0x80,0x80},{0xf5,0x80,0x80,0x80},
  {0xc2,0x80,0x80},{0xe1,0x41,0x80},
}) do
  local invalid=string.char(table.unpack(bytes))
  rejects(function() json.encode({label=invalid}) end)
  rejects(function() json.encode({[invalid]="value"}) end)
end
