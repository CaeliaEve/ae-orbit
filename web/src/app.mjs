import {
  createIcons, Orbit, Network, LogOut, Boxes, ListChecks, Cpu, RadioTower, RefreshCw,
  Search, ListPlus, ChevronLeft, ChevronRight, ChevronDown, X, Plus, Minus, Package,
  Trash2, Check, Clock, CircleAlert, LogIn, ArrowUpRight, Server, Send, Pause, Play, Square,
  LayoutDashboard, Settings, Github, Bell, Activity, Monitor, SlidersHorizontal, Code2, FileText, CircleHelp,
} from "lucide";
import { createAPI } from "./api.mjs";
import { loadOrderSnapshot } from "./orders.mjs";
import { createJobController } from "./job-controller.mjs";
import { isJobClaim } from "./jobs.mjs";
import { jobListHTML, jobDetailHTML, recoveryHTML } from "./job-view.mjs";
import {
  normalizeQuantity, formatQuantity, storageKey, loadDraft, saveDraft, addDraft,
  updateDraft, removeDraft, completeSubmission, networkQualityLabel,
} from "./model.mjs";
import { loadSettings, saveSettings } from "./settings.mjs";
import { clearSession, loadSession, saveSession } from "./session.mjs";

const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];
const esc = value => String(value ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const quantityHTML = value => formatQuantity(value).replaceAll(",", ",<wbr>");
const icon = name => `<i data-lucide="${name}" aria-hidden="true"></i>`;
const icons = { Orbit, Network, LogOut, Boxes, ListChecks, Cpu, RadioTower, RefreshCw, Search, ListPlus, ChevronLeft, ChevronRight, ChevronDown, X, Plus, Minus, Package, Trash2, Check, Clock, CircleAlert, LogIn, ArrowUpRight, Server, Send, Pause, Play, Square, LayoutDashboard, Settings, Github, Bell, Activity, Monitor, SlidersHorizontal, Code2, FileText, CircleHelp };
const paint = () => createIcons({ icons });
const api = createAPI();
const state = {
  member: null, network: null, networks: [], view: "dashboard", items: [], itemCache: new Map(),
  orders: [], devices: [], cpus: { cpus: [] }, icons: {}, filter: "", sort: "name", search: "",
  cursor: "", nextCursor: "", cursors: [""], page: 0, selected: null, amount: "1",
  cpuName: "", waitForCPU: false, inspector: "detail", draft: { version: 1, entries: [], receipts: {} },
  draftKey: "", draftBlocked: false, unchecked: new Set(), busy: false, preparing: false, inventoryLoading: false,
  orderFilter: "active", orderGroup: "batch", stream: null, streamConnected: false, generation: 0,
  context: 0, orderGeneration: 0, orderSnapshot: null, ordersLoading: false, ordersError: false,
  activeOrderCount: 0, activeOrdersTruncated: false, detailId: "", detailGeneration: 0,
  taskMode: "jobs", jobFilter: "unsettled", jobStorageError: false,
  settings: loadSettings(), settingsSection: "display", session: null,
};
const jobs = createJobController({
  storage: localStorage, request: (path, options) => api.request(path, options),
  lock: (key, work) => navigator.locks ? navigator.locks.request(key, work) : work(),
  onChange: () => {
    if (!$("#job-list")) return;
    try {
      state.draft = jobs.local().draft;
      state.jobStorageError = false;
    } catch { state.jobStorageError = true; }
    state.draftBlocked = !!state.draft.warning || state.jobStorageError;
    renderJobs();
    if (!$("#draft-panel").contains(document.activeElement)) renderDraft();
    if (!$("#detail-panel").contains(document.activeElement)) renderDetail();
  },
});
const activeStates = new Set(["queued", "assigned", "computing", "running"]);
const attentionStates = new Set(["failed", "needs_review"]);
const endedStates = new Set(["done", "failed", "canceled", "expired"]);
const stateNames = { queued: "排队中", assigned: "已派发", computing: "计算中", running: "合成中", done: "已完成", failed: "失败", canceled: "已撤回", expired: "已过期", needs_review: "结果待确认" };
let searchTimer, toastTimer, streamTimer, streamRefreshTimer, queryController, ordersController, detailController, cpuCallback, inspectorFocus;

function toast(message, error = false, category = "general") {
  if (!error && category !== "general") {
    const enabled = category === "task" ? state.settings.taskNotifications : category === "network" ? state.settings.networkNotifications : state.settings.syncNotifications;
    if (!enabled) return;
  }
  clearTimeout(toastTimer);
  const el = $("#toast");
  el.textContent = message;
  el.dataset.error = String(error);
  el.hidden = false;
  toastTimer = setTimeout(() => { el.hidden = true; }, error ? 6500 : 3200);
}

function applySettings() {
  document.documentElement.dataset.density = state.settings.density;
  document.documentElement.dataset.inventoryLayout = state.settings.inventoryLayout;
  const remember = $("#remember-session");
  if (remember) remember.checked = state.settings.rememberSession;
}

function updateSettings(patch) {
  try {
    state.settings = saveSettings(localStorage, { ...state.settings, ...patch });
    applySettings();
    if (Object.prototype.hasOwnProperty.call(patch, "rememberSession")) {
      if (state.settings.rememberSession && state.session) saveSession(localStorage, sessionStorage, state.session, true);
      if (!state.settings.rememberSession) clearSession(localStorage, sessionStorage);
    }
    if (Object.prototype.hasOwnProperty.call(patch, "reconnect") && state.network) {
      if (state.settings.reconnect) connectStream();
      else state.stream?.close();
    }
    renderSettings();
    toast("设置已保存");
  } catch { toast("设置无法保存，请检查浏览器存储。", true); }
}

function settingControl(key, label, description, checked) {
  return `<label class="setting-toggle"><span><strong>${label}</strong><small>${description}</small></span><input type="checkbox" data-setting="${key}" ${checked ? "checked" : ""}><span class="toggle-track" aria-hidden="true"></span></label>`;
}

function renderSettings() {
  const detail = $("#settings-detail");
  if (!detail) return;
  const section = state.settingsSection;
  const sections = {
    display: {
      title: "显示",
      description: "调整工作台密度与库存网格，不改变页面结构。",
      body: `<div class="setting-field"><span><strong>界面密度</strong><small>控制页面标题、卡片和列表的垂直间距。</small></span><div class="setting-segment" role="group" aria-label="界面密度"><button type="button" data-setting-value="density" data-value="compact" aria-pressed="${state.settings.density === "compact"}">紧凑</button><button type="button" data-setting-value="density" data-value="comfortable" aria-pressed="${state.settings.density === "comfortable"}">舒适</button></div></div><div class="setting-field"><span><strong>库存网格</strong><small>在桌面端选择每行物品数量，自动模式随窗口宽度调整。</small></span><select data-setting="inventoryLayout" aria-label="库存网格"><option value="auto" ${state.settings.inventoryLayout === "auto" ? "selected" : ""}>自动</option><option value="dense" ${state.settings.inventoryLayout === "dense" ? "selected" : ""}>紧密</option><option value="wide" ${state.settings.inventoryLayout === "wide" ? "selected" : ""}>宽松</option></select></div>`,
    },
    notifications: {
      title: "通知",
      description: "选择哪些状态变化通过页面提示反馈。错误提示始终保留。",
      body: settingControl("taskNotifications", "任务状态", "提交、核对和控制操作完成时提示。", state.settings.taskNotifications) + settingControl("networkNotifications", "网络异常", "网络刷新或连接状态异常时提示。", state.settings.networkNotifications) + settingControl("syncNotifications", "同步提示", "库存和事件流更新时提示，默认关闭以减少打扰。", state.settings.syncNotifications),
    },
    connection: {
      title: "连接与凭据",
      description: "管理服务连接、自动重连和浏览器会话。",
      body: settingControl("reconnect", "自动重连", "网络恢复后自动重新建立事件流连接。", state.settings.reconnect) + settingControl("rememberSession", "记住本次会话", "仅保存会话恢复信息，不保存原始访问令牌。", state.settings.rememberSession) + `<div class="setting-fact"><span>后端地址</span><strong>${esc(location.origin)}</strong><small>页面通过同源代理连接后端；开发环境由启动参数配置。</small></div><div class="setting-fact"><span>会话状态</span><strong>${state.member ? "已连接" : "未连接"}</strong><small>${state.session?.expiresAt ? `会话有效期至 ${esc(dateTime(state.session.expiresAt))}` : "登录后可在此浏览器恢复会话。"}</small></div><div class="setting-actions"><button type="button" class="secondary" data-session-action="test"><i data-lucide="refresh-cw"></i>测试连接</button><button type="button" class="secondary" data-session-action="login"><i data-lucide="log-in"></i>重新登录</button><button type="button" class="secondary" data-session-action="clear"><i data-lucide="trash-2"></i>清除已保存会话</button></div>`,
    },
  };
  const current = sections[section] || sections.display;
  detail.innerHTML = `<div class="settings-detail-heading"><div><h2>${current.title}</h2><span class="muted">${current.description}</span></div></div><div class="settings-detail-body">${current.body}</div>`;
  paint();
}

function errorText(error) {
  if (error?.name === "AbortError") return "";
  if (error?.status === 401) showLogin();
  if (error?.status || error?.code) return error.message;
  return "操作未完成，请检查数量、当前状态或浏览器存储。";
}

function message(id, text) {
  const el = $(id);
  el.textContent = text;
  el.hidden = !text;
}

function dateTime(value) {
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? date.toLocaleString("zh-CN", { hour12: false }) : "尚无记录";
}

function age(value) {
  const time = new Date(value).getTime();
  if (!Number.isFinite(time)) return "未观测";
  const seconds = Math.max(0, Math.floor((Date.now() - time) / 1000));
  if (seconds < 60) return `${seconds} 秒前`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`;
  return `${Math.floor(seconds / 86400)} 天前`;
}

function itemImage(item) {
  const filename = item?.kind === "item" ? state.icons[`${item.name}@${item.damage}`] : null;
  if (typeof filename === "string" && /^[a-f0-9]{64}\.png$/.test(filename)) {
    return `<img class="item-image" src="/assets/nei/${filename}" width="32" height="32" alt="" loading="lazy">`;
  }
  return `<span class="missing-image" title="暂无已核验图标">${icon("package")}</span>`;
}

function itemFor(id) {
  return state.itemCache.get(String(id)) || Object.values(state.draft.receipts).find(r => r.item?.id === String(id))?.item;
}

const networkPath = (suffix = "", network = state.network?.id) => `/api/v1/networks/${encodeURIComponent(network)}${suffix}`;
const maximum = () => state.network?.capabilities?.max_craft_quantity || "9223372036854775807";

function renderDashboard() {
  const network = state.network;
  const cpus = state.cpus.cpus || [];
  const busy = cpus.filter(cpu => cpu.busy).length;
  const activeJobs = jobs.state.sync ? [...jobs.state.sync.jobs.values()].filter(row => !row.settled_at).length : state.activeOrderCount;
  const onlineDevices = state.devices.filter(device => device.online).length;
  const networkState = !state.member ? "未连接" : !network ? "暂无网络" : network.online ? "在线" : "离线";
  const networkMeta = !state.member ? "登录后开始同步" : !network ? "请选择一个网络" : `${network.name} · ${state.streamConnected ? "实时连接" : "等待连接"}`;
  $("#dashboard-network-state").textContent = networkState;
  $("#dashboard-network-meta").textContent = networkMeta;
  $("#dashboard-inventory-count").textContent = formatQuantity(state.items.length);
  $("#dashboard-job-count").textContent = formatQuantity(activeJobs || 0);
  $("#dashboard-cpu-state").textContent = cpus.length ? `${busy} / ${cpus.length}` : "暂无观测";
  $("#dashboard-cpu-meta").textContent = cpus.length ? "忙碌 / 已观测" : "等待 CPU 状态";
  $("#dashboard-cpu-ring").style.setProperty("--ring", cpus.length ? `${Math.round((busy / cpus.length) * 100)}%` : "0%");
  $("#dashboard-cpu-ring span").textContent = cpus.length ? `${Math.round((busy / cpus.length) * 100)}%` : "—";
  $("#dashboard-sync").textContent = network?.last_seen ? `最近同步 ${age(network.last_seen)}` : "等待同步";
  $("#dashboard-health-list").innerHTML = [
    ["网络连接", network ? (network.online ? "运行正常" : "OC 离线") : "等待网络", network?.online ? "ok" : network ? "warn" : "idle"],
    ["库存观测", network?.last_inventory ? `${age(network.last_inventory)} · 修订 ${formatQuantity(network.revision)}` : "尚无记录", network?.last_inventory ? "ok" : "idle"],
    ["设备状态", state.devices.length ? `${onlineDevices} / ${state.devices.length} 在线` : "尚无设备观测", state.devices.length && onlineDevices === state.devices.length ? "ok" : state.devices.length ? "warn" : "idle"],
    ["CPU 观测", cpus.length ? `${busy} 台忙碌 · ${cpus.length} 台已读取` : "等待 CPU 状态", cpus.length && busy < cpus.length ? "ok" : cpus.length ? "warn" : "idle"],
    ["任务队列", activeJobs ? `${activeJobs} 项未完成` : "当前为空", activeJobs ? "warn" : "ok"],
  ].map(([label, value, tone]) => `<div class="health-row"><span><i class="status-dot" data-tone="${tone}"></i>${label}</span><strong>${esc(value)}</strong></div>`).join("");
  const trend = $("#dashboard-trend");
  if (trend) {
    const samples = state.items.slice(0, 32).map(item => {
      try { return Math.log10(Math.max(1, Number(BigInt(item.quantity || "0")))); }
      catch { return 0; }
    }).filter(value => Number.isFinite(value));
    if (samples.length > 1) {
      const min = Math.min(...samples), max = Math.max(...samples);
      const span = Math.max(max - min, 1);
      const points = samples.map((value, index) => {
        const x = 36 + (index / Math.max(samples.length - 1, 1)) * 648;
        const y = 218 - ((value - min) / span) * 158;
        return [x, y];
      });
      const line = points.map(([x, y], index) => `${index ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ");
      const area = `${line} L 684 236 L 36 236 Z`;
      trend.innerHTML = `<div class="trend-plot"><svg viewBox="0 0 720 264" role="img" aria-label="库存样本趋势"><g class="trend-grid"><path d="M36 38H684M36 104H684M36 170H684M36 236H684"></path></g><path class="trend-area" d="${area}"></path><path class="trend-line" d="${line}"></path></svg><div class="trend-axis"><span>样本 1</span><span>样本 ${samples.length}</span></div></div>`;
    } else {
      trend.innerHTML = `<div class="trend-empty"><i data-lucide="activity"></i><span>登录后显示库存样本</span></div>`;
    }
  }
  const activity = [
    network?.last_seen ? ["网络状态", network.online ? "主基地网络在线" : "主基地网络离线", age(network.last_seen)] : ["网络状态", "尚未连接网络", "等待操作"],
    state.items.length ? ["库存同步", `已载入 ${formatQuantity(state.items.length)} 项`, age(state.items[0]?.observed_at || network?.last_inventory)] : ["库存同步", "等待库存观测", network?.last_inventory ? age(network.last_inventory) : "尚无记录"],
    cpus.length ? ["CPU 观测", `${cpus.length} 台 CPU 已读取`, age(state.cpus.observed_at)] : ["CPU 观测", "等待 CPU 状态", network?.services?.cpu_observed_at ? age(network.services.cpu_observed_at) : "尚无记录"],
    state.devices.length ? ["设备同步", `${onlineDevices} / ${state.devices.length} 台设备在线`, age(state.devices.map(device => device.last_seen).filter(Boolean).sort().at(-1))] : ["设备同步", "等待设备观测", "尚无记录"],
  ];
  $("#dashboard-activity-list").innerHTML = activity.map(([label, value, time]) => `<div class="activity-row"><span class="activity-icon"><i data-lucide="activity"></i></span><span><strong>${esc(value)}</strong><small>${esc(label)}</small></span><time>${esc(time)}</time></div>`).join("");
  paint();
}

