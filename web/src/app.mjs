import {
  createIcons, createElement, Orbit, Network, LogOut, Boxes, ListChecks, Cpu, RadioTower, RefreshCw,
  Search, ListPlus, ChevronLeft, ChevronRight, ChevronDown, X, Plus, Minus, Package,
  Trash2, Check, Clock, CircleAlert, LogIn, ArrowUpRight, Server, Send, Pause, Play, Square,
  LayoutDashboard, Settings, Github, Bell, Activity, Monitor, SlidersHorizontal, Code2, FileText, CircleHelp, Copy,
  WifiOff, PanelLeftClose, UserRound,
} from "lucide";
import { createAPI } from "./api.mjs";
import { createInventoryWheel, inventoryKeyboardDirection } from "./inventory-paging.mjs";
import { inventoryTooltipModel, paginateTooltipLines, splitTooltipLine, tooltipLineHTML, tooltipPosition } from "./inventory-tooltip.mjs";
import { quantityKnown, stockText, stockFreshness, inventorySummary } from "./plain-inventory.mjs";
import { inventoryTrendHTML, inventoryTrendModel, loadTrendHistory, recordTrendSample, saveTrendHistory, trendStorageKey } from "./inventory-trend.mjs";
import { dashboardTasks, dashboardInventory, dashboardTasksHTML } from "./dashboard.mjs";
import { createHistoryController, historyChartHTML, historyChartModel, historyRecordsHTML, historyTime } from "./inventory-history.mjs";
import { loadOrderSnapshot } from "./orders.mjs";
import { createJobController } from "./job-controller.mjs";
import { isJobClaim } from "./jobs.mjs";
import { jobListHTML, jobDetailHTML, recoveryHTML } from "./job-view.mjs";
import { craftingJobModel, craftingOverviewHTML, craftingOrdersHTML } from "./crafting-center.mjs";
import {
  normalizeQuantity, formatQuantity, formatCompactQuantity, storageKey, loadDraft, saveDraft, addDraft,
  updateDraft, removeDraft, completeSubmission, networkQualityLabel,
} from "./model.mjs";
import { loadSettings, saveSettings } from "./settings.mjs";
import { draftDisplayItem, draftSelectionSummary } from "./draft-ui.mjs";
import {
  clearCachedTexturePack,
  createTexturePackStore,
  loadCachedTexturePack,
  loadTexturePackURL,
  saveCachedTexturePack,
} from "./texture-pack.mjs";
import { playerName, submitterName, loginPayload, playerAccountHTML } from "./player-identity.mjs";
import { clearSession, loadSession, saveSession } from "./session.mjs";
import { connectionTestTone, connectionTestText } from "./connection-status.mjs";
import { networkDeviceSummary } from "./device-status.mjs";
import { networkNodeModel, networkNodesHTML, networkOverviewHTML, networkSidebarHTML } from "./network-monitor.mjs";
import {
  networkCreateResult,
  networkRefreshState,
  normalizeNetworkDraft,
} from "./network-ui.mjs";
import { connectionDiagnostics, streamStateTone, streamStateText } from "./connection-diagnostics.mjs";
import { networkErrorKind, networkRecoveryState } from "./network-recovery.mjs";
import { cpuDisplayName, cpuSelectionState, cpuMonitorModel } from "./cpu-ui.mjs";
import { cpuMonitorOverviewHTML, cpuMonitorListHTML } from "./cpu-monitor.mjs";
import {
  animationFrameAt,
  catalogAnimation,
  catalogDisplayName,
  spriteLayout,
} from "./inventory-catalog.mjs";

const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];
const esc = value => String(value ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const quantityHTML = value => formatQuantity(value).replaceAll(",", ",<wbr>");
const compactQuantityHTML = value => esc(formatCompactQuantity(value));
const icon = name => `<i data-lucide="${name}" aria-hidden="true"></i>`;
const inventoryIconCache = new Map();
function inventoryIcon(name) {
  if (!inventoryIconCache.has(name)) {
    const node = createElement(name === "plus" ? Plus : Package);
    node.setAttribute("aria-hidden", "true");
    node.setAttribute("class", `lucide lucide-${name}`);
    inventoryIconCache.set(name, node.outerHTML);
  }
  return inventoryIconCache.get(name);
}
const icons = { Orbit, Network, LogOut, Boxes, ListChecks, Cpu, RadioTower, RefreshCw, Search, ListPlus, ChevronLeft, ChevronRight, ChevronDown, X, Plus, Minus, Package, Trash2, Check, Clock, CircleAlert, LogIn, ArrowUpRight, Server, Send, Pause, Play, Square, LayoutDashboard, Settings, Github, Bell, Activity, Monitor, SlidersHorizontal, Code2, FileText, CircleHelp, Copy, WifiOff, PanelLeftClose, UserRound };
const paint = () => {
  const pending = $$('i[data-lucide]');
  if (!pending.length) return;
  for (const element of pending) element.setAttribute("data-icon-pending", element.getAttribute("data-lucide"));
  createIcons({ icons, nameAttr: "data-icon-pending" });
  for (const element of $$('[data-icon-pending]')) element.removeAttribute("data-icon-pending");
};
const api = createAPI();
const texturePack = createTexturePackStore();
const state = {
  member: null, network: null, networks: [], view: "dashboard", items: [], itemCache: new Map(),
  orders: [], devices: [], cpus: { cpus: [] }, icons: {}, filter: "", sort: "name", search: "",
  stockView: "stock", inventoryScope: "", inventoryStats: null,
  inventoryTrend: [], inventoryTrendKey: "", dashboardTrendHours: 24,
  cursor: "", anchorCursor: "", inventoryWindowAnchored: false, nextCursor: "", cursors: [{ cursor: "", anchor: "" }], page: 0, inventoryPageCount: 1, inventoryTotal: null, inventoryDesiredPage: null, inventoryLimit: 50, inventoryTarget: null, inventoryQueryPending: false, selected: null, amount: "1",
  cpuName: "", waitForCPU: false, inspector: "detail", draft: { version: 1, entries: [], receipts: {} },
  draftKey: "", draftBlocked: false, unchecked: new Set(), busy: false, preparing: false, inventoryLoading: false,
  orderFilter: "active", orderGroup: "batch", stream: null, streamConnected: false, streamState: "closed", generation: 0,
  context: 0, orderGeneration: 0, orderSnapshot: null, ordersLoading: false, ordersError: false,
  cpuMonitor: { search: "", filter: "all", sort: "network", expanded: new Set() },
  networkNodes: { search: "", filter: "all", role: "all", expanded: new Set() },
  activeOrderCount: 0, activeOrdersTruncated: false, detailId: "", detailGeneration: 0,
  taskMode: "jobs", jobFilter: "unsettled", jobStorageError: false,
  crafting: { search: "", sort: "newest", owner: "all", orderSearch: "" },
  settings: loadSettings(), settingsSection: "display", session: null, loginMode: "player", teamMembers: [], teamInvitations: [],
  texturePackStatus: texturePack.status(), texturePackLoading: false, texturePackError: "",
  connectionTest: { status: "idle", checkedAt: "", message: "" },
  networkLoading: false, networkError: "", networkErrorKind: "", networkSnapshotReady: false, networkCreateBusy: false,
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
const inventoryHistory = createHistoryController({ request: (path,options) => api.request(networkPath(path),options), onChange: () => renderInventoryHistory() });
let historySearchTimer, historyWheelTimer, historyGesture = null, historyExpanded = false;
function localDateValue(value) { const d=new Date(value);return Number.isFinite(d.getTime())?new Date(d.getTime()-d.getTimezoneOffset()*60000).toISOString().slice(0,16):""; }
function renderInventoryHistory() {
  const h=inventoryHistory.state, series=h.series;
  const view=$("#view-history"),scroll=view.scrollTop;
  view.dataset.mode=h.mode;
  $("#history-chart-section").hidden=h.mode==='table';
  $("#history-chart-section").dataset.expanded=String(historyExpanded);
  $$("[data-history-mode]").forEach(b=>b.setAttribute('aria-pressed',String(b.dataset.historyMode===h.mode)));
  $$("[data-history-range]").forEach(b=>b.setAttribute('aria-pressed',String(b.dataset.historyRange===h.range)));
  if(document.activeElement!==$("#history-from"))$("#history-from").value=localDateValue(h.from);
  if(document.activeElement!==$("#history-to"))$("#history-to").value=localDateValue(h.to);
  const epochOptions=series?.epochs?.map(e=>`<option value="${esc(e.id)}">${esc(e.scope==='nesql_plain'?'NESQL 无 NBT':'无 NBT 库存')} · ${esc(historyTime(e.started_at))}</option>`).join('')||'<option value="">等待记录</option>';
  patchDashboardHTML('#history-epoch',epochOptions);$("#history-epoch").value=h.epoch;
  $("#history-recording-status").textContent=series?.started_at?`开始于 ${historyTime(series.started_at)} · 原始明细长期保留`:'后端持续记录 · 原始明细长期保留';
  $("#history-chart-title").textContent=series?.item?.label||'全部已确认库存';
  $("#history-all-items").hidden=!h.item;
  $("#history-metric").disabled=!!h.item;$("#history-metric").value=h.metric;
  $("#history-kind").value=h.kind;$("#history-direction").value=h.direction;
  $("#history-export").hidden=!state.network||!series?.started_at||!!h.error||!!h.tableError||h.loading||h.tableLoading;$("#history-export").href=state.network?networkPath('/inventory-history/export?'+inventoryHistory.exportQuery()):'';
  const resolution=series?.resolution>86400?`${series.resolution/86400} 天汇总`:series?.resolution===86400?'日汇总':series?.resolution===3600?'小时汇总':'分钟记录';
  const latest=series?.points?.at(-1);
  const coverage=latest&&series.basis!=='last_known_quantity'&&latest.checked!==latest.candidates?` · 已确认 ${formatQuantity(latest.checked)} / ${formatQuantity(latest.candidates)} 种`:'';
  $("#history-chart-summary").textContent=latest?`${formatQuantity(latest[h.metric])} ${h.metric==='quantity'?'件':'种'} · ${series.basis==='last_known_quantity'?'最后已知数量':resolution}${coverage}${h.loading?' · 更新中':''}`:h.loading?'正在读取历史…':'等待后端历史';
  const note=series?.basis==='last_known_quantity'?'单物品曲线展示最后已知数量；两次变化之间不代表持续观测。原始明细保留每一次确认的变化。':`后端${resolution} · ${latest&&!latest.online?'OC 离线，保留上次库存 · ':''}首次观测会改变总量，不能全部视为生产增量。拖选时间段 · 滚轮缩放 · 双击恢复全部`;
  $("#history-chart-note").textContent=note;
  patchDashboardHTML('#history-chart',historyChartHTML(series,h.metric));
  $("#history-chart").setAttribute('aria-busy',String(h.loading));
  message('#history-message',h.error?`历史读取失败，保留已有内容。${h.error}`:!state.network?'选择网络后查看库存历史':'');
  message('#history-table-message',h.tableError?`明细读取失败，保留当前页。${h.tableError}`:'');
  patchDashboardHTML('#history-records',historyRecordsHTML(h.page?.records||[]));
  $("#history-table-empty").hidden=!!h.page?.records?.length;
  $("#history-table-empty").textContent=h.tableLoading?'正在读取明细…':h.tableError?'明细暂不可用':'这段时间没有匹配的记录';
  $("#history-page-status").textContent=`第 ${h.pageIndex+1} 页 · ${h.page?.records?.length||0} 条${h.tableLoading?' · 更新中':''}`;
  $("#history-page-number").textContent=h.pageIndex+1;
  $("#history-prev").disabled=h.tableLoading||h.loading||!!h.error||!!h.tableError||h.pageIndex===0;$("#history-next").disabled=h.tableLoading||h.loading||!!h.error||!!h.tableError||!h.page?.has_more;
  $("#history-new-records").hidden=!h.newData;
  const results=h.searchResults.map(i=>`<button data-history-item="${esc(i.id)}"><span><strong>${esc(i.label||i.name)}</strong><small>${esc(i.name)} · ${i.damage}</small></span><span class="number">${formatQuantity(i.quantity)}</span></button>`).join('');
  patchDashboardHTML('#history-search-results',results||`<span class="muted">${h.searchLoading?'正在搜索…':'没有匹配的已记录物品'}</span>`);
  paint();view.scrollTop=scroll;
}
const endedStates = new Set(["done", "failed", "canceled", "expired"]);
const stateNames = { queued: "排队中", assigned: "已派发", computing: "计算中", running: "合成中", done: "已完成", failed: "失败", canceled: "已撤回", expired: "已过期", needs_review: "结果待确认" };
let searchTimer, toastTimer, streamTimer, streamRefreshTimer, queryController, ordersController, detailController, cpuCallback, inspectorFocus, inventoryResizeTimer;
let draftItemsRequest = null, draftItemsRefreshAt = 0;
const inventoryWheel = createInventoryWheel();
let spriteAnimationFrame = 0;
const inventoryPrefetchControllers = new Set();
const inventoryPageCache = new Map();
const inventoryPageRequests = new Map();
let inventoryCacheEpoch = 0, inventoryLiveRequest = null;
let inventoryPrefetchJob = null, inventoryPrefetchTarget = null;
let inventoryNavigationFrame = 0, inventoryVisibleTimer = 0, inventoryImageTimer = 0;
let inventoryDirection = 1;
const inventoryImages = new Map();
const INVENTORY_PREFETCH_DEPTH = 24;
const INVENTORY_PAGE_CACHE_LIMIT = 160;
const INVENTORY_PAGE_CACHE_TTL = 120000;
const INVENTORY_PAGE_FRESH_TTL = 10000;
const INVENTORY_PAGE_CACHE_ITEMS = 6000;

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
  document.documentElement.dataset.sidebarCollapsed = String(state.settings.sidebarCollapsed);
  const sidebarToggle = $("#sidebar-toggle");
  sidebarToggle.setAttribute("aria-expanded", String(!state.settings.sidebarCollapsed));
  sidebarToggle.setAttribute("aria-label", state.settings.sidebarCollapsed ? "展开导航" : "收起导航");
  sidebarToggle.title = state.settings.sidebarCollapsed ? "展开导航" : "收起导航";
  placeGlobalControls();
  const remember = $("#remember-session");
  if (remember) remember.checked = state.settings.rememberSession;
}

function placeGlobalControls() {
  const controls = $("#global-controls");
  if (matchMedia("(min-width: 901px)").matches) {
    if (controls.parentElement !== $("#sidebar-tools")) $("#sidebar-tools").append(controls);
  } else if (controls.parentElement !== $(".content")) $(".content").insertBefore(controls, $("#main"));
}

function textureAssetURL(item) {
  const imageKey = item?.catalog?.image_key;
  if (state.settings.textureSource !== "server" && imageKey) {
    const localURL = texturePack.resolve(imageKey);
    if (localURL) return localURL;
  }
  return item?.catalog?.image_state === "available" && typeof item.catalog.image_url === "string"
    ? item.catalog.image_url
    : "";
}

function refreshTexturePackState() {
  state.texturePackStatus = texturePack.status();
  renderInventory();
  renderDetail();
  patchDraftAppearance();
  hideInventoryHover();
}

async function applyTexturePackURL(url, notify = true) {
  const normalized = String(url || "").trim();
  if (!normalized) throw new Error("请输入纹理包 URL。");
  state.texturePackLoading = true;
  state.texturePackError = "";
  renderSettings();
  try {
    const bytes = await loadTexturePackURL(normalized);
    const pathname = new URL(normalized, location.href).pathname;
    const label = decodeURIComponent(pathname.split("/").filter(Boolean).at(-1) || "纹理包");
    await texturePack.loadArchive(bytes, label);
    state.settings = saveSettings(localStorage, {
      ...state.settings, textureSource: "url", texturePackURL: normalized,
    });
    refreshTexturePackState();
    if (notify) toast(`纹理包已加载：${label}`);
  } catch (error) {
    state.texturePackError = error?.message || "纹理包加载失败。";
    texturePack.clear();
    refreshTexturePackState();
    if (notify) toast(`${state.texturePackError} 已回退到服务器纹理。`, true);
    throw error;
  } finally {
    state.texturePackLoading = false;
    renderSettings();
  }
}

async function applyTexturePackFile(file) {
  if (!file) return;
  state.texturePackLoading = true;
  state.texturePackError = "";
  renderSettings();
  try {
    const bytes = await file.arrayBuffer();
    await texturePack.loadArchive(bytes, file.name);
    await saveCachedTexturePack(bytes);
    state.settings = saveSettings(localStorage, {
      ...state.settings, textureSource: "local", texturePackURL: "",
    });
    refreshTexturePackState();
    toast(`本地纹理包已加载：${file.name}`);
  } catch (error) {
    state.texturePackError = error?.message || "纹理包加载失败。";
    texturePack.clear();
    refreshTexturePackState();
    toast(`${state.texturePackError} 已回退到服务器纹理。`, true);
  } finally {
    state.texturePackLoading = false;
    renderSettings();
  }
}

function textureSettingsHTML() {
  const status = state.texturePackLoading
    ? "正在读取纹理包…"
    : state.texturePackStatus.loaded
      ? `${state.texturePackStatus.label} · ${state.texturePackStatus.assetCount.toLocaleString("zh-CN")} 个资源`
      : state.texturePackError || "当前使用服务器按需纹理";
  return `<div class="setting-field"><span><strong>纹理来源</strong><small>默认按需读取；纹理包会缓存在当前浏览器，减少重复下载。</small></span><select data-setting="textureSource" aria-label="纹理来源"><option value="server" ${state.settings.textureSource === "server" ? "selected" : ""}>服务器按需加载</option><option value="url" ${state.settings.textureSource === "url" ? "selected" : ""}>指定纹理包 URL</option><option value="local" ${state.settings.textureSource === "local" ? "selected" : ""}>本地已导入纹理包</option></select></div>
    <div class="texture-pack-controls" ${state.settings.textureSource === "url" ? "" : "hidden"}><label class="setting-input"><span>纹理包地址</span><input id="texture-pack-url" type="url" value="${esc(state.settings.texturePackURL)}" placeholder="https://example.com/ae-orbit-textures.zip" autocomplete="off"></label><div class="setting-actions"><button type="button" class="secondary" data-texture-pack-action="load-url" ${state.texturePackLoading ? "disabled" : ""}>${icon("download")}加载并应用</button></div></div>
    <div class="setting-actions"><button type="button" class="secondary" data-texture-pack-action="load-server" ${state.texturePackLoading ? "disabled" : ""}>${icon("server")}加载服务器纹理包</button><button type="button" class="secondary" data-texture-pack-action="choose-local" ${state.texturePackLoading ? "disabled" : ""}>${icon("upload")}导入本地 ZIP</button><button type="button" class="secondary" data-texture-pack-action="clear" ${state.texturePackStatus.loaded ? "" : "disabled"}>${icon("trash-2")}清除纹理包</button></div>
    <div class="texture-pack-status" role="status" data-state="${state.texturePackError ? "error" : state.texturePackStatus.loaded ? "ready" : "idle"}">${esc(status)}</div>`;
}

function updateSettings(patch, notify = true) {
  try {
    state.settings = saveSettings(localStorage, { ...state.settings, ...patch });
    if (patch.textureSource === "server") {
      texturePack.clear();
      clearCachedTexturePack().catch(() => {});
      state.texturePackError = "";
      refreshTexturePackState();
    }
    applySettings();
    if (["sidebarCollapsed", "inventoryLayout", "density"].some(key => Object.prototype.hasOwnProperty.call(patch, key))) {
      hideInventoryHover();
      requestAnimationFrame(() => refreshInventoryCapacity());
    }
    if (Object.prototype.hasOwnProperty.call(patch, "rememberSession")) {
      if (state.settings.rememberSession && state.session) saveSession(localStorage, sessionStorage, state.session, true);
      if (!state.settings.rememberSession) clearSession(localStorage, sessionStorage);
    }
    if (Object.prototype.hasOwnProperty.call(patch, "reconnect") && state.network) {
      if (state.settings.reconnect) connectStream();
      else {
        state.stream?.close();
        state.streamConnected = false;
        state.streamState = "closed";
        refreshHeader();
      }
    }
    renderSettings();
    if (notify) toast("设置已保存");
  } catch { toast("设置无法保存，请检查浏览器存储。", true); }
}

function settingControl(key, label, description, checked) {
  return `<label class="setting-toggle"><span><strong>${label}</strong><small>${description}</small></span><input type="checkbox" data-setting="${key}" ${checked ? "checked" : ""}><span class="toggle-track" aria-hidden="true"></span></label>`;
}

function connectionDiagnosticsSnapshot() {
  return connectionDiagnostics({
    origin: location.origin,
    member: state.member,
    session: state.session,
    network: state.network,
    devices: state.devices,
    streamState: state.streamState,
    connectionTest: state.connectionTest,
  });
}

function diagnosticFact(label, value, detail, tone = "idle") {
  return `<div class="diagnostic-fact" data-state="${esc(tone)}"><span>${esc(label)}</span><strong><i class="status-dot" data-tone="${esc(tone)}"></i>${esc(value)}</strong><small>${esc(detail)}</small></div>`;
}

function connectionDiagnosticsHTML() {
  const diagnostics = connectionDiagnosticsSnapshot();
  const devices = diagnostics.devices;
  const recovery = networkRecoveryState({
    network: state.network,
    devices: state.devices,
    streamState: state.streamState,
    loading: state.networkLoading,
    error: state.networkError,
    errorKind: state.networkErrorKind,
    hasSnapshot: state.networkSnapshotReady,
    formatTime: dateTime,
    formatAge: age,
  });
  const deviceValue = devices.total ? `${devices.online} / ${devices.total} 在线` : "暂无设备观测";
  const deviceDetail = devices.total
    ? `${devices.offline} 台离线 · ${devices.stale} 台数据滞后`
    : "等待 OC 节点上报状态";
  const sessionDetail = diagnostics.session.expiresAt
    ? `有效期至 ${dateTime(diagnostics.session.expiresAt)}`
    : diagnostics.session.key === "active" ? "未返回会话有效期" : "登录后建立浏览器会话";
  const networkDetail = state.network
    ? `${recovery.detail} · 网络标识 ${state.network.id || "未返回"}`
    : "请选择网络后开始同步";
  const streamDetail = diagnostics.stream.key === "connected"
    ? "实时事件会自动触发局部刷新"
    : state.settings.reconnect ? "自动重连已开启，浏览器将继续尝试恢复" : "自动重连已关闭";
  const communication = diagnostics.lastCommunication ? dateTime(diagnostics.lastCommunication) : "尚无记录";
  const inventory = diagnostics.lastInventory ? dateTime(diagnostics.lastInventory) : "尚无记录";
  const test = connectionTestText(diagnostics.connectionTest, dateTime);
  return `<section class="connection-diagnostics" id="connection-diagnostics" aria-label="连接诊断"><div class="connection-diagnostics-heading"><strong>连接诊断</strong><small>状态随网络刷新与事件流自动更新</small></div><div class="connection-diagnostics-grid">${diagnosticFact("后端地址", diagnostics.origin, "页面通过同源代理连接后端", "ok")}${diagnosticFact("会话状态", diagnostics.session.label, sessionDetail, diagnostics.session.tone)}${diagnosticFact("当前网络", diagnostics.network.label, networkDetail, diagnostics.network.tone)}${diagnosticFact("事件流", streamStateText(diagnostics.stream.key), streamDetail, streamStateTone(diagnostics.stream.key))}${diagnosticFact("OC 节点", deviceValue, deviceDetail, devices.total && devices.offline === 0 && devices.stale === 0 ? "ok" : devices.total ? "warn" : "idle")}${diagnosticFact("最近通信", communication, "网络或 OC 节点最近一次心跳", diagnostics.lastCommunication ? "ok" : "idle")}${diagnosticFact("最近库存观测", inventory, "库存同步最近一次收到的时间", diagnostics.lastInventory ? "ok" : "idle")}${diagnosticFact("最近测试", test, diagnostics.connectionTest.checkedAt ? `测试时间 ${dateTime(diagnostics.connectionTest.checkedAt)}` : "尚未执行测试", connectionTestTone(diagnostics.connectionTest.status))}</div></section>`;
}

function renderConnectionDiagnostics() {
  const target = $("#connection-diagnostics");
  if (target) target.outerHTML = connectionDiagnosticsHTML();
}

function connectionSettingsHTML() {
  return settingControl("reconnect", "自动重连", "网络恢复后自动重新建立事件流连接。", state.settings.reconnect)
    + settingControl("rememberSession", "记住本次会话", "仅保存会话恢复信息，不保存原始访问令牌。", state.settings.rememberSession)
    + connectionDiagnosticsHTML()
    + `<div class="setting-actions"><button type="button" class="secondary" data-session-action="test"><i data-lucide="refresh-cw"></i>测试连接</button><button type="button" class="secondary" data-session-action="login"><i data-lucide="log-in"></i>重新登录</button><button type="button" class="secondary" data-session-action="clear"><i data-lucide="trash-2"></i>清除已保存会话</button></div>`;
}

function renderSettings() {
  const detail = $("#settings-detail");
  if (!detail) return;
  const section = state.settingsSection;
  $$("[data-settings-section]").forEach(button => {
    button.setAttribute("aria-selected", String(button.dataset.settingsSection === section));
    button.setAttribute("aria-current", String(button.dataset.settingsSection === section));
  });
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
    textures: {
      title: "纹理资源",
      description: "使用服务器按需纹理，或导入可复用的本地纹理包。",
      body: textureSettingsHTML(),
    },
    account: { title: "玩家与团队", description: "每位玩家使用独立身份，下单自动署名。", body: playerAccountHTML(state.member,state.networks,state.teamMembers,state.teamInvitations) },
    connection: {
      title: "连接与凭据",
      description: "管理服务连接、自动重连和浏览器会话。",
      body: connectionSettingsHTML(),
    },
  };
  const current = sections[section] || sections.display;
  detail.innerHTML = `<div class="settings-detail-heading"><div><h2>${current.title}</h2><span class="muted">${current.description}</span></div></div><div class="settings-detail-body">${current.body}</div>`;
  paint();
}

