import test from 'node:test';
import assert from 'node:assert/strict';
import {historyChartModel,historyChartHTML,historyRecordsHTML,historyDelta,createHistoryController} from '../src/inventory-history.mjs';
const base=Date.parse('2026-11-20T00:00:00Z');
const series={from:new Date(base).toISOString(),to:new Date(base+3600000).toISOString(),resolution:60,basis:'minute_snapshots',epoch:'1',started_at:new Date(base).toISOString(),epochs:[],points:[{at:new Date(base).toISOString(),quantity:'9007199254740993',entries:'1',all_online:true},{at:new Date(base+60000).toISOString(),quantity:'9007199254740994',entries:'2',all_online:true}]};
test('history chart preserves tiny changes beside huge quantities and exposes exact hover values',()=>{
 const m=historyChartModel(series);assert.notEqual(m.points[0].y,m.points[1].y);assert.ok(m.points.every(p=>Number.isFinite(p.x)&&Number.isFinite(p.y)));assert.match(historyChartHTML(series),/9,007,199,254,740/);assert.equal(historyDelta('-9007199254740993'),'-9,007,199,254,740,993');
});
test('network history breaks sampling gaps and offline points; item history is explicitly stepped',()=>{
 const points=[...series.points,{at:new Date(base+600000).toISOString(),quantity:'4',all_online:false},{at:new Date(base+660000).toISOString(),quantity:'5',all_online:true}];assert.deepEqual(historyChartModel({...series,points}).segments.map(s=>s.length),[2,1,1]);const stepped={...series,basis:'last_known_quantity'};assert.equal(historyChartModel(stepped).step,true);assert.match(historyChartHTML(stepped),/H[0-9.]+ V[0-9.]+/);
});
test('history detail escapes names and distinguishes unknown baselines from actual changes',()=>{
 const html=historyRecordsHTML([{id:'1',item_id:'2',name:'<img>',label:'"<script>',damage:0,quantity:'9007199254740993',previous_quantity:null,delta:null,kind:'baseline',recorded_at:series.from,observed_at:series.from}]);assert.doesNotMatch(html,/<script>|<img>/);assert.match(html,/开始记录时的库存/);assert.match(html,/data-history-item="2"/);assert.match(html,/9,007,199,254,740,993/);assert.equal(historyDelta(null),'—');
});
test('incoming history does not reset detail pages and frozen pagination returns cached previous pages',async()=>{
 const paths=[];const ctrl=createHistoryController({now:()=>base+3600000,request:async(path)=>{paths.push(path);return path.startsWith('/inventory-history?')?series:path.includes('before=10')?{records:[{id:'9'}],snapshot:'20',next:'',has_more:false}:{records:[{id:'10'}],snapshot:'20',next:'10',has_more:true}}});await ctrl.load();await ctrl.next();assert.equal(ctrl.state.pageIndex,1);assert.ok(paths.some(p=>p.includes('before=10')&&p.includes('snapshot=20')));const second=ctrl.state.page;await ctrl.refresh();assert.equal(ctrl.state.page,second);assert.equal(ctrl.state.pageIndex,1);assert.equal(ctrl.state.newData,true);ctrl.previous();assert.equal(ctrl.state.page.records[0].id,'10');assert.match(ctrl.exportQuery(),/snapshot=20/);
});
test('failed page requests preserve records and a reset fences delayed data from another network',async()=>{
 let reject=false,resolve;const ctrl=createHistoryController({request:async(path)=>{if(path.startsWith('/inventory-history?'))return series;if(reject)throw new Error('offline');return {records:[{id:'10'}],snapshot:'20',next:'10',has_more:true}}});await ctrl.load();reject=true;await ctrl.next();assert.equal(ctrl.state.pageIndex,0);assert.equal(ctrl.state.page.records[0].id,'10');assert.match(ctrl.state.tableError,/offline/);const late=createHistoryController({request:()=>new Promise(r=>resolve=r)});const work=late.load();late.reset();resolve(series);await work;assert.equal(late.state.series,null);
});

test('refresh moves both ends of rolling ranges and reset cancels its follow-up detail read',async()=>{
 let clock=base+3600000;const paths=[];const ctrl=createHistoryController({now:()=>clock,request:async path=>{paths.push(path);return path.startsWith('/inventory-history?')?{...series,from:new URLSearchParams(path.split('?')[1]).get('from'),to:new URLSearchParams(path.split('?')[1]).get('to')}:{records:[],snapshot:'0',has_more:false}}});await ctrl.setRange('24');clock+=3600000;await ctrl.refresh();const query=new URLSearchParams(paths.filter(p=>p.startsWith('/inventory-history?')).at(-1).split('?')[1]);assert.equal(Date.parse(query.get('from')),clock-24*3600000);assert.equal(Date.parse(query.get('to')),clock);
 let resolve,details=0;const late=createHistoryController({request:path=>path.startsWith('/inventory-history?')?new Promise(r=>resolve=r):(details++,Promise.resolve({records:[]}))});const work=late.refresh();late.reset();resolve(series);await work;assert.equal(details,0);assert.equal(late.state.series,null);
});

test('rolling chart updates retain the original detail time bounds for later pages and export',async()=>{
 let clock=base+3600000;const paths=[];const ctrl=createHistoryController({now:()=>clock,request:async path=>{paths.push(path);const q=new URLSearchParams(path.split('?')[1]);return path.startsWith('/inventory-history?')?{...series,from:q.get('from'),to:q.get('to'),watermark:'21'}:{records:[{id:'10'}],snapshot:'20',next:'10',has_more:true}}});await ctrl.setRange('24');const frozen=ctrl.exportQuery();clock+=3600000;await ctrl.observe();await ctrl.next();const next=new URLSearchParams(paths.at(-1).split('?')[1]),initial=new URLSearchParams(frozen);assert.equal(next.get('from'),initial.get('from'));assert.equal(next.get('to'),initial.get('to'));assert.equal(ctrl.exportQuery(),frozen);assert.equal(ctrl.state.newData,true);
});