function refreshHeader() {
  $("#member-name").textContent = state.member?.name || "未登录";
  $("#logout").hidden = !state.member;
  const network = state.network;
  $("#connection").textContent = !state.member ? "未登录" : !network ? "暂无网络" : !network.online ? "OC 离线" : state.streamConnected ? "已连接" : "状态重连中";
  $("#connection").dataset.state = !state.member || !network ? "idle" : !network.online ? "offline" : state.streamConnected ? "online" : "reconnecting";
  $("#network-quality").textContent = network ? networkQualityLabel(network.quality) : "";
  $("#inventory-summary").textContent = network ? `${network.name} · ${network.online ? "在线" : "离线"}` : "尚未连接网络";
  $("#draft-count").textContent = state.draft.entries.length;
  renderDashboard();
}

function showView(view) {
  state.view = view;
  for (const name of ["dashboard", "inventory", "orders", "cpus", "network", "github", "settings"]) $(`#view-${name}`).hidden = name !== view;
  $$("[data-view]").forEach(button => {
    if (button.dataset.view === view) button.setAttribute("aria-current", "page");
    else button.removeAttribute("aria-current");
  });
  closeInspector(false);
  if (view === "orders") {
    renderJobs();
    renderOrders();
    if (state.network) { if (state.taskMode === "jobs") refreshJobs(); else refreshOrders(); }
  }
  if (view === "cpus") {
    renderCPUs();
    if (state.network) refreshCPUs();
  }
  if (view === "network") {
    renderNetwork();
    if (state.network) refreshNetwork();
  }
  if (view === "dashboard") renderDashboard();
}

function openInspector(tab = "detail") {
  inspectorFocus = document.activeElement;
  state.inspector = tab;
  $("#detail-panel").hidden = tab !== "detail";
  $("#draft-panel").hidden = tab !== "draft";
  $("#detail-tab").setAttribute("aria-pressed", String(tab === "detail"));
  $("#draft-tab").setAttribute("aria-pressed", String(tab === "draft"));
  $("#inspector").dataset.open = "true";
  const mobile = matchMedia("(max-width:800px)").matches;
  document.body.classList.toggle("sheet-open", mobile);
  $("#sheet-backdrop").hidden = !mobile;
  if (mobile) $("#close-inspector").focus();
  if (tab === "draft") renderDraft();
}

function closeInspector(restore = true) {
  delete $("#inspector").dataset.open;
  document.body.classList.remove("sheet-open");
  $("#sheet-backdrop").hidden = true;
  if (restore && inspectorFocus?.isConnected) inspectorFocus.focus();
}

function renderInventory() {
  const tbody = $("#inventory-rows");
  tbody.innerHTML = state.items.map(item => `<tr data-selected="${state.selected?.id === item.id}" data-id="${esc(item.id)}">
    <td><button class="item-button" data-item="${esc(item.id)}">${itemImage(item)}<span class="item-copy"><span class="item-name">${esc(item.label || item.name)}</span><span class="item-meta">${esc(item.name.split(":")[0])}${item.craftable ? " · 可合成" : ""}${!item.exact ? " · 身份待确认" : ""}</span></span></button></td>
    <td><span class="stock-value ${String(item.quantity).length > 12 ? "long-number" : ""}">${quantityHTML(item.quantity)}</span><span class="freshness ${item.current_session ? "" : "stale"}">${item.current_session ? age(item.observed_at) : "库存待同步"}</span></td>
    <td><button class="icon-button" data-quick-add="${esc(item.id)}" title="加入清单" aria-label="将${esc(item.label || item.name)}加入清单" ${item.craftable && item.exact && !state.draftBlocked && !state.busy && !state.preparing ? "" : "disabled"}>${icon("plus")}</button></td>
  </tr>`).join("");
  $("#inventory-empty").hidden = state.items.length > 0;
  $("#inventory-empty").textContent = state.inventoryLoading ? "正在读取库存…" : !state.member ? "登录后查看库存" : !state.network ? "没有可访问的网络" : "没有匹配的物品";
  $("#prev-page").disabled = state.page === 0 || state.inventoryLoading;
  $("#next-page").disabled = !state.nextCursor || state.inventoryLoading;
  $("#page-label").textContent = `第 ${state.page + 1} 页 · 本页 ${state.items.length} 项`;
  paint();
}