function errorText(error) {
  if (error?.name === "AbortError") return "";
  if (error?.code === "identity_changed") {
    api.setCSRF("");
    state.session = null;
    clearSession(localStorage,sessionStorage);
    state.stream?.close();
    showLogin();
  }
  else if (error?.status === 401) showLogin();
  if (error?.status || error?.code) return error.message;
  return "操作未完成，请检查数量、当前状态或浏览器存储。";
}

function message(id, text) {
  const el = $(id);
  el.textContent = text;
  el.hidden = !text;
}

function dateTime(value) {
  if (!value) return "尚无记录";
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? date.toLocaleString("zh-CN", { hour12: false }) : "尚无记录";
}

function age(value) {
  if (!value) return "未观测";
  const time = new Date(value).getTime();
  if (!Number.isFinite(time)) return "未观测";
  const seconds = Math.max(0, Math.floor((Date.now() - time) / 1000));
  if (seconds < 60) return `${seconds} 秒前`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`;
  return `${Math.floor(seconds / 86400)} 天前`;
}

function itemImage(item) {
  const source = itemTextureSource(item);
  if (source) return source;
  return `<span class="missing-image" title="暂无已核验图标">${inventoryIcon("package")}</span>`;
}

function itemTextureSource(item) {
  const imageURL = textureAssetURL(item);
  if (imageURL) {
    const animation = catalogAnimation(item);
    if (animation) {
      scheduleSpriteAnimation();
      return `<span class="item-image item-image-sprite" data-sprite-item="${esc(item.id)}" data-sprite-url="${esc(imageURL)}" data-sheet-width="${animation.width}" data-sheet-height="${animation.height}" role="img" aria-label="${esc(catalogDisplayName(item))}"></span>`;
    }
    return `<img class="item-image" src="${esc(imageURL)}" width="32" height="32" alt="" loading="lazy">`;
  }
  const filename = item?.kind === "item" ? state.icons[`${item.name}@${item.damage}`] : null;
  if (typeof filename === "string" && /^[a-f0-9]{64}\.png$/.test(filename)) {
    return `<img class="item-image" src="/assets/nei/${filename}" width="32" height="32" alt="" loading="lazy">`;
  }
  return "";
}

function scheduleSpriteAnimation() {
  if (!spriteAnimationFrame) spriteAnimationFrame = requestAnimationFrame(animateSpriteImages);
}

function animateSpriteImages(timestamp) {
  spriteAnimationFrame = 0;
  const elements = $$(".item-image-sprite");
  if (!elements.length) return;
  for (const element of elements) {
    const item = itemFor(element.dataset.spriteItem);
    const animation = catalogAnimation(item);
    if (!animation) continue;
    const selected = animationFrameAt(animation, timestamp);
    if (!selected) continue;
    const rect = element.getBoundingClientRect();
    const targetWidth = rect.width || element.offsetWidth || 32;
    const targetHeight = rect.height || element.offsetHeight || 32;
    const layout = spriteLayout(animation, targetWidth, targetHeight, selected.frame);
    const nextLayout = spriteLayout(animation, targetWidth, targetHeight, selected.next);
    if (!layout || !nextLayout) continue;
    const image = `url("${element.dataset.spriteUrl}")`;
    element.style.backgroundImage = image;
    element.style.backgroundSize = `${layout.backgroundWidth}px ${layout.backgroundHeight}px`;
    element.style.backgroundPosition = `${layout.offsetX - selected.frame.x * layout.scale}px ${layout.offsetY - selected.frame.y * layout.scale}px`;
    element.style.setProperty("--sprite-next-image", image);
    element.style.setProperty("--sprite-next-size", `${nextLayout.backgroundWidth}px ${nextLayout.backgroundHeight}px`);
    element.style.setProperty("--sprite-next-position", `${nextLayout.offsetX - selected.next.x * nextLayout.scale}px ${nextLayout.offsetY - selected.next.y * nextLayout.scale}px`);
    element.style.setProperty("--sprite-next-opacity", animation.interpolate ? String(selected.progress) : "0");
  }
  scheduleSpriteAnimation();
}

function itemHasTexture(item) {
  return Boolean(itemTextureSource(item));
}

function itemFor(id) {
  return state.itemCache.get(String(id)) || Object.values(state.draft.receipts).find(r => r.item?.id === String(id))?.item;
}

const inventoryHover = { button: null, pointer: { x: 0, y: 0 }, frame: 0, pages: [], page: 0, model: null, advanced: false, size: null };

function hideInventoryHover() {
  cancelAnimationFrame(inventoryHover.frame);
  inventoryHover.frame = 0;
  inventoryHover.button?.removeAttribute("aria-describedby");
  inventoryHover.button = null;
  const card = $("#inventory-hover-card");
  if (!card) return;
  card.hidden = true;
  card.innerHTML = "";
}

function positionInventoryHover() {
  inventoryHover.frame = 0;
  if (!inventoryHover.button?.isConnected) { hideInventoryHover(); return; }
  const card = $("#inventory-hover-card");
  const position = tooltipPosition(inventoryHover.pointer, inventoryHover.size, { width: innerWidth, height: innerHeight });
  card.style.left = `${position.left}px`;
  card.style.top = `${position.top}px`;
}

function renderInventoryHoverPage() {
  const card = $("#inventory-hover-card");
  const { model, pages, page } = inventoryHover;
  const paging = pages.length > 1 ? tooltipLineHTML({ kind: "hint", text: `${page + 1} / ${pages.length} · Z 下一页` }) : "";
  card.innerHTML = [model.title, ...pages[page], ...model.footers].map(tooltipLineHTML).join("") + paging;
  inventoryHover.size = card.getBoundingClientRect();
  positionInventoryHover();
}

function refreshInventoryHover() {
  const button = inventoryHover.button;
  const item = itemFor(button?.dataset.item);
  if (!item || !button?.isConnected) { hideInventoryHover(); return; }
  const card = $("#inventory-hover-card");
  const model = inventoryTooltipModel(item, inventoryHover.advanced);
  if (JSON.stringify(model) === JSON.stringify(inventoryHover.model) && !card.hidden) return;
  inventoryHover.model = model;
  // Use the longest content's width for every page, avoiding jumps when pressing Z.
  card.style.width = "max-content";
  card.innerHTML = [model.title, ...model.lines, ...model.footers].map(tooltipLineHTML).join("");
  card.hidden = false;
  card.style.width = `${card.getBoundingClientRect().width}px`;
  const rows = [...card.children];
  const rowHeight = row => {
    const style = getComputedStyle(row);
    return row.getBoundingClientRect().height + parseFloat(style.marginTop) + parseFloat(style.marginBottom);
  };
  const reserved = rowHeight(rows[0]) + rows.slice(1 + model.lines.length).reduce((height, row) => height + rowHeight(row), 0) + 36;
  const available = Math.max(18, innerHeight - 16 - reserved);
  const probe = document.createElement("div");
  card.append(probe);
  const measure = line => { probe.innerHTML = tooltipLineHTML(line); return rowHeight(probe.firstElementChild); };
  const lines = model.lines.flatMap(line => splitTooltipLine(line, fragment => measure(fragment) <= available));
  const heights = lines.map(measure);
  inventoryHover.pages = paginateTooltipLines(lines, heights, available);
  inventoryHover.page = Math.min(inventoryHover.page, inventoryHover.pages.length - 1);
  renderInventoryHoverPage();
}

function showInventoryHover(button, event) {
  if (!itemFor(button?.dataset.item) || matchMedia("(max-width: 800px)").matches || !matchMedia("(any-hover: hover)").matches) return;
  if (inventoryHover.button !== button) { hideInventoryHover(); inventoryHover.model = null; inventoryHover.page = 0; }
  inventoryHover.button = button;
  inventoryHover.pointer = { x: event.clientX, y: event.clientY };
  inventoryHover.advanced = event.shiftKey;
  button.setAttribute("aria-describedby", "inventory-hover-card");
  refreshInventoryHover();
  button.querySelectorAll("[title]").forEach(element => element.removeAttribute("title"));
}

const networkPath = (suffix = "", network = state.network?.id) => `/api/v1/networks/${encodeURIComponent(network)}${suffix}`;
const maximum = () => state.network?.capabilities?.max_craft_quantity || "9223372036854775807";
const cpuSelectionSupported = () => state.network?.capabilities?.cpu_selection === true;
const cpuSelectionTitle = () => cpuSelectionSupported() ? "选择合成 CPU" : "当前 OC 未启用严格 CPU 指定";

const dashboardHTML = new WeakMap();
function patchDashboardHTML(selector, html) {
  const element = $(selector);
  if (dashboardHTML.get(element) === html) return;
  dashboardHTML.set(element, html);
  const active = element.contains(document.activeElement) ? document.activeElement : null;
  const key = active?.dataset.job || active?.dataset.order || active?.dataset.view;
  element.innerHTML = html;
  if (key) $$('[data-job], [data-order], [data-view]', element).find(row => (row.dataset.job || row.dataset.order || row.dataset.view) === key)?.focus({ preventScroll: true });
}

function renderDashboard() {
  const network = state.network, now = Date.now();
  const view = $("#view-dashboard"), scroll = view.scrollTop;
  const recovery = networkRecoveryState({ network, devices: state.devices, streamState: state.streamState,
    loading: state.networkLoading, error: state.networkError, errorKind: state.networkErrorKind, hasSnapshot: state.networkSnapshotReady }, now);
  const labels = { healthy: "网络正常", loading: "正在同步", reconnecting: "实时重连中", heartbeat_waiting: "等待游戏心跳", oc_offline: "OC 离线", backend_error: "后端异常", backend_unavailable: "后端不可达", waiting: "等待 OC 节点" };
  const tone = !state.member || !network ? "idle" : recovery.tone;
  $("#dashboard-network-card").dataset.state = tone === "ok" ? "online" : tone === "warn" ? "offline" : "idle";
  $("#dashboard-network-dot").dataset.tone = tone;
  $("#dashboard-network-state").textContent = !state.member ? "未登录" : !network ? "暂无网络" : labels[recovery.key] || recovery.label;
  $("#dashboard-network-name").textContent = network?.name || "选择网络";
  $("#dashboard-network-meta").textContent = !state.member ? "登录后开始同步" : !network ? "请选择一个网络" : recovery.key === "healthy" ? `心跳 ${age(network.last_seen)} · 实时连接` : recovery.key === "loading" ? "保留当前数据，正在同步" : recovery.key === "reconnecting" ? "网络在线 · 等待实时连接恢复" : recovery.key === "oc_offline" ? "等待 OC 恢复 · 显示上次观测" : recovery.label;
  $("#dashboard-network-card").title = network ? recovery.detail : "";
  $("#dashboard-sync").textContent = network?.last_seen ? `最近同步 ${age(network.last_seen)}` : "等待同步";

  const inventory = dashboardInventory(state.inventoryStats);
  $("#dashboard-inventory-total").innerHTML = inventory.known ? `${quantityHTML(inventory.total)}<small>件</small>` : "—";
  $("#dashboard-inventory-total").title = inventory.known ? `${formatQuantity(inventory.total)} 件 · ${inventory.scope}` : "等待库存观测";
  $("#dashboard-inventory-count").innerHTML = inventory.known ? `${quantityHTML(inventory.entries)}<small>种</small>` : "—";
  $("#dashboard-inventory-meta").textContent = inventory.known ? inventory.scope : "等待库存观测";
  $("#dashboard-inventory-kinds-meta").textContent = inventory.pending ? `${formatQuantity(inventory.pending)} 种候选待确认` : inventory.known ? "已确认有库存的种类" : "等待库存观测";
  $("#dashboard-inventory-kinds-meta").dataset.tone = inventory.pending ? "warn" : "idle";
  const cpus = state.cpus.cpus || [], busy = cpus.filter(cpu => cpu.busy).length;
  const observed = state.cpus.observed_at || network?.services?.cpu_observed_at;
  const cpuKnown = !!observed || cpus.length > 0;
  $("#dashboard-cpu-state").innerHTML = cpuKnown ? `${busy}<small>/ ${cpus.length} 台${state.cpus.truncated ? "已观测" : ""}</small>` : "—";
  $("#dashboard-cpu-meta").textContent = cpuKnown ? `${cpus.length - busy} 台空闲${state.cpus.truncated ? " · 列表不完整" : ""}` : "等待 CPU 观测";
  $("#dashboard-cpu-meta").dataset.tone = state.cpus.truncated ? "warn" : "idle";
  const tasks = dashboardTasks(jobs.state, { snapshot: state.orderSnapshot, rows: state.orders, activeCount: state.activeOrderCount, truncated: state.activeOrdersTruncated, loading: state.ordersLoading, error: state.ordersError });
  $("#dashboard-job-label").textContent = tasks.label;
  $("#dashboard-job-count").innerHTML = tasks.count === null ? "—" : `${formatQuantity(tasks.count)}${tasks.lowerBound ? "+" : ""}<small>${tasks.legacy ? "项" : "组"}</small>`;
  $("#dashboard-job-meta").textContent = tasks.notice || (tasks.lowerBound ? "数量下限 · 订单列表不完整" : tasks.attention ? `${tasks.attention} 组暂停或需处理` : tasks.count === 0 ? "队列为空" : tasks.legacy ? "执行中的单项订单" : "未结束任务 · 可查看详情");
  $("#dashboard-job-meta").dataset.tone = tasks.attention || tasks.notice || tasks.lowerBound ? "warn" : "idle";
  $("#dashboard-tasks-title").textContent = tasks.legacy ? "执行中订单" : "生产任务";
  $("#dashboard-tasks-note").textContent = tasks.notice || (tasks.legacy ? "单项订单 · 最近提交优先" : "未结束任务 · 最近提交优先");
  patchDashboardHTML("#dashboard-task-list", dashboardTasksHTML(tasks, { age, itemName: id => itemFor(id)?.label || `物品 #${id}` }));

  const windowMs = state.dashboardTrendHours * 3600000;
  const model = inventoryTrendModel(state.inventoryTrend, now, { windowMs });
  let change = "正在积累时间采样", changeTone = "idle";
  if (model?.samples > 1) {
    const delta = BigInt(model.latest.quantity) - BigInt(model.first.quantity);
    const amount = formatQuantity(delta < 0n ? -delta : delta);
    change = `采样期间${delta > 0n ? "增加" : delta < 0n ? "减少" : "净变化"} ${amount} 件`;
    changeTone = delta > 0n ? "ok" : "idle";
  } else if (!model) change = `最近 ${state.dashboardTrendHours} 小时暂无采样`;
  $("#dashboard-trend-change").textContent = change;
  $("#dashboard-trend-change").dataset.tone = changeTone;
  patchDashboardHTML("#dashboard-trend", inventoryTrendHTML(state.inventoryTrend, { now, windowMs,
    empty: !state.member ? "登录后显示库存时间趋势" : !network ? "选择网络后开始采样" : "所选时间范围暂无库存采样" }));
  const devices = networkDeviceSummary(state.devices, now);
  const deviceDetail = devices.total ? `${devices.stale ? `${devices.stale} 台滞后 · ` : ""}${devices.offline ? `${devices.offline} 台离线` : "OC 节点"}` : "等待设备观测";
  const observations = [
    ["clock", "网络心跳", network?.last_seen ? age(network.last_seen) : "尚无记录", network?.online ? "主 OC 在线" : network ? "等待 OC 恢复" : "等待网络", network?.last_seen || "", network && !network.online ? "warn" : "idle"],
    ["boxes", "库存查询", inventory.coverage, inventory.pending ? `${formatQuantity(inventory.pending)} 种待确认 · ${inventory.observedAt ? age(inventory.observedAt) : "等待观测"}` : inventory.observedAt ? `更新于 ${age(inventory.observedAt)}` : "等待库存观测", inventory.observedAt, inventory.pending ? "warn" : "idle"],
    ["cpu", "CPU 快照", observed ? age(observed) : "尚无记录", cpuKnown ? `${cpus.length} 台已观测${state.cpus.truncated ? " · 列表不完整" : ""}` : "等待 CPU 观测", observed || "", state.cpus.truncated ? "warn" : "idle"],
    ["radio-tower", "OC 节点", devices.total ? `${devices.online} / ${devices.total} 台在线` : "尚无记录", deviceDetail, "", devices.stale || devices.offline ? "warn" : "idle"],
  ];
  patchDashboardHTML("#dashboard-observation-list", observations.map(([symbol, label, value, detail, timestamp, rowTone]) => `<div class="dashboard-observation" data-tone="${rowTone}"${timestamp ? ` title="${esc(dateTime(timestamp))}"` : ""}><i data-lucide="${symbol}" aria-hidden="true"></i><div><span>${esc(label)}</span><strong>${esc(value)}</strong><small>${esc(detail)}</small></div></div>`).join(""));
  paint();
  view.scrollTop = scroll;
}

