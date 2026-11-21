local fixture=require('cooperative_fixture')
local json=require('aeorbit.json')
local s,r=fixture({role='primary',memory=2097152,config={inventory_mode='exact_only'}})
local busy=true
s.me.getCpus=function()return {{busy=true,cpu={isActive=function()return true end,isBusy=function()return busy end}}}end
r:start();r:tick();assert(s.hello.capabilities.cpu_notifications)
local assignment={target_id=string.rep('a',32),cpu_id='1:1',ttl_millis=120000}
s.mutate_poll_ack=function(ack)ack.cpu_watches=json.array({assignment})end
r:tick();assert(r.pending_cpu_watches[1].status=='busy')
s.mutate_poll_ack=nil;s.fail_poll=true;r:tick()
assert(r.pending_cpu_watches[1].status=='busy')
r:tick();assert(r.pending_cpu_watches==nil)
assert(s.polls[#s.polls].body==s.polls[#s.polls-1].body)
busy=false;s.mutate_poll_ack=function(ack)ack.cpu_watches=json.array({assignment})end
r:tick();assert(r.pending_cpu_watches[1].status=='idle')
local w,wr=fixture();wr:start();assert(not w.hello.capabilities.cpu_notifications)
w.mutate_poll_ack=s.mutate_poll_ack;assert(not pcall(wr.tick,wr))
