-- Bounded RFC 1951/1952 decoder. No native data card, external process or full
-- per-byte numeric output table; the sliding window uses 256-byte strings.
local bit=require("aeorbit.bitops")
local xor,band,shr=bit.bxor,bit.band,bit.rshift
local M={}
local crc_table,chars={},{}
for n=0,255 do
  local c=n
  for _=1,8 do c=c%2==1 and xor(shr(c,1),0xedb88320) or shr(c,1) end
  crc_table[n]=c;chars[n]=string.char(n)
end
local function crc(data,last)
  local value=0xffffffff
  for i=1,last or #data do value=xor(shr(value,8),crc_table[band(xor(value,data:byte(i)),255)]) end
  return xor(value,0xffffffff)
end
local function little(data,pos,n)
  assert(pos+n-1<=#data,"NBT: truncated gzip")
  local value,m=0,1
  for i=pos,pos+n-1 do value=value+data:byte(i)*m;m=m*256 end
  return value
end
local function tree(lengths,kind)
  local counts,symbols,offsets={}, {},{}
  for i=0,15 do counts[i]=0 end
  for _,n in ipairs(lengths) do assert(n>=0 and n<=15,"NBT: invalid Huffman length");counts[n]=counts[n]+1 end
  local left=1
  for i=1,15 do left=left*2-counts[i];assert(left>=0,"NBT: oversubscribed Huffman tree") end
  local used=#lengths-counts[0]
  assert(left==0 or (kind~="codes" and used==1 and counts[1]==1) or (kind=="distance" and used==0),"NBT: incomplete Huffman tree")
  offsets[1]=1
  for i=2,15 do offsets[i]=offsets[i-1]+counts[i-1] end
  for symbol,n in ipairs(lengths) do
    if n>0 then symbols[offsets[n]]=symbol-1;offsets[n]=offsets[n]+1 end
  end
  return {counts=counts,symbols=symbols}
end
local fixed_lit,fixed_dist
do
  local lengths={}
  for n=0,287 do lengths[n+1]=n<=143 and 8 or (n<=255 and 9 or (n<=279 and 7 or 8)) end
  fixed_lit=tree(lengths,"literal")
  lengths={};for n=1,32 do lengths[n]=5 end;fixed_dist=tree(lengths,"distance")
end
local length_base={3,4,5,6,7,8,9,10,11,13,15,17,19,23,27,31,35,43,51,59,67,83,99,115,131,163,195,227,258}
local length_extra={0,0,0,0,0,0,0,0,1,1,1,1,2,2,2,2,3,3,3,3,4,4,4,4,5,5,5,5,0}
local distance_base={1,2,3,4,5,7,9,13,17,25,33,49,65,97,129,193,257,385,513,769,1025,1537,2049,3073,4097,6145,8193,12289,16385,24577}
local distance_extra={0,0,0,0,1,1,2,2,3,3,4,4,5,5,6,6,7,7,8,8,9,9,10,10,11,11,12,12,13,13}
local code_order={16,17,18,0,8,7,9,6,10,5,11,4,12,3,13,2,14,1,15}
function M.decode(data,limit)
  assert(type(data)=="string" and #data>=18 and #data<=12288,"NBT: invalid gzip size")
  assert(data:sub(1,3)=="\31\139\8","NBT: invalid gzip header")
  local flags=data:byte(4)
  assert(flags<32,"NBT: reserved gzip flags")
  local pos,last=11,#data-8
  if band(flags,4)~=0 then local n=little(data,pos,2);pos=pos+2+n end
  for _,flag in ipairs({8,16}) do
    if band(flags,flag)~=0 then
      local finish=data:find("\0",pos,true)
      assert(finish and finish<=last,"NBT: unterminated gzip header");pos=finish+1
    end
  end
  if band(flags,2)~=0 then assert(little(data,pos,2)==crc(data,pos-1)%65536,"NBT: bad gzip header checksum");pos=pos+2 end
  assert(pos<=last,"NBT: truncated gzip header")
  local buffer,bits=0,0
  local function read(n)
    while bits<n do
      assert(pos<=last,"NBT: truncated DEFLATE")
      buffer=buffer+data:byte(pos)*2^bits;bits=bits+8;pos=pos+1
    end
    local value=buffer%2^n;buffer=math.floor(buffer/2^n);bits=bits-n;return value
  end
  local function symbol(t)
    local code,first,index=0,0,1
    for n=1,15 do
      code=code+read(1)
      local count=t.counts[n]
      if code<first+count then return t.symbols[index+code-first] end
      index=index+count;first=(first+count)*2;code=code*2
    end
    error("NBT: invalid Huffman code")
  end
  local chunks,pending,count,used={}, {},0,0
  local function emit(char)
    assert(count<limit,"NBT: expanded size exceeds budget")
    count=count+1;used=used+1;pending[used]=char
    -- Reuse the small character buffer. Allocating one table per chunk would
    -- generate >1 MiB of garbage while inflating a 64 KiB item on Lua 5.2.
    if used==256 then chunks[#chunks+1]=table.concat(pending,"",1,used);used=0 end
  end
  local final=0
  repeat
    final=read(1)
    local kind=read(2)
    if kind==0 then
      buffer=0;bits=0
      local len,check=read(16),read(16)
      assert(len+check==65535,"NBT: bad stored block length")
      assert(count+len<=limit,"NBT: expanded size exceeds budget")
      for _=1,len do emit(chars[read(8)]) end
    else
      assert(kind~=3,"NBT: reserved DEFLATE block")
      local lit,dist=fixed_lit,fixed_dist
      if kind==2 then
        local nl,nd,nc=read(5)+257,read(5)+1,read(4)+4
        assert(nl<=286,"NBT: invalid literal alphabet")
        local lengths={};for n=1,19 do lengths[n]=0 end
        for n=1,nc do lengths[code_order[n]+1]=read(3) end
        local codes=tree(lengths,"codes")
        lengths={}
        while #lengths<nl+nd do
          local value=symbol(codes)
          if value<=15 then lengths[#lengths+1]=value
          else
            local repeat_count,previous
            if value==16 then assert(#lengths>0,"NBT: missing previous code length");repeat_count=read(2)+3;previous=lengths[#lengths]
            elseif value==17 then repeat_count=read(3)+3;previous=0
            else repeat_count=read(7)+11;previous=0 end
            assert(#lengths+repeat_count<=nl+nd,"NBT: code length repeat overflow")
            for _=1,repeat_count do lengths[#lengths+1]=previous end
          end
        end
        assert(lengths[257]>0,"NBT: missing end-of-block code")
        local ll,dd={},{}
        for n=1,nl do ll[n]=lengths[n] end
        for n=1,nd do dd[n]=lengths[nl+n] end
        lit,dist=tree(ll,"literal"),tree(dd,"distance")
      end
      while true do
        local value=symbol(lit)
        if value==256 then break end
        if value<256 then emit(chars[value])
        else
          assert(value<=285,"NBT: reserved length code")
          local index=value-256
          local len=length_base[index]+read(length_extra[index])
          local distance=symbol(dist)+1
          assert(distance<=30,"NBT: reserved distance code")
          distance=distance_base[distance]+read(distance_extra[distance])
          assert(distance<=count and count+len<=limit,"NBT: invalid distance or expanded size")
          for _=1,len do
            local offset=count-distance
            local block=math.floor(offset/256)+1
            emit(block<=#chunks and chunks[block]:sub(offset%256+1,offset%256+1) or pending[offset%256+1])
          end
        end
      end
    end
  until final==1
  assert(pos==last+1,"NBT: trailing gzip data")
  if used>0 then chunks[#chunks+1]=table.concat(pending,"",1,used) end
  local output=table.concat(chunks)
  assert(little(data,last+1,4)==crc(output) and little(data,last+5,4)==#output,"NBT: bad gzip checksum or size")
  return output
end
return M