function renderDetail() {
  const item = state.selected;
  if (!item) {
    $("#detail-panel").innerHTML = `<div class="draft-empty">尚未选择物品</div>`;
    return;
  }
  const canAdd = item.craftable && item.exact && !state.draftBlocked && !state.busy && !state.preparing;
  const reason = !item.exact ? "物品身份待确认" : !item.craftable ? "没有可用合成样板" : "";
  $("#detail-panel").innerHTML = `<div class="detail-heading">${itemImage(item)}<div><h2>${esc(item.label || item.name)}</h2><span class="muted">${esc(item.name.split(":")[0])}</span></div></div>
    <span class="muted">库存数量</span><div class="detail-stock number ${String(item.quantity ?? "0").length > 12 ? "long-number" : ""}">${quantityHTML(item.quantity ?? "0")}</div><span class="item-meta">${esc(item.name)} · ${esc(item.damage)}</span>
    <div class="detail-facts"><div class="fact"><span>库存观测</span><span>${item.current_session ? age(item.observed_at) : "待同步"}</span></div><div class="fact"><span>合成样板</span><span>${item.craftable ? item.craftable_current_session ? "已确认" : "待重新核验" : "不可合成"}</span></div><div class="fact"><span>身份</span><span>${item.exact ? "精确" : "待确认"}</span></div></div>
    <label class="field-label" for="craft-quantity">合成数量</label><div class="stepper"><button data-step="-1" title="减少数量" aria-label="减少数量">${icon("minus")}</button><input id="craft-quantity" type="text" inputmode="numeric" maxlength="19" value="${esc(state.amount)}" autocomplete="off"><button data-step="1" title="增加数量" aria-label="增加数量">${icon("plus")}</button></div>
    <span class="field-label">合成 CPU</span><button class="cpu-select" id="detail-cpu" disabled title="生产任务暂不支持指定 CPU"><span>自动分配 · 暂不支持指定 CPU</span>${icon("cpu")}</button>
    <div class="detail-actions"><button class="primary wide" id="add-to-draft" ${canAdd ? "" : "disabled"}>${icon("list-plus")}加入清单</button><button class="secondary wide" id="submit-immediately" ${canAdd && jobs.state.support === "supported" && !state.busy ? "" : "disabled"}>${icon("send")}立即提交</button></div>
    <p id="detail-feedback" class="detail-feedback" role="status">${esc(reason || submissionStatus())}</p>`;
  paint();
}

function selectedDraftIds() {
  return state.draft.entries.filter(e => e.status === "draft" && !state.unchecked.has(e.id)).map(e => e.id);
}

function submissionStatus() {
  if (state.jobStorageError) return "生产任务本机记录无法恢复，提交已停止；请保留浏览器数据并人工核对。";
  if (jobs.state.support === "unsupported") return "后端需要升级生产任务接口；新提交已禁用。";
  if (jobs.state.support !== "supported") return "生产任务接口尚未验证；请刷新任务。";
  return state.network && !state.network.online ? "OC 离线仍可保存生产意图，执行等待设备恢复。" : "";
}

function renderJobs() {
  const model = jobs.state;
  $("#jobs-panel").hidden = state.taskMode !== "jobs";
  $("#legacy-panel").hidden = state.taskMode !== "orders";
  $("#job-list").innerHTML = jobListHTML(model, state.jobFilter, dateTime);
  $("#job-list").setAttribute("aria-busy", String(model.loading));
  if (model.sync) {
    const jobsRows = [...model.sync.jobs.values()];
    const open = jobsRows.filter(row => !row.settled_at);
    const attention = open.filter(row => row.control === "paused" || row.counts.needs_review !== "0" || row.counts.rejected !== "0" || row.counts.failed !== "0").length;
    $("#jobs-summary").textContent = `${open.length} 项未完成${attention ? ` · ${attention} 项待处理` : ""}${model.loading ? " · 刷新中" : ""}`;
  } else {
    $("#jobs-summary").textContent = model.loading ? "正在读取任务…" : "";
  }
  const error = state.jobStorageError ? submissionStatus() : model.error ? `任务读取未完成${model.sync ? "，保留上次记录" : ""}。${model.error.status === 403 ? "当前账号无权限。" : errorText(model.error)}` : "";
  message("#jobs-message", error);
  let local;
  try { local = jobs.local(); $("#job-recoveries").innerHTML = recoveryHTML(local, icon); }
  catch { $("#job-recoveries").innerHTML = `<p class="error">本机生产任务记录损坏。请保留浏览器数据，勿重复提交。</p>`; }
  const more = $("#job-history-more");
  more.hidden = state.jobFilter !== "history" || model.support !== "supported" || !model.historyMore;
  more.disabled = model.historyLoading;
  more.innerHTML = `${icon("chevron-down")}${model.historyLoading ? "正在读取…" : model.historyLoaded ? "加载更多历史" : "加载历史记录"}`;
  if (state.taskMode === "jobs") {
    $("#refresh-orders").disabled = model.loading;
    $("#order-count").textContent = model.sync ? [...model.sync.jobs.values()].filter(row=>!row.settled_at).length || "" : "";
    $("#order-count").title = "未完成生产任务";
  }
  if ($("#job-dialog").open) {
    $("#refresh-job-detail").disabled = model.detailLoading;
    message("#job-detail-status", model.detailError ? `详情不可用。${model.detailError.status === 404 ? "记录已归档或不存在；本机意图不会解锁。" : errorText(model.detailError)}` : model.detailLoading ? "正在读取详情…" : "");
    $("#job-detail-status").dataset.error = String(!!model.detailError);
    $("#job-detail").setAttribute("aria-busy", String(model.detailLoading));
    if (model.detail && local) $("#job-detail").innerHTML = jobDetailHTML(model.detail, local, model.detailError ? null : state.member,
      {itemFor,itemImage,quantity:quantityHTML,dateTime,icon,controlsAvailable:!model.detailLoading && !model.detailError});
    else $("#job-detail").innerHTML = "";
  }
  renderDashboard();
  paint();
}

async function refreshJobs() {
  const context = state.context;
  await jobs.sync();
  if (context !== state.context) return;
  if ($("#job-dialog").open) await refreshJobDetail();
}

async function refreshJobDetail() {
  const context = state.context, id = jobs.state.detailId;
  await jobs.detail();
  if (context !== state.context || id !== jobs.state.detailId || !$("#job-dialog").open) return;
  const entries = jobs.state.detail?.entries;
  if (entries) {
    try { await lookupItems(entries.map(entry=>entry.item_id).filter(id=>!state.itemCache.has(String(id)))); } catch {}
    if (context === state.context && id === jobs.state.detailId && $("#job-dialog").open) renderJobs();
  }
}

async function showJob(id) {
  jobs.closeDetail();
  if (!$("#job-dialog").open) $("#job-dialog").showModal();
  await jobs.detail(id);
  if (jobs.state.detailId === id && $("#job-dialog").open) await refreshJobDetail();
}

function renderDraft() {
  const entries = state.draft.entries;
  const chosen = selectedDraftIds();
  $("#draft-count").textContent = entries.length;
  $("#draft-panel").innerHTML = state.draftBlocked ? `<p class="error">本机清单或生产任务记录无法恢复。请保留浏览器数据并核对已提交任务，不要重复提交。</p>` : `
    <div class="draft-toolbar"><label class="check"><input id="draft-check-all" type="checkbox" ${chosen.length > 0 && chosen.length === entries.filter(e => e.status === "draft").length ? "checked" : ""} ${state.busy ? "disabled" : ""}>全选 · ${chosen.length} 项</label><button class="secondary" id="bulk-cpu" disabled title="生产任务暂不支持指定 CPU">${icon("cpu")}CPU</button></div>
    ${entries.map(entry => `<div class="draft-entry" data-draft-id="${esc(entry.id)}">
      <div class="draft-entry-top"><input class="draft-check" type="checkbox" aria-label="选择${esc(entry.item.label)}" data-check="${esc(entry.id)}" ${state.unchecked.has(entry.id) ? "" : "checked"} ${entry.status === "draft" && !state.busy ? "" : "disabled"}>${itemImage(entry.item)}<label>${esc(entry.item.label)}</label><button class="icon-button" data-remove="${esc(entry.id)}" title="移除草稿" aria-label="移除${esc(entry.item.label)}草稿" ${entry.status === "draft" && !state.busy ? "" : "disabled"}>${icon("trash-2")}</button></div>
      <div class="draft-entry-fields"><input class="draft-quantity" data-quantity="${esc(entry.id)}" type="text" inputmode="numeric" maxlength="19" aria-label="${esc(entry.item.label)}合成数量" value="${esc(entry.quantity)}" ${entry.status === "draft" && !state.busy ? "" : "disabled"}><button class="cpu-select" disabled title="生产任务暂不支持指定 CPU"><span>${esc(entry.cpu_name || "自动分配")}${entry.wait_for_cpu ? " · 等待" : ""}</span>${icon("cpu")}</button></div>
      ${entry.status === "draft" && (entry.cpu_name || entry.wait_for_cpu) ? `<p class="draft-note">已保存的指定 CPU 策略不受支持。</p><button class="secondary wide" data-reset-cpu="${esc(entry.id)}">${icon("cpu")}改为自动分配</button>` : ""}
      ${entry.status !== "draft" || entry.error ? `<p class="draft-note" data-status="${entry.status}">${entry.status === "submitting" ? "正在提交" : entry.status === "uncertain" ? "受理结果待核对" : esc(entry.error)}</p>` : ""}
      ${entry.status === "uncertain" ? isJobClaim(entry) ? `<button class="secondary wide" data-job-reconcile="${esc(entry.batch_id.slice(4))}">${icon("refresh-cw")}核对原生产任务</button>` : `<button class="secondary wide" data-reconcile="${esc(entry.id)}">${icon("refresh-cw")}核对原单项订单</button>` : ""}
    </div>`).join("")}
    ${entries.length ? `<div class="draft-submit"><button class="primary" id="submit-draft" ${chosen.length && !state.busy && jobs.state.support === "supported" && !entries.some(e=>chosen.includes(e.id)&&(e.cpu_name||e.wait_for_cpu)) ? "" : "disabled"}>${icon("send")}${state.busy ? "正在提交生产任务" : `提交 ${chosen.length} 项生产任务`}</button><p class="draft-note">${esc(submissionStatus())}</p></div>` : `<div class="draft-empty">清单为空</div>`}`;
  paint();
}

