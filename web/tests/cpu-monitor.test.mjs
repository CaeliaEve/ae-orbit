import test from "node:test";
import assert from "node:assert/strict";
import { cpuMonitorModel } from "../src/cpu-ui.mjs";
import { cpuMonitorOverviewHTML, cpuMonitorListHTML } from "../src/cpu-monitor.mjs";

test("CPU monitor renders all snapshot rows and keeps quantities exact without invented progress", () => {
  const cpus = Array.from({ length: 31 }, (_, index) => ({
    id: `1:${index + 1}`, name: `CPU ${index + 1}`, busy: index < 17, storage: "1048576", coprocessors: 16,
    ...(index === 0 ? { output: { name: "test:quantum", label: "量子锭", quantity: "9007199254740993" } } : {}),
  }));
  const model = cpuMonitorModel(cpus);
  const overview = cpuMonitorOverviewHTML(model, { truncated: true, online: false, observed: "test", age: () => "5 秒前" });
  const list = cpuMonitorListHTML(model, { supported: true, expanded: new Set(["name:CPU 1"]) });
  assert.equal((list.match(/<details /g) || []).length, 31);
  assert.match(list, /data-cpu-key="name:CPU 1"[^>]* open/);
  assert.match(list, /9,007,199,254,740,993/);
  assert.match(list, /CPU 31/);
  assert.match(overview, /55%/);
  assert.match(overview, /31 MiB/);
  assert.match(overview, /OC 离线/);
  assert.match(overview, /列表不完整/);
  assert.match(list, /没有合成完成百分比/);
  assert.doesNotMatch(list + overview, /load-bar|cpu-donut|role="progressbar"/);
  assert.match(cpuMonitorListHTML(model, { supported: true, truncated: true }), /暂时无法确认名称唯一性/);
});

test("CPU monitor escapes names, keys, and output diagnostics", () => {
  const model = cpuMonitorModel([{ id: '1"<', name: '<img src=x onerror="alert(1)">', busy: true, storage: "0", coprocessors: 0, output_unavailable: "<script>boom</script>" }]);
  const list = cpuMonitorListHTML(model);
  assert.doesNotMatch(list, /<img|<script/);
  assert.match(list, /&lt;img/);
  assert.match(list, /&lt;script&gt;/);
});

test("monitor empty states distinguish missing observations from empty filters", () => {
  assert.match(cpuMonitorListHTML(cpuMonitorModel([])), /暂无 CPU 观测/);
  assert.match(cpuMonitorListHTML(cpuMonitorModel([]), { connected: false }), /尚未连接网络/);
  const model = cpuMonitorModel([{ id: "1", name: "Main", busy: false, storage: "0", coprocessors: 0 }], { filter: "busy" });
  assert.match(cpuMonitorListHTML(model), /没有匹配的 CPU/);
  assert.match(cpuMonitorListHTML(model), /data-cpu-monitor-reset/);
});

test("CPU output textures use catalog names, preserve fluid units and escape text", () => {
  const cpus = [{id:"1",name:"流体 CPU",busy:true,storage:"4096",coprocessors:1,
    output:{kind:"fluid",name:"water",label:"Water",quantity:"1000",catalog:{localized_name:'水 <script>',image_url:"/verified.png"}}},
    {id:"2",name:"空闲 CPU",busy:false,storage:"4096",coprocessors:1}];
  const calls=[];
  const html=cpuMonitorListHTML(cpuMonitorModel(cpus),{outputImage:(output,cpu)=>{calls.push(cpu.id);return '<img class="item-image" src="/verified.png" alt="">';}});
  assert.deepEqual(calls,["1"]);
  assert.match(html,/cpu-output-media/);
  assert.match(html,/src="\/verified.png"/);
  assert.match(html,/水 &lt;script&gt;/);
  assert.match(html,/1,000 mB/);
  assert.doesNotMatch(html,/<script>/);
  assert.equal(cpuMonitorModel(cpus,{search:"水"}).visible.length,1);
});
