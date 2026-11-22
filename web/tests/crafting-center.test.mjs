import test from "node:test";
import assert from "node:assert/strict";
import { craftingJobModel, craftingOverviewHTML, craftingJobsHTML, craftingOrdersHTML, jobDisplayState, jobProgressHTML } from "../src/crafting-center.mjs";
import { jobDetailHTML } from "../src/job-view.mjs";

const counts = { pending:"0", rejected:"0", queued:"0", assigned:"0", computing:"0", running:"0", needs_review:"0", done:"0", failed:"0", canceled:"0", expired:"0" };
const job = (id, changes={}) => ({ id, name:`任务 ${id}`, owner_id:"me", total:"3", control:"active", created_at:"2026-11-20T00:00:00Z", counts:{...counts,running:"3"}, ...changes });
const state = rows => ({ support:"supported", sync:{ jobs:new Map(rows.map(row=>[row.id,row])) }, history:[], historyLoaded:false });

test("crafting overview counts task groups separately from exact entry quantities", () => {
  const rows = [job("one", {total:"9007199254740995",counts:{...counts,pending:"9007199254740993",running:"2"}}),job("two", {counts:{...counts,done:"1",computing:"1",queued:"1"}}),job("history", {settled_at:"2026-11-20T00:00:00Z",counts:{...counts,done:"3"}})];
  const model=craftingJobModel(state(rows));
  assert.equal(model.counts.unsettled,2);
  assert.equal(model.running,3n);
  assert.equal(model.waiting,9007199254740994n);
  assert.match(craftingOverviewHTML(model),/9,007,199,254,740,994/);
  assert.match(craftingOverviewHTML(craftingJobModel({})),/—/);
});

test("job filters combine ownership, state search and deterministic sorting without changing source rows", () => {
  const rows=[job("old",{created_at:"2026-11-19T00:00:00Z"}),job("paused",{owner_id:"other",control:"paused"}),job("mine",{counts:{...counts,failed:"1",running:"2"}})];
  const before=structuredClone(rows);
  assert.deepEqual(craftingJobModel(state(rows),{filter:"attention",owner:"mine",memberId:"me",search:"失败"}).rows.map(row=>row.id),["mine"]);
  assert.deepEqual(craftingJobModel(state(rows),{sort:"oldest"}).rows.map(row=>row.id),["old","paused","mine"]);
  assert.deepEqual(craftingJobModel(state(rows),{sort:"attention"}).rows.map(row=>row.id),["mine","paused","old"]);
  assert.deepEqual(rows,before);
});

test("history uses newest known summaries and searches only loaded history", () => {
  const old=job("h",{settled_at:"2026-11-20T00:00:00Z",name:"旧名称"});
  const fresh={...old,name:"更新后名称"};
  const model=craftingJobModel({...state([fresh]),history:[old],historyLoaded:true},{filter:"history",search:"更新后"});
  assert.equal(model.rows[0].name,"更新后名称");
  assert.equal(model.counts.unsettled,0);
});

test("display status distinguishes paused or stopped scheduling from executed and completed work", () => {
  assert.equal(jobDisplayState(job("paused",{control:"paused"})).label,"已暂停调度");
  assert.equal(jobDisplayState(job("stopped",{control:"stopped"})).label,"已停止调度");
  assert.equal(jobDisplayState(job("running")).label,"合成中");
  assert.equal(jobDisplayState(job("done",{settled_at:"now",counts:{...counts,done:"3"}})).label,"全部完成");
  assert.equal(jobDisplayState(job("partial",{settled_at:"now",counts:{...counts,done:"2",failed:"1"}})).label,"已结束");
});

test("entry state bars preserve completion evidence without inline styles or invented craft percentages", () => {
  const html=jobProgressHTML(job("progress",{total:"4",counts:{...counts,done:"1",running:"1",pending:"1",failed:"1"}}));
  assert.match(html,/crafting-progress-done" x="0" y="0" width="25"/);
  assert.match(html,/crafting-progress-active" x="25" y="0" width="25"/);
  assert.match(html,/已完成 <b>1<\/b> \/ 4 项/);
  assert.doesNotMatch(html,/\sstyle=|25%/);
});

test("job list keeps stale data and escapes task names and identifiers", () => {
  const model=craftingJobModel({...state([job('x" onclick="bad',{name:"<script>alert(1)</script>"})]),error:new Error("offline")});
  const html=craftingJobsHTML(model,"unsettled");
  assert.match(html,/&lt;script&gt;/);
  assert.doesNotMatch(html,/<script>| onclick="bad/);
  assert.match(html,/crafting-job-row/);
  assert.match(craftingJobsHTML(craftingJobModel({support:"unsupported"}),"unsettled"),/data-task-mode="orders"/);
});

test("detail controls remain permission gated and stopped tasks keep their actual entry state", () => {
  const summary=job("stopped",{control:"stopped"});
  const detail={summary,entries:[{id:"entry",ordinal:0,item_id:"1",quantity:"9007199254740993",state:"running",cpu_name:"CPU A",wait_for_cpu:true}]};
  const helpers={itemFor:()=>({label:"<img>"}),itemImage:()=>"",quantity:String,dateTime:String,icon:()=>""};
  const html=jobDetailHTML(detail,{controls:[]},{id:"me"},helpers);
  assert.match(html,/9007199254740993/);
  assert.match(html,/合成中/);
  assert.match(html,/CPU A · 等待指定 CPU/);
  assert.doesNotMatch(html,/data-job-action|<img>/);
  const unavailable=jobDetailHTML({...detail,summary:{...summary,control:"active"}},{controls:[]},{id:"me"},{...helpers,controlsAvailable:false});
  assert.doesNotMatch(unavailable,/data-job-action/);
});

test("single-order grouping keeps exact quantities, safe labels and cancellation only before dispatch", () => {
  const orders=[{id:"q",state:"queued",quantity:"9007199254740993",item_id:"1",cpu_name:"CPU A",wait_for_cpu:true,batch_id:"batch"},{id:"r",state:"running",quantity:"64",item_id:"1",batch_id:"batch"}];
  const html=craftingOrdersHTML(orders,{itemFor:()=>({label:"<svg>"}),itemImage:()=>"",age:()=>"1 秒前",dateTime:()=>"今天"});
  assert.match(html,/9,007,199,254,740,993/);
  assert.match(html,/等待指定 CPU/);
  assert.match(html,/data-cancel="q"/);
  assert.doesNotMatch(html,/data-cancel="r"|<svg>/);
  assert.equal((html.match(/crafting-order-group/g)||[]).length,1);
});
