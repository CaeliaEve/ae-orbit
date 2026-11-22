import test from 'node:test';
import assert from 'node:assert/strict';
import {subnetItemsHTML,subnetRuleValues,subnetHistoryHTML,subnetDiscoveryStatusHTML} from '../src/subnet-monitor.mjs';
import {availableNavigation,partitionNavigation} from '../src/navigation.mjs';

test('zero remains known, failed and stale observations retain unknown state',()=>{
 const item={id:'x',label:'水',name:'water',kind:'fluid',quantity:'0',known:true};
 let html=subnetItemsHTML({items:[item]},'member');assert.match(html,/data-state="normal"/);assert.doesNotMatch(html,/未知/);
 item.known=false;html=subnetItemsHTML({items:[item]},'member');assert.match(html,/未知/);assert.match(html,/最后 0/);
 item.quantity=null;html=subnetItemsHTML({items:[item]},'member');assert.doesNotMatch(html,/最后/);
});
test('rule thresholds remain exact and canonical with proper hysteresis',()=>{
 const f=new Map(Object.entries({item_id:'x',kind:'low',threshold:' 000100 ',recover:'00120',duration:'3'}));
 assert.equal(subnetRuleValues(f).threshold,'100');assert.equal(subnetRuleValues(f).recover,'120');
 f.set('threshold','9007199254740993');f.set('recover','9007199254740994');assert.equal(subnetRuleValues(f).threshold,'9007199254740993');
 f.set('recover','9223372036854775808');assert.throws(()=>subnetRuleValues(f));
 f.set('kind','zero');f.set('recover','1');assert.equal(subnetRuleValues(f).threshold,'0');
 f.set('kind','high');f.set('threshold','100');f.set('recover','99');assert.equal(subnetRuleValues(f).kind,'high');
 f.set('recover','100');assert.throws(()=>subnetRuleValues(f));
});
test('history gaps separate chart lines and plugin starts in More',()=>{
 const html=subnetHistoryHTML([{quantity:'0',at:'2026-11-22T00:00:00Z',epoch:1},{quantity:'9007199254740993',at:'2026-11-22T00:01:00Z',epoch:2}]);assert.equal((html.match(/<polyline/g)||[]).length,2);
 const nav=partitionNavigation(null,availableNavigation([{enabled:true,plugin:{id:'aeorbit.subnet'}}]),20);assert.ok(nav.drawer.some(n=>n.id==='plugin-subnet'));
});
test('connection, selected rows, and inventory discovery have distinct meanings',()=>{
 assert.match(subnetDiscoveryStatusHTML({capable:false},{component_address:'x',online:true}),/更新专用子网 OC/);
 const html=subnetDiscoveryStatusHTML({capable:true,found:2,checked:12,total:120,failed:1},{component_address:'x',online:true});
 assert.match(html,/已发现 2 种/);assert.match(html,/12\/120/);assert.match(html,/10.0%/);assert.match(html,/失败 1 项/);
 assert.match(subnetDiscoveryStatusHTML({capable:true},{component_address:'x',online:false}),/离线/);
});
test('small subnet mode shows list cadence and failures instead of catalog scan progress',()=>{
 const n={component_address:'x',online:true};
 const html=subnetDiscoveryStatusHTML({mode:'native',capable:true,found:23,progress_at:'2026-11-22T12:00:00Z'},n);
 assert.match(html,/每 30 秒/);assert.match(html,/已发现 23 种/);assert.doesNotMatch(html,/72210|正在发现/);
 const failed=subnetDiscoveryStatusHTML({mode:'native',capable:true,error:'item:too_many_items'},n);assert.match(failed,/超过 128 种/);
});