function readLocalDraft(key = state.draftKey) {
  try { return loadDraft(localStorage, key); }
  catch { return { version: 1, entries: [], receipts: {}, warning: "unavailable" }; }
}

async function changeDraft(transform, key = state.draftKey) {
  if (!key || state.draftBlocked) { toast("请先登录并确认本机清单状态。", true); return false; }
  const apply = () => {
    if (key !== state.draftKey) return false;
    const current = jobs.local().draft;
    if (current.warning) throw new Error("Invalid saved draft");
    const next = transform(current);
    saveDraft(localStorage, key, next);
    state.draft = next;
    renderDraft();
    return true;
  };
  try {
    return navigator.locks ? await navigator.locks.request(key, apply) : apply();
  } catch {
    if (key === state.draftKey) {
      try { state.draft = jobs.local().draft; state.jobStorageError = false; }
      catch { state.jobStorageError = true; }
      state.draftBlocked = !!state.draft.warning || state.jobStorageError;
      renderDraft();
    }
    toast("本机清单未保存，操作已停止。请检查浏览器存储。", true);
    return false;
  }
}

async function addItem(item, value, immediately = false) {
  if (!item || state.busy || state.preparing) return;
  let quantity;
  try { quantity = normalizeQuantity(value, maximum()); }
  catch { message("#detail-feedback", `请输入 1 至 ${formatQuantity(maximum())} 的整数。`); return; }
  const cpuName = state.cpuName, waitForCPU = state.waitForCPU;
  state.preparing = true;
  renderDetail(); renderInventory();
  try {
    let entryId;
    const success = await changeDraft(current => {
      const entries = addDraft(current.entries, item, quantity, cpuName, waitForCPU, { merge: !immediately });
      const entry = immediately ? entries.at(-1) : entries.find(e => e.status === "draft" && e.item.id === String(item.id) && e.cpu_name === cpuName && e.wait_for_cpu === waitForCPU);
      normalizeQuantity(entry.quantity, maximum());
      entryId = entry.id;
      return { ...current, entries };
    });
    if (!success) return;
    state.unchecked.delete(entryId);
    renderDraft();
    if (immediately) await submitDraft([entryId]);
    else toast("已加入本机清单");
  } finally {
    state.preparing = false;
    renderDetail(); renderInventory();
  }
}

async function submitDraft(ids = selectedDraftIds()) {
  if (state.busy || !ids.length || state.draftBlocked) return;
  const context = state.context;
  state.busy = true;
  renderDraft(); renderDetail(); renderInventory();
  try {
    await jobs.submit(ids);
    if (context === state.context) { toast(`生产任务已受理 · ${ids.length} 项`, false, "task"); await refreshJobs(); }
  } catch (error) {
    if (context === state.context) toast(`${errorText(error)} 原清单保持锁定，请到生产任务核对。`, true);
  } finally {
    if (context === state.context) {
      state.busy = false;
      try { state.draft = jobs.local().draft; } catch { state.jobStorageError = true; state.draftBlocked = true; }
      renderDraft(); renderDetail(); renderInventory(); renderJobs();
    }
  }
}

async function matchesIntent(order, intent, network, member) {
  if (!order?.id || order.network_id !== network || order.user_id !== member || String(order.quantity) !== intent.quantity) return false;
  if (String(order.item_id) === intent.item.id) return true;
  const value = await api.request(networkPath(`/items/lookup?${new URLSearchParams({ ids: intent.item.id })}`, network));
  return value.items?.some(item => String(item.id) === intent.item.id && String(item.canonical_id || item.id) === String(order.item_id));
}

async function acknowledge(id, intent, order, key = state.draftKey) {
  return changeDraft(current => {
    const pending = current.entries.find(entry => entry.id === id);
    if (!pending || pending.status === "draft" || pending.request_key !== intent.request_key || pending.quantity !== intent.quantity) {
      throw new Error("The pending request changed.");
    }
    const receipts = { ...current.receipts, [order.id]: { batch_id: order.batch_id || intent.batch_id, item: intent.item } };
    const bounded = Object.fromEntries(Object.entries(receipts).slice(-1000));
    return { ...current, receipts: bounded, entries: completeSubmission(current.entries, id, { order }) };
  }, key);
}

async function reconcile(id) {
  const intent = state.draft.entries.find(e => e.id === id && e.status === "uncertain");
  if (!intent || isJobClaim(intent)) return;
  const context = state.context, key = state.draftKey, network = state.network.id, member = state.member.id;
  try {
    const order = await api.request(networkPath(`/orders/by-key/${encodeURIComponent(intent.request_key)}`, network));
    if (context !== state.context) return;
    const matches = await matchesIntent(order, intent, network, member);
    if (context !== state.context) return;
    if (!matches) {
      toast("找到原请求，但物品、数量或所属账号不一致，请人工核对。", true);
      await refreshOrders();
      return;
    }
    if (await acknowledge(id, intent, order, key)) {
      toast("已确认原订单受理，无需重复提交。");
      refreshOrders(false);
    }
  } catch (error) {
    if (context !== state.context) return;
    toast(error.status === 404 ? "暂未查到原订单，清单仍保持待核对，不会重新下单。" : errorText(error), true);
  }
}

function renderCPUs() {
  const cpus = state.cpus.cpus || [];
  if (!state.network) {
    $("#cpu-summary").textContent = "尚未连接网络";
    $("#cpu-overview").innerHTML = `<div class="empty empty-state"><i data-lucide="wifi-off"></i><strong>尚未连接网络</strong><span>连接 AE 网络后显示 CPU 状态与负载。</span></div>`;
    $("#cpu-list").innerHTML = "";
    paint();
    return;
  }
  const busy = cpus.filter(cpu => cpu.busy).length;
  const idle = Math.max(cpus.length - busy, 0);
  const outputs = cpus.filter(cpu => cpu.output).length;
  const observed = state.cpus.observed_at || state.network?.services?.cpu_observed_at;
  const busyPercent = cpus.length ? Math.round((busy / cpus.length) * 100) : 0;
  $("#cpu-summary").textContent = `${cpus.length} 台已观测 · ${observed ? age(observed) : "未观测"}${state.cpus.truncated ? " · 列表不完整" : ""}`;
  const bars = cpus.length ? cpus.map((cpu, index) => {
    const height = cpu.busy ? 72 + (index % 4) * 6 : 18 + (index % 3) * 5;
    return `<i style="height:${height}%" title="${esc(cpu.name || `CPU ${index + 1}`)} · ${cpu.busy ? "忙碌" : "空闲"}"></i>`;
  }).join("") : `<span class="muted">暂无负载样本</span>`;
  const overview = `<section id="cpu-overview" class="cpu-overview" aria-label="CPU 摘要">
    <div class="cpu-donut" style="--value:${busyPercent}%"><strong>${busyPercent}%</strong><small>忙碌</small><span>${busy} / ${cpus.length || 0} 台</span></div>
    <div class="cpu-legend">
      <div><span>已观测 CPU</span><b class="number">${formatQuantity(cpus.length)}</b></div>
      <div><span>空闲 CPU</span><b class="number">${formatQuantity(idle)}</b></div>
      <div><span>可见输出</span><b class="number">${formatQuantity(outputs)}</b></div>
    </div>
    <div class="cpu-load"><span class="muted">当前快照负载</span><div class="load-bars">${bars}</div></div>
  </section>`;
  const rows = cpus.length ? `<div class="cpu-table" role="list" aria-label="CPU 列表">${cpus.map((cpu, index) => {
    const output = cpu.output ? `${cpu.output.label || cpu.output.name || "未知产物"} × ${formatQuantity(cpu.output.quantity)}` :
      cpu.output_unavailable || (cpu.busy ? "输出信息暂不可用" : "暂无合成输出");
    return `<div class="cpu-row" role="listitem">${icon("cpu")}<div><h2>${esc(cpu.name || `未命名 CPU ${index + 1}`)}</h2><p>${formatQuantity(cpu.storage)} B · ${formatQuantity(cpu.coprocessors)} 协处理器 · 快照 ${esc(cpu.id || `${index + 1}`)}</p><p>${esc(output)}</p></div><span class="state-label ${cpu.busy ? "busy" : ""}">${cpu.busy ? "忙碌" : "空闲"}</span></div>`;
  }).join("")}</div>` : `<div class="empty">暂无 CPU 观测</div>`;
  $("#cpu-list").innerHTML = overview + rows;
  renderDashboard();
  paint();
}

async function refreshCPUs() {
  if (!state.network) return;
  const context = state.context;
  try {
    const value = await api.request(networkPath("/cpus"));
    if (context !== state.context) return;
    state.cpus = value;
    renderCPUs();
  } catch (error) { if (context === state.context && state.view === "cpus") toast(errorText(error), true); }
}

