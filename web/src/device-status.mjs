const ROLE_NAMES = Object.freeze({ primary: "主设备", quantity_worker: "数量助手" });

export function roleLabel(role) {
  return ROLE_NAMES[role] || role || "设备";
}

export function parseHealth(value) {
  if (!value) return {};
  if (typeof value === "object") return value;
  if (typeof value !== "string") return {};
  try {
    const parsed = JSON.parse(value);
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

export function healthSummary(value) {
  const health = parseHealth(value);
  const diagnostics = Array.isArray(health.diagnostics) ? health.diagnostics : [];
  const readFailures = Array.isArray(health.read_failures) ? health.read_failures : [];
  return {
    diagnostics: diagnostics.length,
    readFailures: readFailures.length,
    memory: typeof health.free_memory === "string" ? health.free_memory : "",
    inventoryBatches: typeof health.inventory?.batches === "string" ? health.inventory.batches : "",
  };
}

export function deviceState(device, now = Date.now()) {
  if (device?.revoked) return { key: "revoked", label: "已撤销", tone: "offline" };
  if (!device?.online) return { key: "offline", label: "离线", tone: "offline" };
  const seen = Date.parse(device.last_seen || "");
  if (Number.isFinite(seen) && now - seen > 90_000) return { key: "stale", label: "数据滞后", tone: "busy" };
  return { key: "online", label: "在线", tone: "online" };
}

export function networkDeviceSummary(devices, now = Date.now()) {
  const rows = Array.isArray(devices) ? devices : [];
  return rows.reduce((summary, device) => {
    const state = deviceState(device, now);
    summary.total += 1;
    if (state.key === "online") summary.online += 1;
    if (state.key === "offline" || state.key === "revoked") summary.offline += 1;
    if (state.key === "stale") summary.stale += 1;
    return summary;
  }, { total: 0, online: 0, offline: 0, stale: 0 });
}
