import test from "node:test";
import assert from "node:assert/strict";
import {
  cpuDisplayName,
  cpuSelectionState,
  cpuOutputText,
} from "../src/cpu-ui.mjs";

const named = (name, busy = false) => ({ name, busy, storage: "4096", coprocessors: 1, id: "1:1" });

test("CPU display names preserve game names and provide stable unnamed fallbacks", () => {
  assert.equal(cpuDisplayName(named("Assembler CPU"), 0), "Assembler CPU");
  assert.equal(cpuDisplayName(named(""), 0), "未命名 CPU 1");
  assert.equal(cpuDisplayName({}, 2), "未命名 CPU 3");
});

test("only uniquely named CPUs can be strictly selected", () => {
  const cpus = [named("Main"), named(""), named("Duplicate"), named("Duplicate")];
  assert.deepEqual(cpuSelectionState(cpus[0], cpus, true), { selectable: true, reason: "" });
  assert.deepEqual(cpuSelectionState(cpus[1], cpus, true), {
    selectable: false,
    reason: "CPU 未命名。请在游戏内为 AE2 CPU 设置唯一名称；未命名 CPU 只能自动分配。",
  });
  assert.deepEqual(cpuSelectionState(cpus[2], cpus, true), {
    selectable: false,
    reason: "CPU 名称重复，无法安全指定。请在游戏内调整名称。",
  });
  assert.deepEqual(cpuSelectionState(cpus[0], cpus, false), {
    selectable: false,
    reason: "当前 OC 不支持严格指定 CPU，使用自动分配。",
  });
});

test("CPU output text distinguishes final output from monitor limitations", () => {
  assert.equal(cpuOutputText({ output: { label: "量子锭", name: "test:quantum", quantity: "64" } }), "最终产物：量子锭 × 64");
  assert.equal(cpuOutputText({ output_unavailable: "No crafting monitor" }), "未安装合成监控器，游戏未提供最终产物");
  assert.equal(cpuOutputText({ output_unavailable: "monitor unavailable" }), "合成监控器不可用，游戏未提供最终产物");
  assert.equal(cpuOutputText({ busy: true }), "最终产物暂不可用");
  assert.equal(cpuOutputText({ busy: false }), "当前空闲，无合成输出");
});
