import { formatQuantity } from "./model.mjs";
import { jobStates } from "./job-view.mjs";

const esc = value => String(value ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const nonzero = value => typeof value === "string" && /^[0-9]+$/.test(value) && BigInt(value) > 0n;
const activeOrderStates = new Set(["queued", "assigned", "computing", "running"]);
const newest = (a, b) => (Date.parse(b.created_at) || 0) - (Date.parse(a.created_at) || 0) || String(a.id).localeCompare(String(b.id));

// Production jobs and their child orders are different units; never add them together.
export function dashboardTasks(jobs = {}, orders = {}) {
  const legacy = jobs.support === "unsupported";
  const snapshot = legacy ? orders.snapshot : jobs.sync;
  const error = legacy ? orders.error : jobs.error;
  const loading = legacy ? orders.loading : jobs.loading;
  const label = legacy ? "执行中订单" : "未结束生产任务";
  const notice = error ? snapshot ? "刷新失败 · 显示上次记录" : "任务读取失败" : loading ? "正在同步任务" : "";
  if (!snapshot) return {
    label, count: null, lowerBound: false, rows: [], attention: 0, legacy,
    notice: notice || "等待任务观测",
    empty: error?.status === 403 ? "当前账号无权查看任务" : error ? "任务暂不可用，请从左侧刷新重试" : loading ? "正在读取任务…" : "等待任务观测",
  };
  const rows = legacy
    ? (orders.rows || []).filter(row => activeOrderStates.has(row.state))
    : [...snapshot.jobs.values()].filter(row => !row.settled_at);
  const attention = legacy ? 0 : rows.filter(row => row.control === "paused" || ["needs_review", "rejected", "failed"].some(key => nonzero(row.counts?.[key]))).length;
  return {
    label, count: legacy ? orders.activeCount : rows.length,
    lowerBound: legacy && orders.truncated === true, rows: [...rows].sort(newest), attention, legacy,
    notice, empty: legacy ? orders.activeCount > 0 ? "执行中订单未包含在当前列表，前往合成中心查看" : "当前没有执行中的订单" : "当前没有未结束的生产任务",
  };
}

export function dashboardInventory(stats) {
  if (!stats) return { known: false, total: null, entries: null, pending: null, coverage: "等待库存观测", scope: "已观测库存", observedAt: "" };
  const plain = stats.scope === "nesql_plain";
  return {
    known: true, total: stats.total_quantity, entries: stats.entries,
    pending: plain && nonzero(stats.pending) ? stats.pending : null,
    coverage: plain ? `已查询 ${formatQuantity(stats.checked)} / ${formatQuantity(stats.candidates)} 种` : "已接收库存快照",
    scope: plain ? "NESQL 无 NBT · 已确认库存" : "已观测库存",
    observedAt: stats.last_observation || "",
  };
}

export function dashboardTasksHTML(model, { age, itemName = id => `物品 #${id}` } = {}) {
  if (model.count === null || !model.rows.length) return `<div class="dashboard-task-empty"><i data-lucide="list-checks" aria-hidden="true"></i><span>${esc(model.empty)}</span></div>`;
  const rows = model.rows.slice(0, 3).map(row => {
    const name = model.legacy ? itemName(row.item_id) : row.name || `生产任务 ${row.id}`;
    const counts = model.legacy ? `× ${formatQuantity(row.quantity)}` : Object.entries(row.counts || {}).filter(([, count]) => nonzero(count)).map(([key, count]) => `${jobStates[key] || key} ${formatQuantity(count)}`).join(" · ");
    const attention = !model.legacy && (row.control === "paused" || ["needs_review", "rejected", "failed"].some(key => nonzero(row.counts?.[key])));
    const status = model.legacy ? jobStates[row.state] || row.state : row.control === "paused" ? "已暂停" : row.control === "stopped" ? "已停止调度" : attention ? "需处理" : "调度中";
    return `<button class="dashboard-task-row" ${model.legacy ? "data-order" : "data-job"}="${esc(row.id)}"><span class="dashboard-task-icon"><i data-lucide="${attention ? "circle-alert" : "list-checks"}" aria-hidden="true"></i></span><span class="dashboard-task-copy"><strong>${esc(name)}</strong><small>${esc(counts)}</small></span><time>${esc(age?.(row.created_at) || "")}</time><span class="dashboard-task-state" data-tone="${attention ? "warn" : "idle"}">${esc(status)}</span><i data-lucide="arrow-up-right" aria-hidden="true"></i></button>`;
  }).join("");
  return rows + (model.rows.length > 3 ? `<button class="dashboard-task-more" data-view="orders">还有 ${formatQuantity(model.rows.length - 3)} ${model.legacy ? "项订单" : "组任务"} · 前往合成中心</button>` : "");
}