async function chooseCPU(callback) {
  const context = state.context;
  cpuCallback = callback;
  await refreshCPUs();
  if (context !== state.context || !state.network) return;
  const supported = state.network?.capabilities?.cpu_selection === true;
  const cpus = state.cpus.cpus || [];
  const counts = new Map();
  cpus.forEach(cpu => counts.set(cpu.name, (counts.get(cpu.name) || 0) + 1));
  $("#cpu-choice-error").textContent = supported ? "" : "当前 OC 不支持严格指定 CPU，使用自动分配。";
  $("#cpu-options").innerHTML = `<button class="cpu-option" data-cpu-auto><span><strong>自动分配</strong></span>${icon("check")}</button>` + cpus.map((cpu, index) => {
    const valid = supported && cpu.name?.trim() && counts.get(cpu.name) === 1;
    return `<div><button class="cpu-option" data-cpu-index="${index}" ${valid ? "" : "disabled"} title="${!supported ? "指定 CPU 不可用" : counts.get(cpu.name) !== 1 ? "CPU 名称重复" : !cpu.name.trim() ? "CPU 未命名" : cpu.busy ? "CPU 忙碌，可选择等待" : "指定此 CPU"}"><span><strong>${esc(cpu.name || "未命名 CPU")}</strong><small>${formatQuantity(cpu.storage)} B · ${cpu.coprocessors} 协处理器</small></span><span class="state-label ${cpu.busy ? "busy" : ""}">${cpu.busy ? "忙碌" : "空闲"}</span></button>${valid && cpu.busy ? `<button class="secondary wide" data-cpu-wait="${index}">${icon("clock")}等待此 CPU</button>` : ""}</div>`;
  }).join("");
  paint();
  if (!$("#cpu-dialog").open) $("#cpu-dialog").showModal();
}

function selectCPU(name, wait) {
  cpuCallback?.(name, wait);
  $("#cpu-dialog").close();
}

function cpuChoice(index, wait = false) {
  const cpu = state.cpus.cpus?.[index];
  if (!cpu || !state.network?.capabilities?.cpu_selection) return;
  if (cpu.busy && !wait) {
    $("#cpu-choice-error").textContent = "这台 CPU 正忙，请重新选择，或明确选择等待此 CPU。";
    return;
  }
  selectCPU(cpu.name, wait);
}

