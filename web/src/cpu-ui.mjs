import { formatQuantity } from "./model.mjs";

export function cpuDisplayName(cpu, index = 0) {
  const name = typeof cpu?.name === "string" ? cpu.name.trim() : "";
  return name || `未命名 CPU ${index + 1}`;
}

export function cpuSelectionState(cpu, cpus = [], supported = false) {
  if (!supported) {
    return { selectable: false, reason: "当前 OC 不支持严格指定 CPU，使用自动分配。" };
  }
  const name = typeof cpu?.name === "string" ? cpu.name : "";
  if (!name.trim()) {
    return { selectable: false, reason: "CPU 未命名。请在游戏内为 AE2 CPU 设置唯一名称；未命名 CPU 只能自动分配。" };
  }
  const matches = cpus.filter(value => value && value.name === name).length;
  if (matches !== 1) {
    return { selectable: false, reason: "CPU 名称重复，无法安全指定。请在游戏内调整名称。" };
  }
  return { selectable: true, reason: "" };
}

export function cpuOutputText(cpu) {
  if (cpu?.output) {
    const label = cpu.output.label || cpu.output.name || "未知产物";
    return `最终产物：${label} × ${formatQuantity(cpu.output.quantity)}`;
  }
  const reason = typeof cpu?.output_unavailable === "string" ? cpu.output_unavailable.trim() : "";
  const normalized = reason.toLowerCase();
  if (normalized === "no crafting monitor") {
    return "未安装合成监控器，游戏未提供最终产物";
  }
  if (normalized === "monitor unavailable") {
    return "合成监控器不可用，游戏未提供最终产物";
  }
  if (normalized === "quantity precision unavailable") {
    return "最终产物数量精度不可用";
  }
  if (normalized === "packet budget") return "本次优先同步 CPU 列表，产物详情暂未传输";
  if (reason) return `最终产物不可用：${reason}`;
  return cpu?.busy ? "最终产物暂不可用" : "当前空闲，无合成输出";
}