function refreshHeader() {
  $("#member-name").textContent = playerName(state.member);
  $("#logout").hidden = !state.member;
  const network = state.network;
  const recovery = networkRecoveryState({
    network,
    devices: state.devices,
    streamState: state.streamState,
    loading: state.networkLoading,
    error: state.networkError,
    errorKind: state.networkErrorKind,
    hasSnapshot: state.networkSnapshotReady,
  });
  const connectionLabel = !state.member ? "未登录" : !network ? "暂无网络"
    : recovery.key === "backend_unavailable" ? "后端不可达"
      : recovery.key === "backend_error" ? "后端异常"
        : recovery.key === "heartbeat_waiting" ? "等待游戏心跳"
          : recovery.key === "oc_offline" ? "OC 离线"
            : recovery.key === "reconnecting" ? "状态重连中"
              : network.online ? "已连接" : "OC 离线";
  $("#connection").textContent = connectionLabel;
  $("#connection").dataset.state = !state.member || !network ? "idle" : recovery.tone === "ok" ? "online" : recovery.tone === "warn" ? "offline" : "reconnecting";
  const picker = $(".network-picker");
  picker.dataset.state = $("#connection").dataset.state;
  picker.title = `${network?.name || "选择网络"} · ${connectionLabel}`;
  $("#network").title = picker.title;
  $("#network-quality").textContent = network ? networkQualityLabel(network.quality) : "";
  const summary = inventorySummary(state.inventoryStats, age);
  $("#inventory-summary").textContent = network ? (summary ? `${summary}${network.online ? "" : " · OC 离线，显示缓存"}` : "") || `${network.name} · ${network.online ? "在线" : "离线"}` : "尚未连接网络";
  $("#stock-view-label").hidden = state.inventoryScope !== "nesql_plain";
  $("#draft-count").textContent = state.draft.entries.length;
  renderDashboard();
  renderConnectionDiagnostics();
}

function showView(view) {
  hideInventoryHover();
  state.view = view;
  const viewName = $("#sidebar [data-view='" + view + "']")?.getAttribute("aria-label") || "AE Orbit";
  document.title = `${viewName} · AE Orbit`;
  let hasRefresh = false;
  $$("[data-refresh-view]").forEach(button => { button.hidden = button.dataset.refreshView !== view; if (!button.hidden) hasRefresh = true; });
  $(".page-refresh").hidden = !hasRefresh;
  for (const name of ["dashboard", "inventory", "history", "orders", "cpus", "network", "github", "settings"]) $(`#view-${name}`).hidden = name !== view;
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
  if (view === "inventory") refreshInventoryCapacity();
  if (view === "dashboard") renderDashboard();
  if (view === "history") { renderInventoryHistory(); if(state.network&&!inventoryHistory.state.series) void inventoryHistory.load(); }
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
  if (tab === "draft") void refreshDraftItems(true);
}

function closeInspector(restore = true) {
  delete $("#inspector").dataset.open;
  document.body.classList.remove("sheet-open");
  $("#sheet-backdrop").hidden = true;
  if (restore && inspectorFocus?.isConnected) inspectorFocus.focus();
}

function renderInventory() {
  const hoveredID = inventoryHover.button?.dataset.item;
  if (hoveredID) hideInventoryHover();
  const tbody = $("#inventory-rows");
  tbody.innerHTML = state.items.map(item => `<tr data-selected="${state.selected?.id === item.id}" data-id="${esc(item.id)}">
    <td><button class="item-button ${itemHasTexture(item) ? "" : "missing-texture"}" data-item="${esc(item.id)}" aria-label="${esc(`${catalogDisplayName(item)}，库存 ${stockText(item)}`)}">${itemImage(item)}<span class="item-copy"><span class="item-name">${esc(catalogDisplayName(item))}</span><span class="item-meta">${esc(item.name.split(":")[0])}${item.craftable ? " · 可合成" : ""}${!item.exact ? " · 身份待确认" : ""}</span></span></button></td>
    <td><span class="stock-value ${String(item.quantity).length > 12 ? "long-number" : ""} ${item.current_session ? "" : "stale"}" title="${esc(stockText(item))}">${(quantityKnown(item) ? compactQuantityHTML(item.quantity) : "未查询")}</span><span class="freshness ${item.current_session ? "" : "stale"}">${stockFreshness(item, age)}</span></td>
    <td><button class="icon-button" data-quick-add="${esc(item.id)}" title="加入清单" aria-label="将${esc(catalogDisplayName(item))}加入清单" ${item.craftable && item.exact && !state.draftBlocked && !state.busy && !state.preparing ? "" : "disabled"}>${inventoryIcon("plus")}</button></td>
  </tr>`).join("");
  $("#inventory-empty").hidden = state.items.length > 0;
  $("#inventory-empty").textContent = state.inventoryLoading ? "正在读取库存…" : !state.member ? "登录后查看库存" : !state.network ? "没有可访问的网络" : "没有匹配的物品";
  renderInventoryPager();
  paint();
  scheduleSpriteAnimation();
  if (hoveredID) {
    const button = document.elementFromPoint(inventoryHover.pointer.x, inventoryHover.pointer.y)?.closest(".inventory-table .item-button");
    if (button?.dataset.item === hoveredID) showInventoryHover(button, { clientX: inventoryHover.pointer.x, clientY: inventoryHover.pointer.y, shiftKey: inventoryHover.advanced });
  }
}

function patchInventoryRows() {
  const tbody = $("#inventory-rows");
  const rows = [...tbody.children];
  if (rows.length !== state.items.length || rows.some((row, index) => row.dataset.id !== String(state.items[index]?.id))) {
    return false;
  }
  rows.forEach((row, index) => {
    const item = state.items[index];
    const button = $(".item-button", row);
    if (!button) return;
    row.dataset.selected = String(state.selected?.id === item.id);
    button.classList.toggle("missing-texture", !itemHasTexture(item));
    button.setAttribute("aria-label", `${catalogDisplayName(item)}，库存 ${stockText(item)}`);
    const image = $(".item-image, .missing-image", button);
    const nextURL = textureAssetURL(item);
    const nextTexture = itemTextureSource(item);
    const keepAnimatedImage = image?.classList.contains("item-image-sprite")
      && Boolean(catalogAnimation(item))
      && image.dataset.spriteUrl === nextURL;
    const keepStaticImage = image?.tagName === "IMG"
      && (image.getAttribute("src") === nextURL
        || (!nextURL && image.getAttribute("src")?.startsWith("/assets/nei/")));
    const keepMissingImage = image?.classList.contains("missing-image") && !nextTexture;
    if (!keepAnimatedImage && !keepStaticImage) {
      if (!keepMissingImage) {
        if (image) image.outerHTML = nextTexture;
        else button.insertAdjacentHTML("afterbegin", nextTexture);
      }
    }
    const name = $(".item-name", button);
    if (name) name.textContent = catalogDisplayName(item);
    const meta = $(".item-meta", button);
    if (meta) meta.textContent = `${item.name.split(":")[0]}${item.craftable ? " · 可合成" : ""}${!item.exact ? " · 身份待确认" : ""}`;
    const stock = $(".stock-value", row);
    if (stock) {
      stock.classList.toggle("long-number", String(item.quantity).length > 12);
      stock.classList.toggle("stale", !item.current_session);
      stock.title = stockText(item);
      stock.innerHTML = (quantityKnown(item) ? compactQuantityHTML(item.quantity) : "未查询");
    }
    const freshness = $(".freshness", row);
    if (freshness) {
      freshness.classList.toggle("stale", !item.current_session);
      freshness.textContent = stockFreshness(item, age);
    }
    const quickAdd = $("[data-quick-add]", row);
    if (quickAdd) {
      quickAdd.disabled = !(item.craftable && item.exact && !state.draftBlocked && !state.busy && !state.preparing);
    }
  });
  $("#inventory-empty").hidden = state.items.length > 0;
  renderInventoryPager();
  paint();
  scheduleSpriteAnimation();
  if (inventoryHover.button) refreshInventoryHover();
  return true;
}

function renderInventoryPager() {
  const loading = state.inventoryLoading;
  $("#first-page").disabled = state.inventoryQueryPending || (state.page === 0 && !state.inventoryWindowAnchored);
  $("#prev-page").disabled = state.inventoryQueryPending || state.inventoryPageCount <= 1;
  $("#next-page").disabled = state.inventoryQueryPending || state.inventoryPageCount <= 1;
  $("#refresh-inventory").disabled = loading;
  $("#inventory-rows").setAttribute("aria-busy", String(loading && !state.inventoryTarget?.absolute));
  const targetPage = state.inventoryDesiredPage ?? state.inventoryTarget?.page;
  const position = `${state.page + 1} / ${state.inventoryPageCount}`;
  $("#page-label").textContent = loading
    ? `${position} 页 · 正在读取${targetPage != null && targetPage !== state.page ? `第 ${targetPage + 1} 页` : ""}…`
    : state.inventoryQueryPending ? "读取失败 · 点击刷新重试"
    : `${position} 页 · ${state.items.length} 项`;
}

function patchSelectedDetail(item) {
  if (!item) return;
  const stock = $(".detail-stock", $("#detail-panel"));
  if (stock) {
    stock.classList.toggle("long-number", String(item.quantity ?? "0").length > 12);
    stock.innerHTML = (quantityKnown(item) ? quantityHTML(item.quantity ?? "0") : "未查询");
  }
  const facts = $$(".detail-facts .fact", $("#detail-panel"));
  if (facts[0]?.lastElementChild) facts[0].lastElementChild.textContent = stockFreshness(item, age);
}

function detailNeedsRender(before, after) {
  if (!before || !after) return true;
  return before.id !== after.id
    || before.label !== after.label
    || before.name !== after.name
    || before.damage !== after.damage
    || before.craftable !== after.craftable
    || before.craftable_current_session !== after.craftable_current_session
    || before.quantity_known !== after.quantity_known
    || before.craftable_observed_at !== after.craftable_observed_at
    || before.exact !== after.exact
    || textureAssetURL(before) !== textureAssetURL(after);
}

