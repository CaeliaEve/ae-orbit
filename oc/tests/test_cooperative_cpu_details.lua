local fixture=require('cooperative_fixture')
local json=require('aeorbit.json')
local s,r=fixture({role='primary',memory=2097152,config={inventory_mode='exact_only'}})
local calls=0
local cpu={isBusy=function()return true end,finalOutput=function()return {name='test:goal',size=1}end,activeItems=function()calls=calls+1;return {{name='test:part',size=1}}end}
s.me.getCpus=function()return {{busy=true,cpu=cpu}}end
r:start();r:tick();assert(calls==0 and s.hello.capabilities.cpu_details)
local d={assignment_id=string.rep('a',32),cpu_id='1:1',kind='active',ttl_millis=8000}
s.mutate_poll_ack=function(ack)ack.cpu_detail=d end
s.clock=1;r:tick();assert(calls==1)
s.fail_poll=true;r:tick();assert(calls==1)
r:tick();assert(calls==1)
local body=s.polls[#s.polls].body;assert(body==s.polls[#s.polls-1].body)
assert(s.polls[#s.polls].value.cpu_detail.items[1].name=='test:part')
-- No grant: assignment cannot reach native code.
s.mutate_poll_ack=nil;s.clock=11;r:tick();s.grant=0
s.mutate_poll_ack=function(ack)ack.grant_id=nil;ack.grant_ttl_millis=nil;ack.cpu_detail={assignment_id=string.rep('b',32),cpu_id='1:1',kind='active',ttl_millis=8000}end
assert(not pcall(r.tick,r));assert(calls==1)
-- Memory pressure reports a bounded failure, without a native read.
local l,lr=fixture({role='primary',memory=300000,config={inventory_mode='exact_only'}})
l.me.getCpus=s.me.getCpus;lr:start();l.mutate_poll_ack=function(ack)ack.cpu_detail=d end
lr:tick();assert(calls==1 and lr.pending_cpu_detail.error=='memory_low')
-- A large detail must not overflow the combined control packet.
lr.pending_cpu_detail={assignment_id=d.assignment_id,cpu_id=d.cpu_id,kind='active',total=64,truncated=false,items=json.array({})}
for i=1,64 do lr.pending_cpu_detail.items[i]={row_id=tostring(i),name='test:part',label=string.rep('x',512),quantity='1',kind='item'}end
l.mutate_poll_ack=nil;lr:tick();assert(l.polls[#l.polls].value.cpu_detail.error=='packet_budget')
-- Workers never negotiate or execute this lane.
local w,wr=fixture();wr:start();assert(not w.hello.capabilities.cpu_details)
w.mutate_poll_ack=function(ack)ack.cpu_detail=d end
assert(not pcall(wr.tick,wr));assert(calls==1)
