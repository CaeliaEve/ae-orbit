import { formatQuantity } from "./model.mjs";
import { cpuOutputText, cpuSelectionState, formatCPUStorage } from "./cpu-ui.mjs";

const esc = value => String(value ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const icon = name => `<i data-lucide="${name}" aria-hidden="true"></i>`;

export function cpuMonitorOverviewHTML(model, { observed = "", online = true, truncated = false, age = () => "未观测" } = {}) {
  const time = observed ? `更新于 ${age(observed)}` : "等待 CPU 状态上报";
  const notice = [
    !online ? "OC 离线，显示最近观测" : "",
    truncated ? "列表不完整，以下统计仅包含已上报 CPU" : "",
  ].filter(Boolean).join(" · ");
  const naming = [model.unnamed ? `${model.unnamed} 台未命名` : "", model.duplicates ? `${model.duplicates} 台名称重复` : ""].filter(Boolean).join(" · ");
  return `<div class="cpu-monitor-metrics">
    <div class="cpu-monitor-metric"><span>已观测 CPU</span><strong>${model.total ? formatQuantity(model.total) : "—"}<small>台</small></strong></div>
    <div class="cpu-monitor-metric" data-tone="busy"><span>忙碌</span><strong>${model.total ? formatQuantity(model.busy) : "—"}<small>台${model.busyPercent === null ? "" : ` · ${model.busyPercent}%`}</small></strong></div>
    <div class="cpu-monitor-metric" data-tone="idle"><span>空闲</span><strong>${model.total ? formatQuantity(model.idle) : "—"}<small>台</small></strong></div>
    <div class="cpu-monitor-metric"><span>合计合成存储</span><strong class="cpu-storage-total" title="${model.storage === null ? "尚无完整容量观测" : `${formatQuantity(model.storage)} B`}">${model.storage === null ? "—" : formatCPUStorage(model.storage)}</strong></div>
  </div>
  <div class="cpu-monitor-meta"><span>${icon("clock")}<span>${esc(time)}</span></span>${naming ? `<span class="cpu-naming-note" title="未命名或重名 CPU 无法严格指定；可展开单台详情查看。">${icon("circle-help")}${esc(naming)}</span>` : ""}</div>
  ${notice ? `<p class="cpu-monitor-notice" role="status">${icon("circle-alert")}${esc(notice)}</p>` : ""}`;
}

export function cpuMonitorListHTML(model, { connected = true, supported = false, truncated = false, expanded = new Set(), outputImage = () => icon("package") } = {}) {
  if (!connected) return `<div class="empty cpu-monitor-empty">${icon("wifi-off")}<strong>尚未连接网络</strong><span>连接 AE 网络后显示 CPU 状态。</span></div>`;
  if (!model.total) return `<div class="empty cpu-monitor-empty">${icon("cpu")}<strong>暂无 CPU 观测</strong><span>等待 OC 上报 CPU 列表。</span></div>`;
  if (!model.visible.length) return `<div class="empty cpu-monitor-empty">${icon("search")}<strong>没有匹配的 CPU</strong><span>尝试其他名称、产物或筛选条件。</span><button class="secondary" data-cpu-monitor-reset>清除筛选</button></div>`;
  const rows = model.visible.map(row => {
    const { cpu } = row;
    const selection = truncated ? { selectable: false, reason: "CPU 列表不完整，暂时无法确认名称唯一性。" } : cpuSelectionState(cpu, model.rows.map(value => value.cpu), supported);
    const output = cpu.output;
    const quantity = output ? formatQuantity(output.quantity) : "";
    const outputLabel = output?.catalog?.localized_name || output?.label || output?.name || "未知产物";
    const unit = output?.kind === "fluid" ? " mB" : output?.kind === "essentia" ? " 源质" : "";
    const outputText = cpuOutputText(cpu);
    const hint = row.unnamed ? "未设置游戏名称" : row.duplicate ? "名称重复" : "";
    return `<details class="cpu-monitor-row" data-cpu-key="${esc(row.key)}" data-state="${cpu.busy ? "busy" : "idle"}"${expanded.has(row.key) ? " open" : ""}>
      <summary aria-label="${esc(row.name)}，${cpu.busy ? "忙碌" : "空闲"}，${esc(formatCPUStorage(cpu.storage))}，展开详情">
        <span class="cpu-monitor-identity">${icon("cpu")}<span><strong>${esc(row.name)}</strong><small>${esc(hint || `快照 ${cpu.id || row.index + 1}`)}</small></span></span>
        <span class="cpu-monitor-state"><i class="cpu-state-dot" aria-hidden="true"></i>${cpu.busy ? "忙碌" : "空闲"}</span>
        <span class="cpu-monitor-storage number" title="${formatQuantity(cpu.storage)} B"><span class="sr-only">合成存储 </span>${esc(formatCPUStorage(cpu.storage))}</span>
        <span class="cpu-monitor-coprocessors number"><span class="sr-only">协处理器 </span>${formatQuantity(cpu.coprocessors)}<span class="cpu-mobile-unit"> 协处理器</span></span>
        <span class="cpu-monitor-output${output ? " has-output" : ""}" title="${esc(outputText)}">${output ? `<span class="cpu-output-media"${output.catalog?.base_fallback ? ' title="使用基础纹理；NBT 外观未单独确认"' : ""}>${outputImage(output,cpu)}</span>` : icon(cpu.busy ? "circle-help" : "minus")}<span>${output ? `<strong>${esc(outputLabel)}</strong><small>× ${quantity}${unit}</small>` : cpu.busy ? "产物未提供" : "暂无合成任务"}</span></span>
        <span class="cpu-monitor-expand">${icon("chevron-down")}</span>
      </summary>
      <div class="cpu-monitor-details">
        <dl><div><dt>快照 ID</dt><dd>${esc(cpu.id || "未提供")}</dd></div><div><dt>合成存储</dt><dd>${formatQuantity(cpu.storage)} B</dd></div><div><dt>指定合成</dt><dd>${selection.selectable ? "可通过合成清单指定" : esc(selection.reason)}</dd></div></dl>
        <p>${esc(outputText)}${output?.name ? `<span class="cpu-output-registry">${esc(output.name)}</span>` : ""}</p>
        ${cpu.busy ? `<button class="secondary cpu-process-open" data-cpu-process="${esc(cpu.id)}"><i data-lucide="activity" aria-hidden="true"></i>查看合成过程</button>` : ""}
        ${output?.catalog?.base_fallback ? '<p class="cpu-detail-note">产物使用基础纹理，名称保留游戏上报值；NBT 变种外观未单独确认。</p>' : ""}<p class="cpu-detail-note">快照 ID 可能随重新枚举变化。详情按需读取三类任务清单，没有合成完成百分比。</p>
      </div>
    </details>`;
  }).join("");
  return `<div class="cpu-monitor-table" aria-label="CPU 监控列表"><div class="cpu-monitor-columns" aria-hidden="true"><span>CPU</span><span>状态</span><span>合成存储</span><span>协处理器</span><span>最终产物</span><span></span></div>${rows}</div>`;
}