function renderDetail() {
  const item = state.selected;
  if (!item) {
    $("#detail-panel").innerHTML = `<div class="detail-empty">${icon("boxes")}<strong>选择一个物品</strong><span>从库存网格中选择物品后，这里会显示数量、状态与合成操作。</span></div>`;
    return;
  }
  const canAdd = item.craftable && item.exact && !state.draftBlocked && !state.busy && !state.preparing;
  const reason = !item.exact ? "物品身份待确认" : !item.craftable ? "合成样板尚未确认" : "";
  $("#detail-panel").innerHTML = `<div class="detail-heading">${itemImage(item)}<div><h2>${esc(catalogDisplayName(item))}</h2><span class="muted">${esc(item.name.split(":")[0])}</span></div></div>
    <span class="muted">库存数量</span><div class="detail-stock number ${String(item.quantity ?? "0").length > 12 ? "long-number" : ""}">${(quantityKnown(item) ? quantityHTML(item.quantity ?? "0") : "未查询")}</div><span class="item-meta">${esc(item.name)} · ${esc(item.damage)}</span>
    <div class="detail-facts"><div class="fact"><span>库存观测</span><span>${stockFreshness(item, age)}</span></div><div class="fact"><span>合成样板</span><span>${!item.craftable_observed_at ? "未确认" : item.craftable ? item.craftable_current_session ? "已确认" : "待重新核验" : "不可合成"}</span></div><div class="fact"><span>身份</span><span>${item.exact ? "精确" : "待确认"}</span></div></div>
    <label class="field-label" for="craft-quantity">合成数量</label><div class="stepper"><button data-step="-1" title="减少数量" aria-label="减少数量">${icon("minus")}</button><input id="craft-quantity" type="text" inputmode="numeric" maxlength="19" value="${esc(state.amount)}" autocomplete="off"><button data-step="1" title="增加数量" aria-label="增加数量">${icon("plus")}</button></div>
    <span class="field-label">合成 CPU</span><button class="cpu-select" id="detail-cpu" ${state.busy || state.preparing || !state.network ? "disabled" : ""} title="${cpuSelectionTitle()}"><span>${esc(state.cpuName || "自动分配")}${state.waitForCPU ? " · 等待" : ""}${!cpuSelectionSupported() ? " · 当前 OC 未启用" : ""}</span>${icon("cpu")}</button>
    <div class="detail-actions"><button class="primary wide" id="add-to-draft" ${canAdd ? "" : "disabled"}>${icon("list-plus")}加入清单</button><button class="secondary wide" id="submit-immediately" ${canAdd && jobs.state.support === "supported" && !state.busy ? "" : "disabled"}>${icon("send")}立即提交</button></div>
    <p class="draft-submitter">下单人：${esc(playerName(state.member))}</p><p id="detail-feedback" class="detail-feedback" role="status">${esc(reason || submissionStatus())}</p>`;
  paint();
  scheduleSpriteAnimation();
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
  const view = $("#view-orders"), scroll = view.scrollTop;
  const options = { ...state.crafting, filter: state.jobFilter, memberId: state.member?.id || "" };
  const roster = craftingJobModel(model, options);
  patchDashboardHTML("#job-list", jobListHTML(model, state.jobFilter, dateTime, options));
  $("#job-list").setAttribute("aria-busy", String(model.loading || model.historyLoading));
  for (const key of ["unsettled", "attention", "history"]) $("#crafting-count-" + key).textContent = roster.known ? roster.counts[key] : "—";
  $$("[data-job-filter]").forEach(button => button.setAttribute("aria-pressed", String(button.dataset.jobFilter === state.jobFilter)));
  $$("[data-task-mode]").forEach(button => button.setAttribute("aria-pressed", String(button.dataset.taskMode === state.taskMode)));
  if (state.taskMode === "jobs") {
    patchDashboardHTML("#crafting-overview", craftingOverviewHTML(roster));
    $("#crafting-overview").hidden = false;
    $("#crafting-status").textContent = model.support === "unsupported" ? "后端尚不支持生产任务，新提交已禁用。"
      : model.error ? "读取失败，保留已有任务记录。" : !roster.known ? "等待任务观测"
      : !state.network?.online ? "OC 离线 · 已保存的生产任务等待设备恢复" : "来自后端任务记录 · 自动更新";
  }
  $("#jobs-summary").textContent = roster.known
    ? (state.jobFilter === "history" ? "已加载 " + roster.counts.history + " 组 · 显示 " + roster.rows.length + " 组" : "显示 " + roster.rows.length + " 组")
    : model.loading ? "正在读取…" : "";
  const error = state.jobStorageError ? submissionStatus() : model.error ? `任务读取未完成${model.sync ? "，保留上次记录" : ""}。${model.error.status === 403 ? "当前账号无权限。" : errorText(model.error)}` : "";
  message("#jobs-message", error);
  let local;
  try {
    local = jobs.local();
    const recoveries = recoveryHTML(local, icon);
    patchDashboardHTML("#job-recoveries", recoveries);
    const section = $("#crafting-recoveries");
    if (section.hidden && recoveries) section.open = true;
    section.hidden = !recoveries;
  }
  catch {
    $("#crafting-recoveries").hidden = false;
    $("#crafting-recoveries").open = true;
    patchDashboardHTML("#job-recoveries", '<p class="error">本机生产任务记录损坏。请保留浏览器数据，勿重复提交。</p>');
  }
  const more = $("#job-history-more");
  $("#crafting-history-note").textContent = state.jobFilter === "history" ? "历史按批次加载，搜索仅匹配已加载记录。" : "";
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
    message("#job-detail-status", model.detailError ? `详情不可用。${model.detailError.status === 404 ? "记录已归档或不存在；本机意图不会解锁。" : errorText(model.detailError)}` : model.detailLoading && !model.detail ? "正在读取详情…" : "");
    $("#job-detail-status").dataset.error = String(!!model.detailError);
    $("#job-detail").setAttribute("aria-busy", String(model.detailLoading));
    if (model.detail && local) {
      const detailScroll = $("#job-dialog").scrollTop;
      const identifiersOpen = $(".crafting-detail-identifiers", $("#job-detail"))?.open || false;
      patchDashboardHTML("#job-detail", jobDetailHTML(model.detail, local, model.detailError ? null : state.member,
      {itemFor,itemImage,quantity:quantityHTML,dateTime,icon,controlsAvailable:!model.detailLoading && !model.detailError}));
      const identifiers = $(".crafting-detail-identifiers", $("#job-detail"));
      if (identifiers) identifiers.open = identifiersOpen;
      $("#job-dialog").scrollTop = detailScroll;
    } else patchDashboardHTML("#job-detail", "");
  }
  renderDashboard();
  paint();
  view.scrollTop = scroll;
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
  const summary = draftSelectionSummary(entries, state.unchecked);
  const panel = $("#draft-panel");
  const scrollTop = $(".draft-list", panel)?.scrollTop || 0;
  const focused = panel.contains(document.activeElement) ? document.activeElement : null;
  const focusKey = focused?.id ? `#${CSS.escape(focused.id)}`
    : focused ? ["quantity", "check", "draftCpu", "remove", "resetCpu"].map(key => {
      const attribute = key.replace(/[A-Z]/g, char => `-${char.toLowerCase()}`);
      return focused.dataset[key] ? `[data-${attribute}="${CSS.escape(focused.dataset[key])}"]` : "";
    }).find(Boolean) : "";
  const selection = focused?.tagName === "INPUT" && focused.type === "text" ? [focused.selectionStart, focused.selectionEnd] : null;
  $("#draft-count").textContent = entries.length;
  panel.innerHTML = state.draftBlocked ? `<p class="error">本机清单或生产任务记录无法恢复。请保留浏览器数据并核对已提交任务，不要重复提交。</p>` : entries.length ? `
    <div class="draft-toolbar"><label class="check"><input id="draft-check-all" type="checkbox" aria-label="选择全部可编辑条目" ${chosen.length > 0 && chosen.length === summary.editable ? "checked" : ""} ${state.busy || !summary.editable ? "disabled" : ""}><span>全选 <small>${entries.length} 项</small></span></label><div class="draft-toolbar-actions"><button class="icon-button" id="refresh-draft-items" title="刷新清单纹理与库存" aria-label="刷新清单纹理与库存">${icon("refresh-cw")}</button><button class="secondary" id="bulk-cpu" ${!cpuSelectionSupported() || !chosen.length || state.busy ? "disabled" : ""} title="${cpuSelectionTitle()}">${icon("cpu")}批量 CPU</button></div></div>
    <div class="draft-list" role="list" aria-label="待提交物品">${entries.map(entry => {
      const item = draftDisplayItem(entry, state.itemCache);
      const label = catalogDisplayName(item);
      const editable = entry.status === "draft" && !state.busy;
      return `<div class="draft-entry" role="listitem" data-draft-id="${esc(entry.id)}" data-selected="${entry.status === "draft" && !state.unchecked.has(entry.id)}" data-status="${entry.status}">
        <div class="draft-entry-top"><input id="draft-check-${esc(entry.id)}" class="draft-check" type="checkbox" aria-label="选择${esc(label)}" data-check="${esc(entry.id)}" ${entry.status === "draft" && !state.unchecked.has(entry.id) ? "checked" : ""} ${editable ? "" : "disabled"}><span class="draft-entry-media" data-texture-key="${esc(draftTextureKey(item))}">${itemImage(item)}</span><div class="draft-entry-name"><label for="draft-check-${esc(entry.id)}" title="${esc(label)}">${esc(label)}</label><span class="draft-entry-stock" title="${esc(stockText(item))}">${esc(draftStockLabel(item))}</span></div><button class="icon-button" data-remove="${esc(entry.id)}" title="移除草稿" aria-label="移除${esc(label)}草稿" ${editable ? "" : "disabled"}>${icon("trash-2")}</button></div>
        <div class="draft-entry-fields"><label class="draft-quantity-field"><span>合成数量</span><input class="draft-quantity" data-quantity="${esc(entry.id)}" type="text" inputmode="numeric" maxlength="19" aria-label="${esc(label)}合成数量" value="${esc(entry.quantity)}" ${editable ? "" : "disabled"}></label><div class="draft-cpu-field"><span>合成 CPU</span><button class="cpu-select" data-draft-cpu="${esc(entry.id)}" ${!editable || !cpuSelectionSupported() ? "disabled" : ""} title="${cpuSelectionTitle()}"><span>${esc(entry.cpu_name || "自动分配")}${entry.wait_for_cpu ? " · 等待" : ""}${!cpuSelectionSupported() ? " · 未启用" : ""}</span>${icon("cpu")}</button></div></div>
        ${entry.status === "draft" && (entry.cpu_name || entry.wait_for_cpu) ? `<button class="text-button draft-reset-cpu" data-reset-cpu="${esc(entry.id)}" ${state.busy ? "disabled" : ""}>恢复自动分配</button>` : ""}
        ${entry.status !== "draft" || entry.error ? `<p class="draft-note" data-status="${entry.status}">${entry.status === "submitting" ? "正在提交" : entry.status === "uncertain" ? "受理结果待核对 · 此条目已锁定" : esc(entry.error)}</p>` : ""}
        ${entry.status === "uncertain" ? isJobClaim(entry) ? `<button class="secondary wide" data-job-reconcile="${esc(entry.batch_id.slice(4))}">${icon("refresh-cw")}核对原生产任务</button>` : `<button class="secondary wide" data-reconcile="${esc(entry.id)}">${icon("refresh-cw")}核对原单项订单</button>` : ""}
      </div>`;
    }).join("")}</div>
    <div class="draft-submit"><p class="draft-submitter">以 <strong>${esc(playerName(state.member))}</strong> 的身份提交</p><div class="draft-total"><span>已选 <strong>${summary.selected}</strong> 项</span><span title="所选条目的合成数量合计">共 <strong class="number">${quantityHTML(summary.quantity)}</strong> 件</span></div><button class="primary" id="submit-draft" ${chosen.length && !state.busy && jobs.state.support === "supported" ? "" : "disabled"}>${icon("send")}${state.busy ? "正在提交生产任务" : `提交 ${chosen.length} 项`}</button><p class="draft-note">${esc(submissionStatus() || "提交后可在合成中心查看进度")}</p></div>`
    : `<div class="draft-empty">${icon("list-plus")}<strong>清单还是空的</strong><span>点击物品右上角的 +，或在物品详情中加入清单</span></div>`;
  const checkAll = $("#draft-check-all", panel);
  if (checkAll) checkAll.indeterminate = summary.selected > 0 && summary.selected < summary.editable;
  paint();
  const list = $(".draft-list", panel);
  if (list) list.scrollTop = scrollTop;
  const nextFocus = focusKey ? $(focusKey, panel) : null;
  nextFocus?.focus({ preventScroll: true });
  if (selection && nextFocus?.setSelectionRange) nextFocus.setSelectionRange(...selection);
  scheduleSpriteAnimation();
}

function draftTextureKey(item) {
  return JSON.stringify([textureAssetURL(item), state.icons[`${item.name}@${item.damage}`] || "", catalogAnimation(item)]);
}

function draftStockLabel(item) {
  if (!quantityKnown(item)) return "库存待查询";
  return `库存 ${formatCompactQuantity(item.quantity)}${item.current_session ? "" : " · 待同步"}`;
}

function patchDraftAppearance() {
  const entries = new Map(state.draft.entries.map(entry => [entry.id, entry]));
  for (const row of $$("#draft-panel .draft-entry")) {
    const entry = entries.get(row.dataset.draftId);
    if (!entry) continue;
    const item = draftDisplayItem(entry, state.itemCache);
    const media = $(".draft-entry-media", row);
    const key = draftTextureKey(item);
    if (media && media.dataset.textureKey !== key) { media.dataset.textureKey = key; media.innerHTML = itemImage(item); }
    const label = catalogDisplayName(item);
    const name = $(".draft-entry-name label", row);
    if (name) { name.textContent = label; name.title = label; }
    const stock = $(".draft-entry-stock", row);
    if (stock) { stock.textContent = draftStockLabel(item); stock.title = stockText(item); }
    $("[data-check]", row)?.setAttribute("aria-label", `选择${label}`);
    $("[data-quantity]", row)?.setAttribute("aria-label", `${label}合成数量`);
    $("[data-remove]", row)?.setAttribute("aria-label", `移除${label}草稿`);
  }
  paint();
  scheduleSpriteAnimation();
}

async function refreshDraftItems(force = false) {
  if (!state.network || !state.draft.entries.length) return;
  const context = state.context;
  if (draftItemsRequest?.context === context) return draftItemsRequest.promise;
  if (!force && Date.now() - draftItemsRefreshAt < 10000) { patchDraftAppearance(); return; }
  const ids = state.draft.entries.map(entry => entry.item.id);
  const work = { context, promise: null };
  const button = $("#refresh-draft-items");
  if (button) button.disabled = true;
  work.promise = lookupItems(ids).then(() => {
    if (context !== state.context) return;
    draftItemsRefreshAt = Date.now();
    patchDraftAppearance();
  }).catch(() => {
    if (force && context === state.context) toast("清单纹理与库存读取失败，已保留当前清单。", true);
  }).finally(() => {
    if (draftItemsRequest === work) draftItemsRequest = null;
    if (context === state.context) { const refresh = $("#refresh-draft-items"); if (refresh) refresh.disabled = false; }
  });
  draftItemsRequest = work;
  return work.promise;
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
    if (context === state.context) { toast(`${playerName(state.member)} 的生产任务已受理 · ${ids.length} 项`, false, "task"); await refreshJobs(); }
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
  const model = cpuMonitorModel(state.cpus.cpus || [], state.cpuMonitor);
  const observed = state.cpus.observed_at || state.network?.services?.cpu_observed_at;
  $("#cpu-summary").textContent = state.network
    ? `${model.total} 台已观测 · ${observed ? age(observed) : "未观测"}${state.cpus.truncated ? " · 列表不完整" : ""}`
    : "尚未连接网络";
  const overview = state.network ? cpuMonitorOverviewHTML(model, { observed, online: !!state.network?.online, truncated: state.cpus.truncated, age }) : "";
  $("#cpu-overview").innerHTML = overview;
  $("#cpu-monitor-count").textContent = model.total ? `${model.visible.length} / ${model.total} 台` : "";
  for (const [name, count] of Object.entries({ all: model.total, busy: model.busy, idle: model.idle, output: model.outputs })) {
    $(`#cpu-count-${name}`).textContent = count;
  }
  $$("[data-cpu-monitor-filter]").forEach(button => button.setAttribute("aria-pressed", String(button.dataset.cpuMonitorFilter === state.cpuMonitor.filter)));
  renderCPUList(model);
  renderDashboard();
  paint();
}

