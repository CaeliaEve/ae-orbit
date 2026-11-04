local M = {}
local MAX = "9223372036854775807"
local SAFE = "9007199254740991"

local function above(a, b)
  return #a > #b or (#a == #b and a > b)
end
function M.validate(value)
  assert(type(value) == "string" and value:match("^%d+$"), "quantity: decimal string required")
  assert(value == "0" or value:sub(1, 1) ~= "0", "quantity: leading zero")
  assert(not above(value, MAX), "quantity: int64 overflow")
  return value
end
function M.from_number(value)
  assert(type(value) == "number" and value >= 0 and value == value and value ~= math.huge and value % 1 == 0, "quantity: invalid number")
  if math.type and math.type(value) == "integer" then return string.format("%d", value) end
  assert(value <= 9007199254740991, "quantity: precision unavailable; select Lua 5.3")
  return string.format("%.0f", value)
end
function M.maximum()
  return math.type and MAX or SAFE
end
function M.to_number(value)
  M.validate(value)
  assert(not above(value, M.maximum()), "quantity: not exactly representable by this Lua runtime")
  local number = tonumber(value)
  assert(number and M.from_number(number) == value, "quantity: precision lost")
  return number
end
function M.increment(value)
  M.validate(value)
  assert(value ~= MAX, "quantity: counter exhausted")
  local result, carry = {}, 1
  for index = #value, 1, -1 do
    local digit = value:byte(index) - 48 + carry
    if digit == 10 then digit, carry = 0, 1 else carry = 0 end
    result[index] = string.char(48 + digit)
  end
  return (carry == 1 and "1" or "") .. table.concat(result)
end
return M
