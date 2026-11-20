import { formatQuantity } from "./model.mjs";

export const jobStates = { pending:"待调度", rejected:"未通过", queued:"排队中", assigned:"已派发", computing:"计算中", running:"合成中", needs_review:"结果待确认", done:"已完成", failed:"失败", canceled:"已撤回", expired:"已过期" };
const esc = value => String(value ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const icon = name => `<i data-lucide="${name}" aria-hidden="true"></i>`;
const count = value => /^\d+$/.test(String(value ?? "")) ? BigInt(value) : 0n;
const sum = (counts, keys) => keys.reduce((total, key) => total + count(counts?.[key]), 0n);
const attentionKeys = ["needs_review", "rejected", "failed"];
const newest = (a, b) => (Date.parse(b.created_at) || 0) - (Date.parse(a.created_at) || 0) || String(a.id).localeCompare(String(b.id));

export function jobAttention(row) { return row.control === "paused" || sum(row.counts, attentionKeys) > 0n; }

export function jobDisplayState(row) {
  if (row.settled_at) return count(row.counts?.done) === count(row.total) ? { label: "全部完成", tone: "ok" } : { label: "已结束", tone: sum(row.counts, attentionKeys) ? "warn" : "idle" };
  if (row.control === "stopped") return { label: "已停止调度", tone: "idle" };
  if (row.control === "paused") return { label: "已暂停调度", tone: "warn" };
  if (sum(row.counts, attentionKeys)) return { label: "需要处理", tone: "warn" };
  if (count(row.counts?.running)) return { label: "合成中", tone: "active" };
  if (count(row.counts?.computing)) return { label: "计算中", tone: "active" };
  if (count(row.counts?.assigned)) return { label: "已派发", tone: "active" };
  return { label: "等待调度", tone: "idle" };
}

export function craftingJobModel(model = {}, { filter = "unsettled", search = "", sort = "newest", memberId = "", owner = "all" } = {}) {
  const live = model.sync ? [...model.sync.jobs.values()] : [];
  const open = live.filter(row => !row.settled_at);
  const history = (model.history || []).map(row => model.sync?.jobs.get(row.id) || row);
  const counts = { unsettled: open.length, attention: open.filter(jobAttention).length, history: history.length };
  const base = filter === "history" ? history : filter === "attention" ? open.filter(jobAttention) : open;
  const terms = String(search).trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  const rows = base.filter(row => {
    if (owner === "mine" && row.owner_id !== memberId) return false;
    const haystack = [row.name, row.id, row.owner_id, row.owner_id === memberId ? "我的任务" : "", jobDisplayState(row).label,
      ...Object.keys(jobStates).filter(key => count(row.counts?.[key]) > 0n).map(key => jobStates[key])].join(" ").toLocaleLowerCase();
    return terms.every(term => haystack.includes(term));
  });
  rows.sort(sort === "oldest" ? (a, b) => newest(b, a) : sort === "attention" ? (a, b) => Number(jobAttention(b)) - Number(jobAttention(a)) || newest(a, b) : newest);
  const totals = open.reduce((result, row) => {
    result.running += sum(row.counts, ["assigned", "computing", "running"]);
    result.waiting += sum(row.counts, ["pending", "queued"]);
    return result;
  }, { running: 0n, waiting: 0n });
  return { rows, open, counts, ...totals, known: !!model.sync, support: model.support, loading: model.loading, error: model.error, historyLoaded: model.historyLoaded, historyLoading: model.historyLoading };
}

export function craftingOverviewHTML(model) {
  const metrics = [
    ["未结束任务", model.known ? model.counts.unsettled : null, "组", "生产清单按任务统计", ""],
    ["执行中条目", model.known ? model.running : null, "项", "已派发 · 计算中 · 合成中", "active"],
    ["等待调度", model.known ? model.waiting : null, "项", "待调度与排队条目", ""],
    ["需要处理", model.known ? model.counts.attention : null, "组", "暂停 · 未通过 · 失败 · 待确认", model.counts.attention ? "warn" : ""],
  ];
  return `<div class="crafting-metrics" aria-label="生产任务概况">${metrics.map(([label, value, unit, detail, tone]) => `<div class="crafting-metric" data-tone="${tone}"><span>${label}</span><strong>${value == null ? "—" : formatQuantity(value)}<small>${unit}</small></strong><p>${detail}</p></div>`).join("")}</div>`;
}

export function jobProgressHTML(row) {
  const total = count(row.total);
  const segments = [
    ["done", sum(row.counts, ["done"])],
    ["active", sum(row.counts, ["assigned", "computing", "running"])],
    ["attention", sum(row.counts, attentionKeys)],
    ["waiting", sum(row.counts, ["pending", "queued"])],
    ["ended", sum(row.counts, ["canceled", "expired"])],
  ];
  // SVG attributes keep this bar compatible with the strict style CSP.
  let offset = 0;
  const rects = segments.map(([state, amount]) => {
    const width = total ? Number(amount * 10000n / total) / 100 : 0;
    const rect = width ? `<rect class="crafting-progress-${state}" x="${offset}" y="0" width="${width}" height="4"/>` : "";
    offset += width;
    return rect;
  }).join("");
  return `<div class="crafting-progress"><span>已完成 <b>${formatQuantity(row.counts?.done || "0")}</b> / ${formatQuantity(row.total)} 项</span><svg viewBox="0 0 100 4" preserveAspectRatio="none" aria-hidden="true"><rect class="crafting-progress-track" width="100" height="4"/>${rects}</svg></div>`;
}

export function jobStateCountsHTML(summary) {
  return `<div class="job-counts">${Object.entries(summary.counts || {}).filter(([, n]) => count(n) > 0n).map(([key, n]) => `<span data-state="${esc(key)}">${esc(jobStates[key] || key)} <b class="number">${formatQuantity(n)}</b></span>`).join("")}</div>`;
}

export function craftingJobsHTML(model, filter, { dateTime = String, memberId = "" } = {}) {
  const empty = (title, detail, action = "") => `<div class="crafting-empty">${icon(model.error ? "circle-alert" : "list-checks")}<strong>${title}</strong><span>${detail}</span>${action}</div>`;
  if (model.support === "unsupported") return empty("生产任务接口尚不可用", "单项订单仍可查看和核对。", '<button class="secondary" data-task-mode="orders">查看单项订单</button>');
  if (!model.known) return empty(model.error?.status === 403 ? "没有任务查看权限" : model.error ? "任务读取失败" : model.loading ? "正在读取生产任务…" : "选择网络后查看任务", model.error ? "可从左侧导航刷新重试，原记录会保留。" : "生产任务提交后会显示在这里。");
  if (!model.rows.length) {
    if (filter === "history" && !model.historyLoaded) return empty(model.historyLoading ? "正在读取历史任务…" : "历史尚未读取", "历史任务按批次加载，可保留当前搜索条件。");
    const actualEmpty = filter === "unsettled" && !model.open.length;
    return empty(actualEmpty ? "当前没有未结束任务" : filter === "history" ? "没有匹配的历史任务" : filter === "attention" && !model.counts.attention ? "没有需要处理的任务" : "没有匹配的任务", actualEmpty ? "从库存选择物品，提交清单后在这里跟踪。" : "试试任务名称、ID，或调整筛选条件。", actualEmpty ? '<button class="secondary" data-crafting-new>前往库存添加合成</button>' : '<button class="secondary" data-crafting-search-reset>清除搜索与归属筛选</button>');
  }
  return `<div class="crafting-list-columns" aria-hidden="true"><span>生产任务</span><span>条目状态</span><span>完成条目</span><span>调度状态</span><span></span></div>${model.rows.map(row => {
    const status = jobDisplayState(row);
    return `<button class="crafting-job-row" data-job="${esc(row.id)}" aria-label="查看${esc(row.name || `生产任务 ${row.id}`)}详情"><span class="crafting-job-identity">${icon(jobAttention(row) ? "circle-alert" : "list-checks")}<span><strong>${esc(row.name || `生产任务 ${row.id}`)}</strong><small>${row.owner_id === memberId ? "我的任务" : `所有者 ${esc(row.owner_id)}`} · ${esc(dateTime(row.created_at))}</small></span></span>${jobStateCountsHTML(row)}${jobProgressHTML(row)}<span class="crafting-state" data-tone="${status.tone}"><i aria-hidden="true"></i>${status.label}</span><span class="crafting-row-arrow">${icon("chevron-right")}</span></button>`;
  }).join("")}`;
}

export function craftingOrdersHTML(orders, { group = "batch", itemFor, itemImage, age, dateTime } = {}) {
  const groups = new Map();
  for (const order of orders) {
    const key = group === "cpu" ? order.cpu_name || "自动分配" : order.batch_id || `single:${order.id}`;
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(order);
  }
  return [...groups].map(([key, rows]) => `<section class="crafting-order-group"><div class="crafting-group-heading"><h3>${group === "cpu" ? esc(key) : `${key.startsWith("single:") ? "单项订单" : "提交批次"} · ${esc(dateTime(rows[0].created_at))}`}</h3><span>${rows.length} 项</span></div><div class="crafting-order-columns" aria-hidden="true"><span>物品</span><span>数量</span><span>CPU 策略</span><span>状态</span><span></span></div>${rows.map(order => {
    const item = itemFor(order.item_id);
    const state = order.state === "queued" && order.wait_for_cpu ? "等待指定 CPU" : jobStates[order.state] || order.state;
    const tone = ["needs_review", "failed"].includes(order.state) ? "warn" : order.state === "done" ? "ok" : ["assigned", "running", "computing"].includes(order.state) ? "active" : "idle";
    return `<div class="crafting-order-row"><button class="crafting-order-item" data-order="${esc(order.id)}">${itemImage(item)}<span><strong>${esc(item?.label || `物品 #${order.item_id}`)}</strong><small>${esc(age(order.created_at))} · ${esc(order.id)}</small></span></button><span class="crafting-order-quantity number">× ${formatQuantity(order.quantity)}</span><span class="crafting-order-cpu">${esc(order.cpu_name || "自动分配")}</span><span class="crafting-state" data-tone="${tone}"><i aria-hidden="true"></i>${esc(state)}</span>${order.state === "queued" ? `<button class="icon-button" data-cancel="${esc(order.id)}" title="撤回未派发订单" aria-label="撤回未派发订单">${icon("x")}</button>` : `<span></span>`}</div>`;
  }).join("")}</section>`).join("");
}
