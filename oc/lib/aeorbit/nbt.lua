local gzip=require("aeorbit.gzip")
local sha256=require("aeorbit.sha256")
local M={}
function M.digest(compressed)
  local data=gzip.decode(compressed,65536)
  local pos,nodes=1,0
  local function take(n)
    assert(n>=0 and n<=#data-pos+1,"NBT: truncated payload")
    local start=pos;pos=pos+n;return start
  end
  local function number(n)
    local start=take(n);local result=0
    for i=start,start+n-1 do result=result*256+data:byte(i) end
    return result
  end
  local function text() take(number(2)) end
  local sizes={1,2,4,8,4,8}
  local payload
  payload=function(kind,depth)
    nodes=nodes+1
    assert(depth<=32 and nodes<=1024,"NBT: structure exceeds budget")
    local node={pos,pos-1}
    if sizes[kind] then take(sizes[kind])
    elseif kind==7 or kind==11 or kind==12 then
      local count=number(4);local width=kind==7 and 1 or (kind==11 and 4 or 8)
      assert(count<=math.floor((#data-pos+1)/width),"NBT: invalid array length");take(count*width)
    elseif kind==8 then text()
    elseif kind==9 then
      local subtype,count=number(1),number(4)
      assert(subtype<=12 and (subtype~=0 or count==0) and count<=1024-nodes,"NBT: invalid list")
      node[2]=pos-1;node[3]={}
      for n=1,count do node[3][n]=payload(subtype,depth+1) end
      return node
    elseif kind==10 then
      local children={};node[3]=children;node[6]=true
      while true do
        local start=pos;local subtype=number(1)
        if subtype==0 then break end
        text();local finish=pos-1
        local child=payload(subtype,depth+1);child[4]=start;child[5]=finish
        children[#children+1]=child
      end
      local function less(a,b)
        local first,second=a[4]+3,b[4]+3
        local alen,blen=a[5]-first+1,b[5]-second+1
        for offset=0,math.min(alen,blen)-1 do
          local av,bv=data:byte(first+offset),data:byte(second+offset)
          if av~=bv then return av<bv end
        end
        return alen<blen
      end
      -- Lua's string ordering can depend on the process locale. Compare raw
      -- name bytes directly, without allocating substrings during the sort.
      table.sort(children,less)
      for n=2,#children do assert(less(children[n-1],children[n]),"NBT: duplicate compound name") end
      return node
    else error("NBT: unsupported tag type") end
    node[2]=pos-1;return node
  end
  assert(number(1)==10,"NBT: root must be a compound");text()
  local root=payload(10,1)
  assert(pos==#data+1,"NBT: trailing payload")
  local hash=sha256.new();hash:update("\10\0\0")
  local function write(node)
    if node[4] then hash:update(data:sub(node[4],node[5])) end
    hash:update(data:sub(node[1],node[2]))
    if node[3] then for _,child in ipairs(node[3]) do write(child) end end
    if node[6] then hash:update("\0") end
  end
  write(root);return hash:finish()
end
return M
