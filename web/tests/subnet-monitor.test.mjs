import test from 'node:test';
import assert from 'node:assert/strict';
import {subnetItemsHTML,subnetRuleValues,subnetHistoryHTML} from '../src/subnet-monitor.mjs';
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
