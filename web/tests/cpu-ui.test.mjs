import test from "node:test";
import assert from "node:assert/strict";
import {
  cpuDisplayName,
  cpuSelectionState,
  cpuOutputText,
  cpuMonitorModel,
  formatCPUStorage,
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

test("CPU monitor filters preserve snapshot identities and full-network totals", () => {
  const cpus = [
    { ...named(""), id: "4:1" },
    { ...named("合成 CPU 2", true), id: "4:2", output: { label: "量子锭", name: "test:quantum", quantity: "64" } },
    { ...named("合成 CPU 10", true), id: "4:3" },
  ];
  const model = cpuMonitorModel(cpus, { search: "量子 test:quantum", filter: "busy" });
  assert.equal(model.total, 3);
  assert.equal(model.busy, 2);
  assert.equal(model.idle, 1);
  assert.equal(model.outputs, 1);
  assert.equal(model.busyPercent, 67);
  assert.deepEqual(model.visible.map(row => row.index), [1]);
  assert.equal(cpuMonitorModel(cpus, { filter: "idle" }).visible[0].name, "未命名 CPU 1");
  assert.deepEqual(cpuMonitorModel(cpus, { sort: "name" }).visible.filter(row => !row.unnamed).map(row => row.name), ["合成 CPU 2", "合成 CPU 10"]);
  assert.deepEqual(cpuMonitorModel(cpus, { sort: "busy" }).visible.map(row => row.index), [1, 2, 0]);
  assert.deepEqual(cpuMonitorModel(cpus, { filter: "output" }).visible.map(row => row.index), [1]);
  assert.equal(cpus[0].id, "4:1");
});

test("CPU monitor capacity aggregation and sorting remain exact above Number precision", () => {
  const cpus = [{ ...named("A"), storage: "9007199254740992" }, { ...named("B"), storage: "9007199254740993" }];
  const model = cpuMonitorModel(cpus, { sort: "storage" });
  assert.equal(model.storage, "18014398509481985");
  assert.deepEqual(model.visible.map(row => row.name), ["B", "A"]);
  assert.equal(formatCPUStorage("4096"), "4 KiB");
  assert.equal(formatCPUStorage("1572864"), "1.5 MiB");
  assert.equal(formatCPUStorage("0"), "0 B");
  assert.equal(formatCPUStorage(-1), "未提供");
  assert.equal(formatCPUStorage(9007199254740993), "未提供");
  assert.equal(cpuMonitorModel([{ ...named("A"), storage: null }]).storage, null);
  assert.equal(cpuMonitorModel([]).busyPercent, null);
});

test("unique names retain expansion identity when snapshot IDs rotate", () => {
  const before = cpuMonitorModel([{ ...named("Main"), id: "1:1" }, { ...named(""), id: "1:2" }, named("Duplicate"), named("Duplicate")]);
  const after = cpuMonitorModel([{ ...named("Main"), id: "2:7" }]);
  assert.equal(before.rows[0].key, after.rows[0].key);
  assert.equal(before.rows[1].key, "snapshot:1:2");
  assert.equal(before.duplicates, 2);
  assert.equal(before.unnamed, 1);
  assert.equal(before.rows[2].duplicate, true);
});
