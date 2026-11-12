import test from "node:test";
import assert from "node:assert/strict";
import { createJobController, overlayJobClaims } from "../src/job-controller.mjs";
import { jobDetailHTML } from "../src/job-view.mjs";
import { saveDraft, loadDraft, storageKey } from "../src/model.mjs";
const scope = { memberId: "member-1", networkId: "network-1" };
const counts = { pending:"1", rejected:"0", queued:"0", assigned:"0", computing:"0", running:"0", needs_review:"0", done:"0", failed:"0", canceled:"0", expired:"0" };
const row = id => ({ id, item:{ id:"1", name:"minecraft:stone", damage:0, label:"Stone", exact:true, craftable:true }, quantity:"64", cpu_name:"", wait_for_cpu:false, status:"draft", request_key:"", batch_id:"" });
function setup(handler) {
  const values = new Map();
  const storage = { get length(){return values.size;}, key:i=>[...values.keys()][i], getItem:k=>values.get(k)??null, setItem:(k,v)=>values.set(k,v), removeItem:k=>values.delete(k) };
  saveDraft(storage, storageKey(scope.memberId,scope.networkId), {entries:[row("source-001"),row("source-002")]});
  const requests=[];
  const controller=createJobController({storage, request:async(path,options={})=>{requests.push({path,...options}); return handler(path,options);}, makeKey:()=> "manifest-001"});
  controller.reset(scope);
  return {controller,storage,requests};
}
function receipt(body) {
  return {receipt:{job_id:"job-1",manifest_digest:"a".repeat(64),archived:false},job:{summary:{
    id:"job-1",network_id:scope.networkId,owner_id:scope.memberId,name:"",manifest_digest:"a".repeat(64),
    control:"active",version:"1",control_version:"1",total:String(body.entries.length),
    counts:{...counts,pending:String(body.entries.length)},created_at:"2026-11-11T00:00:00Z",updated_at:"2026-11-11T00:00:00Z",
  },entries:body.entries.map((e,i)=>({...e,id:`entry-${i}`,ordinal:i,requested_item_id:e.item_id,state:"pending"}))}};
}
const snapshot = {jobs:[],watermark:"0"};
test("single and batch submissions each send one immutable jobs manifest and clear verified claims",async()=>{
  for(const ids of [["source-001"],["source-001","source-002"]]) {
    const {controller,storage,requests}=setup((path,o)=>o.method==="POST"?receipt(o.body):path.endsWith("/jobs")?snapshot:{changes:[],next_after:"0",watermark:"0",has_more:false,reset:false});
    await controller.sync();
    await controller.submit(ids);
    assert.equal(requests.filter(r=>r.method==="POST").length,1);
    assert.equal(requests.find(r=>r.method==="POST").path,"/api/v1/networks/network-1/jobs");
    assert.equal(requests.find(r=>r.method==="POST").body.entries.length,ids.length);
    assert.equal(loadDraft(storage,storageKey(scope.memberId,scope.networkId)).entries.length,2-ids.length);
  }
});
test("unsupported snapshot fails closed without a legacy fallback",async()=>{
  const {controller,requests}=setup(()=>{throw Object.assign(new Error("missing"),{status:404});});
  await controller.sync();
  await assert.rejects(()=>controller.submit(["source-001"]));
  assert.equal(controller.state.support,"unsupported");
  assert.equal(requests.some(r=>r.method==="POST"),false);
});
test("lost and malformed acceptance stays locked; original-key 404 never reposts or unlocks",async()=>{
  for(const malformed of [false,true]) {
    const {controller,storage,requests}=setup((path,o)=>{
      if(o.method==="POST") {if(malformed)return {};throw new Error("lost");}
      if(path.includes("by-key"))throw Object.assign(new Error("missing"),{status:404});
      return path.endsWith("/jobs")?snapshot:{changes:[],next_after:"0",watermark:"0",has_more:false,reset:false};
    });
    await controller.sync(); await assert.rejects(()=>controller.submit(["source-001"]));
    await assert.rejects(()=>controller.reconcile("manifest-001"));
    assert.equal(controller.local().intents[0].state,"uncertain");
    assert.equal(controller.local().draft.entries[0].status,"uncertain");
    assert.equal(requests.filter(r=>r.method==="POST").length,1);
    assert.equal(loadDraft(storage,storageKey(scope.memberId,scope.networkId)).entries.length,2);
  }
});
test("journal overlays preclaim crashes without altering genuine legacy uncertainty",()=>{
  const draft={entries:[row("source-001"),{...row("legacy-001"),status:"uncertain",batch_id:"old-batch",request_key:"original-legacy"}]};
  const value=overlayJobClaims(draft,[{requestKey:"manifest-001",draftIds:["source-001"]}]);
  assert.equal(value.entries[0].batch_id,"job:manifest-001");
  assert.equal(value.entries[0].status,"uncertain");
  assert.equal(value.entries[1].request_key,"original-legacy");
});
test("late submission response cannot clean another network generation",async()=>{
  let release;
  const {controller,storage}=setup((path,o)=>o.method==="POST"?new Promise(resolve=>{release=()=>resolve(receipt(o.body));}):path.endsWith("/jobs")?snapshot:{changes:[],next_after:"0",watermark:"0",has_more:false,reset:false});
  await controller.sync();
  const pending=controller.submit(["source-001"]);
  await new Promise(resolve=>setImmediate(resolve));
  controller.reset({...scope,networkId:"network-2"}); release(); await pending;
  assert.equal(loadDraft(storage,storageKey(scope.memberId,scope.networkId)).entries.length,2);
});
test("uncertain controls replay only the exact explicitly selected operation",async()=>{
  const detail=receipt({entries:[{entry_key:"draft-source-001",item_id:"1",quantity:"64"}]}).job;
  const {controller,requests}=setup((path,o)=>{
    if(o.method==="POST")throw new Error("lost");
    if(path.endsWith("/job-1"))return detail;
    return path.endsWith("/jobs")?snapshot:{changes:[],next_after:"0",watermark:"0",has_more:false,reset:false};
  });
  await controller.sync();
  const summary=receipt({entries:[{entry_key:"draft-source-001",item_id:"1",quantity:"64"}]}).job.summary;
  await assert.rejects(()=>controller.control(summary,"pause"));
  assert.equal(controller.local().controls.length,1);
  await assert.rejects(()=>controller.control(summary,"stop"));
  const refreshed=await controller.detail("job-1");
  await assert.rejects(()=>controller.replayControl("job-1","manifest-001",refreshed));
  assert.deepEqual(requests.filter(r=>r.method==="POST").map(r=>r.body),[
    {operation_key:"manifest-001",expected_control_version:"1"},
    {operation_key:"manifest-001",expected_control_version:"1"},
  ]);
});
test("failed pre-replay reads preserve the operation without another POST",async()=>{
  const detail=receipt({entries:[{entry_key:"draft-source-001",item_id:"1",quantity:"64"}]}).job;
  for(const failure of [{status:403},{status:404},{}]) {
    let fail=false;
    const {controller,requests}=setup((_path,o)=>{
      if(o.method==="POST")throw new Error("lost acknowledgement");
      if(fail)throw Object.assign(new Error("unavailable"),failure);
      return detail;
    });
    await controller.detail("job-1");
    await assert.rejects(()=>controller.control(detail.summary,"pause"));
    const saved=controller.local().controls;
    fail=true;
    const refreshed=await controller.detail("job-1");
    await assert.rejects(async()=>controller.replayControl("job-1","manifest-001",refreshed));
    assert.equal(requests.filter(r=>r.method==="POST").length,1);
    assert.deepEqual(controller.local().controls,saved);
  }
});
test("dismissed or superseded pre-replay reads cannot authorize a POST",async()=>{
  const detail=receipt({entries:[{entry_key:"draft-source-001",item_id:"1",quantity:"64"}]}).job;
  for(const invalidate of [c=>c.closeDetail(),c=>c.detail("job-1")]) {
    let resolve,delay=false;
    const {controller,requests}=setup((_path,o)=>{
      if(o.method==="POST")throw new Error("lost acknowledgement");
      return delay?new Promise(done=>{resolve=done;}):detail;
    });
    await controller.detail("job-1");
    await assert.rejects(()=>controller.control(detail.summary,"pause"));
    const saved=controller.local().controls;
    delay=true;const pending=controller.detail("job-1");
    delay=false;await invalidate(controller);resolve(detail);
    const refreshed=await pending;
    await assert.rejects(async()=>controller.replayControl("job-1","manifest-001",refreshed));
    assert.equal(requests.filter(r=>r.method==="POST").length,1);
    assert.deepEqual(controller.local().controls,saved);
  }
});
test("detail controls are suppressed when unauthorized or unavailable while operation records remain",()=>{
  const detail=receipt({entries:[{entry_key:"draft-source-001",item_id:"1",quantity:"64"}]}).job;
  const helpers={itemFor:()=>null,itemImage:()=>"",quantity:String,dateTime:String,icon:()=>""};
  const operation={jobId:"job-1",operationKey:"saved-operation",expectedControlVersion:"1",action:"pause"};
  for(const [member,controlsAvailable] of [[null,true],[{id:"other"},true],[{id:scope.memberId},false]]) {
    for(const controls of [[],[operation]]) {
      const html=jobDetailHTML(detail,{controls},member,{...helpers,controlsAvailable});
      assert.doesNotMatch(html,/data-job-action|data-control-replay/);
      if(controls.length)assert.match(html,/saved-operation/);
    }
  }
});
test("definite initial rejection permits one explicit same-key retry, then ambiguity is GET-only",async()=>{
  let attempt=0;
  const {controller,requests}=setup((path,o)=>{
    if(o.method==="POST") {
      if(++attempt===1)throw Object.assign(new Error("full"),{status:429,code:"capacity_exceeded",uncertain:false});
      throw new Error("lost retry");
    }
    return path.endsWith("/jobs")?snapshot:{changes:[],next_after:"0",watermark:"0",has_more:false,reset:false};
  });
  await controller.sync(); await assert.rejects(()=>controller.submit(["source-001"]));
  assert.equal(controller.local().intents[0].state,"rejected");
  await assert.rejects(()=>controller.retryRejected("manifest-001"));
  assert.equal(controller.local().intents[0].state,"uncertain");
  await assert.rejects(()=>controller.retryRejected("manifest-001"));
  assert.deepEqual(requests.filter(r=>r.method==="POST").map(r=>r.body),[
    {request_key:"manifest-001",name:"",ttl_seconds:3600,entries:[{entry_key:"draft-source-001",item_id:"1",quantity:"64"}]},
    {request_key:"manifest-001",name:"",ttl_seconds:3600,entries:[{entry_key:"draft-source-001",item_id:"1",quantity:"64"}]},
  ]);
});
test("each definite capacity rejection permits another explicit exact-key resend until accepted",async()=>{
  let attempt=0;
  const {controller,requests}=setup((path,o)=>{
    if(o.method==="POST") {
      if(++attempt<=2)throw Object.assign(new Error("full"),{status:429,code:"capacity_exceeded",uncertain:false});
      return receipt(o.body);
    }
    return path.endsWith("/jobs")?snapshot:{changes:[],next_after:"0",watermark:"0",has_more:false,reset:false};
  });
  await controller.sync();await assert.rejects(()=>controller.submit(["source-001"]));
  await assert.rejects(()=>controller.retryRejected("manifest-001"));
  assert.equal(controller.local().intents[0].state,"rejected");
  await controller.retryRejected("manifest-001");
  assert.equal(controller.local().intents[0].state,"accepted");
  const manifests=requests.filter(r=>r.method==="POST").map(r=>r.body);
  assert.equal(manifests.length,3);
  assert.deepEqual(manifests[1],manifests[0]);assert.deepEqual(manifests[2],manifests[0]);
});
test("unknown rejection and conflict never grant same-key resend eligibility",async()=>{
  for(const error of [
    {status:429,code:"capacity_exceeded",uncertain:true},
    {status:400,code:"request_failed",uncertain:false},
    {status:409,code:"conflict",uncertain:false},
    {status:404,code:"not_found",uncertain:false},
  ]) {
    const {controller,requests}=setup((path,o)=>{
      if(o.method==="POST")throw Object.assign(new Error("rejected"),error);
      return path.endsWith("/jobs")?snapshot:{changes:[],next_after:"0",watermark:"0",has_more:false,reset:false};
    });
    await controller.sync();await assert.rejects(()=>controller.submit(["source-001"]));
    assert.equal(controller.local().intents[0].state,"uncertain");
    await assert.rejects(()=>controller.retryRejected("manifest-001"));
    assert.equal(requests.filter(r=>r.method==="POST").length,1);
  }
});
test("a restored journal preclaim source cannot be merged, edited or submitted as new intent",async()=>{
  const {controller,storage,requests}=setup(path=>path.endsWith("/jobs")?snapshot:{changes:[],next_after:"0",watermark:"0",has_more:false,reset:false});
  const draft=loadDraft(storage,storageKey(scope.memberId,scope.networkId));
  storage.setItem("ae-orbit:jobs:v1:member-1:network-1:intent:crashed-001",JSON.stringify({
    ...scope,requestKey:"crashed-001",state:"uncertain",draftIds:["source-001"],
    manifest:{request_key:"crashed-001",name:"",ttl_seconds:3600,entries:[{entry_key:"draft-source-001",item_id:"1",quantity:"64"}]},
  }));
  assert.equal(draft.entries[0].status,"draft");
  assert.equal(controller.local().draft.entries[0].status,"uncertain");
  await controller.sync();await assert.rejects(()=>controller.submit(["source-001"]));
  assert.equal(requests.some(r=>r.method==="POST"),false);
});
test("dismissed and superseded job details cannot be restored by delayed reads",async()=>{
  let resolve;
  const one=receipt({entries:[{entry_key:"draft-source-001",item_id:"1",quantity:"64"}]}).job;
  const two={...one,summary:{...one.summary,id:"job-2"}};
  const {controller}=setup(path=>path.endsWith("/job-1")?new Promise(done=>{resolve=done;}):two);
  const pending=controller.detail("job-1");
  controller.closeDetail();resolve(one);await pending;
  assert.equal(controller.state.detail,null);
  const earlier=controller.detail("job-1");
  await controller.detail("job-2");resolve(one);await earlier;
  assert.equal(controller.state.detail.summary.id,"job-2");
});
test("reset invalidates in-flight history without leaving its loading state stuck",async()=>{
  let resolveHistory,reset=false;
  const {controller}=setup(path=>{
    if(path.includes("/history"))return new Promise(resolve=>{resolveHistory=resolve;});
    if(path.endsWith("/jobs"))return snapshot;
    if(reset){reset=false;return {changes:[],next_after:"0",watermark:"0",has_more:false,reset:true};}
    return {changes:[],next_after:"0",watermark:"0",has_more:false,reset:false};
  });
  await controller.sync();const history=controller.history();
  reset=true;await controller.sync();
  resolveHistory({jobs:[],next_cursor:"",has_more:false});await history;
  assert.equal(controller.state.historyLoaded,false);
  assert.equal(controller.state.historyLoading,false);
});
test("removal tombstones cannot be revived by a delayed history page",async()=>{
  let resolveHistory,removed=false;
  const ended={...receipt({entries:[{entry_key:"draft-source-001",item_id:"1",quantity:"64"}]}).job.summary,settled_at:"2026-11-11T01:00:00Z"};
  const {controller}=setup(path=>{
    if(path.includes("/history"))return new Promise(resolve=>{resolveHistory=resolve;});
    if(path.endsWith("/jobs"))return snapshot;
    return removed?{changes:[{revision:"1",job_id:"job-1",version:"2",removed:true}],next_after:"1",watermark:"1",has_more:false,reset:false}:
      {changes:[],next_after:"0",watermark:"0",has_more:false,reset:false};
  });
  await controller.sync();const history=controller.history();removed=true;await controller.sync();
  resolveHistory({jobs:[ended],next_cursor:"",has_more:false});await history;
  assert.equal(controller.state.history.length,0);
});
