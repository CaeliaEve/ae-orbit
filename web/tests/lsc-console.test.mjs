import test from 'node:test';
import assert from 'node:assert/strict';
import {lscConsoleModel,lscConsoleHTML,lscHistoryChart,shortEnergy} from '../src/lsc-console.mjs';
const now=Date.parse('2026-11-21T08:00:00Z');
const flow={source:'lsc_sensor_v1',window_seconds:5,input_eut:'2621440',output_eut:'155790541',loss_eut:'70174',avg_input_eut:'2097152',avg_output_eut:'107431812'};
const object={id:'one',name:'蓝波顿',status:'online',enabled:true,epoch:1,stored_eu:'500000000000',capacity_eu:'600000000000',observed_at:new Date(now).toISOString(),flow};
const points=Array.from({length:6},(_,i)=>({at:new Date(now-(5-i)*10000).toISOString(),epoch:1,stored_eu:String(500000000000n-BigInt(5-i)*100000n),capacity_eu:object.capacity_eu,flow}));
test('single-device console separates observed EU/t from real-time storage change',()=>{
 const m=lscConsoleModel(object,points,now);
 assert.equal(m.flow.net,-153239275n);assert.equal(m.rate,10000n);assert.equal(m.delta,500000n);
 assert.equal(m.etaLabel,'预计充满');assert.equal(m.remaining,100000000000n);
 const html=lscConsoleHTML(object,{history:{points,resolution:'raw'},livePoints:points,objects:[object],now});
 assert.match(html,/输入 − 输出 − 损耗/);assert.match(html,/-153,239,275/);assert.match(html,/EU\/s/);assert.match(html,/EU\/t/);
 assert.doesNotMatch(html,/energy-objects|合计占比/);
});
test('unknown flow is never zero and stale data does not invent a charge time',()=>{
 const old={...object,flow:undefined};
 const m=lscConsoleModel(old,points,now);assert.equal(m.flow,null);
 const html=lscConsoleHTML(old,{history:{points:[]},now});assert.match(html,/更新监控 OC/);
 const stale=lscConsoleModel({...object,observed_at:new Date(now-120000).toISOString()},points,now);
 assert.equal(stale.flow,null);assert.equal(stale.rate,null);assert.equal(stale.stored,500000000000n);
 assert.equal(lscConsoleModel({...object,status:'read_error'},points,now).eta,'等待稳定趋势');
});
test('trend does not span epochs, capacity changes, reversed timestamps or an old last point',()=>{
 for(const change of [xs=>xs[0].epoch=2,xs=>xs[0].capacity_eu='1',xs=>xs[1].at=xs[0].at,xs=>xs.at(-1).at=new Date(now-120000).toISOString()]){
  const xs=structuredClone(points);change(xs);assert.equal(lscConsoleModel(object,xs,now).rate,null);
 }
});
test('history preserves gaps in input/output coverage and escapes device text',()=>{
 const xs=structuredClone(points);delete xs[2].flow;xs[4].epoch=2;xs[5].epoch=2;
 const html=lscHistoryChart(xs,'flow');assert.equal((html.match(/<polyline/g)||[]).length,6);
 assert.match(lscHistoryChart(xs,'storage'),/储能数量/);
 assert.match(lscHistoryChart([{...points[0],flow:undefined}],'flow'),/尚未记录输入输出/);
 const escaped=lscConsoleHTML({...object,name:'<script>alert(1)</script>'},{now});assert.match(escaped,/&lt;script&gt;/);assert.doesNotMatch(escaped,/<script>/);
});
test('compact energy and huge signed net values remain exact',()=>{
 assert.deepEqual(shortEnergy(528240747145n),{value:'528.24',unit:'G EU'});
 const huge=10n**100n;
 const m=lscConsoleModel({...object,flow:{...flow,input_eut:String(huge)}},points,now);
 assert.equal(m.flow.net,huge-155790541n-70174n);
});
