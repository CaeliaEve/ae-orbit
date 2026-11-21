import test from 'node:test';
import assert from 'node:assert/strict';
import {createCPUDetails,cpuDetailColumnsHTML} from '../src/cpu-details.mjs';

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
 const c=createCPUDetails({id:()=> 'c'.repeat(32),setTimer:()=>{scheduled++;},request:async()=>({parts:[],status:'target_changed'})});
 await c.open({network:'n',session:'s',cpu:{id:'old:1'}});
 assert.equal(scheduled,0);assert.match(c.current.error,/重新选择/);await c.close();
});
