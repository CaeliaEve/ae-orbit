import { newId } from './model.mjs';
const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const number = /^[1-9][0-9]{4,14}$/;
export function notificationTarget(value) {
  if (value == null) return null;
  const n = {group_id: String(value.group_id || ''), recipient: String(value.recipient || ''), private_copy: value.private_copy === true};
  if (!number.test(n.group_id) || n.recipient && !number.test(n.recipient) || n.private_copy && !n.recipient) throw new Error('请填写有效群号和 QQ 号；私聊需要接收人。');
  return n;
}
export function notificationLabel(n) { return n ? `${n.group_id} · ${n.recipient ? `@${n.recipient}` : '群内播报'}${n.private_copy ? ' · 同时私聊' : ''}` : '未设置完成通知'; }
const stateLabels={queued:'等待发送',leased:'准备发送',sending:'发送中',confirmed:'投递已确认',uncertain:'发送待确认',failed:'发送失败',expired:'已过期'};
export function qqDeliveryHTML(rows=[]) {
 return rows.length ? rows.map(d=>`<article class="qq-delivery"><div><strong>${esc(d.payload?.title || '任务通知')}</strong><small>${esc(d.payload?.cpu || d.payload?.device || d.payload?.network || '')} · ${esc(d.group_id)}${d.recipient ? ` · @${esc(d.recipient)}` : ''} · ${d.channel==='private'?'私聊':'群消息'}</small><p>${esc(d.payload?.reason || d.detail || '')}</p></div><div><span data-state="${esc(d.state)}">${esc(stateLabels[d.state] || d.state)}</span><small>${esc(new Date(d.created_at).toLocaleString('zh-CN',{hour12:false}))}</small></div></article>`).join('') : '<div class="empty">尚无通知。完成任务或触发告警后在这里查看发送状态。</div>';
}
export async function prepareCPUNotificationTarget({request,network,cpu,session,sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms))}) {
 const captured=network()?.id;
 if(!captured)throw new Error('请先选择网络');
 if(network()?.capabilities?.cpu_notifications!==true)return null;
 const base=`/api/v1/networks/${captured}/qq-reminders/targets`;
 let target=await request(base,{method:'POST',body:{request_key:`cpu-target-${newId()}`,session_id:session,cpu_id:cpu.id}});
 for(let attempt=0;target.status==='pending'&&attempt<30;attempt++){
  await sleep(1000);
  if(network()?.id!==captured)throw new Error('网络已切换，请重新设置');
  target=await request(`${base}/${target.id}`);
 }
 if(network()?.id!==captured)throw new Error('网络已切换，请重新设置');
 if(target.status==='pending')throw new Error('主 OC 尚未确认这台 CPU，请稍后重试');
 if(target.status!=='busy')throw new Error('这台 CPU 的任务已结束、变化或断开，请刷新后重新选择');
 return target.id;
}
export function createQQNotifications({root,request,member,network,toast,changed=()=>{}}) {
 let settings=null,group='',generation=0,chooser=null,preparing=false;
 const path=(suffix='')=>`/api/v1/monitoring/groups/${encodeURIComponent(group)}/qq${suffix}`;
 async function refresh(){
  const token=++generation;group=network()?.server_group || '';if(!group){root.innerHTML='<div class="empty">选择网络后配置 QQ 联动。</div>';return}
  const [v,rows,reminders]=await Promise.all([request(path()),request(path('/deliveries')),request(`/api/v1/networks/${network().id}/qq-reminders`)]);if(token!==generation)return;
  settings=v;changed(v);render(rows,reminders);
 }
 function render(rows,reminders){
  const admin=member()?.admin===true;
  root.innerHTML=`<div class="qq-heading"><div><span class="plugin-kicker">MaiBot · SnowLuma</span><h2>QQ 联动</h2><p class="muted">本次任务提醒、蓝波顿告警与采集异常。</p></div><span class="qq-connection">${settings.connected?'机器人已连接':'等待机器人连接'} · ${settings.sending_enabled?'允许发送':'仅预览'}</span></div>
  <div class="qq-layout"><form data-qq-settings><h3>通知配置</h3><label class="check"><input name="enabled" type="checkbox" ${settings.enabled?'checked':''} ${admin?'':'disabled'}>启用 QQ 联动</label><label class="check"><input name="sending_enabled" type="checkbox" ${settings.sending_enabled?'checked':''} ${admin?'':'disabled'}>允许发送真实 QQ 消息</label><p class="muted">关闭发送时仍保存通知，可先检查内容和接收群。</p>
  ${['alarms','exceptions','recoveries'].map((k,i)=>`<label class="check"><input name="${k}" type="checkbox" ${settings[k]?'checked':''} ${admin?'':'disabled'}>${['蓝波顿告警','网络采集异常','告警恢复通知'][i]}</label>`).join('')}
  <h3>MaiBot 已授权的通知群</h3><p class="muted">群名单由 MaiBot 插件管理，刷新后自动同步。${settings.catalog_at ? `最近同步：${esc(new Date(settings.catalog_at).toLocaleString('zh-CN'))}` : '等待首次同步'}</p><div class="qq-channel-list">${settings.groups.filter(g=>g.enabled).map(g=>`<div class="qq-channel"><strong>${esc(g.name||g.group_id)}</strong><small>${esc(g.group_id)}${g.alarm_recipient?` · 告警 @${esc(g.alarm_recipient)}`:''}</small></div>`).join('')||'<p class="muted">请在 MaiBot → 插件扩展 → AE Orbit 通知渠道中勾选群。</p>'}</div><p class="muted">私聊副本：${settings.allow_private?'MaiBot 已允许':'MaiBot 未允许'}</p>
  ${admin?'<div class="energy-actions"><button class="primary">保存配置</button><button type="button" class="secondary" data-qq-token>更换连接令牌</button></div>':''}</form><section><h3>本次任务提醒</h3>${reminders.filter(r=>r.state==='watching').map(r=>`<article class="qq-delivery"><div><strong>${esc(r.cpu_name||r.payload?.item||'合成任务')}</strong><small>${esc(notificationLabel(r.notification))}</small><small>${r.entry_id?'关联网页订单，确认完成后通知':'游戏内任务结束提醒，结果未确认'}</small></div><div><button type="button" class="text-button" data-edit-reminder="${r.id}">修改</button><button type="button" class="text-button" data-cancel-reminder="${r.id}">取消</button></div></article>`).join('')||'<p class="muted">暂无正在跟踪的提醒。</p>'}<h3>发送记录</h3><p class="muted">回执不确定的消息保留待确认状态，避免重复 @。记录保留 30 天。</p>${qqDeliveryHTML(rows)}</section></div>`;
  root.querySelectorAll('[data-edit-reminder]').forEach(b=>b.onclick=async()=>{try{const r=reminders.find(r=>r.id===b.dataset.editReminder),n=await choose(r.notification);if(!n)return;await request(`/api/v1/networks/${network().id}/qq-reminders/${r.id}`,{method:'PUT',body:n});await refresh();toast('通知接收人已更新');}catch(e){if(e.message!=='已取消设置')toast(e.message,true)}});
  root.querySelectorAll('[data-cancel-reminder]').forEach(b=>b.onclick=async()=>{try{await request(`/api/v1/networks/${network().id}/qq-reminders/${b.dataset.cancelReminder}`,{method:'DELETE'});await refresh();toast('本次提醒已取消');}catch(e){toast(e.message,true)}});
  root.querySelector('form').onsubmit=async e=>{e.preventDefault();try{const f=new FormData(e.target);await request(path(),{method:'PUT',body:{enabled:f.has('enabled'),sending_enabled:f.has('sending_enabled'),revision:settings.revision,alarms:f.has('alarms'),exceptions:f.has('exceptions'),recoveries:f.has('recoveries')}});await refresh();toast('通知规则已保存；通知群由 MaiBot 管理');}catch(e){toast(e.message,true)}};
  root.querySelector('[data-qq-token]')?.addEventListener('click',async()=>{try{const v=await request(path('/token'),{method:'POST',body:{}});const d=makeDialog('MaiBot 连接令牌','<p>此令牌仅允许本服务器组的 QQ 联动。复制到插件配置；关闭后不再显示，旧令牌立即失效。</p><textarea readonly rows="3" aria-label="QQ 连接令牌"></textarea>');d.querySelector('textarea').value=v.token;d.querySelector('textarea').select();await refresh();}catch(e){toast(e.message,true)}});
 }
 function makeDialog(title,body){const d=document.createElement('dialog');d.className='qq-dialog';d.innerHTML=`<div class="qq-dialog-head"><h3>${esc(title)}</h3><button type="button" class="icon-button" data-close aria-label="关闭">×</button></div>${body}`;document.body.append(d);d.querySelector('[data-close]').onclick=()=>d.close();d.addEventListener('close',()=>d.remove(),{once:true});d.showModal();return d}
 async function choose(initial=null){
  const captured=network()?.id;group=network()?.server_group||'';if(!group)throw new Error('请先选择网络');
  const v=await request(path());if(network()?.id!==captured)throw new Error("网络已切换，请重新设置");const groups=v.groups.filter(g=>g.enabled);if(!v.enabled||!v.catalog_ready||!groups.length)throw new Error('请启用 QQ 联动，并在 MaiBot 选择允许通知的群');
  if(chooser)chooser.close();
  const d=makeDialog('本次任务通知',`<form class="qq-chooser"><label>通知群<select name="group_id">${groups.map(g=>`<option value="${g.group_id}" ${initial?.group_id===g.group_id?'selected':''}>${esc(g.name||g.group_id)}</option>`).join('')}</select></label><div class="qq-members-heading"><label>接收人<input name="recipient" inputmode="numeric" maxlength="15" value="${esc(initial?.recipient||'')}" placeholder="留空则普通群播报"></label><button type="button" class="secondary" data-refresh-members>刷新群友</button></div><input type="search" data-member-search placeholder="搜索昵称、群名片或 QQ 号" aria-label="搜索群成员"><small data-member-status role="status"></small><div class="qq-members" data-members></div><label class="check"><input name="private_copy" type="checkbox" ${v.allow_private?'':'disabled'} ${initial?.private_copy&&v.allow_private?'checked':''}>同时私聊接收人</label><p class="muted">通知仅对当前任务生效，接收人不改变订单归属。</p><div class="energy-actions"><button class="primary">保存通知</button><button type="button" class="secondary" data-clear>不设置通知</button></div><p class="error" data-error role="alert"></p></form>`);
  chooser=d;let members=[],timer=null,load=0;const f=d.querySelector('form'),search=d.querySelector('[data-member-search]');
  const draw=()=>{const q=search.value.toLowerCase();d.querySelector('[data-members]').innerHTML=members.filter(m=>`${m.nickname} ${m.card} ${m.user_id}`.toLowerCase().includes(q)).slice(0,100).map(m=>`<button type="button" data-qq-member="${m.user_id}"><strong>${esc(m.card||m.nickname||m.user_id)}</strong><small>${esc(m.user_id)}</small></button>`).join('')||'<p class="muted">暂无匹配群友，可手动输入 QQ 号。</p>'};
  async function loadMembers(refresh=false){const version=++load,gid=f.elements.group_id.value;try{const data=await request(path(`/groups/${gid}/members`),{method:refresh?'POST':'GET',...(refresh?{body:{}}:{})});if(!d.open||version!==load)return;members=data.members;draw();const pending=data.group.refresh_version!==data.group.refreshed_version;d.querySelector('[data-member-status]').textContent=data.group.error?`刷新失败：${data.group.error}，保留旧名单`:pending?'正在等待机器人刷新名单…':data.group.members_at?`名单更新于 ${new Date(data.group.members_at).toLocaleTimeString('zh-CN')}`:'等待首次同步';clearTimeout(timer);if(pending)timer=setTimeout(()=>void loadMembers(),2500)}catch(e){if(d.open)d.querySelector('[data-member-status]').textContent=e.message}}
  search.oninput=draw;f.elements.group_id.onchange=()=>{f.elements.recipient.value='';void loadMembers()};d.querySelector('[data-refresh-members]').onclick=()=>void loadMembers(true);d.querySelector('[data-members]').onclick=e=>{const b=e.target.closest('[data-qq-member]');if(b)f.elements.recipient.value=b.dataset.qqMember};void loadMembers();
  return new Promise((resolve,reject)=>{let result=null,saved=false;f.onsubmit=e=>{e.preventDefault();try{if(network()?.id!==captured)throw new Error('网络已切换，请重新设置');result=notificationTarget({group_id:f.elements.group_id.value,recipient:f.elements.recipient.value.trim(),private_copy:f.elements.private_copy.checked});saved=true;d.close()}catch(e){d.querySelector('[data-error]').textContent=e.message}};d.querySelector('[data-clear]').onclick=()=>{saved=true;d.close()};d.addEventListener('close',()=>{clearTimeout(timer);if(chooser===d)chooser=null;saved?resolve(result):reject(new Error('已取消设置'))},{once:true})});
 }
 async function cpuReminder(cpu,session){
  if(preparing)return;
  preparing=true;const captured=network()?.id;
  try{
   if(network()?.capabilities?.cpu_notifications===true)toast('正在确认这台 CPU 的本次任务…');
   const target=await prepareCPUNotificationTarget({request,network,cpu,session});
   const n=await choose();if(!n)return;
   if(network()?.id!==captured)throw new Error('网络已切换，请重新设置');
   await request(`/api/v1/networks/${captured}/qq-reminders`,{method:'POST',body:{request_key:`cpu-notify-${newId()}`,session_id:session,cpu_id:cpu.id,...(target?{target_id:target}:{}),notification:n}});
   toast('已设置本次 CPU 任务提醒');
  }catch(e){
   if(e.code==='view_changed'||e.code==='not_found')throw new Error('CPU 任务或连接已变化，请刷新后重新选择');
   if(e.code==='capability_unavailable')throw new Error('请更新并重启主 OC，再设置本次任务通知');
   throw e;
  }finally{preparing=false}
 }
 function reset(){generation++;settings=null;group='';chooser?.close()}
 return {refresh,show:()=>void refresh().catch(e=>toast(e.message,true)),choose,cpuReminder,reset};
}
