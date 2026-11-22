const esc=value=>String(value??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
export function lscAlertRuleHTML(settings,admin=false){
 if(!settings)return '';
 return `<section class="lsc-alert-rule"><div><h3>低电量告警</h3><p>${settings.alerts?`电量低于 <strong>${esc(settings.low_percent)}%</strong> ${settings.low_duration_seconds?`持续 ${esc(settings.low_duration_seconds)} 秒后告警`:'立即告警'} · 回升至 <strong>${esc(settings.recover_percent)}%</strong> 解除`:'低电量告警已关闭'}</p><small>仅发送低电量预警；电量恢复与 OC 上下线不播报。</small></div>${admin?'<button class="secondary" data-lsc-alert-config>设置电量阈值</button>':''}</section>`;
}
export function lscAlertFormHTML(settings){
 return `<form data-lsc-alert-settings><div class="plugin-form-grid"><label>低于多少百分比告警<input name="low_percent" type="number" min="1" max="95" value="${esc(settings.low_percent)}" required></label><label>回升到多少百分比解除<input name="recover_percent" type="number" min="2" max="100" value="${esc(settings.recover_percent)}" required></label><label>持续时间（秒，0 立即告警）<input name="low_duration_seconds" type="number" min="0" max="3600" value="${esc(settings.low_duration_seconds)}" required></label><label class="check"><input name="alerts" type="checkbox" ${settings.alerts?'checked':''}>启用低电量告警</label></div><p class="muted">恢复阈值用于解除站内告警并允许下一次低电量预警，不发送恢复消息；它应高于告警阈值，避免边界波动时反复通知。离线和过期数据不参与判断。QQ 通知还需启用 QQ 联动中的蓝波顿低电量通知。</p><p class="error" data-error role="alert"></p><button class="primary" type="submit">保存电量阈值</button></form>`;
}
export function lscAlertSettings(settings,values){
 const next={...settings,low_percent:Number(values.low_percent),recover_percent:Number(values.recover_percent),low_duration_seconds:Number(values.low_duration_seconds),alerts:values.alerts===true};
 if(!Number.isInteger(next.low_percent)||next.low_percent<1||next.low_percent>95||!Number.isInteger(next.recover_percent)||next.recover_percent<=next.low_percent||next.recover_percent>100)throw new Error('告警阈值应为 1–95% 的整数，恢复阈值必须更高且不超过 100%。');
 if(!Number.isInteger(next.low_duration_seconds)||next.low_duration_seconds<0||next.low_duration_seconds>3600)throw new Error('持续时间应为 0–3600 秒的整数。');
 return next;
}
