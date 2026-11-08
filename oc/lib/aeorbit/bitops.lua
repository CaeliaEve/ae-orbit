-- Lua 5.2 supplies bit32; 5.3/5.4 parse the constant native implementation only.
if bit32 then return bit32 end
return assert(load([[return {
  band=function(a,b) return (a & b) & 0xffffffff end,
  bxor=function(a,b,c) local n=a ~ b;if c then n=n ~ c end;return n & 0xffffffff end,
  bnot=function(a) return (~a) & 0xffffffff end,
  rshift=function(a,n) return (a & 0xffffffff) >> n end,
  lshift=function(a,n) return (a << n) & 0xffffffff end,
  rrotate=function(a,n) n=n%32;return ((a >> n) | (a << ((32-n)%32))) & 0xffffffff end
}]],"aeorbit.bitops","t"))()
