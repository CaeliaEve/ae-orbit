local M = {}
local alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
local reverse = {}
for index=1,#alphabet do reverse[alphabet:sub(index,index)]=index-1 end
function M.encode(value)
  local output={}
  for index=1,#value,3 do
    local a,b,c=value:byte(index,index+2);b=b or 0;c=c or 0
    local n=a*65536+b*256+c
    local x1,x2,x3,x4=math.floor(n/262144)%64,math.floor(n/4096)%64,math.floor(n/64)%64,n%64
    output[#output+1]=alphabet:sub(x1+1,x1+1)..alphabet:sub(x2+1,x2+1)..(index+1<=#value and alphabet:sub(x3+1,x3+1) or "=")..(index+2<=#value and alphabet:sub(x4+1,x4+1) or "=")
  end
  return table.concat(output)
end
function M.decode(value)
  assert(type(value)=="string" and #value%4==0,"base64: invalid length")
  local output={}
  for index=1,#value,4 do
    local a,b,c,d=value:sub(index,index),value:sub(index+1,index+1),value:sub(index+2,index+2),value:sub(index+3,index+3)
    assert(reverse[a] and reverse[b],"base64: invalid alphabet")
    assert((reverse[c] or c=="=") and (reverse[d] or d=="="),"base64: invalid alphabet")
    assert(c~="=" or d=="=","base64: invalid padding")
    assert((c~="=" and d~="=") or index+3==#value,"base64: misplaced padding")
    local n=reverse[a]*262144+reverse[b]*4096+(reverse[c] or 0)*64+(reverse[d] or 0)
    output[#output+1]=string.char(math.floor(n/65536)%256)
    if c~="=" then output[#output+1]=string.char(math.floor(n/256)%256) end
    if d~="=" then output[#output+1]=string.char(n%256) end
  end
  local decoded=table.concat(output);assert(M.encode(decoded)==value,"base64: noncanonical encoding");return decoded
end
return M
