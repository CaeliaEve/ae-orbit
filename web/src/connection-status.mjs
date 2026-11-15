export const connectionStates = new Set(["idle", "checking", "ok", "error"]);

export function normalizeConnectionTest(value) {
  const source = value && typeof value === "object" ? value : {};
  const status = connectionStates.has(source.status) ? source.status : "idle";
  const checkedAt = typeof source.checkedAt === "string" ? source.checkedAt : "";
  const message = typeof source.message === "string" ? source.message.slice(0, 240) : "";
  return { status, checkedAt, message };
}

export function connectionTestTone(status) {
  return status === "ok" ? "ok" : status === "error" ? "warn" : "idle";
}

export function connectionTestText(state, dateTime) {
  const current = normalizeConnectionTest(state);
  if (current.status === "ok") return `最近测试正常 · ${dateTime(current.checkedAt)}`;
  if (current.status === "error") return `最近测试失败 · ${current.message || "连接不可用"} · ${dateTime(current.checkedAt)}`;
  if (current.status === "checking") return "正在测试当前会话连接…";
  return "尚未测试当前会话连接";
}