function renderCPUList(model = cpuMonitorModel(state.cpus.cpus || [], state.cpuMonitor)) {
  const list = $("#cpu-list"), scroll = list.scrollTop;
  const active = document.activeElement?.closest(".cpu-monitor-row");
  const activeKey = active && list.contains(active) ? active.dataset.cpuKey : null;
  list.innerHTML = cpuMonitorListHTML(model, { connected: !!state.network, supported: state.network?.capabilities?.cpu_selection === true, truncated: state.cpus.truncated, expanded: state.cpuMonitor.expanded });
  list.scrollTop = scroll;
  if (activeKey) $$(".cpu-monitor-row", list).find(row => row.dataset.cpuKey === activeKey)?.querySelector("summary")?.focus({ preventScroll: true });
  $("#cpu-monitor-count").textContent = model.total ? `${model.visible.length} / ${model.total} 台` : "";
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
  $("#cpu-choice-error").textContent = supported ? "只有游戏内设置了唯一名称的 CPU 才能严格指定；未命名 CPU 只能自动分配。" : "当前 OC 不支持严格指定 CPU，使用自动分配。";
  $("#cpu-options").innerHTML = `<button class="cpu-option" data-cpu-auto><span><strong>自动分配</strong></span>${icon("check")}</button>` + cpus.map((cpu, index) => {
    const selection = cpuSelectionState(cpu, cpus, supported);
    const valid = selection.selectable;
    const displayName = cpuDisplayName(cpu, index);
    const title = valid ? (cpu.busy ? "CPU 忙碌，可选择等待" : "指定此 CPU") : selection.reason;
    return `<div><button class="cpu-option" data-cpu-index="${index}" ${valid ? "" : "disabled"} title="${esc(title)}"><span><strong>${esc(displayName)}</strong><small>${formatQuantity(cpu.storage)} B · ${cpu.coprocessors} 协处理器 · 快照 ${esc(cpu.id || `${index + 1}`)}</small>${!valid ? `<em class="cpu-option-reason">${esc(selection.reason)}</em>` : ""}</span><span class="state-label ${cpu.busy ? "busy" : ""}">${cpu.busy ? "忙碌" : "空闲"}</span></button>${valid && cpu.busy ? `<button class="secondary wide" data-cpu-wait="${index}">${icon("clock")}等待此 CPU</button>` : ""}</div>`;
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
  const terms = state.crafting.orderSearch.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  const visible = orders.filter(order => terms.every(term => [itemFor(order.item_id)?.label, itemFor(order.item_id)?.name, order.id, order.owner_name, order.cpu_name, stateNames[order.state]].join(" ").toLocaleLowerCase().includes(term)));
  const view = $("#view-orders"), scroll = view.scrollTop;
  const groupsHTML = visible.length ? craftingOrdersHTML(visible, { group: state.orderGroup, itemFor, itemImage, age, dateTime, member:state.member })
    : '<div class="crafting-empty">' + icon("list-checks") + '<strong>' + (!snapshot ? state.ordersLoading ? "正在读取订单…" : state.ordersError ? "订单读取失败" : "等待订单观测" : orders.length ? "没有匹配的订单" : "当前筛选没有订单") + '</strong><span>搜索仅匹配已读取的订单；可调整条件或从左侧刷新。</span></div>';
  patchDashboardHTML("#order-list", groupsHTML);
  if (snapshot && state.crafting.orderSearch) $("#orders-summary").textContent += " · 匹配 " + visible.length + " 项";
  if (state.taskMode === "orders") {
    $("#crafting-overview").hidden = true;
    $("#crafting-status").textContent = "单项订单按提交批次或 CPU 分组 · 搜索仅匹配已读取记录";
  }
  view.scrollTop = scroll;
  renderDashboard();
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
  patchDraftAppearance();
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
  $("#order-facts").innerHTML = `<div class="detail-heading">${itemImage(item)}<h2>${esc(item?.label || `物品 #${order.item_id}`)}</h2></div><div class="order-detail-facts"><div class="fact"><span>数量</span><span class="number">${formatQuantity(order.quantity)}</span></div><div class="fact"><span>状态</span><span>${esc(stateNames[order.state] || order.state)}</span></div><div class="fact"><span>CPU 策略</span><span>${esc(order.cpu_name || "自动分配")}</span></div><div class="fact"><span>下单人</span><span>${esc(submitterName(order))}</span></div><div class="fact"><span>提交时间</span><span>${esc(dateTime(order.created_at))}</span></div><div class="fact"><span>过期时间</span><span>${esc(dateTime(order.expires_at))}</span></div></div>${order.reason ? `<p class="error">${esc(order.reason)}</p>` : ""}`;
  $("#order-actions").innerHTML = order.state === "queued" && (state.member?.admin || state.member?.id === order.user_id) ? `<div class="order-actions"><button class="secondary" data-cancel-detail="${esc(order.id)}">${icon("x")}撤回未派发任务</button></div>` : "";
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
  const feedback = $("#network-feedback");
  if (feedback) {
    const status = networkRefreshState({ loading: state.networkLoading, error: state.networkError, hasSnapshot: state.networkSnapshotReady });
    feedback.hidden = status.hidden || state.networkLoading && state.networkSnapshotReady;
    feedback.dataset.state = status.tone;
    feedback.innerHTML = status.hidden ? "" : `<span>${esc(status.text)}</span>${status.retry ? `<button type="button" class="secondary" data-network-retry><i data-lucide="refresh-cw"></i>重试</button>` : ""}`;
  }
  const refreshButton = $("#refresh-network");
  if (refreshButton) {
    refreshButton.disabled = state.networkLoading;
    refreshButton.setAttribute("aria-busy", String(state.networkLoading));
  }
  const addButton = $("#add-network");
  if (addButton) {
    addButton.disabled = state.networkCreateBusy;
    addButton.title = state.member?.admin ? "创建一个新的网络" : "仅管理员可创建网络";
  }
  const recovery = $("#network-recovery");
  if (recovery) {
    const status = networkRecoveryState({
      network: state.network,
      devices: state.devices,
      streamState: state.streamState,
      loading: state.networkLoading && !state.networkSnapshotReady,
      error: state.networkError,
      errorKind: state.networkErrorKind,
      hasSnapshot: state.networkSnapshotReady,
      formatTime: dateTime,
      formatAge: age,
    });
    recovery.hidden = status.key === "healthy" || status.key === "loading";
    recovery.dataset.state = status.key;
    recovery.innerHTML = networkRecoveryHTML(status);
  }
  $("#network-empty").hidden = !!network;
  $("#network-info").hidden = !network;
  if (!network) {
    $("#network-summary").textContent = "尚未连接网络";
    renderDashboard();
    paint();
    return;
  }
  $("#network-summary").textContent = network.name;
  const view = $("#view-network"), scroll = view.scrollTop;
  patchDashboardHTML("#network-overview", networkOverviewHTML(network, state.devices, state.streamState, { age }));
  renderNetworkNodes();
  const identifiersOpen = $(".network-identifiers", $("#network-sidebar"))?.open || false;
  patchDashboardHTML("#network-sidebar", networkSidebarHTML(network, state.streamState, { dateTime }));
  const identifiers = $(".network-identifiers", $("#network-sidebar"));
  if (identifiers) identifiers.open = identifiersOpen;
  renderDashboard();
  paint();
  view.scrollTop = scroll;
}

function renderNetworkNodes() {
  const model = networkNodeModel(state.devices, state.networkNodes);
  const view = $("#view-network"), scroll = view.scrollTop;
  const focusedRow = document.activeElement?.closest(".network-node");
  const focusedKey = focusedRow?.dataset.nodeKey;
  $("#network-nodes-count").textContent = model.visible.length === model.counts.all
    ? model.counts.all + " 台节点"
    : "显示 " + model.visible.length + " / " + model.counts.all + " 台";
  for (const key of ["all", "online", "attention"]) $("#network-nodes-" + key).textContent = model.counts[key];
  $$("[data-network-node-filter]").forEach(button => button.setAttribute("aria-pressed", String(button.dataset.networkNodeFilter === state.networkNodes.filter)));
  patchDashboardHTML("#network-nodes-list", networkNodesHTML(model, { expanded: state.networkNodes.expanded, age, dateTime }));
  if (focusedKey) {
    const row = $$(".network-node", $("#network-nodes-list")).find(node => node.dataset.nodeKey === focusedKey);
    if (row) $("summary", row)?.focus({ preventScroll: true });
  }
  paint();
  view.scrollTop = scroll;
}

function networkRecoveryHTML(status) {
  const action = status.action === "refresh"
    ? `<button type="button" class="secondary" data-network-recovery="refresh"><i data-lucide="refresh-cw"></i>${esc(status.actionLabel)}</button>`
    : status.action === "reconnect"
      ? `<button type="button" class="secondary" data-network-recovery="reconnect"><i data-lucide="radio-tower"></i>${esc(status.actionLabel)}</button>`
      : status.action === "settings"
        ? `<button type="button" class="secondary" data-network-recovery="settings"><i data-lucide="settings"></i>${esc(status.actionLabel)}</button>`
        : "";
  return `<div class="network-recovery-copy"><strong>${esc(status.label)}</strong><span>${esc(status.detail)}</span></div>${action}`;
}

async function refreshNetwork() {
  if (!state.network || state.networkLoading) return;
  const context = state.context;
  state.networkLoading = true;
  state.networkError = "";
  state.networkErrorKind = "";
  refreshHeader();
  renderNetwork();
  try {
    const [value, devices] = await Promise.all([api.request(networkPath()), api.request(networkPath("/devices"))]);
    if (context !== state.context) return;
    const onlineChanged = state.network.online !== value.online;
    state.network = value;
    state.devices = devices;
    state.networkSnapshotReady = true;
    refreshHeader();
    renderNetwork();
    if (onlineChanged && !$("#draft-panel").contains(document.activeElement)) renderDraft();
    if (onlineChanged && !$("#detail-panel").contains(document.activeElement)) renderDetail();
  } catch (error) {
    if (context !== state.context) return;
    state.networkError = errorText(error) || "网络状态读取失败，请稍后重试。";
    state.networkErrorKind = networkErrorKind(error);
    refreshHeader();
    if (state.view === "network") toast(state.networkError, true, "network");
  } finally {
    if (context === state.context) {
      state.networkLoading = false;
      renderNetwork();
      refreshHeader();
      renderConnectionDiagnostics();
    }
  }
}

function resetNetworkDialog() {
  const form = $("#network-form");
  if (form) form.reset();
  const error = $("#network-form-error");
  if (error) { error.textContent = ""; error.hidden = true; }
  const result = $("#network-create-result");
  if (result) result.hidden = true;
  const token = $("#network-device-token");
  if (token) token.textContent = "";
  const submit = $("#create-network");
  if (submit) { submit.disabled = false; submit.removeAttribute("aria-busy"); submit.innerHTML = `${icon("plus")}创建网络`; }
  paint();
}

function openNetworkDialog() {
  if (!state.member) { showLogin(); return; }
  if (!state.member?.admin) { toast("当前账号没有创建网络权限。", true, "network"); return; }
  resetNetworkDialog();
  $("#network-dialog").showModal();
  $("#network-name")?.focus();
}

async function createNetwork(event) {
  event.preventDefault();
  if (state.networkCreateBusy) return;
  const form = new FormData(event.currentTarget);
  const draft = normalizeNetworkDraft({
    name: form.get("name"), server_group: form.get("server_group"), source: form.get("source"),
  });
  const error = $("#network-form-error");
  if (!draft.ok) {
    error.textContent = draft.error;
    error.hidden = false;
    return;
  }
  const submit = $("#create-network");
  state.networkCreateBusy = true;
  error.textContent = "";
  error.hidden = true;
  submit.disabled = true;
  submit.setAttribute("aria-busy", "true");
  $$("[data-close]", $("#network-dialog")).forEach(button => { button.disabled = true; });
  submit.innerHTML = `${icon("refresh-cw")}正在创建…`;
  paint();
  try {
    const result = networkCreateResult(await api.request("/api/v1/networks", { method: "POST", body: draft.value }));
    if (!result.network) throw new Error("网络创建响应无效。");
    state.networks = [...state.networks.filter(network => network.id !== result.network.id), result.network];
    $("#network").innerHTML = state.networks.map(network => `<option value="${esc(network.id)}">${esc(network.name)}</option>`).join("");
    $("#network").value = result.network.id;
    await switchNetwork(result.network.id);
    const token = $("#network-device-token");
    token.textContent = result.deviceToken || "后端未返回设备令牌，请检查服务日志。";
    $("#network-create-result").hidden = false;
    submit.disabled = true;
    submit.removeAttribute("aria-busy");
    submit.innerHTML = `${icon("check")}已创建`;
    toast("网络已创建，设备令牌仅显示一次。", false, "network");
    paint();
  } catch (requestError) {
    error.textContent = errorText(requestError) || "网络创建失败，请稍后重试。";
    error.hidden = false;
    submit.disabled = false;
    submit.removeAttribute("aria-busy");
    submit.innerHTML = `${icon("plus")}创建网络`;
    paint();
  } finally {
    state.networkCreateBusy = false;
    $$("[data-close]", $("#network-dialog")).forEach(button => { button.disabled = false; });
    renderNetwork();
  }
}

function inventoryPageLimit() {
  const fallback = 50;
  if (matchMedia("(max-width: 900px)").matches) return fallback;
  const wrap = $("#view-inventory .table-wrap");
  if (!wrap || wrap.clientWidth < 100 || wrap.clientHeight < 100) return state.inventoryLimit || fallback;
  const empty = $("#inventory-empty");
  const wasEmptyVisible = empty && !empty.hidden;
  if (wasEmptyVisible) empty.hidden = true;
  const width = wrap.clientWidth;
  const height = wrap.clientHeight;
  if (wasEmptyVisible) empty.hidden = false;
  const gridStyle = getComputedStyle($("#inventory-rows"));
  const gap = parseFloat(gridStyle.gap) || 8;
  const cardWidth = parseFloat(gridStyle.getPropertyValue("--inventory-card-min")) || 76;
  const cardHeight = parseFloat(gridStyle.getPropertyValue("--inventory-card-height")) || 84;
  const tableStyle = getComputedStyle($("#view-inventory .inventory-table"));
  const horizontalPadding = parseFloat(tableStyle.paddingLeft) + parseFloat(tableStyle.paddingRight);
  const verticalPadding = parseFloat(tableStyle.paddingTop) + parseFloat(tableStyle.paddingBottom);
  const columns = Math.max(1, Math.floor((width - horizontalPadding + gap) / (cardWidth + gap)));
  const rows = Math.max(1, Math.floor((height - verticalPadding + gap) / (cardHeight + gap)));
  wrap.style.setProperty("--inventory-rows", String(rows));
  return Math.min(200, columns * rows);
}

function refreshInventoryCapacity() {
  const nextLimit = inventoryPageLimit();
  if (nextLimit === state.inventoryLimit) return false;
  if (state.network && state.view === "inventory") {
    if (!state.inventoryLoading) void loadInventory(false, { page: state.page, cursor: "", anchor: inventoryWindowAnchor(), limit: nextLimit });
  } else state.inventoryLimit = nextLimit;
  return true;
}

function inventoryWindowAnchor() {
  return state.page > 0 || state.inventoryWindowAnchored ? state.anchorCursor : "";
}

function inventoryQuerySpec(limit) {
  return { search: state.search, sort: state.sort, filter: state.filter, limit, view: state.stockView, scope: state.inventoryScope };
}

function inventoryPageKey(cursor, spec) {
  return JSON.stringify([spec.search, spec.sort, spec.filter, spec.limit, spec.view, spec.scope, cursor, spec.anchor || "", spec.page || ""]);
}

function cachedInventoryPage(cursor, spec, fresh = false) {
  const key = inventoryPageKey(cursor, spec);
  const cached = inventoryPageCache.get(key);
  if (!cached) return null;
  if (Date.now() - cached.cachedAt > INVENTORY_PAGE_CACHE_TTL) {
    inventoryPageCache.delete(key);
    return null;
  }
  if (fresh && Date.now() - cached.cachedAt > INVENTORY_PAGE_FRESH_TTL) return null;
  inventoryPageCache.delete(key);
  inventoryPageCache.set(key, cached);
  return cached;
}

function cacheInventoryPage(cursor, spec, page) {
  const key = inventoryPageKey(cursor, spec);
  inventoryPageCache.delete(key);
  inventoryPageCache.set(key, { ...page, cachedAt: Date.now() });
  while (inventoryPageCache.size > INVENTORY_PAGE_CACHE_LIMIT) inventoryPageCache.delete(inventoryPageCache.keys().next().value);
  let items = [...inventoryPageCache.values()].reduce((sum, page) => sum + page.items.length, 0);
  while (items > INVENTORY_PAGE_CACHE_ITEMS && inventoryPageCache.size > 1) {
    const first = inventoryPageCache.keys().next().value;
    items -= inventoryPageCache.get(first).items.length;
    inventoryPageCache.delete(first);
  }
}

function invalidateInventoryPageNumbers(total, pageCount) {
  for (const [key, page] of inventoryPageCache) {
    if (JSON.parse(key)[8]) { page.cachedAt = Math.min(page.cachedAt, Date.now() - INVENTORY_PAGE_FRESH_TTL - 1); page.pageCount = pageCount; page.total = total; }
  }
}

function clearInventoryPages() {
  inventoryCacheEpoch++;
  for (const controller of inventoryPrefetchControllers) controller.abort();
  inventoryPrefetchControllers.clear();
  inventoryPageCache.clear(); inventoryPageRequests.clear();
  inventoryPrefetchJob = null; inventoryPrefetchTarget = null;
  clearTimeout(inventoryVisibleTimer); clearTimeout(inventoryImageTimer);
  cancelAnimationFrame(inventoryNavigationFrame); inventoryNavigationFrame = 0;
}

function requestInventoryPage(cursor, limit, signal, spec = inventoryQuerySpec(limit), fresh = false) {
  const key = inventoryPageKey(cursor, spec);
  const cached = !fresh && cachedInventoryPage(cursor, spec);
  if (cached) return Promise.resolve(cached);
  const pending = inventoryPageRequests.get(key);
  if (pending && !fresh && !pending.signal?.aborted) return pending.promise;
  const epoch = inventoryCacheEpoch;
  const query = new URLSearchParams({ q: spec.search, sort: spec.sort, limit: String(spec.limit), consistency: "window", pagination: "pages" });
  if (spec.filter) query.set("craftable", spec.filter);
  if (spec.scope === "nesql_plain") query.set("view", spec.view);
  if (cursor) query.set("cursor", cursor);
  if (spec.anchor) query.set("anchor", spec.anchor);
  if (spec.page) query.set("page", String(spec.page));
  const request = api.request(networkPath(`/items?${query}`), signal ? { signal } : undefined)
    .then(value => {
      const page = { items: value.items || [], nextCursor: value.next_cursor || "", anchorCursor: value.anchor_cursor || "", scope: value.scope || "", pageNumber: value.page_number, pageCount: value.page_count, total: value.total };
      if (epoch === inventoryCacheEpoch && !signal?.aborted) cacheInventoryPage(cursor, spec, page);
      return page;
    })
    .finally(() => {
      if (inventoryPageRequests.get(key)?.promise === request) inventoryPageRequests.delete(key);
    });
  inventoryPageRequests.set(key, { promise: request, signal });
  return request;
}

function prefetchInventoryPages(pageIndex, generation, limit) {
  const spec = inventoryQuerySpec(limit);
  inventoryPrefetchTarget = { pageIndex, spec, direction: inventoryDirection };
  if (inventoryPrefetchJob) return inventoryPrefetchJob.promise;
  const controller = new AbortController();
  const job = { controller, epoch: inventoryCacheEpoch, promise: null };
  inventoryPrefetchJob = job;
  inventoryPrefetchControllers.add(controller);
  job.promise = (async () => {
    try {
      while (!controller.signal.aborted && job.epoch === inventoryCacheEpoch) {
        const target = inventoryPrefetchTarget;
        if (!target || state.inventoryLoading || state.inventoryQueryPending) break;
        const count = state.inventoryPageCount;
        const depth = Math.min(count - 1, INVENTORY_PREFETCH_DEPTH, Math.floor(INVENTORY_PAGE_CACHE_ITEMS / target.spec.limit / 2));
        const candidates = [];
        for (let n = 1; n <= depth; n++) {
          candidates.push({ index: (target.pageIndex + target.direction * n + count) % count, direction: target.direction });
          if (n <= 4) candidates.push({ index: (target.pageIndex - target.direction * n + count) % count, direction: -target.direction });
        }
        const missing = candidates.find(value => !cachedInventoryPage("", { ...target.spec, page: value.index + 1 }, true));
        if (missing == null) break;
        const batch = Math.max(1, Math.min(Math.floor(200 / target.spec.limit), depth, missing.direction < 0 ? missing.index + 1 : count - missing.index));
        const start = missing.direction < 0 ? Math.max(0, missing.index - batch + 1) : missing.index;
        const value = await requestInventoryBlock(start, batch, target.spec, controller.signal, job.epoch);
        if (job.epoch !== inventoryCacheEpoch || controller.signal.aborted) break;
        if (value.total != null && state.inventoryTotal != null && value.total !== state.inventoryTotal) break;
      }
    } catch {} finally {
      inventoryPrefetchControllers.delete(controller);
      if (inventoryPrefetchJob === job) inventoryPrefetchJob = null;
      scheduleInventoryImageWarmup();
    }
  })();
  return job.promise;
}

async function requestInventoryBlock(start, pages, spec, signal, epoch) {
  const query = new URLSearchParams({ q: spec.search, sort: spec.sort, limit: String(Math.min(200, pages * spec.limit)), page_size: String(spec.limit), consistency: "window", pagination: "pages", page: String(start + 1) });
  if (spec.filter) query.set("craftable", spec.filter);
  if (spec.scope === "nesql_plain") query.set("view", spec.view);
  const value = await api.request(networkPath(`/items?${query}`), { signal });
  if (epoch !== inventoryCacheEpoch || signal.aborted) return value;
  if (value.total != null && state.inventoryTotal != null && value.total !== state.inventoryTotal) invalidateInventoryPageNumbers(value.total, value.page_count);
  for (let offset = 0; offset < value.items.length; offset += spec.limit) {
    const index = value.page_number - 1 + offset / spec.limit;
    const page = { items: value.items.slice(offset, offset + spec.limit), anchorCursor: value.page_anchors?.[offset / spec.limit] || "", nextCursor: index < value.page_count - 1 ? value.next_cursor || "more" : "", scope: value.scope, pageNumber: index + 1, pageCount: value.page_count, total: value.total };
    cacheInventoryPage("", { ...spec, page: index + 1 }, page);
  }
  return value;
}

function scheduleInventoryImageWarmup() {
  clearTimeout(inventoryImageTimer);
  inventoryImageTimer = setTimeout(() => {
    const spec = inventoryQuerySpec(state.inventoryLimit);
    for (const distance of [1, -1, 2, 3]) {
      const index = (state.page + inventoryDirection * distance + state.inventoryPageCount) % state.inventoryPageCount;
      const page = cachedInventoryPage("", { ...spec, page: index + 1 });
      for (const item of page?.items || []) {
        const url = textureAssetURL(item);
        if (!url || inventoryImages.has(url)) continue;
        const img = new Image(); img.decoding = "async"; img.src = url;
        inventoryImages.set(url, img);
        while (inventoryImages.size > 320) inventoryImages.delete(inventoryImages.keys().next().value);
      }
    }
  }, 120);
}

async function loadInventory(reset = false, target = null) {
  if (!state.network) return;
  const limit = target?.limit || inventoryPageLimit();
  const destination = reset ? { page: 0, cursor: "", anchor: "" }
    : target || { page: state.page, cursor: "", anchor: inventoryWindowAnchor() };
  if (reset) {
    state.inventoryDesiredPage = null;
    state.inventoryQueryPending = true;
    clearInventoryPages();
    inventoryWheel.reset();
  }
  inventoryLiveRequest?.abort(); inventoryLiveRequest = null;
  queryController?.abort();
  queryController = new AbortController();
  const generation = ++state.generation;
  const cursor = destination.cursor;
  const spec = { ...inventoryQuerySpec(limit), anchor: destination.anchor || "", page: destination.absolute ? destination.page + 1 : "" };
  state.inventoryLoading = true;
  state.inventoryTarget = destination;
  renderInventoryPager();
  if (!state.items.length) renderInventory();
  try {
    const value = await requestInventoryPage(cursor, limit, queryController.signal, spec);
    if (generation !== state.generation) return;
    if (reset) { state.cursors = [{ cursor: "", anchor: "" }]; state.inventoryQueryPending = false; }
    if (limit !== state.inventoryLimit) clearInventoryPages();
    state.inventoryLimit = limit;
    state.inventoryDesiredPage = null;
    applyInventoryPage(destination, value);
  } catch (error) {
    if (generation !== state.generation || error.name === "AbortError") return;
    inventoryWheel.reset();
    state.inventoryDesiredPage = null;
    message("#inventory-message", errorText(error));
  } finally {
    if (generation === state.generation) {
      state.inventoryLoading = false;
      state.inventoryTarget = null;
      renderInventoryPager();
      if (!state.items.length) renderInventory();
      if (inventoryPageLimit() !== limit && inventoryPageLimit() !== state.inventoryLimit) refreshInventoryCapacity();
    }
  }
}

function applyInventoryPage(destination, page) {
  const navigated = destination.page !== state.page;
  const hadItemFocus = $("#inventory-rows").contains(document.activeElement);
  const focusedItem = document.activeElement?.dataset?.item || document.activeElement?.dataset?.quickAdd;
  const focusKind = document.activeElement?.dataset?.quickAdd ? "data-quick-add" : "data-item";
  const windowX = window.scrollX, windowY = window.scrollY;
  if (navigated) hideInventoryHover();
  if (page.total != null && state.inventoryTotal != null && page.total !== state.inventoryTotal) invalidateInventoryPageNumbers(page.total, page.pageCount);
  state.page = page.pageNumber ? page.pageNumber - 1 : destination.page;
  state.inventoryPageCount = page.pageCount || state.inventoryPageCount;
  state.inventoryTotal = page.total ?? state.inventoryTotal;
  state.cursor = destination.cursor;
  state.items = page.items;
  state.anchorCursor = page.anchorCursor;
  state.inventoryWindowAnchored = Boolean(destination.anchor);
  state.nextCursor = page.nextCursor;
  state.inventoryScope = page.scope;
  state.cursors[state.page] = { cursor: state.cursor, anchor: inventoryWindowAnchor() };
  state.cursors.length = state.page + 1;
  state.items.forEach(item => state.itemCache.set(String(item.id), item));
  patchDraftAppearance();
  const selected = state.itemCache.get(String(state.selected?.id));
  if (selected) state.selected = selected;
  cacheInventoryPage(state.cursor, { ...inventoryQuerySpec(state.inventoryLimit), anchor: destination.anchor || "", page: destination.absolute ? state.page + 1 : "" }, page);
  cacheInventoryPage("", { ...inventoryQuerySpec(state.inventoryLimit), anchor: inventoryWindowAnchor() }, page);
  if (destination.absolute || !destination.anchor) cacheInventoryPage("", { ...inventoryQuerySpec(state.inventoryLimit), page: state.page + 1 }, page);
  state.inventoryLoading = false;
  state.inventoryTarget = null;
  message("#inventory-message", "");
  $("#stock-view-label").hidden = state.inventoryScope !== "nesql_plain";
  renderInventory();
  if (!navigated) {
    if (window.scrollY !== windowY) window.scrollTo(windowX, windowY);
    if (focusedItem) $(`[${focusKind}="${CSS.escape(focusedItem)}"]`)?.focus({ preventScroll: true });
  }
  if (navigated) {
    const wrap = $("#view-inventory .table-wrap");
    wrap.scrollTop = 0;
    if (matchMedia("(max-width: 900px)").matches) wrap.scrollIntoView({ block: "start" });
    if (hadItemFocus) $("[data-item]", wrap)?.focus({ preventScroll: true });
  }
  if (state.selected && state.inspector === "detail") patchSelectedDetail(state.selected);
  void prefetchInventoryPages(state.page, state.generation, state.inventoryLimit);
  scheduleInventoryImageWarmup();
  clearTimeout(inventoryVisibleTimer);
  inventoryVisibleTimer = setTimeout(() => { if (!state.inventoryLoading) { void patchVisibleInventory(); void refreshInventoryWindow(); } }, 180);
}

function changeInventoryPage(direction) {
  if (!state.network || state.inventoryQueryPending || (direction !== 0 && state.inventoryPageCount <= 1)) return false;
  const current = state.inventoryDesiredPage ?? state.inventoryTarget?.page ?? state.page;
  const next = direction === 0 ? 0 : (current + direction + state.inventoryPageCount) % state.inventoryPageCount;
  state.inventoryDesiredPage = next;
  if (direction) inventoryDirection = direction;
  if (cachedInventoryPage("", { ...inventoryQuerySpec(state.inventoryLimit), page: next + 1 })) {
    cancelAnimationFrame(inventoryNavigationFrame); inventoryNavigationFrame = 0;
    navigateInventoryPage(next);
  } else {
    if (!inventoryNavigationFrame) inventoryNavigationFrame = requestAnimationFrame(() => {
      inventoryNavigationFrame = 0;
      if (state.inventoryDesiredPage != null) navigateInventoryPage(state.inventoryDesiredPage);
    });
  }
  return true;
}

function navigateInventoryPage(pageIndex) {
  const destination = { page: pageIndex, cursor: "", anchor: "", absolute: true };
  const limit = inventoryPageLimit();
  const cached = cachedInventoryPage("", { ...inventoryQuerySpec(limit), page: pageIndex + 1 });
  if (cached) {
    state.generation++;
    queryController?.abort();
    inventoryLiveRequest?.abort(); inventoryLiveRequest = null;
    state.inventoryLimit = limit;
    state.inventoryDesiredPage = null;
    applyInventoryPage(destination, cached);
    return;
  }
  void loadInventory(false, { ...destination, limit });
}

function inventoryWheelDelta(event) {
  const raw = Math.abs(event.deltaX) > Math.abs(event.deltaY) ? event.deltaX : event.deltaY;
  if (!raw) return 0;
  const multiplier = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? window.innerHeight : 1;
  return raw * multiplier;
}

async function patchVisibleInventory() {
  if (!state.network || !state.items.length || state.inventoryLoading || inventoryLiveRequest) return;
  const generation = state.generation, context = state.context;
  try {
    const value = await lookupItems(state.items.map(i => i.id).concat(state.selected?.id || []));
    if (!value || generation !== state.generation || context !== state.context) return;
    const previousSelected = state.selected;
    const live = new Map(value.items.map(i => [String(i.id), i]));
    state.items = state.items.map(item => live.get(String(item.id)) || item);
    if (state.inventoryScope === "nesql_plain" && state.stockView === "stock") {
      state.items = state.items.filter(item => quantityKnown(item) && BigInt(item.quantity) > 0n);
    }
    if (state.selected && live.has(String(state.selected.id))) state.selected = live.get(String(state.selected.id));
    // Quantity changes do not reorder the list while the user is choosing items.
    const focused = document.activeElement;
    const focusKind = focused?.dataset?.quickAdd ? "data-quick-add" : "data-item";
    const focusItem = focused?.dataset?.item || focused?.dataset?.quickAdd;
    if (!patchInventoryRows()) renderInventory();
    if (focusItem) $(`[${focusKind}="${CSS.escape(focusItem)}"]`)?.focus({ preventScroll: true });
    if (state.inspector === "detail" && state.selected) {
      if (detailNeedsRender(previousSelected, state.selected)) renderDetail();
      else patchSelectedDetail(state.selected);
    }
  } catch {}
}

let inventoryStatsRequest = null;
let inventoryStatsAt = 0, inventoryStatsContext = -1;
async function refreshInventoryStats() {
  if (!state.network) return;
  const context = state.context;
  if (inventoryStatsRequest?.context === context) return inventoryStatsRequest.promise;
  if (inventoryStatsContext === context && Date.now() - inventoryStatsAt < 5000) return;
  const promise = api.request(networkPath("/inventory-stats")).then(value => {
    if (context !== state.context) return;
    state.inventoryStats = value;
    const now = Date.now();
    const key = trendStorageKey(state.member.id, state.network.id, value.scope || "observed");
    if (key !== state.inventoryTrendKey) {
      state.inventoryTrendKey = key;
      state.inventoryTrend = loadTrendHistory(localStorage, key, now);
    }
    state.inventoryTrend = recordTrendSample(state.inventoryTrend, value.total_quantity, now);
    saveTrendHistory(localStorage, key, state.inventoryTrend);
    inventoryStatsAt = Date.now(); inventoryStatsContext = context;
    state.inventoryScope = value.scope || "";
    refreshHeader();
  }).catch(() => {}).finally(() => { if (inventoryStatsRequest?.promise === promise) inventoryStatsRequest = null; });
  inventoryStatsRequest = { context, promise };
  return promise;
}

let catalogListRefreshAt = 0, inventoryRefreshTimer = null;
async function refreshInventoryWindow(force = false) {
  if (!state.network || state.inventoryLoading || state.inventoryQueryPending || inventoryLiveRequest) return;
  const context = state.context, generation = state.generation;
  const spec = { ...inventoryQuerySpec(state.inventoryLimit), anchor: inventoryWindowAnchor() };
  const cursor = spec.anchor ? "" : state.cursor;
  if (force) clearInventoryPages();
  const controller = new AbortController();
  inventoryLiveRequest = controller;
  try {
    const value = await requestInventoryPage(cursor, spec.limit, controller.signal, spec, true);
    if (context !== state.context || generation !== state.generation) return;
    // A selected item can leave the stock filter while its detail stays open.
    const selectedID = state.selected?.id;
    if (selectedID && !value.items.some(item => String(item.id) === String(selectedID))) {
      try { await lookupItems([selectedID]); } catch {}
      if (context !== state.context || generation !== state.generation) return;
    }
    const previousSelected = state.selected;
    const focused = document.activeElement;
    const focusKind = focused?.dataset?.quickAdd ? "data-quick-add" : "data-item";
    const focusItem = focused?.dataset?.item || focused?.dataset?.quickAdd;
    const scroll = $(".table-wrap");
    const scrollTop = scroll?.scrollTop || 0;
    const windowX=window.scrollX, windowY=window.scrollY;
    state.items = value.items; state.nextCursor = value.nextCursor;
    if (value.total != null && state.inventoryTotal != null && value.total !== state.inventoryTotal) {
      invalidateInventoryPageNumbers(value.total, value.pageCount);
    }
    state.inventoryPageCount = value.pageCount || state.inventoryPageCount;
    state.inventoryTotal = value.total ?? state.inventoryTotal;
    state.page = Math.min(state.page, state.inventoryPageCount - 1);
    state.anchorCursor = value.anchorCursor || state.anchorCursor;
    state.cursors[state.page] = { cursor: state.cursor, anchor: inventoryWindowAnchor() };
    cacheInventoryPage("", { ...inventoryQuerySpec(state.inventoryLimit), anchor: inventoryWindowAnchor() }, value);
    state.items.forEach(item => state.itemCache.set(String(item.id), item));
    patchDraftAppearance();
    const selected = state.items.find(item => String(item.id) === String(state.selected?.id)) || state.itemCache.get(String(state.selected?.id));
    if (selected) state.selected = selected;
    if (!patchInventoryRows()) renderInventory();
    if (scroll) scroll.scrollTop = scrollTop;
    if (window.scrollY!==windowY) window.scrollTo(windowX,windowY);
    renderInventoryPager();
    if (focusItem) $(`[${focusKind}="${CSS.escape(focusItem)}"]`)?.focus({ preventScroll: true });
    if (state.inspector === "detail" && state.selected) {
      if (detailNeedsRender(previousSelected, state.selected)) renderDetail();
      else patchSelectedDetail(state.selected);
    }
    message("#inventory-message", "");
    if (!spec.anchor) cacheInventoryPage("", { ...inventoryQuerySpec(state.inventoryLimit), page: state.page + 1 }, value);
    void prefetchInventoryPages(state.page, generation, spec.limit);
  } catch (error) {
    if (context === state.context && generation === state.generation && error.name !== "AbortError") message("#inventory-message", errorText(error));
  } finally {
    if (inventoryLiveRequest === controller) inventoryLiveRequest = null;
  }
}
function refreshInventoryObservation() {
  void refreshInventoryStats();
  if (state.view === "inventory" && state.inspector === "draft") void refreshDraftItems();
  if (Date.now() - catalogListRefreshAt >= 5000 && !state.inventoryLoading) {
    clearTimeout(inventoryRefreshTimer); inventoryRefreshTimer = null;
    catalogListRefreshAt = Date.now();
    void refreshInventoryWindow();
  } else {
    void patchVisibleInventory();
    if (!inventoryRefreshTimer) {
      const context = state.context;
      inventoryRefreshTimer = setTimeout(() => {
        inventoryRefreshTimer = null;
        if (context === state.context) refreshInventoryObservation();
      }, Math.max(500, 5000 - (Date.now() - catalogListRefreshAt)));
    }
  }
}

function connectStream() {
  state.stream?.close();
  clearInterval(streamTimer);
  clearTimeout(streamRefreshTimer);
  streamRefreshTimer = null;
  state.streamConnected = false;
  state.streamState = "reconnecting";
  refreshHeader();
  const network = state.network?.id;
  const context = state.context, pending = new Set();
  if (!network) {
    state.streamState = "closed";
    renderConnectionDiagnostics();
    return;
  }
  const stream = new EventSource(networkPath("/events"));
  state.stream = stream;
  stream.onopen = () => { if (context === state.context) { state.streamConnected = true; state.streamState = "connected"; refreshHeader(); if (state.view === "network") renderNetwork(); refreshJobs(); } };
  stream.onerror = () => {
    if (context !== state.context) return;
    state.streamConnected = false;
    state.streamState = state.settings.reconnect ? "reconnecting" : "closed";
    if (!state.settings.reconnect) stream.close();
    refreshHeader();
    if (state.view === "network") renderNetwork();
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
      if (reset || kinds.some(kind => kind.includes("inventory") || kind.includes("item") || kind.includes("recipe"))) refreshInventoryObservation();
      if (reset) refreshNetwork();
    }, 400);
  };
  stream.addEventListener("change", update);
  stream.addEventListener("reset", update);
  streamTimer = setInterval(() => {
    if (document.visibilityState !== "visible" || !state.network) return;
    refreshNetwork();
    refreshInventoryObservation(); refreshOrders(false); refreshCPUs(); refreshJobs();
    if(state.view==='history') void inventoryHistory.observe();
  }, 15000);
}

