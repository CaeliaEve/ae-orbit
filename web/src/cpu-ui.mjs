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
    const label = cpu.output.catalog?.localized_name || cpu.output.label || cpu.output.name || "未知产物";
    return `最终产物：${label} × ${formatQuantity(cpu.output.quantity)}${cpu.output.kind === "fluid" ? " mB" : cpu.output.kind === "essentia" ? " 源质" : ""}`;
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
  if (normalized === "output metadata unavailable") return "产物信息无法识别，CPU 基础状态正常上报";
  if (normalized === "packet budget") return "本次优先同步 CPU 列表，产物详情暂未传输";
  if (reason) return `最终产物不可用：${reason}`;
  return cpu?.busy ? "最终产物暂不可用" : "当前空闲，无合成输出";
}

function exactBytes(value) {
  if (typeof value === "number" && !Number.isSafeInteger(value)) return null;
  if (!["string", "number", "bigint"].includes(typeof value) || !/^\d{1,64}$/.test(String(value))) return null;
  return BigInt(value);
}

export function formatCPUStorage(value) {
  const bytes = exactBytes(value);
  if (bytes === null) return "未提供";
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"];
  let unit = 0, divisor = 1n;
  while (bytes >= divisor * 1024n && unit < units.length - 1) { divisor *= 1024n; unit++; }
  if (!unit) return `${formatQuantity(bytes)} B`;
  const tenths = (bytes * 10n + divisor / 2n) / divisor;
  const fraction = tenths % 10n;
  return `${formatQuantity(tenths / 10n)}${fraction ? `.${fraction}` : ""} ${units[unit]}`;
}

export function cpuMonitorModel(cpus = [], { search = "", filter = "all", sort = "network" } = {}) {
  const snapshot = Array.isArray(cpus) ? cpus : [];
  const names = new Map();
  for (const cpu of snapshot) if (cpu?.name?.trim()) names.set(cpu.name, (names.get(cpu.name) || 0) + 1);
  const rows = snapshot.map((cpu, index) => {
    const named = typeof cpu.name === "string" && !!cpu.name.trim();
    const duplicate = named && names.get(cpu.name) > 1;
    return {
      cpu, index, name: cpuDisplayName(cpu, index), unnamed: !named, duplicate,
      key: named && !duplicate ? `name:${cpu.name}` : `snapshot:${cpu.id || index}`,
      storage: exactBytes(cpu.storage),
    };
  });
  const busy = rows.filter(row => row.cpu.busy).length;
  const outputs = rows.filter(row => row.cpu.output).length;
  const storageKnown = rows.every(row => row.storage !== null);
  const tokens = String(search).normalize("NFKC").toLocaleLowerCase().trim().split(/\s+/).filter(Boolean);
  const visible = rows.filter(row => {
    if (filter === "busy" && !row.cpu.busy || filter === "idle" && row.cpu.busy || filter === "output" && !row.cpu.output) return false;
    const text = [row.name, row.cpu.id, row.cpu.output?.catalog?.localized_name, row.cpu.output?.label, row.cpu.output?.name].join(" ").normalize("NFKC").toLocaleLowerCase();
    return tokens.every(token => text.includes(token));
  });
  visible.sort((a, b) => {
    if (sort === "busy" && a.cpu.busy !== b.cpu.busy) return a.cpu.busy ? -1 : 1;
    if (sort === "storage" && a.storage !== b.storage) {
      if (a.storage === null) return 1;
      if (b.storage === null) return -1;
      return a.storage > b.storage ? -1 : 1;
    }
    if (sort === "name") return a.name.localeCompare(b.name, "zh-CN", { numeric: true }) || a.index - b.index;
    return a.index - b.index;
  });
  return {
    rows, visible, total: rows.length, busy, idle: rows.length - busy, outputs,
    busyPercent: rows.length ? Math.round(busy / rows.length * 100) : null,
    storage: rows.length && storageKnown ? rows.reduce((sum, row) => sum + row.storage, 0n).toString() : null,
    unnamed: rows.filter(row => row.unnamed).length, duplicates: rows.filter(row => row.duplicate).length,
  };
}
