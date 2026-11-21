import test from 'node:test';
import assert from 'node:assert/strict';
import { notificationTarget,notificationLabel,qqDeliveryHTML,prepareCPUNotificationTarget } from '../src/qq-notifications.mjs';
import { createJobIntent,createJobJournal } from '../src/jobs.mjs';
import { saveDraft,storageKey } from '../src/model.mjs';

test('QQ recipients remain explicit and private copy needs an account',()=>{
 assert.equal(notificationTarget(null),null);
 assert.deepEqual(notificationTarget({group_id:'1081348823'}),{group_id:'1081348823',recipient:'',private_copy:false});
 for(const value of [{group_id:'all'},{group_id:'1081348823',recipient:'all'},{group_id:'1081348823',private_copy:true}])assert.throws(()=>notificationTarget(value));
 assert.match(notificationLabel({group_id:'1081348823',recipient:'12345678'}),/@12345678/);
});
test('notification target persists inside immutable order manifest',()=>{
 const scope={memberId:'member-1',networkId:'network-1'},row={id:'row-0001',status:'draft',quantity:'64',cpu_name:'',wait_for_cpu:false,request_key:'',batch_id:'',item:{id:'1',name:'test:item',damage:0,label:'物品',exact:true,craftable:true}};
 const notification={group_id:'1081348823',recipient:'12345678',private_copy:true};
 const intent=createJobIntent({...scope,requestKey:'qq-request-0001',entries:[row],notification});notification.recipient='87654321';
 const values=new Map(),storage={get length(){return values.size},key:i=>[...values.keys()][i],getItem:k=>values.get(k)||null,setItem:(k,v)=>values.set(k,v),removeItem:k=>values.delete(k)};
 saveDraft(storage,storageKey(scope.memberId,scope.networkId),{entries:[row],receipts:{}});
 createJobJournal(storage,scope).prepare(intent);
 const restored=createJobJournal(storage,scope).list()[0];assert.equal(restored.manifest.entries[0].notification.recipient,'12345678');
});
test('delivery history escapes member and device supplied values',()=>{
 const html=qqDeliveryHTML([{id:'1',group_id:'1081348823',channel:'group',state:'uncertain',created_at:'2026-11-21T00:00:00Z',payload:{title:'<script>',cpu:'<img>',reason:'&'}}]);
 assert.ok(!html.includes('<script>'));assert.match(html,/发送待确认/);assert.match(html,/&lt;img&gt;/);
});
test('CPU notification preparation pins clicked ID before recipient selection',async()=>{
 const network=()=>({id:'net',capabilities:{cpu_notifications:true}}),calls=[];
 const request=async(path,options)=>{calls.push({path,options});return {id:'target',status:calls.length===1?'pending':'busy'}};
 assert.equal(await prepareCPUNotificationTarget({network,request,cpu:{id:'old:7'},session:'boot',sleep:async()=>{}}),'target');
 assert.equal(calls[0].options.body.cpu_id,'old:7');assert.equal(calls[1].path,'/api/v1/networks/net/qq-reminders/targets/target');
});
test('changed tasks and network switching never receive a notification target',async()=>{
 const options={network:()=>({id:'net',capabilities:{cpu_notifications:true}}),cpu:{id:'1:1'},session:'boot',sleep:async()=>{},request:async()=>({id:'t',status:'idle'})};
 await assert.rejects(prepareCPUNotificationTarget(options),/任务已结束/);
 let id='net';options.network=()=>({id,capabilities:{cpu_notifications:true}});options.request=async()=>({id:'t',status:'pending'});options.sleep=async()=>{id='other'};
 await assert.rejects(prepareCPUNotificationTarget(options),/网络已切换/);
});
test('HTTP context without randomUUID can prepare CPU reminders',async()=>{
 const previous=Object.getOwnPropertyDescriptor(globalThis,'crypto');
 Object.defineProperty(globalThis,'crypto',{configurable:true,value:{getRandomValues:values=>{values.fill(12);return values}}});
 try{
  let key;
  const target=await prepareCPUNotificationTarget({network:()=>({id:'net',capabilities:{cpu_notifications:true}}),cpu:{id:'1:1'},session:'boot',request:async(_path,options)=>{key=options.body.request_key;return {id:'target',status:'busy'}}});
  assert.equal(target,'target');assert.match(key,/^cpu-target-[0-9a-f]{32}$/);
 }finally{Object.defineProperty(globalThis,'crypto',previous)}
});
