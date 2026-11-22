import test from 'node:test';
import assert from 'node:assert/strict';
import {createCPUDetails,cpuDetailColumnsHTML,cpuDetailStatus} from '../src/cpu-details.mjs';

test('three lists distinguish unobserved, successful empty and failed previous data',()=>{
 const view={parts:[{kind:'active',items:[{row_id:'a',name:'test:part',label:'<Part>',quantity:'9007199254740993',has_tag:true,catalog:{base_fallback:true}}],total:2,truncated:true,observed_at:'now',error:'read_failed'},{kind:'pending',items:[],total:0,observed_at:'now'}]};
 const html=cpuDetailColumnsHTML(view,{image:()=>'<img src="/checked.png" alt="">'});
 assert.match(html,/处理中 · 等待返回/);assert.match(html,/CPU 内材料/);assert.match(html,/9,007,199,254,740,993/);
 assert.match(html,/&lt;Part&gt;/);assert.doesNotMatch(html,/<Part>/);
 assert.match(html,/本次观测为空/);assert.match(html,/等待本栏首次读取/);assert.match(html,/保留上次观测/);assert.match(html,/基础纹理/);
 assert.match(cpuDetailColumnsHTML(view,{search:'absent'}),/没有匹配的物品/);
});
test('close deletes after a late POST; response cannot revive closed details',async()=>{
 let resolve;const calls=[];const changes=[];
 const c=createCPUDetails({id:()=> 'a'.repeat(32),request:async(path,opts)=>{calls.push([path,opts]);if(opts.method==='POST')return new Promise(r=>{resolve=r;});},changed:t=>changes.push(t.data)});
 const open=c.open({network:'n',session:'s',cpu:{id:'1:1'}});
 await new Promise(r=>setImmediate(r));const close=c.close();
 resolve({parts:[],status:'ready'});await open;await close;
 assert.equal(c.current,null);assert.deepEqual(calls.map(c=>c[1].method),['POST','DELETE']);assert.deepEqual(changes,[null]);
});
test('lease renewal keeps original CPU ID and preserves last data on transport errors',async()=>{
 const calls=[];let timer;let n=0;
 const c=createCPUDetails({id:()=> 'b'.repeat(32),setTimer:cb=>{timer=cb;return 1;},clearTimer:()=>{},request:async(path,opts)=>{calls.push(opts);if(opts.method==='POST'){if(n++)throw Error('offline');return {parts:[],status:'ready'};}}});
 await c.open({network:'n',session:'s',cpu:{id:'old:1'}});await timer();
 assert.equal(c.current.data.status,'ready');assert.equal(c.current.error,'offline');
 assert.equal(calls[1].body.cpu_id,'old:1');await c.close();
});
test('task changed stops lease renewal and cannot silently select another task',async()=>{
 let scheduled=0;
 const c=createCPUDetails({id:()=> 'c'.repeat(32),setTimer:(_,ms)=>{if(ms===5000)scheduled++;},request:async()=>({parts:[],status:'target_changed'})});
 await c.open({network:'n',session:'s',cpu:{id:'old:1'}});
 assert.equal(scheduled,0);assert.match(c.current.error,/重新选择/);await c.close();
});

test('a timed out old POST cannot block the next CPU and a late result cannot revive it',async()=>{
 const timers=new Map();let key=0;let oldResolve;const calls=[];const changes=[];
 const c=createCPUDetails({id:()=> 'd'.repeat(32),setTimer:(fn,ms)=>{timers.set(++key,{fn,ms});return key;},clearTimer:key=>timers.delete(key),changed:t=>changes.push([t.cpu.id,t.data]),request:async(path,opts)=>{
  calls.push(opts);if(opts.method==='DELETE')return null;
  if(opts.body.cpu_id==='old')return new Promise(resolve=>{oldResolve=resolve;});
  return {parts:[],status:'collecting'};
 }});
 const openOld=c.open({network:'n',session:'s',cpu:{id:'old'}});await new Promise(r=>setImmediate(r));
 const openNew=c.open({network:'n',session:'s',cpu:{id:'new'}});
 [...timers.values()].find(t=>t.ms===8000).fn();await openOld;await openNew;
 assert.equal(c.current.cpu.id,'new');assert.equal(c.current.data.status,'collecting');
 assert.deepEqual(calls.map(x=>x.method),['POST','DELETE','POST']);assert.equal(calls[0].signal.aborted,true);
 oldResolve({parts:[],status:'ready'});await new Promise(r=>setImmediate(r));
 assert.equal(c.current.data.status,'collecting');await c.close();
});
test('detail status distinguishes transport, partial observations and memory protection',()=>{
 assert.match(cpuDetailStatus({loading:true}),/正在连接/);
 assert.match(cpuDetailStatus({receivedAt:1000,data:{parts:[{kind:'active',observed_at:'now'}]}},1001),/已观测 1\/3 栏/);
 assert.match(cpuDetailStatus({receivedAt:1000,data:{parts:[{kind:'stored',error:'memory_low'}]}},1001),/本次读取未完成/);
 assert.match(cpuDetailStatus({receivedAt:1000,data:{parts:[]}},21000),/20 秒未更新/);
});
