import { networkDeviceSummary } from "./device-status.mjs";

const MAX_LENGTH = 128;
const sources = new Set(["oc", "simulator"]);

function timestamp(value) {
  const parsed = Date.parse(typeof value === "string" ? value : "");
  return Number.isFinite(parsed) ? parsed : 0;
}

function latest(values) {
  return values
    .filter(value => timestamp(value) > 0)
    .sort((left, right) => timestamp(right) - timestamp(left))[0] || "";
}

export function normalizeNetworkDraft(input = {}) {
  const name = String(input.name ?? "").trim();
  const server_group = String(input.server_group ?? "").trim();
  const source = String(input.source ?? "oc").trim();
  if (!name) return { ok: false, error: "请输入网络名称。" };
  if (name.length > MAX_LENGTH) return { ok: false, error: "网络名称不能超过 128 个字符。" };
  if (!server_group) return { ok: false, error: "请输入 Minecraft 服务组。" };
  if (server_group.length > MAX_LENGTH) return { ok: false, error: "Minecraft 服务组不能超过 128 个字符。" };
  if (!sources.has(source)) return { ok: false, error: "网络来源无效。" };
  return { ok: true, value: { name, server_group, source } };
}

export function networkCreateResult(value = {}) {
  const network = value?.network && typeof value.network.id === "string" ? value.network : null;
  const deviceToken = typeof value?.device_token === "string" ? value.device_token : "";
  return { network, deviceToken };
}

export function networkRefreshState({ loading = false, error = "", hasSnapshot = false } = {}) {
  if (loading) return { hidden: false, tone: "loading", text: "正在刷新网络状态…", retry: false };
  if (error) return {
    hidden: false,
    tone: "error",
    text: hasSnapshot ? `刷新失败，已保留上次数据。${error}` : `网络状态读取失败。${error}`,
    retry: true,
  };
  return { hidden: true, tone: "", text: "", retry: false };
}

export function networkDashboardSummary(network = {}, devices = [], now = Date.now()) {
  const rows = Array.isArray(devices) ? devices : [];
  const { total, online, offline, stale } = networkDeviceSummary(rows, now);
  const health = !network?.online ? "offline" : !total ? "waiting" : offline || stale ? "degraded" : "healthy";
  const capabilities = network?.capabilities && typeof network.capabilities === "object" ? network.capabilities : {};
  const serviceCount = ["inventory", "recipes", "craft", "cpu_output", "cpu_selection"].filter(key => capabilities[key] === true).length;
  return {
    total,
    online,
    offline,
    stale,
    health,
    lastCommunication: latest([network?.last_seen, ...rows.map(device => device?.last_seen)]),
    serviceCount,
  };
}

export function networkCapabilityRows(capabilities = {}, services = {}) {
  const readers = Number.isFinite(Number(services?.quantity_readers)) ? Number(services.quantity_readers) : 0;
  const rows = [
    ["inventory", "库存同步", "同步已确认的物品数量"],
    ["recipes", "配方读取", "读取可合成配方"],
    ["craft", "合成下单", "支持生产任务"],
    ["cpu_output", "CPU 输出", "可读取最终产物"],
    ["cpu_selection", "严格指定 CPU", "按唯一 CPU 名称指定"],
  ];
  return rows.map(([key, label, detail]) => ({
    key,
    label,
    value: capabilities[key] === true ? "可用" : "不可用",
    tone: capabilities[key] === true ? "ok" : "idle",
    detail: capabilities[key] === true
      ? key === "inventory" && readers ? `${readers} 个数量读取器` : detail
      : key === "cpu_selection" ? "需 OC 支持唯一 CPU 名称" : "当前网络未声明",
  }));
}