function renderOrders() {
  const orders = state.orders.filter(order => state.orderFilter === "all" ||
    (state.orderFilter === "active" && activeStates.has(order.state)) ||
    (state.orderFilter === "attention" && attentionStates.has(order.state)) ||
    (state.orderFilter === "done" && endedStates.has(order.state)));
  const groups = new Map();
  for (const order of orders) {
    const key = state.orderGroup === "cpu" ? order.cpu_name || "自动分配" : order.batch_id || `single:${order.id}`;
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(order);
  }
  const snapshot = state.orderSnapshot;
  const limit = snapshot?.limited_states.length ? snapshot.recent_only ? " · 仅最近 200 项" : ` · ${snapshot.limited_states.map(value => stateNames[value]).join("、")}各仅最近 200 项` : "";
  $("#orders-summary").textContent = !state.network ? "尚未连接网络" : snapshot
    ? `${snapshot.recent_only ? "最近" : "已读取"} ${orders.length} 项${limit}${state.ordersLoading ? " · 刷新中" : ""}`
    : state.ordersLoading ? "正在读取任务…" : state.ordersError ? "任务未加载" : "尚未读取任务";
  if (state.taskMode === "orders") {
    $("#order-count").textContent = state.activeOrderCount ? `${state.activeOrderCount}${state.activeOrdersTruncated ? "+" : ""}` : "";
    $("#order-count").title = state.activeOrdersTruncated ? "未完成任务数量下限；每种状态最多读取 200 项" : "未完成任务";
  }
  $("#order-list").setAttribute("aria-busy", String(state.ordersLoading));
  if (state.taskMode === "orders") $("#refresh-orders").disabled = state.ordersLoading;
  const counts = orders.reduce((result, order) => {
    result[order.state] = (result[order.state] || 0) + 1;
    return result;
  }, {});
  const summary = groups.size ? `<div class="network-toolbar" aria-label="任务摘要"><span>共 ${formatQuantity(orders.length)} 项 · ${formatQuantity(groups.size)} 个${state.orderGroup === "cpu" ? " CPU 分组" : "提交批次"}</span><span class="muted">${formatQuantity(counts.queued || 0)} 排队 · ${formatQuantity((counts.running || 0) + (counts.computing || 0))} 执行中 · ${formatQuantity(counts.needs_review || 0)} 待核对</span></div>` : "";
  const groupsHTML = groups.size ? [...groups].map(([key, rows]) => `<section class="order-group"><div class="group-heading"><h2>${state.orderGroup === "cpu" ? esc(key) : `${key.startsWith("single:") ? "单项任务" : "提交批次"} · ${esc(dateTime(rows[0].created_at))}`}</h2><span class="muted">${rows.length} 项</span></div><div class="network-table order-table"><div class="network-table-head"><span>物品</span><span>数量</span><span>CPU 策略</span><span>提交时间</span><span>状态</span><span></span></div>${rows.map(order => {
    const item = itemFor(order.item_id);
    const itemName = item?.label || `物品 #${order.item_id}`;
    const status = order.state === "queued" && order.wait_for_cpu ? "等待指定 CPU" : stateNames[order.state] || order.state;
    return `<div class="network-table-row order-table-row"><span class="network-item-cell">${itemImage(item)}<button class="task-main" data-order="${esc(order.id)}"><span class="task-title">${esc(itemName)}</span><span class="task-sub">${esc(order.id)}</span></button></span><span class="number">× ${formatQuantity(order.quantity)}</span><span>${esc(order.cpu_name || "自动分配")}</span><span class="muted">${esc(age(order.created_at))}</span><span class="task-state" data-state="${esc(order.state)}">${esc(status)}${order.state === "needs_review" ? " · 需人工核对" : ""}</span>${order.state === "queued" ? `<button class="icon-button" data-cancel="${esc(order.id)}" title="撤回未派发任务" aria-label="撤回未派发任务">${icon("x")}</button>` : `<span></span>`}</div>`;
  }).join("")}</div></section>`).join("") : `<div class="empty">${!snapshot ? state.ordersLoading ? "正在读取任务…" : state.ordersError ? "任务读取失败" : "尚未读取任务" : `暂无${state.orderFilter === "active" ? "未完成" : state.orderFilter === "attention" ? "待处理" : ""}任务`}</div>`;
  $("#order-list").innerHTML = summary + groupsHTML;
  paint();
}

async function lookupItems(ids) {
  const unique = [...new Set(ids.map(String))];
  if (!unique.length) return;
  const context = state.context;
  const requests = [];
  for (let offset = 0; offset < unique.length; offset += 200) {
    requests.push(api.request(networkPath(`/items/lookup?${new URLSearchParams({ ids: unique.slice(offset, offset + 200).join(",") })}`)));
  }
  const pages = await Promise.all(requests);
  if (context !== state.context) return;
  const items = pages.flatMap(value => value.items || []);
  for (const item of items) {
    state.itemCache.set(String(item.id), item);
    if (item.canonical_id) state.itemCache.set(String(item.canonical_id), item);
  }
  return { items };
}

async function refreshOrders(showError = true) {
  if (!state.network) return;
  const context = state.context, filter = state.orderFilter, network = state.network.id;
  const generation = ++state.orderGeneration;
  ordersController?.abort();
  const controller = ordersController = new AbortController();
  const current = () => context === state.context && generation === state.orderGeneration;
  if (state.orderSnapshot?.filter !== filter) { state.orders = []; state.orderSnapshot = null; }
  state.ordersLoading = true;
  state.ordersError = false;
  renderOrders();
  refreshOrderDetail();
  try {
    const snapshot = await loadOrderSnapshot(query => api.request(
      networkPath(`/orders?${new URLSearchParams(query)}`, network), { signal: controller.signal },
    ), filter);
    if (!current()) return;
    state.orders = snapshot.orders;
    state.orderSnapshot = { ...snapshot, filter };
    state.activeOrderCount = snapshot.active_count;
    state.activeOrdersTruncated = snapshot.active_truncated;
    renderOrders();
    try { await lookupItems(snapshot.orders.map(o => o.item_id).filter(id => !state.itemCache.has(String(id)))); } catch {}
    if (!current()) return;
    message("#orders-message", "");
  } catch (error) {
    if (!current() || error.name === "AbortError") return;
    state.ordersError = true;
    if (showError || state.view === "orders") message("#orders-message", `任务刷新失败${state.orderSnapshot ? "，当前显示上次读取的记录" : ""}。${errorText(error)}`);
  } finally {
    if (current()) { state.ordersLoading = false; renderOrders(); }
  }
}

async function cancelOrder(id) {
  const context = state.context;
  if (!state.network) return;
  try {
    await api.request(networkPath(`/orders/${encodeURIComponent(id)}/cancel`), { method: "POST", body: {} });
    if (context === state.context) toast("已撤回尚未派发的任务");
  } catch (error) { if (context === state.context) toast(errorText(error), true); }
  if (context === state.context) await refreshOrders();
}

function invalidateOrderDetail() {
  state.detailId = "";
  state.detailGeneration++;
  detailController?.abort();
  $("#refresh-order-detail").disabled = false;
  $("#order-detail").setAttribute("aria-busy", "false");
  message("#order-detail-status", "");
}

function showOrder(id) {
  if (!state.network) return;
  invalidateOrderDetail();
  state.detailId = id;
  $("#order-detail").innerHTML = `<div id="order-facts"></div><div id="order-actions"></div><div id="order-review"></div><div id="order-history"></div>`;
  if (!$("#order-dialog").open) $("#order-dialog").showModal();
  return refreshOrderDetail();
}

function renderOrderDetail(order, history) {
  const item = itemFor(order.item_id);
  $("#order-facts").innerHTML = `<div class="detail-heading">${itemImage(item)}<h2>${esc(item?.label || `物品 #${order.item_id}`)}</h2></div><div class="order-detail-facts"><div class="fact"><span>数量</span><span class="number">${formatQuantity(order.quantity)}</span></div><div class="fact"><span>状态</span><span>${esc(stateNames[order.state] || order.state)}</span></div><div class="fact"><span>CPU 策略</span><span>${esc(order.cpu_name || "自动分配")}</span></div><div class="fact"><span>提交时间</span><span>${esc(dateTime(order.created_at))}</span></div><div class="fact"><span>过期时间</span><span>${esc(dateTime(order.expires_at))}</span></div></div>${order.reason ? `<p class="error">${esc(order.reason)}</p>` : ""}`;
  $("#order-actions").innerHTML = order.state === "queued" ? `<div class="order-actions"><button class="secondary" data-cancel-detail="${esc(order.id)}">${icon("x")}撤回未派发任务</button></div>` : "";
  const review = $("#order-review");
  if (order.state === "needs_review" && state.member?.admin) {
    // Keep the live form connected so background reads cannot discard an edit.
    if (!$(".resolve-form", review)) review.innerHTML = `<form class="resolve-form" data-resolve="${esc(order.id)}"><label>核对结果<select name="state" required><option value="">请选择核对结果</option><option value="done">确认已完成</option><option value="failed">确认失败</option><option value="canceled">确认已取消</option></select></label><label>核对依据<input name="reason" maxlength="1024" required></label><button type="submit" class="secondary">记录核对结果</button></form>`;
  } else review.replaceChildren();
  $("#order-history").innerHTML = `<h3 class="field-label">状态记录</h3>${history.map(event => `<div class="history-entry">${esc(stateNames[event.state] || event.state)}<p>${esc(dateTime(event.created_at))}${event.reason ? ` · ${esc(event.reason)}` : ""}</p></div>`).join("")}`;
  paint();
}

async function refreshOrderDetail() {
  if (!state.network || !state.detailId || !$("#order-dialog").open) return;
  const context = state.context, id = state.detailId, generation = ++state.detailGeneration;
  detailController?.abort();
  const controller = detailController = new AbortController();
  const current = () => context === state.context && generation === state.detailGeneration && id === state.detailId && $("#order-dialog").open;
  $("#refresh-order-detail").disabled = true;
  $("#order-detail").setAttribute("aria-busy", "true");
  $("#order-detail-status").dataset.error = "false";
  message("#order-detail-status", $("#order-facts").hasChildNodes() ? "正在刷新…" : "正在读取任务…");
  try {
    const [order, history] = await Promise.all([
      api.request(networkPath(`/orders/${encodeURIComponent(id)}`), { signal: controller.signal }),
      api.request(networkPath(`/orders/${encodeURIComponent(id)}/events`), { signal: controller.signal }),
    ]);
    if (!current()) return;
    if (order?.id !== id || !Array.isArray(history)) throw new Error("Invalid task detail.");
    renderOrderDetail(order, history);
    message("#order-detail-status", "");
  } catch (error) {
    if (!current() || error.name === "AbortError") return;
    $("#order-detail-status").dataset.error = "true";
    message("#order-detail-status", `详情刷新失败。${errorText(error)}`);
  } finally {
    if (current()) {
      $("#refresh-order-detail").disabled = false;
      $("#order-detail").setAttribute("aria-busy", "false");
    }
  }
}

function renderNetwork() {
  const network = state.network;
  if (!network) {
    $("#network-summary").textContent = "尚未连接网络";
    $("#network-info").innerHTML = `<div class="empty empty-state"><i data-lucide="wifi-off"></i><strong>尚未连接网络</strong><span>添加或选择一个网络后查看 OC 节点、服务能力与同步状态。</span><button class="secondary" data-view="settings">打开设置</button></div>`;
    $("#network-table").innerHTML = "";
    renderDashboard();
    paint();
    return;
  }
  $("#network-summary").textContent = network.name;
  const caps = network.capabilities || {};
  const services = network.services || {};
  const bridgeMode = network.bridge_mode === "cooperative" ? "多 OC 协作" : network.bridge_mode === "single" ? "单 OC" : network.bridge_mode || "标准";
  const capabilities = [
    caps.inventory ? "库存" : "",
    caps.recipes ? "配方" : "",
    caps.craft ? "合成" : "",
    caps.cpu_output ? "CPU 输出" : "",
  ].filter(Boolean).join(" · ") || "尚未声明";
  const facts = [
    ["服务状态", network.online ? "在线" : "离线"],
    ["桥接模式", bridgeMode],
    ["数据质量", networkQualityLabel(network.quality)],
    ["最近通信", dateTime(network.last_seen)],
    ["最近库存观测", dateTime(network.last_inventory)],
    ["最近 CPU 观测", dateTime(services.cpu_observed_at)],
    ["单次最大合成量", formatQuantity(caps.max_craft_quantity)],
    ["服务能力", capabilities],
    ["数量读取器", `${formatQuantity(services.quantity_readers || 0)} 个`],
    ["严格指定 CPU", caps.cpu_selection ? "可用" : "当前 OC 不可用"],
  ];
  const deviceRows = state.devices.length ? state.devices.map(device => {
    const role = device.role === "primary" ? "主设备" : device.role === "quantity_worker" ? "数量助手" : device.role || "设备";
    const status = device.revoked ? "已撤销" : device.online ? "在线" : "离线";
    return `<div class="network-table-row"><span><strong>${esc(device.name || device.id)}</strong><small class="task-sub">${esc(device.id)}</small></span><span>${esc(role)}</span><span class="state-label ${device.online && !device.revoked ? "" : "offline"}">${status}</span><span>${esc(age(device.last_seen))}</span><span>${esc(age(device.last_inventory))}</span><span class="muted">${esc(device.session_id || "未建立会话")}</span></div>`;
  }).join("") : `<div class="empty">暂无设备观测</div>`;
  $("#network-info").innerHTML = `<div class="network-toolbar"><span>${esc(network.name)} · ${esc(network.id)}</span><span class="muted">${state.devices.length} 台设备 · ${network.online ? "实时连接" : "等待 OC 恢复"}</span></div><div class="network-facts">${facts.map(([label, value]) => `<div class="fact"><span>${esc(label)}</span><span>${esc(value)}</span></div>`).join("")}</div><div class="network-table" id="network-table"><div class="network-table-head"><span>设备</span><span>角色</span><span>状态</span><span>最近通信</span><span>库存观测</span><span>会话</span></div>${deviceRows}</div>`;
  renderDashboard();
  paint();
}

async function refreshNetwork() {
  if (!state.network) return;
  const context = state.context;
  try {
    const [value, devices] = await Promise.all([api.request(networkPath()), api.request(networkPath("/devices"))]);
    if (context !== state.context) return;
    const onlineChanged = state.network.online !== value.online;
    state.network = value;
    state.devices = devices;
    refreshHeader();
    renderNetwork();
    if (onlineChanged && !$("#draft-panel").contains(document.activeElement)) renderDraft();
    if (onlineChanged && !$("#detail-panel").contains(document.activeElement)) renderDetail();
  } catch (error) { if (context === state.context && state.view === "network") toast(errorText(error), true, "network"); }
}

async function loadInventory(reset = false) {
  if (!state.network) return;
  if (reset) { state.page = 0; state.cursor = ""; state.cursors = [""]; }
  queryController?.abort();
  queryController = new AbortController();
  const generation = ++state.generation;
  const query = new URLSearchParams({ q: state.search, sort: state.sort, limit: "50", consistency: state.sort === "name" ? "live" : "strict" });
  if (state.filter) query.set("craftable", state.filter);
  if (state.cursor) query.set("cursor", state.cursor);
  state.inventoryLoading = true;
  renderInventory();
  try {
    const value = await api.request(networkPath(`/items?${query}`), { signal: queryController.signal });
    if (generation !== state.generation) return;
    state.items = value.items || [];
    state.nextCursor = value.next_cursor || "";
    state.items.forEach(item => state.itemCache.set(String(item.id), item));
    message("#inventory-message", "");
  } catch (error) {
    if (generation !== state.generation || error.name === "AbortError") return;
    message("#inventory-message", errorText(error));
    if (error.code === "view_changed") { state.nextCursor = ""; }
  } finally {
    if (generation === state.generation) { state.inventoryLoading = false; renderInventory(); }
  }
}

async function patchVisibleInventory() {
  if (!state.network || !state.items.length || state.inventoryLoading) return;
  try {
    const value = await lookupItems(state.items.map(i => i.id).concat(state.selected?.id || []));
    if (!value) return;
    const live = new Map(value.items.map(i => [String(i.id), i]));
    state.items = state.items.map(item => live.get(String(item.id)) || item);
    if (state.selected && live.has(String(state.selected.id))) state.selected = live.get(String(state.selected.id));
    // Quantity changes do not reorder the list while the user is choosing items.
    const focused = document.activeElement;
    const focusKind = focused?.dataset?.quickAdd ? "data-quick-add" : "data-item";
    const focusItem = focused?.dataset?.item || focused?.dataset?.quickAdd;
    renderInventory();
    if (focusItem) $(`[${focusKind}="${CSS.escape(focusItem)}"]`)?.focus({ preventScroll: true });
    if (!$("#detail-panel").contains(document.activeElement) && state.inspector === "detail") renderDetail();
  } catch {}
}

function connectStream() {
  state.stream?.close();
  clearInterval(streamTimer);
  clearTimeout(streamRefreshTimer);
  streamRefreshTimer = null;
  state.streamConnected = false;
  const network = state.network?.id;
  const context = state.context, pending = new Set();
  if (!network) return;
  const stream = new EventSource(networkPath("/events"));
  state.stream = stream;
  stream.onopen = () => { if (context === state.context) { state.streamConnected = true; refreshHeader(); refreshJobs(); } };
  stream.onerror = () => {
    if (context !== state.context) return;
    state.streamConnected = false;
    if (!state.settings.reconnect) stream.close();
    refreshHeader();
  };
  const update = event => {
    if (context !== state.context) return;
    let kind = "reset";
    try { const value = JSON.parse(event.data).kind; if (typeof value === "string") kind = value; } catch {}
    pending.add(kind);
    if (streamRefreshTimer) return;
    streamRefreshTimer = setTimeout(() => {
      streamRefreshTimer = null;
      if (context !== state.context) return;
      const kinds = [...pending];
      pending.clear();
      const reset = kinds.includes("reset") || kinds.includes("session");
      if (kinds.includes("jobs") || reset) refreshJobs();
      if (kinds.includes("orders") || reset) refreshOrders(false);
      if (kinds.includes("cpus") || reset) refreshCPUs();
      if (reset || kinds.some(kind => kind.includes("inventory") || kind.includes("item") || kind.includes("recipe"))) patchVisibleInventory();
      if (reset) refreshNetwork();
    }, 400);
  };
  stream.addEventListener("change", update);
  stream.addEventListener("reset", update);
  streamTimer = setInterval(() => {
    if (document.visibilityState !== "visible" || !state.network) return;
    refreshNetwork();
    patchVisibleInventory(); refreshOrders(false); refreshCPUs(); refreshJobs();
  }, 15000);
}

async function switchNetwork(id) {
  if (state.busy || state.preparing) { $("#network").value = state.network?.id || ""; toast("请等待本轮提交结束。"); return; }
  state.context++;
  const network = state.networks.find(n => n.id === id);
  queryController?.abort();
  ordersController?.abort();
  state.orderGeneration++;
  invalidateOrderDetail();
  state.generation++;
  state.stream?.close();
  clearInterval(streamTimer);
  clearTimeout(streamRefreshTimer);
  streamRefreshTimer = null;
  state.streamConnected = false;
  cpuCallback = null;
  for (const dialog of ["cpu-dialog", "order-dialog", "job-dialog"]) if ($(`#${dialog}`).open) $(`#${dialog}`).close();
  state.network = network || null;
  state.itemCache.clear();
  state.selected = null;
  state.items = [];
  state.nextCursor = "";
  state.inventoryLoading = false;
  state.orders = [];
  state.orderSnapshot = null;
  state.ordersLoading = false;
  state.ordersError = false;
  state.activeOrderCount = 0;
  state.activeOrdersTruncated = false;
  state.devices = [];
  state.cpus = { cpus: [] };
  state.amount = "1";
  state.cpuName = "";
  state.waitForCPU = false;
  state.unchecked.clear();
  state.draftKey = network ? storageKey(state.member.id, network.id) : "";
  state.draft = network ? readLocalDraft() : { version: 1, entries: [], receipts: {} };
  state.draftBlocked = !!state.draft.warning;
  jobs.reset(network ? { memberId: state.member.id, networkId: network.id } : null);
  for (const id of ["#inventory-message", "#orders-message"]) message(id, "");
  refreshHeader(); renderInventory(); renderDetail(); renderDraft(); renderOrders(); renderCPUs(); renderNetwork();
  if (!network) return;
  try { localStorage.setItem("ae-orbit:last-network", network.id); } catch {}
  connectStream();
  const context = state.context;
  await Promise.all([loadInventory(true), refreshOrders(false), refreshCPUs(), refreshNetwork(), refreshJobs()]);
  if (context !== state.context) return;
  try {
    for (const intent of jobs.local().intents.filter(intent=>intent.state === "accepted")) {
      if (context !== state.context) return;
      await jobs.reconcile(intent.requestKey).catch(()=>{});
    }
  } catch {}
}

