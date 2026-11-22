import { deviceState, parseHealth, roleLabel } from "./device-status.mjs";
import { formatQuantity, networkQualityLabel } from "./model.mjs";
import { networkCapabilityRows, networkDashboardSummary } from "./network-ui.mjs";
import { streamStateText, streamStateTone } from "./connection-diagnostics.mjs";

const esc = value => String(value ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const icon = name => `<i data-lucide="${name}" aria-hidden="true"></i>`;
const informationalCodes = new Set(["native_scan_disabled"]);
const diagnosticNames = { identity_mismatch: "物品身份不匹配", low_memory: "可用内存不足", native_scan_disabled: "已关闭整网枚举", exact_read_failed: "精确读取失败" };
const capabilityIcons = { inventory: "boxes", recipes: "file-text", craft: "list-checks", cpu_output: "cpu", cpu_selection: "sliders-horizontal" };

export function deviceMemoryText(value) {
  const raw = String(value ?? "");
  if (!/^\d+$/.test(raw)) return "未上报";
  const bytes = BigInt(raw);
  if (bytes < 1024n) return `${bytes} B`;
  const unit = bytes >= 1048576n ? 1048576n : 1024n;
  const tenths = (bytes * 10n + unit / 2n) / unit;
  return `${tenths / 10n}${tenths % 10n ? `.${tenths % 10n}` : ""} ${unit === 1024n ? "KiB" : "MiB"}`;
}

export function networkNodeModel(devices = [], { search = "", filter = "all", role = "all" } = {}, now = Date.now()) {
  const rows = (Array.isArray(devices) ? devices : []).map(device => {
    const status = deviceState(device, now);
    const health = parseHealth(device.health);
    const diagnostics = Array.isArray(health.diagnostics) ? health.diagnostics.filter(value => value && typeof value === "object") : [];
    const failures = Array.isArray(health.read_failures) ? health.read_failures.filter(value => value && typeof value === "object") : [];
    const notices = diagnostics.filter(value => !informationalCodes.has(value.code));
    const attention = status.key !== "online" || notices.length > 0 || failures.length > 0;
    const capabilities = device.capabilities || {};
    return { device, key: String(device.id), status, health, diagnostics, failures, notices, attention,
      exact: capabilities.native_scan_disabled === true && capabilities.events_disabled === true };
  });
  const counts = { all: rows.length, online: rows.filter(row => row.status.key === "online").length, attention: rows.filter(row => row.attention).length };
  const terms = String(search).trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  const visible = rows.filter(row => {
    if (filter === "online" && row.status.key !== "online" || filter === "attention" && !row.attention) return false;
    if (role !== "all" && row.device.role !== role) return false;
    const haystack = [row.device.name, row.device.id, row.device.role, roleLabel(row.device.role), row.device.session_id].join(" ").toLocaleLowerCase();
    return terms.every(term => haystack.includes(term));
  });
  // Keep the OC roles grouped without reordering nodes on every heartbeat.
  const rank = role => role === "primary" ? 0 : role === "quantity_worker" ? 1 : 2;
  visible.sort((a, b) => rank(a.device.role) - rank(b.device.role));
  return { rows, visible, counts };
}

export function networkOverviewHTML(network, devices, streamState, { age = value => value || "未观测" } = {}, now = Date.now()) {
  const dashboard = networkDashboardSummary(network, devices, now);
  const nodes = networkNodeModel(devices, {}, now);
  const health = !network.online ? ["网络离线", "warn"] : !nodes.rows.length ? ["等待 OC 接入", "idle"] : nodes.counts.attention ? ["有节点需要关注", "warn"] : ["运行正常", "ok"];
  const bridge = network.bridge_mode === "cooperative" ? "多 OC 协作" : network.bridge_mode === "single" ? "单 OC" : "桥接模式未上报";
  const readers = network.services?.quantity_readers;
  const metrics = [
    ["在线节点", String(dashboard.online), `/ ${dashboard.total}`, dashboard.stale ? `${dashboard.stale} 台通信滞后` : dashboard.offline ? `${dashboard.offline} 台离线或已撤销` : dashboard.total ? "OC 心跳正常" : "等待首次设备上报", dashboard.total && !dashboard.offline && !dashboard.stale ? "ok" : "idle"],
    ["数量读取器", readers == null ? "—" : formatQuantity(readers), "", "参与库存精确数量查询", ""],
    ["需要关注", String(nodes.counts.attention), "台", nodes.counts.attention ? "可在下方筛选并查看详情" : dashboard.total ? "没有节点告警" : "等待节点上报", nodes.counts.attention ? "warn" : ""],
    ["网页实时连接", streamState === "connected" ? "已连接" : streamState === "reconnecting" ? "重连中" : "已关闭", "", "浏览器与后端的事件流", streamStateTone(streamState)],
  ];
  return `<div class="network-overview-heading"><div><div class="network-name-line"><h2>${esc(network.name)}</h2><span class="network-state" data-tone="${health[1]}"><i class="network-state-dot" aria-hidden="true"></i>${health[0]}</span></div><p class="network-context"><span>${esc(network.server_group || "服务组未上报")}</span><span>${network.source === "simulator" ? "模拟器" : "OpenComputers"}</span><span>${esc(bridge)}</span></p></div><div class="network-last-contact"><span>最近通信</span><strong title="${esc(dashboard.lastCommunication)}">${esc(age(dashboard.lastCommunication))}</strong></div></div><div class="network-overview-metrics" aria-label="网络状态摘要">${metrics.map(([label, value, unit, detail, tone]) => `<div class="network-overview-metric" data-tone="${tone}"><span>${label}</span><strong>${esc(value)}${unit ? `<small>${unit}</small>` : ""}</strong><p>${esc(detail)}</p></div>`).join("")}</div>`;
}

function diagnosticHTML(diagnostics, failures) {
  if (!diagnostics.length && !failures.length) return `<p class="network-node-note">当前快照没有诊断记录。</p>`;
  const values = [
    ...diagnostics.map(value => `<li${informationalCodes.has(value.code) ? ' data-tone="info"' : ""}><strong title="${esc(value.code)}">${esc(diagnosticNames[value.code] || value.code || "诊断记录")}</strong><span>${value.count != null ? `${esc(value.count)} 次累计` : "已上报"}</span></li>`),
    ...failures.map(value => `<li><strong title="${esc(value.code)}">${esc(diagnosticNames[value.code] || value.code || "读取失败")}</strong><span>${esc([value.item_id && `物品 ${value.item_id}`, value.stage, value.source].filter(Boolean).join(" · ") || "读取异常样本")}</span></li>`),
  ];
  return `<ul class="network-node-diagnostics">${values.join("")}</ul>`;
}

export function networkNodesHTML(model, { expanded = new Set(), age = value => value || "未观测", dateTime = value => value || "未观测" } = {}) {
  if (!model.rows.length) return `<div class="network-nodes-empty">${icon("radio-tower")}<strong>等待 OC 节点接入</strong><span>设备首次上报心跳后，会出现在这里。</span></div>`;
  if (!model.visible.length) return `<div class="network-nodes-empty">${icon("search")}<strong>没有匹配的节点</strong><span>试试设备名称、ID，或调整筛选条件。</span><button class="secondary" type="button" data-network-nodes-reset>清除筛选</button></div>`;
  return `<div class="network-node-columns" aria-hidden="true"><span>OC 节点</span><span>连接状态</span><span>库存同步</span><span>可用内存</span><span></span></div>${model.visible.map(row => {
    const { device, status, health } = row;
    const tone = status.key === "online" ? "ok" : status.key === "stale" ? "warn" : "idle";
    const alerts = row.notices.length + row.failures.length;
    const protection = row.exact ? "精确查询 · 整网枚举和物品订阅已关闭" : "查询保护未完整上报";
    return `<details class="network-node" data-node-key="${esc(row.key)}" data-state="${status.key}"${expanded.has(row.key) ? " open" : ""}>
      <summary aria-label="${esc(device.name || device.id)}，${esc(roleLabel(device.role))}，${status.label}，展开节点详情">
        <span class="network-node-identity">${icon(device.role === "primary" ? "radio-tower" : "boxes")}<span><strong>${esc(device.name || device.id)}</strong><small>${esc(roleLabel(device.role))}${alerts ? `<b>${alerts} 项提醒</b>` : ""}</small></span></span>
        <span class="network-node-state"><span class="network-state" data-tone="${tone}"><i class="network-state-dot" aria-hidden="true"></i>${status.label}</span><small title="${esc(dateTime(device.last_seen))}">${esc(age(device.last_seen))}</small></span>
        <span class="network-node-observation" title="${esc(dateTime(device.last_inventory))}"><span class="network-node-mobile-label">库存同步</span>${esc(age(device.last_inventory))}</span>
        <span class="network-node-memory"><span class="network-node-mobile-label">可用内存</span>${esc(deviceMemoryText(health.free_memory))}</span><span class="network-node-chevron">${icon("chevron-down")}</span>
      </summary><div class="network-node-details"><p class="network-node-protection" data-confirmed="${row.exact}">${icon(row.exact ? "check" : "circle-help")}${protection}${row.exact ? `<small>${status.key === "online" ? "当前节点上报" : "上次节点上报"}</small>` : ""}</p><dl><div><dt>设备 ID</dt><dd>${esc(device.id)}</dd></div><div><dt>当前会话</dt><dd>${esc(device.session_id || "未建立")}</dd></div><div><dt>最近心跳</dt><dd>${esc(dateTime(device.last_seen))}</dd></div><div><dt>最近库存观测</dt><dd>${esc(dateTime(device.last_inventory))}</dd></div></dl>${diagnosticHTML(row.diagnostics, row.failures)}</div>
    </details>`;
  }).join("")}`;
}

export function networkSidebarHTML(network, streamState, { dateTime = value => value || "未观测" } = {}) {
  const capabilities = networkCapabilityRows(network.capabilities || {}, network.services || {});
  const count = capabilities.filter(row => row.tone === "ok").length;
  return `<section class="network-side-section"><div class="network-section-heading"><h3>服务能力</h3><span>${count} / 5 可用</span></div><ul class="network-service-list">${capabilities.map(row => `<li data-tone="${row.tone}" title="${esc(row.detail)}">${icon(capabilityIcons[row.key])}<span>${esc(row.label)}</span><small>${esc(row.value)}</small></li>`).join("")}</ul>${!network.online ? '<p class="network-side-note">显示上次上报的能力，待 OC 恢复后确认。</p>' : ""}</section>
    <section class="network-side-section"><div class="network-section-heading"><h3>同步记录</h3>${icon("activity")}</div><dl class="network-sync-list"><div><dt>库存观测</dt><dd>${esc(dateTime(network.last_inventory))}</dd></div><div><dt>CPU 观测</dt><dd>${esc(dateTime(network.services?.cpu_observed_at))}</dd></div><div><dt>网页事件流</dt><dd class="network-stream-state" data-tone="${streamStateTone(streamState)}">${esc(streamStateText(streamState))}</dd></div></dl></section>
    <details class="network-identifiers"><summary><span>网络标识与限制</span>${icon("chevron-down")}</summary><dl class="network-sync-list"><div><dt>网络 ID <button type="button" class="icon-button" data-copy-network-id aria-label="复制网络 ID" title="复制网络 ID">${icon("copy")}</button></dt><dd class="network-id-value">${esc(network.id)}</dd></div><div><dt>数据质量</dt><dd>${esc(networkQualityLabel(network.quality))}</dd></div><div><dt>单次最大合成量</dt><dd>${network.capabilities?.max_craft_quantity == null ? "未上报" : `${formatQuantity(network.capabilities.max_craft_quantity)} 件`}</dd></div></dl></details>`;
}
