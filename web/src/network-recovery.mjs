import { networkDeviceSummary } from "./device-status.mjs";

function normalizeError(value) {
  return typeof value === "string" ? value.slice(0, 240) : "";
}

function timestamp(value) {
  const parsed = Date.parse(typeof value === "string" ? value : "");
  return Number.isFinite(parsed) ? parsed : 0;
}

function latest(values) {
  return values
    .filter(value => timestamp(value) > 0)
    .sort((left, right) => timestamp(right) - timestamp(left))[0] || "";
}

function defaultTime(value) {
  return value || "尚无记录";
}

function defaultAge(value, now = Date.now()) {
  const seen = timestamp(value);
  if (!seen) return "未观测";
  const seconds = Math.max(0, Math.floor((now - seen) / 1000));
  if (seconds < 60) return `${seconds} 秒前`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`;
  return `${Math.floor(seconds / 86400)} 天前`;
}

export function networkErrorKind(error) {
  const source = error && typeof error === "object" ? error : {};
  if (source.code === "backend_unavailable" || source.code === "network_error"
      || source.code === "dev_backend_unavailable" || source.status === 0) {
    return "backend_unavailable";
  }
  return "backend_error";
}

export function recoveryEvidence({ network = null, devices = [] } = {}) {
  const rows = Array.isArray(devices) ? devices : [];
  return {
    lastSeen: latest([network?.last_seen, ...rows.map(device => device?.last_seen)]),
    lastInventory: latest([network?.last_inventory, ...rows.map(device => device?.last_inventory)]),
  };
}

function action(key, label, detail, actionName = "refresh", actionLabel = "重新读取") {
  return { key, tone: key === "healthy" ? "ok" : key === "unavailable" ? "idle" : "warn", label, detail, action: actionName, actionLabel };
}

export function networkRecoveryState({
  network = null,
  devices = [],
  streamState = "closed",
  loading = false,
  error = "",
  errorKind = "",
  hasSnapshot = false,
  formatTime = defaultTime,
  formatAge = value => defaultAge(value, now),
} = {}, now = Date.now()) {
  if (!network) return {
    key: "unavailable",
    tone: "idle",
    label: "未连接网络",
    detail: "选择一个网络后才能检查 OC 状态。",
    action: "settings",
    actionLabel: "打开设置",
  };

  if (loading) return {
    key: "loading",
    tone: "idle",
    label: "正在读取网络状态",
    detail: "保留当前页面数据，等待最新 OC 心跳。",
    action: "",
    actionLabel: "",
  };

  const failure = normalizeError(error);
  if (failure) {
    const kind = errorKind || "backend_error";
    if (kind === "backend_unavailable") {
      return action(
        "backend_unavailable",
        "后端不可达",
        hasSnapshot
          ? "无法读取最新网络状态，已保留上次数据；请检查服务器进程或 HTTPS 入口。"
          : "无法读取网络状态；请确认 AE Orbit 服务已启动，并检查服务器或 HTTPS 入口。",
        "refresh",
        "重试",
      );
    }
    return action(
      "backend_error",
      "后端返回异常",
      hasSnapshot ? `已保留上次数据，可重试。${failure}` : `尚未取得网络快照，可重试。${failure}`,
      "refresh",
      "重试",
    );
  }

  const summary = networkDeviceSummary(devices, now);
  const evidence = recoveryEvidence({ network, devices });
  const heartbeat = evidence.lastSeen
    ? `最近心跳：${formatTime(evidence.lastSeen)}（${formatAge(evidence.lastSeen)}）`
    : "尚未收到本次会话的心跳。";

  if (!network.online) {
    const primary = (Array.isArray(devices) ? devices : []).find(device => device?.role === "primary");
    if (!evidence.lastSeen && !primary?.session_id) {
      return action(
        "heartbeat_waiting",
        "等待游戏心跳",
        `${heartbeat} 请确认游戏已进入存档、主 OC 所在区块已加载，并执行 rc ae-orbit status/start。`,
      );
    }
    return action(
      "oc_offline",
      "OC 桥接器离线",
      `后端仍可访问，但主 OC 已超过离线阈值；${heartbeat} 请检查 OpenOS 服务与区块加载。`,
    );
  }

  if (streamState !== "connected") return {
    key: "reconnecting",
    tone: "warn",
    label: "事件流等待重连",
    detail: `网络在线，${heartbeat}实时事件尚未恢复。`,
    action: "reconnect",
    actionLabel: "重新连接",
  };

  if (summary.stale > 0) return {
    key: "stale",
    tone: "warn",
    label: `${summary.stale} 台 OC 数据滞后`,
    detail: `${summary.online} / ${summary.total} 台在线，部分节点数据滞后；${heartbeat}`,
    action: "refresh",
    actionLabel: "重新读取",
  };

  if (summary.offline > 0) return {
    key: "partial",
    tone: "warn",
    label: `${summary.offline} 台 OC 离线`,
    detail: `${summary.online} / ${summary.total} 台在线，后端仍在等待离线节点恢复；${heartbeat}`,
    action: "refresh",
    actionLabel: "重新读取",
  };

  if (summary.total === 0) return {
    key: "waiting",
    tone: "idle",
    label: "等待 OC 节点",
    detail: `网络在线，但尚未收到设备状态；${heartbeat}`,
    action: "refresh",
    actionLabel: "重新读取",
  };

  return {
    key: "healthy",
    tone: "ok",
    label: "网络状态正常",
    detail: `${summary.online} / ${summary.total} 台 OC 在线，事件流已连接。`,
    action: "",
    actionLabel: "",
  };
}
