const esc = value => String(value ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
export const jobStates = { pending:"待调度", rejected:"未通过", queued:"排队中", assigned:"已派发", computing:"计算中", running:"合成中", needs_review:"结果待确认", done:"已完成", failed:"失败", canceled:"已撤回", expired:"已过期" };
const controlNames = {active:"正常调度",paused:"已暂停",stopped:"已停止"};
const countsHTML = summary => `<div class="job-counts">${Object.entries(summary.counts).filter(([,n])=>n!=="0").map(([key,n])=>`<span data-state="${key}">${jobStates[key]} <b class="number">${esc(n)}</b></span>`).join("")}</div>`;
export function jobListHTML(model, filter, dateTime) {
  if (model.support === "unsupported") return `<div class="empty">后端尚不支持生产任务。请升级服务；新提交已禁用，单项订单仍可查看和核对。</div>`;
  if (!model.sync) return `<div class="empty">${model.loading ? "正在读取生产任务…" : model.error?.status === 403 ? "当前账号无权查看此网络任务。" : model.error ? "生产任务暂不可用，请刷新重试。" : "尚未连接网络"}</div>`;
  const live = [...model.sync.jobs.values()];
  const rows = filter === "history" ? model.history.map(row => model.sync.jobs.get(row.id) || row) : live.filter(row => !row.settled_at &&
    (filter !== "attention" || row.control === "paused" || row.counts.needs_review !== "0" || row.counts.rejected !== "0" || row.counts.failed !== "0"));
  return rows.length ? rows.map(row => `<div class="job-row"><button class="task-main" data-job="${esc(row.id)}"><span class="task-title">${esc(row.name || `生产任务 ${row.id}`)}</span><span class="task-sub">所有者 ${esc(row.owner_id)} · ${esc(dateTime(row.created_at))}</span>${countsHTML(row)}</button><div class="job-admission"><span>${controlNames[row.control]}</span><span class="muted number">共 ${esc(row.total)} 项</span></div></div>`).join("") :
    `<div class="empty">${filter === "history" && !model.historyLoaded ? model.historyLoading ? "正在读取历史…" : "历史尚未读取，请加载历史记录。" : filter === "attention" ? "暂无待处理生产任务" : filter === "history" ? "暂无保留的历史任务" : "暂无未完成生产任务"}</div>`;
}
export function recoveryHTML(local, icon) {
  const uncertain = local.intents.filter(intent => intent.state !== "accepted" || local.draft.entries.some(row=>row.batch_id === `job:${intent.requestKey}`));
  const intents = uncertain.map(intent => `<div class="job-recovery"><strong>${intent.state === "accepted" ? "已受理，本机清理待核对" : intent.state === "rejected" ? "提交已明确拒绝" : "受理结果待核对"}</strong><p>${esc(intent.requestKey)} · ${intent.manifest.entries.length} 项</p><p>${intent.state === "rejected" ? "原清单保持锁定。条件恢复后，可重发相同请求。" : "请核对原请求。暂未找到时，清单仍会保留，不会重复提交。"}</p><div class="order-actions"><button class="secondary" data-job-reconcile="${esc(intent.requestKey)}">${icon("refresh-cw")}核对原请求</button>${intent.state === "rejected" ? `<button class="secondary" data-job-retry="${esc(intent.requestKey)}">${icon("send")}重发相同请求</button>` : ""}</div>${(intent.conflictJobIds || []).map(id=>`<button class="secondary" data-job="${esc(id)}">${icon("arrow-up-right")}查看关联任务</button>`).join("")}${intent.conflictJobIds?.length ? "<p>关联任务不代表本次清单已受理。</p>" : ""}</div>`);
  const controls = local.controls.map(op=>`<div class="job-recovery"><strong>控制结果待核对 · ${esc(op.action)}</strong><p>${esc(op.jobId)} · ${esc(op.operationKey)}</p><button class="secondary" data-job="${esc(op.jobId)}">${icon("arrow-up-right")}查看任务并核对操作</button></div>`);
  const archived = local.intents.filter(intent=>intent.state==="accepted"&&intent.archived).map(intent=>
    `<div class="job-recovery"><strong>已受理并归档</strong><p>${esc(intent.jobId)} · 原请求 ${esc(intent.requestKey)}</p><p>历史明细已归档，不可编辑、控制或重新调度。</p></div>`);
  return intents.concat(controls, archived).join("");
}
export function jobDetailHTML(detail, local, member, {itemFor,itemImage,quantity,dateTime,icon,controlsAvailable = true}) {
  const s = detail.summary, operations = local.controls.filter(op=>op.jobId===s.id);
  const authorized = member?.admin || member?.id === s.owner_id;
  const mutable = controlsAvailable && authorized && !s.settled_at && s.control !== "stopped" && !operations.length;
  return `<div class="job-heading"><h2>${esc(s.name || `生产任务 ${s.id}`)}</h2><span>${controlNames[s.control]}</span></div>
    <div class="order-detail-facts"><div class="fact"><span>任务标识</span><span>${esc(s.id)}</span></div><div class="fact"><span>所有者</span><span>${esc(s.owner_id)}</span></div><div class="fact"><span>提交时间</span><span>${esc(dateTime(s.created_at))}</span></div><div class="fact"><span>条目总数</span><span>${esc(s.total)}</span></div></div>${countsHTML(s)}
    ${s.reason ? `<p class="error">${esc(s.reason)}</p>` : ""}
    <div class="order-actions">${mutable ? `<button class="secondary" data-job-action="${s.control === "paused" ? "resume" : "pause"}">${icon(s.control === "paused" ? "play" : "pause")}${s.control === "paused" ? "继续调度" : "暂停调度"}</button><button class="secondary" data-job-action="stop">${icon("square")}停止任务</button>` : !authorized ? `<span class="muted">仅所有者或管理员可控制此任务。</span>` : s.settled_at ? `<span class="muted">任务已结束，不可再次调度。</span>` : ""}</div>
    ${operations.map(op=>`<div class="job-recovery"><strong>控制结果待核对 · ${esc(op.action)}</strong><p>${esc(op.operationKey)} · 原控制版本 ${esc(op.expectedControlVersion)}</p>${authorized && controlsAvailable ? `<button class="secondary" data-control-replay="${esc(op.operationKey)}">${icon("refresh-cw")}核对并重放此原操作</button>` : ""}<p>只使用保存的操作键和版本。状态冲突不会清除本机记录。</p></div>`).join("")}
    <div class="job-entries">${detail.entries.map(entry=>{
      const item = itemFor(entry.item_id);
      return `<div class="job-entry">${itemImage(item)}<div class="job-entry-main"><strong>${esc(item?.label || `物品 #${entry.item_id}`)}</strong><span class="muted number">× ${quantity(entry.quantity)}</span><span class="task-state" data-state="${esc(entry.state)}">${jobStates[entry.state]}</span>${entry.reason ? `<p class="error">${esc(entry.reason)}</p>` : ""}${entry.order_id ? `<button class="job-order-link" data-order="${esc(entry.order_id)}">查看单项订单 ${icon("arrow-up-right")}</button>` : ""}</div><span class="muted">#${entry.ordinal+1}</span></div>`;
    }).join("")}</div>`;
}
