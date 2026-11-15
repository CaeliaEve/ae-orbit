import { networkDeviceSummary } from "./device-status.mjs";
import { normalizeConnectionTest } from "./connection-status.mjs";

const STREAM_STATES = new Set(["connected", "reconnecting", "closed"]);

function timestamp(value) {
  const parsed = Date.parse(typeof value === "string" ? value : "");
  return Number.isFinite(parsed) ? parsed : 0;
}

function latest(values) {
  return values
    .filter(value => timestamp(value) > 0)
    .sort((left, right) => timestamp(right) - timestamp(left))[0] || "";
}

function normalizeStreamState(value) {
  return STREAM_STATES.has(value) ? value : "closed";
}

export function streamStateText(value) {
  const state = normalizeStreamState(value);
  if (state === "connected") return "事件流已连接";
  if (state === "reconnecting") return "事件流重连中";
  return "事件流已关闭";
}

export function streamStateTone(value) {
  const state = normalizeStreamState(value);
  return state === "connected" ? "ok" : state === "reconnecting" ? "warn" : "idle";
}

function sessionState(member, session, now) {
  if (!member?.id) return { key: "unauthenticated", label: "未登录", tone: "idle", expiresAt: "" };
  const expiresAt = typeof session?.expiresAt === "string" ? session.expiresAt : "";
  if (expiresAt && timestamp(expiresAt) <= now) return { key: "expired", label: "会话已过期", tone: "warn", expiresAt };
  return { key: "active", label: "会话有效", tone: "ok", expiresAt };
}

function networkState(network) {
  if (!network) return { key: "unavailable", label: "未选择网络", tone: "idle" };
  const name = typeof network.name === "string" && network.name ? network.name : "当前网络";
  return network.online
    ? { key: "online", label: `${name} · 在线`, tone: "ok" }
    : { key: "offline", label: `${name} · 离线`, tone: "warn" };
}

export function connectionDiagnostics({
  origin = "",
  member = null,
  session = null,
  network = null,
  devices = [],
  streamState = "closed",
  connectionTest = null,
} = {}, now = Date.now()) {
  const rows = Array.isArray(devices) ? devices : [];
  return {
    origin: typeof origin === "string" && origin ? origin : "当前页面",
    session: sessionState(member, session, now),
    network: networkState(network),
    stream: { key: normalizeStreamState(streamState), label: streamStateText(streamState), tone: streamStateTone(streamState) },
    devices: networkDeviceSummary(rows, now),
    lastCommunication: latest([network?.last_seen, ...rows.map(device => device?.last_seen)]),
    lastInventory: latest([network?.last_inventory, ...rows.map(device => device?.last_inventory)]),
    connectionTest: normalizeConnectionTest(connectionTest),
  };
}