function showLogin() {
  if (!$("#auth-dialog").open) $("#auth-dialog").showModal();
}

async function acceptSession(member, csrf, expiresAt = "", persist = true) {
  await switchNetwork("");
  const context = state.context;
  state.member = member;
  api.setCSRF(csrf);
  state.session = { member: member.id, csrf, expiresAt };
  if (persist) {
    try { saveSession(localStorage, sessionStorage, state.session, state.settings.rememberSession); }
    catch { toast("会话无法保存，请检查浏览器存储。", true); }
  }
  $("#token").value = "";
  $("#network").innerHTML = `<option value="">正在读取网络</option>`;
  $("#network").disabled = true;
  refreshHeader();
  try {
    const networks = await api.request("/api/v1/networks");
    if (context !== state.context) return;
    state.networks = networks;
    $("#network").innerHTML = state.networks.length ? state.networks.map(n => `<option value="${esc(n.id)}">${esc(n.name)}</option>`).join("") : `<option value="">暂无网络</option>`;
    let last = "";
    try { last = localStorage.getItem("ae-orbit:last-network") || ""; } catch {}
    const id = state.networks.some(n => n.id === last) ? last : state.networks[0]?.id;
    $("#network").value = id || "";
    await switchNetwork(id);
    if ($("#auth-dialog").open) $("#auth-dialog").close();
  } catch (error) {
    message("#login-error", errorText(error));
    showLogin();
  } finally { $("#network").disabled = false; }
}

$("#login-form").addEventListener("submit", async event => {
  event.preventDefault();
  if (state.busy || state.preparing) { toast("请等待当前提交结束后重新登录。", true); return; }
  $("#login-button").disabled = true;
  $("#login-error").textContent = "";
  try {
    const value = await api.request("/api/v1/session", { method: "POST", body: { token: $("#token").value.trim() }, login: true });
    await acceptSession(value.member, value.csrf, value.expires_at);
  } catch (error) { $("#login-error").textContent = errorText(error); }
  finally { $("#login-button").disabled = false; }
});
$("#auth-dialog").addEventListener("cancel", event => event.preventDefault());
$("#logout").addEventListener("click", async () => {
  if (state.busy || state.preparing) { toast("请等待本轮提交结束。"); return; }
  try {
    await api.request("/api/v1/session", { method: "DELETE" });
    state.stream?.close(); clearInterval(streamTimer); clearTimeout(streamRefreshTimer);
    api.setCSRF(""); state.member = null; state.networks = [];
    clearSession(localStorage, sessionStorage);
    state.session = null;
    $("#network").innerHTML = `<option value="">未连接</option>`;
    await switchNetwork("");
    showLogin();
  } catch (error) { toast(errorText(error), true); }
});
$("#network").addEventListener("change", event => switchNetwork(event.target.value));
$("#refresh-dashboard").addEventListener("click", () => {
  renderDashboard();
  if (state.network) { refreshNetwork(); patchVisibleInventory(); refreshCPUs(); refreshJobs(); }
});
$("#search").addEventListener("input", event => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => { state.search = event.target.value.trim(); loadInventory(true); }, 220);
});
$("#sort").addEventListener("change", event => { state.sort = event.target.value; loadInventory(true); });
$("#refresh-inventory").addEventListener("click", () => loadInventory(true));
$("#prev-page").addEventListener("click", () => { if (state.page > 0) { state.page--; state.cursor = state.cursors[state.page]; loadInventory(); } });
$("#next-page").addEventListener("click", () => { if (state.nextCursor) { state.page++; state.cursor = state.nextCursor; state.cursors[state.page] = state.cursor; loadInventory(); } });
$("#open-draft").addEventListener("click", () => openInspector("draft"));
$("#detail-tab").addEventListener("click", () => openInspector("detail"));
$("#draft-tab").addEventListener("click", () => openInspector("draft"));
$("#close-inspector").addEventListener("click", () => closeInspector());
$("#sheet-backdrop").addEventListener("click", () => closeInspector());
$("#refresh-orders").addEventListener("click", () => state.taskMode === "jobs" ? refreshJobs() : refreshOrders());
$("#task-mode").addEventListener("change", event => {
  state.taskMode = event.target.value; renderJobs(); renderOrders();
  if (state.taskMode === "jobs") refreshJobs(); else refreshOrders();
});
$("#job-history-more").addEventListener("click", () => jobs.history());
$("#refresh-job-detail").addEventListener("click", () => refreshJobDetail());
$("#job-dialog").addEventListener("close", () => jobs.closeDetail());
$("#job-dialog").addEventListener("cancel", () => jobs.closeDetail());
$("#refresh-order-detail").addEventListener("click", () => refreshOrderDetail());
$("#order-dialog").addEventListener("cancel", () => invalidateOrderDetail());
$("#order-dialog").addEventListener("close", () => { if (!$("#order-dialog").open) invalidateOrderDetail(); });
$("#refresh-cpus").addEventListener("click", () => refreshCPUs());
$("#refresh-network").addEventListener("click", () => refreshNetwork());
$("#order-group").addEventListener("change", event => { state.orderGroup = event.target.value; renderOrders(); });
$("#settings-detail").addEventListener("change", event => {
  const input = event.target;
  if (input.dataset.setting === "inventoryLayout") updateSettings({ inventoryLayout: input.value });
  if (input.dataset.setting && input.type === "checkbox") updateSettings({ [input.dataset.setting]: input.checked });
});
$("#settings-detail").addEventListener("click", async event => {
  const actionButton = event.target.closest("[data-session-action]");
  const sessionAction = actionButton?.dataset.sessionAction;
  if (sessionAction === "test") {
    actionButton.disabled = true;
    actionButton.setAttribute("aria-busy", "true");
    try {
      const member = await api.request("/api/v1/me");
      if (!member?.id) throw new Error("missing member");
      state.member = member;
      toast("连接正常");
      renderSettings();
    } catch (error) {
      toast(errorText(error) || "连接失败，请检查后端和当前会话。", true, "network");
    } finally {
      if (actionButton.isConnected) {
        actionButton.disabled = false;
        actionButton.removeAttribute("aria-busy");
      }
    }
    return;
  }
  if (sessionAction === "login") { showLogin(); return; }
  if (sessionAction === "clear") {
    clearSession(localStorage, sessionStorage);
    toast("已清除保存的会话");
    renderSettings();
    return;
  }
  const button = event.target.closest("[data-setting-value]");
  if (!button) return;
  updateSettings({ [button.dataset.settingValue]: button.dataset.value });
});
$("#remember-session").addEventListener("change", event => updateSettings({ rememberSession: event.target.checked }));
$("#settings-sections").addEventListener("click", event => {
  const button = event.target.closest("[data-settings-section]");
  if (!button) return;
  state.settingsSection = button.dataset.settingsSection;
  $("#settings-sections").querySelectorAll("[data-settings-section]").forEach(item => item.setAttribute("aria-current", String(item === button)));
  renderSettings();
});

