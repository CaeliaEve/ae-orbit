import { jobStates, jobDisplayState, jobProgressHTML, jobStateCountsHTML, craftingJobModel, craftingJobsHTML } from "./crafting-center.mjs";
export { jobStates } from "./crafting-center.mjs";
const esc = value => String(value ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
export function jobListHTML(model, filter, dateTime, options = {}) {
  return craftingJobsHTML(craftingJobModel(model, { ...options, filter }), filter, { dateTime, memberId: options.memberId });
}
export function recoveryHTML(local, icon) {
  const uncertain = local.intents.filter(intent => intent.state !== "accepted" || local.draft.entries.some(row=>row.batch_id === `job:${intent.requestKey}`));
  const intents = uncertain.map(intent => {
    const policies = intent.manifest.entries.map(entry => `${entry.cpu_name || "自动分配"}${entry.wait_for_cpu ? " · 等待" : ""}`);
    const policyText = [...new Set(policies)].join("、");
    return `<div class="job-recovery"><strong>${intent.state === "accepted" ? "已受理，本机清理待核对" : intent.state === "rejected" ? "提交已明确拒绝" : "受理结果待核对"}</strong><p>${esc(intent.requestKey)} · ${intent.manifest.entries.length} 项 · CPU ${esc(policyText)}</p><p>${intent.state === "rejected" ? "原清单保持锁定。条件恢复后，可重发相同请求。" : "请核对原请求。暂未找到时，清单仍会保留，不会重复提交。"}</p><div class="order-actions"><button class="secondary" data-job-reconcile="${esc(intent.requestKey)}">${icon("refresh-cw")}核对原请求</button>${intent.state === "rejected" ? `<button class="secondary" data-job-retry="${esc(intent.requestKey)}">${icon("send")}重发相同请求</button>` : ""}</div>${(intent.conflictJobIds || []).map(id=>`<button class="secondary" data-job="${esc(id)}">${icon("arrow-up-right")}查看关联任务</button>`).join("")}${intent.conflictJobIds?.length ? "<p>关联任务不代表本次清单已受理。</p>" : ""}</div>`;
  });
  const controls = local.controls.map(op=>`<div class="job-recovery"><strong>控制结果待核对 · ${esc(op.action)}</strong><p>${esc(op.jobId)} · ${esc(op.operationKey)}</p><button class="secondary" data-job="${esc(op.jobId)}">${icon("arrow-up-right")}查看任务并核对操作</button></div>`);
  const archived = local.intents.filter(intent=>intent.state==="accepted"&&intent.archived).map(intent=>
    `<div class="job-recovery"><strong>已受理并归档</strong><p>${esc(intent.jobId)} · 原请求 ${esc(intent.requestKey)}</p><p>历史明细已归档，不可编辑、控制或重新调度。</p></div>`);
  return intents.concat(controls, archived).join("");
}
export function jobDetailHTML(detail, local, member, {itemFor,itemImage,quantity,dateTime,icon,controlsAvailable = true}) {
  const s = detail.summary, operations = local.controls.filter(op=>op.jobId===s.id);
  const authorized = member?.admin || member?.id === s.owner_id;
  const mutable = controlsAvailable && authorized && !s.settled_at && s.control !== "stopped" && !operations.length;
  const status = jobDisplayState(s);
  return `<div class="crafting-detail-heading"><div><h2>${esc(s.name || `生产任务 ${s.id}`)}</h2><p>${member?.id === s.owner_id ? "我的任务" : `所有者 ${esc(s.owner_id)}`} · ${esc(dateTime(s.created_at))}</p></div><span class="crafting-state" data-tone="${status.tone}"><i aria-hidden="true"></i>${status.label}</span></div>
    <div class="crafting-detail-progress">${jobProgressHTML(s)}${jobStateCountsHTML(s)}<p>按清单条目统计；暂停或停止调度后，已经执行的合成仍可能继续。</p></div>
    ${s.reason ? `<p class="error">${esc(s.reason)}</p>` : ""}
    <div class="crafting-detail-actions">${mutable ? `<button class="secondary" data-job-action="${s.control === "paused" ? "resume" : "pause"}">${icon(s.control === "paused" ? "play" : "pause")}${s.control === "paused" ? "继续调度" : "暂停调度"}</button><button class="secondary crafting-stop" data-job-action="stop">${icon("square")}停止调度</button>` : !authorized ? `<span class="muted">仅所有者或管理员可控制此任务。</span>` : s.settled_at ? `<span class="muted">任务已结束，不可再次调度。</span>` : s.control === "stopped" ? '<span class="muted">调度已停止，已执行条目继续保留。</span>' : ""}</div>
    ${operations.map(op=>`<div class="job-recovery"><strong>控制结果待核对 · ${esc(op.action)}</strong><p>${esc(op.operationKey)} · 原控制版本 ${esc(op.expectedControlVersion)}</p>${authorized && controlsAvailable ? `<button class="secondary" data-control-replay="${esc(op.operationKey)}">${icon("refresh-cw")}核对并重放此原操作</button>` : ""}<p>只使用保存的操作键和版本。状态冲突不会清除本机记录。</p></div>`).join("")}
    <div class="crafting-entry-heading"><h3>合成清单</h3><span>${esc(s.total)} 项</span></div><div class="job-entries">${detail.entries.map(entry=>{
      const item = itemFor(entry.item_id);
      const cpu = entry.cpu_name || "自动分配";
      const tone = ["needs_review", "rejected", "failed"].includes(entry.state) ? "warn" : entry.state === "done" ? "ok" : ["assigned", "computing", "running"].includes(entry.state) ? "active" : "idle";
      return `<div class="crafting-detail-entry"><span class="crafting-entry-media">${itemImage(item)}</span><div class="crafting-entry-copy"><strong>${esc(item?.label || `物品 #${entry.item_id}`)}</strong><small>CPU：${esc(cpu)}${entry.wait_for_cpu ? " · 等待指定 CPU" : ""}</small>${entry.reason ? `<p class="error">${esc(entry.reason)}</p>` : ""}${entry.order_id ? `<button class="job-order-link" data-order="${esc(entry.order_id)}">查看单项订单 ${icon("arrow-up-right")}</button>` : ""}</div><span class="crafting-entry-quantity number">× ${quantity(entry.quantity)}</span><span class="crafting-state" data-tone="${tone}"><i aria-hidden="true"></i>${esc(jobStates[entry.state] || entry.state)}</span></div>`;
    }).join("")}</div><details class="crafting-detail-identifiers"><summary>任务标识与时间${icon("chevron-down")}</summary><dl><div><dt>任务 ID</dt><dd>${esc(s.id)}</dd></div><div><dt>所有者</dt><dd>${esc(s.owner_id)}</dd></div><div><dt>最近更新</dt><dd>${esc(dateTime(s.updated_at))}</dd></div>${s.settled_at ? `<div><dt>结束时间</dt><dd>${esc(dateTime(s.settled_at))}</dd></div>` : ""}</dl></details>`;
}