async function switchNetwork(id) {
  if (state.busy || state.preparing) { $("#network").value = state.network?.id || ""; toast("请等待本轮提交结束。"); return; }
  clearTimeout(historySearchTimer);clearTimeout(historyWheelTimer);inventoryHistory.reset();
  state.context++;
  draftItemsRequest = null; draftItemsRefreshAt = 0;
  clearTimeout(inventoryRefreshTimer); inventoryRefreshTimer = null;
  inventoryLiveRequest?.abort(); inventoryLiveRequest = null;
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
  state.streamState = "closed";
  cpuCallback = null;
  for (const dialog of ["cpu-dialog", "order-dialog", "job-dialog"]) if ($(`#${dialog}`).open) $(`#${dialog}`).close();
  state.network = network || null;
  state.inventoryStats = null;
  state.inventoryTrend = [];
  state.inventoryTrendKey = "";
  state.inventoryScope = "";
  state.stockView = "stock";
  $("#stock-view").value = "stock";
  state.itemCache.clear();
  state.selected = null;
  state.items = [];
  state.nextCursor = "";
  state.page = 0; state.cursor = ""; state.anchorCursor = ""; state.inventoryWindowAnchored = false;
  state.inventoryPageCount = 1; state.inventoryTotal = null; state.inventoryDesiredPage = null;
  state.cursors = [{ cursor: "", anchor: "" }]; state.inventoryTarget = null; state.inventoryQueryPending = false;
  clearInventoryPages(); inventoryWheel.reset();
  state.inventoryLoading = false;
  state.orders = [];
  state.orderSnapshot = null;
  state.crafting = { search: "", sort: "newest", owner: state.member?.admin ? "all" : "mine", orderSearch: "" };
  $("#crafting-search").value = "";
  $("#crafting-order-search").value = "";
  $("#crafting-sort").value = "newest";
  $("#crafting-owner").value = state.crafting.owner;
  state.ordersLoading = false;
  state.ordersError = false;
  state.activeOrderCount = 0;
  state.activeOrdersTruncated = false;
  state.devices = [];
  state.networkNodes = { search: "", filter: "all", role: "all", expanded: new Set() };
  $("#network-nodes-search").value = "";
  $("#network-nodes-role").value = "all";
  state.networkLoading = false;
  state.networkError = "";
  state.networkErrorKind = "";
  state.networkSnapshotReady = false;
  state.cpus = { cpus: [] };
  state.cpuMonitor = { search: "", filter: "all", sort: "network", expanded: new Set() };
  $("#cpu-monitor-search").value = "";
  $("#cpu-monitor-sort").value = "network";
  state.amount = "1";
  state.cpuName = "";
  state.waitForCPU = false;
  state.unchecked.clear();
  state.draftKey = network ? storageKey(state.member.id, network.id) : "";
  state.draft = network ? readLocalDraft() : { version: 1, entries: [], receipts: {} };
  state.draftBlocked = !!state.draft.warning;
  jobs.reset(network ? { memberId: state.member.id, networkId: network.id } : null);
  for (const id of ["#inventory-message", "#orders-message", "#network-feedback"]) message(id, "");
  refreshHeader(); renderInventory(); renderDetail(); renderDraft(); renderOrders(); renderCPUs(); renderNetwork();
  if (!network) return;
  try { localStorage.setItem("ae-orbit:last-network", network.id); } catch {}
  connectStream();
  const context = state.context;
  await Promise.all([loadInventory(true), refreshInventoryStats(), refreshOrders(false), refreshCPUs(), refreshNetwork(), refreshJobs(), refreshDraftItems(true)]);
  if (context !== state.context) return;
  if(state.view==='history')void inventoryHistory.load();
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

$("#auth-dialog").addEventListener("click", event => {
  const mode = event.target.closest("[data-login-mode]")?.dataset.loginMode;
  if (!mode || $("#login-button").disabled) return;
  state.loginMode = mode;
  $("#player-login-fields").hidden = mode === "token";
  $("#token-login-fields").hidden = mode !== "token";
  $("#player-register-fields").hidden = mode !== "register";
  $("#token").required = mode === "token";
  for (const id of ["#login-game-id", "#login-password"]) { $(id).required = mode !== "token"; $(id).disabled = mode === "token"; }
  for (const id of ["#login-confirm-password", "#login-invitation"]) { $(id).required = mode === "register"; $(id).disabled = mode !== "register"; }
  $("#login-password").autocomplete = mode === "register" ? "new-password" : "current-password";
  $("#login-password").minLength = mode === "register" ? 10 : 1;
  $("#login-error").textContent = "";
  $("#login-button").innerHTML = `${icon("log-in")}${mode === "register" ? "注册并登录" : "登录"}`;
  $$("[data-login-mode]").forEach(button => button.setAttribute("aria-pressed", String(button.dataset.loginMode === mode)));
  paint();
});

async function refreshPlayerTeam() {
  if (!state.member?.admin) return;
  const memberID = state.member.id;
  try {
    const [members, invitations] = await Promise.all([api.request("/api/v1/members"),api.request("/api/v1/player-invitations")]);
    if (state.member?.id !== memberID) return;
    state.teamMembers = members; state.teamInvitations = invitations;
    if (state.settingsSection === "account") renderSettings();
  } catch (error) { toast(errorText(error),true); }
}

$("#settings-detail").addEventListener("submit", async event => {
  const form = event.target;
  if (!["player-profile-form","player-invite-form","player-reset-form"].includes(form.id)) return;
  event.preventDefault();
  if (state.busy || state.preparing) { toast("请等待当前提交结束。",true); return; }
  const button = $("button[type=submit]",form); button.disabled = true;
  const errorTarget = $(".error",form); if (errorTarget) errorTarget.textContent = "";
  try {
    if (form.id === "player-profile-form") {
      const value = await api.request("/api/v1/me/player",{method:"PUT",body:{game_id:$("#profile-game-id").value.trim(),current_password:$("#profile-current-password")?.value || "",password:$("#profile-password").value}});
      await acceptSession(value.member,value.csrf,value.expires_at); renderSettings(); toast("玩家身份已保存，旧订单署名保留。");
    } else if (form.id === "player-invite-form") {
      const value = await api.request("/api/v1/player-invitations",{method:"POST",body:{network_id:$("#invite-network").value,can_craft:$("#invite-permission").value === "craft",max_uses:Number($("#invite-uses").value)}});
      state.teamInvitations.unshift(value.invitation);
      $("#player-invite-code").textContent = value.code; $("#player-invite-result").hidden = false;
    } else {
      const id = $("#player-reset-member").value;
      const member = await api.request(`/api/v1/members/${encodeURIComponent(id)}/player`,{method:"PUT",body:{game_id:$("#player-reset-game-id").value.trim(),password:$("#player-reset-password").value}});
      $("#player-reset-password").value = "";
      if (member.id === state.member.id) { showLogin(); toast("凭据已更新，请重新登录。"); }
      else { await refreshPlayerTeam(); toast("成员身份与密码已重设。"); }
    }
  } catch (error) { if (errorTarget) errorTarget.textContent = errorText(error); }
  finally { if (button.isConnected) button.disabled = false; }
});

$("#settings-detail").addEventListener("click", async event => {
  const edit = event.target.closest("[data-edit-player]");
  if (edit) {
    const member = state.teamMembers.find(m=>m.id===edit.dataset.editPlayer); if (!member) return;
    $("#player-reset-form").hidden=false; $("#player-reset-member").value=member.id;
    $("#player-reset-name").textContent=`重设 ${playerName(member)}`;
    $("#player-reset-game-id").value=member.game_id || ""; $("#player-reset-password").value="";
    $("#player-reset-game-id").focus(); return;
  }
  if (event.target.closest("#copy-player-invite")) {
    const code = $("#player-invite-code");
    try {
      if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(code.textContent);
      else {
        const range = document.createRange(); range.selectNodeContents(code);
        const selection = window.getSelection(); selection.removeAllRanges(); selection.addRange(range);
        if (!document.execCommand("copy")) throw new Error("copy unavailable");
        selection.removeAllRanges();
      }
      toast("邀请码已复制");
    } catch {
      const range = document.createRange(); range.selectNodeContents(code);
      const selection = window.getSelection(); selection.removeAllRanges(); selection.addRange(range);
      toast("已选中邀请码，请手动复制。",true);
    }
    return;
  }
  const invite = event.target.closest("[data-revoke-invite]");
  const member = event.target.closest("[data-revoke-player]");
  if (!invite && !member) return;
  if (member && !confirm("停用此成员？他的历史订单保留，未继续派发的任务将按权限变化暂停。")) return;
  try {
    await api.request(invite ? `/api/v1/player-invitations/${encodeURIComponent(invite.dataset.revokeInvite)}` : `/api/v1/members/${encodeURIComponent(member.dataset.revokePlayer)}`,{method:"DELETE"});
    await refreshPlayerTeam(); toast(invite ? "邀请码已撤销" : "成员已停用");
  } catch (error) { toast(errorText(error),true); }
});

async function acceptSession(member, csrf, expiresAt = "", persist = true) {
  await switchNetwork("");
  const context = state.context;
  state.member = member;
  api.setMember(member.id);
  api.setCSRF(csrf);
  state.session = { member: member.id, csrf, expiresAt };
  if (persist) {
    try { saveSession(localStorage, sessionStorage, state.session, state.settings.rememberSession); }
    catch { toast("会话无法保存，请检查浏览器存储。", true); }
  }
  for (const id of ["#token","#login-password","#login-confirm-password","#login-invitation"]) $(id).value = "";
  state.teamMembers=[]; state.teamInvitations=[];
  if (!member.game_id) toast("可在设置 → 玩家与团队绑定游戏 ID；管理员可生成团队邀请码。");
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
    const request = loginPayload(state.loginMode, {token:$("#token").value,gameID:$("#login-game-id").value,password:$("#login-password").value,confirmPassword:$("#login-confirm-password").value,invitation:$("#login-invitation").value});
    const value = await api.request(request.path, { method: "POST", body: request.body, login: true });
    await acceptSession(value.member, value.csrf, value.expires_at);
  } catch (error) { $("#login-error").textContent = error?.message || "登录未完成，请核对信息。"; }
  finally { $("#login-button").disabled = false; }
});
$("#auth-dialog").addEventListener("cancel", event => event.preventDefault());
$("#logout").addEventListener("click", async () => {
  if (state.busy || state.preparing) { toast("请等待本轮提交结束。"); return; }
  try {
    await api.request("/api/v1/session", { method: "DELETE" });
    state.stream?.close(); clearInterval(streamTimer); clearTimeout(streamRefreshTimer);
    api.setCSRF(""); api.setMember(""); state.member = null; state.networks = [];
    clearSession(localStorage, sessionStorage);
    state.session = null;
    $("#network").innerHTML = `<option value="">未连接</option>`;
    await switchNetwork("");
    showLogin();
  } catch (error) { toast(errorText(error), true); }
});
$("#network").addEventListener("change", event => switchNetwork(event.target.value));
$("#notifications").addEventListener("click", () => { refreshHeader(); renderCPUs(); renderNetwork(); $("#sync-dialog").showModal(); });
$("#refresh-dashboard").addEventListener("click", () => {
  renderDashboard();
  if (state.network) { refreshNetwork(); refreshInventoryObservation(); refreshCPUs(); refreshJobs(); }
});
$("#search").addEventListener("input", event => {
  clearTimeout(searchTimer);
  if (event.isComposing) return;
  searchTimer = setTimeout(() => { state.search = event.target.value.trim(); loadInventory(true); }, 220);
});
$("#search").addEventListener("compositionend", event => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => { state.search = event.target.value.trim(); loadInventory(true); }, 220);
});
$("#sort").addEventListener("change", event => { state.sort = event.target.value; loadInventory(true); });
$("#stock-view").addEventListener("change", event => { state.stockView = event.target.value; loadInventory(true); });
$("#refresh-inventory").addEventListener("click", () => { void (state.inventoryQueryPending ? loadInventory(true) : refreshInventoryWindow(true)); void refreshInventoryStats(); });
$("#refresh-history").addEventListener('click',()=>{if(state.network)void inventoryHistory.refresh()});
$("#history-search").addEventListener('input',event=>{clearTimeout(historySearchTimer);if(event.isComposing)return;$("#history-search-results").hidden=false;event.target.setAttribute('aria-expanded','true');historySearchTimer=setTimeout(()=>inventoryHistory.search(event.target.value.trim()),220)});
$("#history-search").addEventListener('compositionend',event=>{clearTimeout(historySearchTimer);historySearchTimer=setTimeout(()=>inventoryHistory.search(event.target.value.trim()),220)});
$("#history-search").addEventListener('focus',()=>{if(state.network){$("#history-search-results").hidden=false;$("#history-search").setAttribute('aria-expanded','true');void inventoryHistory.search($("#history-search").value.trim())}});
$("#history-search").addEventListener('keydown',event=>{if(event.key==='Escape'){ $("#history-search-results").hidden=true;event.target.setAttribute('aria-expanded','false') }if(event.key==='ArrowDown'){$('button',$("#history-search-results"))?.focus();event.preventDefault()}});
$("#history-epoch").addEventListener('change',event=>{inventoryHistory.state.epoch=event.target.value;inventoryHistory.state.item='';inventoryHistory.state.from='';inventoryHistory.state.range='all';void inventoryHistory.load()});
$("#history-metric").addEventListener('change',event=>{inventoryHistory.state.metric=event.target.value;renderInventoryHistory()});
for(const id of ['kind','direction'])$(`#history-${id}`).addEventListener('change',event=>{inventoryHistory.state[id]=event.target.value;void inventoryHistory.page({first:true})});
$("#history-prev").addEventListener('click',()=>inventoryHistory.previous());
$("#history-next").addEventListener('click',()=>void inventoryHistory.next());
$("#history-new-records").addEventListener('click',()=>void inventoryHistory.page({first:true}));
$("#history-all-items").addEventListener('click',()=>{ $("#history-search").value='';void inventoryHistory.selectItem('') });
$("#history-apply-dates").addEventListener('click',()=>{const from=Date.parse($("#history-from").value),to=Date.parse($("#history-to").value);if(!Number.isFinite(from)||!Number.isFinite(to)||to<=from){toast('请选择有效的起止时间。',true);return}void inventoryHistory.setWindow(from,to)});
$("#history-expand").addEventListener('click',()=>{historyExpanded=!historyExpanded;$("#history-expand").textContent=historyExpanded?'还原图表':'放大图表';renderInventoryHistory()});
const historyPosition=event=>{const svg=$("#history-svg");if(!svg)return null;const box=svg.getBoundingClientRect();const x=Math.max(0,Math.min(1,(event.clientX-box.left)/box.width));const s=inventoryHistory.state.series;return {x:x*1000,time:Date.parse(s.from)+(Date.parse(s.to)-Date.parse(s.from))*x}};
$("#history-chart").addEventListener('pointermove',event=>{const pos=historyPosition(event);if(!pos)return;const h=inventoryHistory.state,model=historyChartModel(h.series,h.metric);if(!model)return;if(historyGesture){const brush=$("#history-brush");brush.setAttribute('x',Math.min(pos.x,historyGesture.x));brush.setAttribute('width',Math.abs(pos.x-historyGesture.x));return}const closest=model.points.reduce((a,b)=>Math.abs(b.time-pos.time)<Math.abs(a.time-pos.time)?b:a);const tip=$("#history-chart-tooltip");tip.hidden=false;tip.textContent=`${historyTime(closest.at)} · ${formatQuantity(closest[h.metric])} ${h.metric==='quantity'?'件':'种'}${h.series.basis==='last_known_quantity'?' · 最后已知值':!closest.all_online?' · 含离线观测':''}`;const cross=$("#history-crosshair");cross.hidden=false;cross.setAttribute('d',`M${closest.x.toFixed(2)} 0 V260`)});
$("#history-chart").addEventListener('pointerleave',()=>{if($("#history-chart-tooltip"))$("#history-chart-tooltip").hidden=true;if($("#history-crosshair"))$("#history-crosshair").hidden=true});
$("#history-chart").addEventListener('pointerdown',event=>{if(event.button!==0||event.pointerType==='touch')return;historyGesture=historyPosition(event);if(historyGesture){$("#history-chart").setPointerCapture(event.pointerId);event.preventDefault()}});
$("#history-chart").addEventListener('pointerup',event=>{const start=historyGesture;historyGesture=null;const end=historyPosition(event);if(start&&end&&Math.abs(start.x-end.x)>8)void inventoryHistory.setWindow(Math.min(start.time,end.time),Math.max(start.time,end.time));else if($("#history-brush"))$("#history-brush").setAttribute('width','0')});
$("#history-chart").addEventListener('pointercancel',()=>{historyGesture=null});
$("#history-chart").addEventListener('dblclick',()=>void inventoryHistory.setRange('all'));
$("#history-chart").addEventListener('wheel',event=>{const pos=historyPosition(event);if(!pos)return;event.preventDefault();clearTimeout(historyWheelTimer);const s=inventoryHistory.state.series,from=Date.parse(s.from),to=Date.parse(s.to),factor=event.deltaY>0?1.5:0.67;const start=Math.max(Date.parse(s.started_at)||from,pos.time-(pos.time-from)*factor),end=Math.min(Date.now(),pos.time+(to-pos.time)*factor);historyWheelTimer=setTimeout(()=>void inventoryHistory.setWindow(start,end),100)},{passive:false});
$("#first-page").addEventListener("click", () => { inventoryWheel.reset(); changeInventoryPage(0); });
$("#prev-page").addEventListener("click", () => { inventoryWheel.reset(); changeInventoryPage(-1); });
$("#next-page").addEventListener("click", () => { inventoryWheel.reset(); changeInventoryPage(1); });
$("#view-inventory .catalog").addEventListener("wheel", event => {
  if (matchMedia("(max-width: 900px)").matches || state.view !== "inventory") return;
  if (event.ctrlKey || event.target.closest("input, select, textarea")) return;
  const delta = inventoryWheelDelta(event);
  if (!delta) return;
  event.preventDefault();
  const direction = inventoryWheel.consume(delta, performance.now());
  if (direction) changeInventoryPage(direction);
}, { passive: false });
document.addEventListener("keydown", event => {
  if (state.view !== "inventory" || $$("dialog[open]").length) return;
  const direction = inventoryKeyboardDirection(event);
  if (direction === null) return;
  event.preventDefault(); inventoryWheel.reset(); changeInventoryPage(direction);
});
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
$("#crafting-search").addEventListener("input", event => { state.crafting.search = event.target.value; renderJobs(); });
$("#crafting-order-search").addEventListener("input", event => { state.crafting.orderSearch = event.target.value; renderOrders(); });
$("#crafting-sort").addEventListener("change", event => { state.crafting.sort = event.target.value; renderJobs(); });
$("#crafting-owner").addEventListener("change", event => { state.crafting.owner = event.target.value; renderJobs(); });
$("#view-orders").addEventListener("click", event => {
  const button = event.target.closest("button");
  if (!button) return;
  if (button.dataset.taskMode) {
    $("#task-mode").value = button.dataset.taskMode;
    $("#task-mode").dispatchEvent(new Event("change"));
  } else if ("craftingNew" in button.dataset) {
    showView("inventory");
    $("#search").focus();
  } else if ("craftingSearchReset" in button.dataset) {
    state.crafting.search = ""; state.crafting.owner = "all";
    $("#crafting-search").value = ""; $("#crafting-owner").value = "all";
    renderJobs();
  }
});
$("#job-history-more").addEventListener("click", () => jobs.history());
$("#refresh-job-detail").addEventListener("click", () => refreshJobDetail());
$("#job-dialog").addEventListener("close", () => jobs.closeDetail());
$("#job-dialog").addEventListener("cancel", () => jobs.closeDetail());
$("#refresh-order-detail").addEventListener("click", () => refreshOrderDetail());
$("#order-dialog").addEventListener("cancel", () => invalidateOrderDetail());
$("#order-dialog").addEventListener("close", () => { if (!$("#order-dialog").open) invalidateOrderDetail(); });
$("#refresh-cpus").addEventListener("click", () => refreshCPUs());
$("#cpu-monitor-search").addEventListener("input", event => {
  state.cpuMonitor.search = event.target.value;
  $("#cpu-list").scrollTop = 0;
  renderCPUList();
});
$("#cpu-monitor-sort").addEventListener("change", event => {
  state.cpuMonitor.sort = event.target.value;
  $("#cpu-list").scrollTop = 0;
  renderCPUList();
});
$("#cpu-list").addEventListener("toggle", event => {
  const row = event.target;
  if (!row.matches?.(".cpu-monitor-row") || !row.isConnected) return;
  if (row.open) state.cpuMonitor.expanded.add(row.dataset.cpuKey);
  else state.cpuMonitor.expanded.delete(row.dataset.cpuKey);
}, true);
$("#view-cpus").addEventListener("click", event => {
  const button = event.target.closest("button");
  if (!button) return;
  if (button.dataset.cpuMonitorFilter) state.cpuMonitor.filter = button.dataset.cpuMonitorFilter;
  else if ("cpuMonitorReset" in button.dataset) {
    state.cpuMonitor.search = "";
    state.cpuMonitor.filter = "all";
    $("#cpu-monitor-search").value = "";
  } else return;
  $("#cpu-list").scrollTop = 0;
  renderCPUs();
});
$("#refresh-network").addEventListener("click", () => refreshNetwork());
$("#network-nodes-search").addEventListener("input", () => {
  state.networkNodes.search = $("#network-nodes-search").value;
  renderNetworkNodes();
});
$("#network-nodes-role").addEventListener("change", event => {
  state.networkNodes.role = event.target.value;
  renderNetworkNodes();
});
$("#network-nodes-list").addEventListener("toggle", event => {
  const row = event.target;
  if (!row.matches?.(".network-node") || !row.isConnected) return;
  if (row.open) state.networkNodes.expanded.add(row.dataset.nodeKey);
  else state.networkNodes.expanded.delete(row.dataset.nodeKey);
}, true);
$("#view-network").addEventListener("click", async event => {
  const button = event.target.closest("button");
  if (!button) return;
  if (button.dataset.networkNodeFilter) {
    state.networkNodes.filter = button.dataset.networkNodeFilter;
    renderNetworkNodes();
  } else if ("networkNodesReset" in button.dataset) {
    state.networkNodes.search = "";
    state.networkNodes.filter = "all";
    state.networkNodes.role = "all";
    $("#network-nodes-search").value = "";
    $("#network-nodes-role").value = "all";
    renderNetworkNodes();
  } else if ("copyNetworkId" in button.dataset && state.network) {
    try { await navigator.clipboard.writeText(state.network.id); toast("网络 ID 已复制"); }
    catch { toast("请手动选择并复制网络 ID。", true); }
  }
});
$("#add-network").addEventListener("click", () => openNetworkDialog());
$("#network-form").addEventListener("submit", createNetwork);
$("#network-dialog").addEventListener("cancel", event => { if (state.networkCreateBusy) event.preventDefault(); });
$("#network-dialog").addEventListener("close", () => resetNetworkDialog());
$("#copy-network-token").addEventListener("click", async event => {
  const token = $("#network-device-token")?.textContent || "";
  if (!token || token.startsWith("后端未返回")) return;
  try {
    await navigator.clipboard.writeText(token);
    event.currentTarget.innerHTML = `${icon("check")}已复制`;
    toast("设备令牌已复制，请立即保存。", false, "network");
    paint();
  } catch {
    toast("浏览器拒绝访问剪贴板，请手动复制设备令牌。", true, "network");
  }
});
$("#order-group").addEventListener("change", event => { state.orderGroup = event.target.value; renderOrders(); });
$("#settings-detail").addEventListener("change", async event => {
  const input = event.target;
  if (input.dataset.setting === "inventoryLayout") updateSettings({ inventoryLayout: input.value });
  if (input.dataset.setting === "textureSource") {
    updateSettings({ textureSource: input.value });
    if (input.value === "url" && state.settings.texturePackURL) {
      applyTexturePackURL(state.settings.texturePackURL, false).catch(() => {});
    } else if (input.value === "local") {
      try {
        const bytes = await loadCachedTexturePack();
        if (!bytes) throw new Error("未找到已保存的本地纹理包。");
        await texturePack.loadArchive(bytes, "本地纹理包");
        state.texturePackError = "";
        refreshTexturePackState();
        renderSettings();
      } catch (error) {
        state.texturePackError = error?.message || "本地纹理包无法读取。";
        renderSettings();
      }
    }
  }
  if (input.dataset.setting && input.type === "checkbox") updateSettings({ [input.dataset.setting]: input.checked });
});
$("#settings-detail").addEventListener("click", async event => {
  const textureAction = event.target.closest("[data-texture-pack-action]")?.dataset.texturePackAction;
  if (textureAction === "load-url") {
    const url = $("#texture-pack-url")?.value.trim() || "";
    try {
      await applyTexturePackURL(url);
    } catch {}
    return;
  }
  if (textureAction === "load-server") {
    try {
      await applyTexturePackURL("/catalog-assets/texture-pack.zip");
    } catch {}
    return;
  }
  if (textureAction === "choose-local") {
    $("#texture-pack-file")?.click();
    return;
  }
  if (textureAction === "clear") {
    texturePack.clear();
    clearCachedTexturePack().catch(() => {});
    state.texturePackError = "";
    state.settings = saveSettings(localStorage, { ...state.settings, textureSource: "server", texturePackURL: "" });
    refreshTexturePackState();
    renderSettings();
    toast("已清除本地纹理包");
    return;
  }
  const actionButton = event.target.closest("[data-session-action]");
  const sessionAction = actionButton?.dataset.sessionAction;
  if (sessionAction === "test") {
    actionButton.disabled = true;
    actionButton.setAttribute("aria-busy", "true");
    state.connectionTest = { status: "checking", checkedAt: new Date().toISOString(), message: "" };
    renderConnectionDiagnostics();
    try {
      const member = await api.request("/api/v1/me");
      if (!member?.id) throw new Error("missing member");
      state.member = member;
      state.connectionTest = { status: "ok", checkedAt: new Date().toISOString(), message: "" };
      toast("连接正常");
      renderSettings();
    } catch (error) {
      const message = errorText(error) || "连接失败，请检查后端和当前会话。";
      state.connectionTest = { status: "error", checkedAt: new Date().toISOString(), message };
      toast(message, true, "network");
      renderSettings();
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
$("#texture-pack-file").addEventListener("change", async event => {
  try {
    await applyTexturePackFile(event.target.files?.[0]);
  } finally {
    event.target.value = "";
  }
});
$("#settings-sections").addEventListener("click", event => {
  const button = event.target.closest("[data-settings-section]");
  if (!button) return;
  state.settingsSection = button.dataset.settingsSection;
  if (state.settingsSection === "account") refreshPlayerTeam();
  $("#settings-sections").querySelectorAll("[data-settings-section]").forEach(item => {
    item.setAttribute("aria-selected", String(item === button));
    item.setAttribute("aria-current", String(item === button));
  });
  renderSettings();
});

document.addEventListener("mouseover", event => {
  const button = event.target.closest(".inventory-table .item-button");
  if (!button || (event.relatedTarget instanceof Node && button.contains(event.relatedTarget))) return;
  showInventoryHover(button, event);
});
document.addEventListener("mousemove", event => {
  const button = event.target.closest(".inventory-table .item-button");
  if (!button) return;
  if (inventoryHover.button !== button) { showInventoryHover(button, event); return; }
  inventoryHover.pointer = { x: event.clientX, y: event.clientY };
  if (!inventoryHover.frame) inventoryHover.frame = requestAnimationFrame(positionInventoryHover);
});
document.addEventListener("mouseout", event => {
  const button = event.target.closest(".inventory-table .item-button");
  if (button && !(event.relatedTarget instanceof Node && button.contains(event.relatedTarget))) hideInventoryHover();
});
document.addEventListener("keydown", event => {
  if (!inventoryHover.button || $$("dialog[open]").length) return;
  if (event.key === "Shift") {
    if (!inventoryHover.advanced) { inventoryHover.advanced = true; refreshInventoryHover(); }
    return;
  }
  if (event.target.closest("input,textarea,select,[contenteditable]")) return;
  if (event.key === "Escape") hideInventoryHover();
  else if (event.code === "KeyZ" && !event.ctrlKey && !event.metaKey && !event.altKey && inventoryHover.pages.length > 1) {
    event.preventDefault();
    inventoryHover.page = (inventoryHover.page + 1) % inventoryHover.pages.length;
    renderInventoryHoverPage();
  }
});
document.addEventListener("keyup", event => {
  if (event.key === "Shift" && inventoryHover.button && inventoryHover.advanced) {
    inventoryHover.advanced = false; refreshInventoryHover();
  }
});
window.addEventListener("blur", hideInventoryHover);
document.addEventListener("scroll", hideInventoryHover, true);
document.addEventListener("click", async event => {
  const button = event.target.closest("button");
  if (!button || button.disabled) return;
  if (!button.closest(".inventory-table .item-button")) hideInventoryHover();
  const data = button.dataset;
  if (button.id === "refresh-draft-items") { void refreshDraftItems(true); return; }
  if (button.id === "sidebar-toggle") {
    updateSettings({ sidebarCollapsed: !state.settings.sidebarCollapsed }, false);
    return;
  }
  if (data.networkRecovery) {
    if (data.networkRecovery === "refresh") refreshNetwork();
    if (data.networkRecovery === "reconnect") { connectStream(); refreshNetwork(); }
    if (data.networkRecovery === "settings") showView("settings");
    return;
  }
  if (data.networkRetry !== undefined) { refreshNetwork(); return; }
  if (!button.closest('.history-search-wrap')) { $("#history-search-results").hidden=true;$("#history-search").setAttribute('aria-expanded','false'); }
  if (data.historyMode) { inventoryHistory.state.mode=data.historyMode;renderInventoryHistory();return; }
  if (data.historyRange) { if(state.network)void inventoryHistory.setRange(data.historyRange);return; }
  if (data.historyItem) { $("#history-search-results").hidden=true;$("#history-search").setAttribute('aria-expanded','false');void inventoryHistory.selectItem(data.historyItem);return; }
  if (data.historyPan) { const s=inventoryHistory.state.series;if(!s)return;const from=Date.parse(s.from),to=Date.parse(s.to),shift=(to-from)*0.5*Number(data.historyPan);void inventoryHistory.setWindow(Math.max(Date.parse(s.started_at)||from,from+shift),Math.min(Date.now(),to+shift));return; }
  if (data.trendHours) {
    state.dashboardTrendHours = Number(data.trendHours);
    $$("[data-trend-hours]").forEach(row => row.setAttribute("aria-pressed", String(row === button)));
    renderDashboard();
    return;
  }
  if (data.view) showView(data.view);
  if ("filter" in data) {
    state.filter = data.filter;
    $$("[data-filter]").forEach(b => b.setAttribute("aria-pressed", String(b === button)));
    loadInventory(true);
  }
  if (data.item) {
    hideInventoryHover();
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
    if (state.jobFilter === "history" && !jobs.state.historyLoaded) void jobs.history();
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
  placeGlobalControls();
  hideInventoryHover();
  const sheet = matchMedia("(max-width:800px)").matches && $("#inspector").dataset.open === "true";
  $("#sheet-backdrop").hidden = !sheet;
  document.body.classList.toggle("sheet-open", sheet);
  clearTimeout(inventoryResizeTimer);
  inventoryResizeTimer = setTimeout(() => refreshInventoryCapacity(), 120);
});
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState !== "visible") hideInventoryHover();
  if (document.visibilityState === "visible" && state.member && state.network) { refreshNetwork(); refreshInventoryObservation(); refreshOrders(false); refreshJobs(); }
});
document.addEventListener("error", event => {
  if (event.target instanceof HTMLImageElement && event.target.classList.contains("item-image")) {
    const fallback = document.createElement("span"); fallback.className = "missing-image"; fallback.title = "图标不可用";
    event.target.closest(".item-button")?.classList.add("missing-texture");
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
  if (state.settings.textureSource === "url" && state.settings.texturePackURL) {
    try { await applyTexturePackURL(state.settings.texturePackURL, false); } catch {}
  } else if (state.settings.textureSource === "local") {
    try {
      const bytes = await loadCachedTexturePack();
      if (bytes) {
        await texturePack.loadArchive(bytes, "本地纹理包");
        refreshTexturePackState();
      } else {
        state.texturePackError = "未找到已保存的本地纹理包，已回退到服务器纹理。";
        state.settings = saveSettings(localStorage, { ...state.settings, textureSource: "server" });
        renderSettings();
      }
    } catch {
      state.texturePackError = "本地纹理包无法读取，已回退到服务器纹理。";
      state.settings = saveSettings(localStorage, { ...state.settings, textureSource: "server" });
      texturePack.clear();
      renderSettings();
    }
  }
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