document.addEventListener("click", async event => {
  const button = event.target.closest("button");
  if (!button || button.disabled) return;
  const data = button.dataset;
  if (data.view) showView(data.view);
  if ("filter" in data) {
    state.filter = data.filter;
    $$("[data-filter]").forEach(b => b.setAttribute("aria-pressed", String(b === button)));
    loadInventory(true);
  }
  if (data.item) {
    state.selected = state.itemCache.get(data.item); state.amount = "1";
    renderDetail();
    $$("tr[data-id]").forEach(row => row.dataset.selected = String(row.dataset.id === data.item));
    openInspector("detail");
  }
  if (data.quickAdd) { const item = state.itemCache.get(data.quickAdd); if (item) await addItem(item, "1"); }
  if (data.step) {
    try { state.amount = (BigInt($("#craft-quantity").value || 1) + BigInt(data.step)).toString(); state.amount = normalizeQuantity(state.amount, maximum()); }
    catch { state.amount = data.step === "-1" ? "1" : String(maximum()); }
    $("#craft-quantity").value = state.amount;
  }
  if (button.id === "add-to-draft" || button.id === "submit-immediately") {
    await addItem(state.selected, $("#craft-quantity").value, button.id === "submit-immediately");
  }
  if (button.id === "detail-cpu") chooseCPU((name, wait) => { state.cpuName = name; state.waitForCPU = wait; renderDetail(); });
  if (data.remove) await changeDraft(current => ({ ...current, entries: removeDraft(current.entries, data.remove) }));
  if (button.id === "submit-draft") submitDraft();
  if (data.reconcile) reconcile(data.reconcile);
  if (data.resetCpu) await changeDraft(current=>({...current,entries:updateDraft(current.entries,data.resetCpu,{cpu_name:"",wait_for_cpu:false})}));
  if (data.jobFilter) {
    state.jobFilter = data.jobFilter;
    $$("[data-job-filter]").forEach(b=>b.setAttribute("aria-pressed",String(b===button)));
    renderJobs();
  }
  if (data.job) showJob(data.job);
  if (data.jobReconcile || data.jobRetry) {
    const context = state.context;
    button.disabled = true;
    try {
      let archived = false;
      if (data.jobRetry) {
        if (!confirm("重发完全相同的清单。若结果待确认，只能继续核对，不会重复提交。继续？")) return;
        await jobs.retryRejected(data.jobRetry);
      } else {
        const receipt = await jobs.reconcile(data.jobReconcile);
        archived = receipt?.archived === true;
      }
      if (context === state.context) { toast(archived ? "原生产任务已受理并归档，不可编辑或重新调度。" : "已核对生产任务受理记录"); await refreshJobs(); }
    } catch (error) {
      if (context === state.context) toast(error.status === 404 ? "暂未查到原生产任务；原清单仍锁定，不会重新提交。" : `${errorText(error)} 请保留原请求继续核对。`, true);
    } finally { if (context === state.context) { renderJobs(); renderDraft(); } }
  }
  if (data.jobAction || data.controlReplay) {
    const context = state.context, summary = jobs.state.detail?.summary;
    if (!summary || !$("#job-dialog").open || jobs.state.detailLoading || jobs.state.detailError ||
        !(state.member?.admin || state.member?.id === summary.owner_id)) return;
    if (data.jobAction === "stop" && !confirm("停止不可撤销：将撤回待调度和排队条目；已派发、计算中、合成中及结果待确认的工作仍保留。不能取消已经执行的合成。确定停止？")) return;
    if (data.controlReplay && !confirm("先读取任务，再以保存的原操作键及原控制版本重放这一操作。状态冲突时仍保留记录。继续？")) return;
    button.disabled = true;
    try {
      if (data.controlReplay) {
        const refreshed = await jobs.detail(summary.id);
        if (!refreshed || context !== state.context || !$("#job-dialog").open ||
            !(state.member?.admin || state.member?.id === refreshed.detail.summary.owner_id)) return;
        await jobs.replayControl(summary.id,data.controlReplay,refreshed);
      } else await jobs.control(summary,data.jobAction);
      if (context === state.context) toast("控制结果已确认");
    } catch (error) { if (context === state.context) toast(`${errorText(error)} 原控制操作保持待核对。`,true); }
    finally { if (context === state.context) { renderJobs(); await refreshJobDetail(); } }
  }
  if (data.draftCpu) chooseCPU((name, wait) => changeDraft(current => ({ ...current, entries: updateDraft(current.entries, data.draftCpu, { cpu_name: name, wait_for_cpu: wait }) })));
  if (button.id === "bulk-cpu") {
    const ids = selectedDraftIds();
    chooseCPU((name, wait) => changeDraft(current => ({ ...current, entries: ids.reduce((entries, id) => updateDraft(entries, id, { cpu_name: name, wait_for_cpu: wait }), current.entries) })));
  }
  if ("cpuAuto" in data) selectCPU("", false);
  if ("cpuIndex" in data) cpuChoice(Number(data.cpuIndex));
  if ("cpuWait" in data) cpuChoice(Number(data.cpuWait), true);
  if (data.close) {
    if (data.close === "order-dialog") invalidateOrderDetail();
    if (data.close === "job-dialog") jobs.closeDetail();
    $(`#${data.close}`).close();
  }
  if (data.orderFilter) {
    state.orderFilter = data.orderFilter;
    $$("[data-order-filter]").forEach(b => b.setAttribute("aria-pressed", String(b === button)));
    refreshOrders();
  }
  if (data.order) showOrder(data.order);
  if (data.cancel) cancelOrder(data.cancel);
  if (data.cancelDetail) { await cancelOrder(data.cancelDetail); if (state.detailId === data.cancelDetail) $("#order-dialog").close(); }
  if (button.id === "reset-draft" && confirm("请确认已核对现有任务。重置只清除本机草稿，不会撤回任何订单。")) {
    try {
      const original = localStorage.getItem(state.draftKey);
      if (original) localStorage.setItem(`${state.draftKey}:recovery`, original);
      saveDraft(localStorage, state.draftKey, { entries: [], receipts: {} });
      state.draftBlocked = false; state.draft = readLocalDraft(); renderDraft(); renderDetail();
    } catch { toast("无法保存清单，重置未完成。", true); }
  }
});
document.addEventListener("input", event => {
  if (event.target.id === "craft-quantity") state.amount = event.target.value;
});
document.addEventListener("change", async event => {
  const input = event.target;
  if (input.dataset.quantity) {
    try {
      const quantity = normalizeQuantity(input.value, maximum());
      await changeDraft(current => ({ ...current, entries: updateDraft(current.entries, input.dataset.quantity, { quantity }) }));
    } catch { toast(`请输入 1 至 ${formatQuantity(maximum())} 的整数。`, true); renderDraft(); }
  }
  if (input.dataset.check) {
    if (input.checked) state.unchecked.delete(input.dataset.check);
    else state.unchecked.add(input.dataset.check);
    renderDraft();
  }
  if (input.id === "draft-check-all") {
    state.draft.entries.filter(e => e.status === "draft").forEach(e => input.checked ? state.unchecked.delete(e.id) : state.unchecked.add(e.id));
    renderDraft();
  }
});
document.addEventListener("contextmenu", event => {
  const button = event.target.closest("[data-cpu-index]");
  if (button && !button.disabled) { event.preventDefault(); cpuChoice(Number(button.dataset.cpuIndex), true); }
});
document.addEventListener("submit", async event => {
  const form = event.target.closest("[data-resolve]");
  if (!form) return;
  event.preventDefault();
  const fields = new FormData(form);
  const button = $("button", form); button.disabled = true;
  try {
    await api.request(networkPath(`/orders/${encodeURIComponent(form.dataset.resolve)}/resolve`), { method: "POST", body: { state: fields.get("state"), reason: fields.get("reason").trim() } });
    $("#order-dialog").close(); await refreshOrders(); toast("核对结果已记录");
  } catch (error) { toast(errorText(error), true); }
  finally { button.disabled = false; }
});
document.addEventListener("keydown", event => {
  if (event.key === "Escape" && !$$("dialog[open]").length) closeInspector();
  if (event.key === "Tab" && $("#inspector").dataset.open === "true" && matchMedia("(max-width:800px)").matches && !$$("dialog[open]").length) {
    const focusable = $$('button:not(:disabled),input:not(:disabled),select:not(:disabled)', $("#inspector")).filter(el => el.getClientRects().length);
    const first = focusable[0], last = focusable.at(-1);
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
    if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
  }
});
window.addEventListener("storage", event => {
  if ((event.key === state.draftKey || event.key?.startsWith("ae-orbit:jobs:v1:")) && !state.busy) {
    try { state.draft = jobs.local().draft; state.jobStorageError = false; } catch { state.jobStorageError = true; }
    state.draftBlocked = !!state.draft.warning || state.jobStorageError; renderDraft(); renderDetail(); renderJobs();
  }
});
window.addEventListener("resize", () => {
  const sheet = matchMedia("(max-width:800px)").matches && $("#inspector").dataset.open === "true";
  $("#sheet-backdrop").hidden = !sheet;
  document.body.classList.toggle("sheet-open", sheet);
});
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible" && state.member && state.network) { refreshNetwork(); patchVisibleInventory(); refreshOrders(false); refreshJobs(); }
});
document.addEventListener("error", event => {
  if (event.target instanceof HTMLImageElement && event.target.classList.contains("item-image")) {
    const fallback = document.createElement("span"); fallback.className = "missing-image"; fallback.title = "图标不可用";
    fallback.innerHTML = icon("package"); event.target.replaceWith(fallback); paint();
  }
}, true);

async function start() {
  applySettings();
  renderSettings();
  renderDetail(); renderDraft(); showView(state.view); paint();
  try {
    const response = await fetch("/assets/nei/manifest.json", { credentials: "same-origin" });
    if (response.ok) { const value = await response.json(); if (value.version === 1 && value.icons && typeof value.icons === "object") state.icons = value.icons; }
  } catch {}
  try {
    const saved = loadSession(localStorage, sessionStorage, state.settings.rememberSession);
    if (saved) {
      const member = await api.request("/api/v1/me");
      if (member.id === saved.member) { await acceptSession(member, saved.csrf, saved.expiresAt); return; }
    }
  } catch (error) { if (error?.status === 401) clearSession(localStorage, sessionStorage); }
  showLogin();
}
start();
