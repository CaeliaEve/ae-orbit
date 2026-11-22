import {formatQuantity} from './model.mjs';
const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const rules={low:'低于',high:'高于',zero:'归零'};
export function subnetDiscoveryStatusHTML(status={},subnet={}){
 if(!subnet?.component_address)return '<p class="muted">等待绑定 ME 接口，之后开始发现实际库存。</p>';
 if(!status.capable)return '<p class="muted">OC 已连接；请更新专用子网 OC 以开启库存发现。现有监控继续采集。</p>';
 if(!subnet.online)return '<p class="muted">OC 离线，库存发现暂停。</p>';
 const total=status.total||0,checked=status.checked||0,percent=total?Math.min(100,checked/total*100).toFixed(1):'0.0';
 return `<p class="muted">子网实际库存：已发现 ${status.found||0} 种 · ${status.completed_at?'本轮已完成':'正在发现'} ${checked}/${total}（${percent}%）${status.failed?` · 查询失败 ${status.failed} 项`:''}</p>`;
}
export function subnetItemsHTML(view,memberID){
 return (view.items||[]).map(item=>{
  const mine=(view.rules||[]).filter(r=>r.item_id===item.id&&r.member_id===memberID&&r.enabled),active=mine.some(r=>r.active),unit=item.kind==='fluid'?'mB':'件';
  return `<article class="subnet-item" data-item="${item.id}" data-state="${!item.known?'unknown':active?'alert':'normal'}"><span class="subnet-texture">${item.catalog?.image_url?`<img src="${esc(item.catalog.image_url)}" alt="" loading="lazy">`:'◇'}</span><div class="subnet-item-name"><strong>${esc(item.label)}</strong><small>${item.nbt?'NBT 变种 · ':''}${esc(item.name)}${item.kind==='item'?` · ${item.damage}`:''}</small><span>${mine.map(r=>`${rules[r.kind]}${r.kind==='zero'?'':` ${formatQuantity(r.threshold)} ${unit}`} · ${r.duration_seconds} 秒`).join('；')||'尚未设置预警'}</span></div><div class="subnet-item-count"><strong>${item.known?formatQuantity(item.quantity):'未知'}</strong><small>${unit}${item.observed_at?` · ${new Date(item.observed_at).toLocaleTimeString('zh-CN')}`:''}${active?' · 库存预警':''}${!item.known&&item.quantity!==null?` · 最后 ${formatQuantity(item.quantity)}`:''}</small></div><div class="subnet-item-actions"><button class="text-button" data-rule-item="${item.id}">预警</button><button class="text-button" data-history-item="${item.id}">趋势</button>${view.can_manage?`<button class="text-button" data-remove-item="${item.id}">移除</button>`:''}</div></article>`;
 }).join('')||'<div class="empty">后台发现实际库存后，点击“从子网库存选择”勾选监控项。已选库存即使为零也会保留。</div>';
}
export function subnetRuleValues(form){
 const value={item_id:String(form.get('item_id')),kind:String(form.get('kind')),threshold:String(form.get('threshold')).trim(),recover:String(form.get('recover')).trim(),duration_seconds:Number(form.get('duration')),enabled:true};
 if(!Object.hasOwn(rules,value.kind))throw Error('无效的预警类型。');
 if(value.kind==='zero')value.threshold='0';
 if(!/^\d{1,19}$/.test(value.threshold)||!/^\d{1,19}$/.test(value.recover)||BigInt(value.threshold)>9223372036854775807n||BigInt(value.recover)>9223372036854775807n)throw Error('请输入有效整数数量。');
 if(value.kind==='high'?BigInt(value.recover)>=BigInt(value.threshold):BigInt(value.recover)<=BigInt(value.threshold))throw Error('低库存的解除数量应更高，高库存的解除数量应更低。');
 if(!Number.isInteger(value.duration_seconds)||value.duration_seconds<0||value.duration_seconds>3600)throw Error('持续时间应为 0–3600 秒。');
 value.threshold=BigInt(value.threshold).toString();value.recover=BigInt(value.recover).toString();
 return value;
}
export function subnetHistoryHTML(points){
 if(!points.length)return '<p class="muted">等待历史记录。历史与实时采集分开保存。</p>';
 const top=points.reduce((m,p)=>BigInt(p.quantity)>m?BigInt(p.quantity):m,1n),start=Date.parse(points[0].at),end=Date.parse(points.at(-1).at);
 const segments=[];let current=[],epoch;
 for(const p of points){if(epoch!==undefined&&epoch!==p.epoch){segments.push(current);current=[]}epoch=p.epoch;current.push(`${30+(Date.parse(p.at)-start)/(end-start||1)*630},${160-Number(BigInt(p.quantity)*13000n/top)/100}`)}segments.push(current);
 return `<svg class="subnet-chart" viewBox="0 0 700 190" role="img" aria-label="库存数量趋势">${segments.map(xs=>`<polyline points="${xs.join(' ')}"/>`).join('')}<text x="30" y="185">${esc(new Date(points[0].at).toLocaleString('zh-CN'))}</text></svg><div class="subnet-history-table">${points.slice(-100).reverse().map(p=>`<p><time>${esc(new Date(p.at).toLocaleString('zh-CN'))}</time><strong>${formatQuantity(p.quantity)}</strong></p>`).join('')}</div>`;
}
export function createSubnetMonitor({root,market,request,member,network,toast,showView,changed}){
 let group='',plugin=null,list=[],selected='',view=null,generation=0,loading=false,searchTimer,dialogNode=null;
 const base=()=>`/api/v1/monitoring/groups/${encodeURIComponent(group)}/subnets`;
 const admin=()=>member()?.admin===true;
 // Patch observations in place so one-second updates keep focus, scroll and
 // loaded textures. Structural edits still insert/remove the affected rows.
 const mark=(node,html)=>{if(!node||node.innerHTML===html)return;const t=document.createElement('template');t.innerHTML=html;
  function patch(a,b){if(a.nodeType!==b.nodeType||a.nodeName!==b.nodeName){a.replaceWith(b.cloneNode(true));return}if(a.nodeType===3){if(a.data!==b.data)a.data=b.data;return}if(a.nodeType!==1)return;
   for(const x of [...a.attributes])if(!(a.nodeName==='DETAILS'&&x.name==='open')&&!b.hasAttribute(x.name))a.removeAttribute(x.name);for(const x of b.attributes)if(a.getAttribute(x.name)!==x.value)a.setAttribute(x.name,x.value);
   const xs=[...a.childNodes],ys=[...b.childNodes];for(let i=0;i<Math.max(xs.length,ys.length);i++){if(!ys[i])xs[i].remove();else if(!xs[i])a.append(ys[i].cloneNode(true));else patch(xs[i],ys[i])}
  }const xs=[...node.childNodes],ys=[...t.content.childNodes];for(let i=0;i<Math.max(xs.length,ys.length);i++){if(!ys[i])xs[i].remove();else if(!xs[i])node.append(ys[i].cloneNode(true));else patch(xs[i],ys[i])}};
 function marketRender(){const host=market.querySelector('[data-subnet-market]');if(!host)return;
  mark(host,`<article class="market-plugin"><div class="market-plugin-symbol">ME</div><div><span class="plugin-kicker">只读 · 子网库存</span><h2>子 ME 网络监控</h2><p>自选物品与流体，精确库存、数量预警和趋势。每个子网一台专用 OC，目标最快 1 秒。</p><div class="energy-actions">${admin()?`<button class="primary" data-subnet-toggle>${plugin?.enabled?'停用插件':'启用插件'}</button>`:''}${plugin?.enabled?'<button class="secondary" data-subnet-open>打开子网监控</button>':''}</div></div></article>`);
 }
 function render(){marketRender();if(!member()){root.innerHTML='<div class="empty">登录后查看子网。</div>';return}
  if(!plugin?.enabled){root.innerHTML='<div class="empty">在插件市场启用子网库存监控。</div>';return}
  const n=view?.subnet,ds=view?.discovery;
  mark(root,`<div class="subnet-heading"><div><span class="plugin-kicker">ME · 精确库存监控</span><h2>子网库存</h2><p class="muted">已选库存常驻 · 查询失败为未知 · 恢复与上下线不播报</p></div>${admin()?'<button class="primary" data-subnet-create>新增子网 OC</button>':''}</div><div class="subnet-tabs">${list.map(n=>`<button class="secondary" data-subnet-select="${n.id}" aria-pressed="${selected===n.id}">${esc(n.name)}<small>${n.online?'OC 在线':'OC 离线'} · 已选 ${n.item_count} 种</small></button>`).join('')}</div>${n?`<div class="subnet-status"><strong>${esc(n.name)}</strong><span>目标 ${n.interval_seconds} 秒 · 实际一轮 ${n.cycle_millis?(n.cycle_millis/1000).toFixed(2)+' 秒':'等待观测'} · 历史 ${n.history_seconds} 秒</span>${admin()?'<button class="text-button" data-subnet-config>连接与采集设置</button>':''}</div>${!n.component_address?'<p class="muted">安装并启动专用 OC 后，在“连接与采集设置”选择实际 ME 组件。</p>':''}<div class="subnet-discovery-status">${subnetDiscoveryStatusHTML(ds,n)}</div><div class="subnet-toolbar"><button class="secondary" data-subnet-add>从子网库存选择</button><span class="muted">已选 ${view.items.length}/64 种 · ${n.online?'OC 在线':'OC 离线，显示最后观测'}${!n.enabled?' · 已暂停':''}</span></div><div class="subnet-items">${subnetItemsHTML(view,member().id)}</div><details class="subnet-alert-history"><summary>我的预警记录（最近 ${view.alerts?.length||0} 条）</summary>${(view.alerts||[]).map(a=>`<p><time>${esc(new Date(a.at).toLocaleString('zh-CN'))}</time> ${esc(view.items.find(i=>i.id===a.item_id)?.label||'已移除库存')} · ${rules[a.kind]} · ${formatQuantity(a.quantity)} · ${a.state==='active'?'预警中':'已解除'}</p>`).join('')||'<p>暂无预警。</p>'}</details>`:'<div class="empty">创建子网后，安装专用 OC，再选择监控库存。</div>'}`);
 }
 async function refresh(){
  if(!member()||loading)return;loading=true;const version=generation;
  try{
   const groups=await request('/api/v1/monitoring/groups');const preferred=network()?.server_group;
   const next=groups.includes(preferred)?preferred:groups[0]||'';
   if(version!==generation)return;if(group!==next){group=next;selected='';view=null}if(!group)return;
   const [p,ns]=await Promise.all([request(base()+'/plugin'),request(base())]);if(version!==generation)return;const wasEnabled=plugin?.enabled;plugin=p;list=ns;
   if(wasEnabled!==p.enabled)changed({plugin:{id:'aeorbit.subnet'},enabled:p.enabled});if(!ns.some(n=>n.id===selected))selected=ns[0]?.id||'';
   const target=selected;const updated=target?await request(base()+'/'+target):null;if(version!==generation||target!==selected)return;view=updated;if(view)view.can_manage=admin();render();
  }catch(e){if(version===generation)toast(e.message,true)}finally{if(version===generation)loading=false}
 }
 function dialog(title,html){dialogNode?.close();const d=document.createElement('dialog');d.className='qq-dialog subnet-dialog';d.innerHTML=`<div class="dialog-heading"><h2>${esc(title)}</h2><button class="icon-button" data-subnet-close aria-label="关闭">×</button></div>${html}`;document.body.append(d);d.querySelector('[data-subnet-close]').onclick=()=>d.close();d.addEventListener('close',()=>d.remove(),{once:true});d.showModal();dialogNode=d;return d}
 function configInput(n={}){return `<label>子网名称<input name="name" value="${esc(n.name||'生产线子网')}" maxlength="128" required></label>${n.id?`<label>ME 组件<select name="component_address"><option value="">请选择</option>${n.components.filter(c=>c.supported).map(c=>`<option value="${c.address}"${n.component_address===c.address?' selected':''}>${esc(c.type)} · ${c.address}</option>`).join('')}</select></label>`:''}<label>维度与位置<input name="location" value="${esc(n.location||'')}" maxlength="128"></label><label>目标采集间隔（秒）<input name="interval_seconds" type="number" min="1" max="3600" value="${n.interval_seconds||1}" required></label><label>历史保存间隔（秒）<input name="history_seconds" type="number" min="5" max="3600" value="${n.history_seconds||10}" required></label><label>观测有效期（秒）<input name="stale_seconds" type="number" min="10" max="10800" value="${n.stale_seconds||30}" required></label><label class="check"><input type="checkbox" name="enabled"${n.enabled!==false?' checked':''}>启用采集</label>`}
 async function openConfig(create=false){const n=create?{}:view.subnet,captured=base(),d=dialog(create?'新增子网 OC':'连接与采集设置',`<form class="qq-chooser">${configInput(n)}<p class="muted">目标最快 1 秒，实际周期受游戏 tick 和服务器总预算限制。一台 OC 只连接一个子 ME 网络。</p><p class="error" data-error></p><button class="primary">${create?'创建并获取安装命令':'保存设置'}</button></form>${!create?'<button class="text-button" data-reset-token>重置 OC 安装令牌</button>':''}`);
  d.querySelector('[data-reset-token]')?.addEventListener('click',async()=>{try{const v=await request(captured+'/'+n.id+'/token',{method:'POST'});d.close();dialog('新的 OC 安装令牌',`<p>旧令牌已失效。在原专用 OC 执行：</p><pre>ae-orbit-subnet-setup ${esc(location.origin)} ${esc(v.token)}
reboot
ae-orbit start</pre>`);await refresh()}catch(e){toast(e.message,true)}});
  d.querySelector('form').onsubmit=async e=>{e.preventDefault();try{if(captured!==base())throw Error('服务器组已变化');const f=new FormData(e.target),body={name:f.get('name'),component_address:f.get('component_address')||'',location:f.get('location'),interval_seconds:Number(f.get('interval_seconds')),history_seconds:Number(f.get('history_seconds')),stale_seconds:Number(f.get('stale_seconds')),enabled:f.has('enabled'),revision:n.revision||0};const v=await request(captured+(create?'':'/'+n.id),{method:create?'POST':'PUT',body});d.close();if(create){selected=v.id;const origin=location.origin;dialog('安装专用子网 OC',`<p>只在新的专用 OC 执行，令牌仅本次显示。</p><pre class="monitor-install-command">wget -f ${esc(origin)}/oc/install.lua /tmp/ae-orbit-install.lua
lua /tmp/ae-orbit-install.lua ${esc(origin)}
ae-orbit-subnet-setup ${esc(origin)} ${esc(v.token)}
reboot
ae-orbit start</pre><p>启动后回到子网页面，选择实际 ME 组件，再添加库存。</p>`)}await refresh()}catch(error){d.querySelector('[data-error]').textContent=error.message}};
 }
 function addItems(){
  const captured=base()+'/'+selected,version=generation,d=dialog('选择子网实际库存','<div class="subnet-search"><select data-kind><option value="item">物品</option><option value="fluid">流体</option></select><input type="search" data-search placeholder="中文、拼音、首字母或 @modid" aria-label="搜索子网库存"></div><div data-discovery-status></div><p class="muted">这里只显示本子网精确查询成功且数量大于零的物品。首次发现会逐步增加；完整范围以 NESQL 目录为限。</p><div data-results></div><div class="subnet-picker-pages"><button class="secondary" data-prev>上一页</button><span data-page></span><button class="secondary" data-next>下一页</button></div><p class="error" data-error></p>');let serial=0,offset=0,busy=false;
  async function search(){if(busy||!d.open||version!==generation)return;busy=true;const k=++serial,query=d.querySelector('[data-search]').value,kind=d.querySelector('[data-kind]').value;
   try{const page=await request(captured+'/discovered?kind='+kind+'&q='+encodeURIComponent(query)+'&offset='+offset);if(k!==serial||!d.open||version!==generation)return;
    mark(d.querySelector('[data-discovery-status]'),subnetDiscoveryStatusHTML(page.status,view?.subnet));
    mark(d.querySelector('[data-results]'),page.items.map(i=>`<button class="subnet-search-row" data-add-id="${i.id}"${i.monitored?' disabled':''}><span>${i.catalog?.image_url?`<img src="${esc(i.catalog.image_url)}" alt="">`:''}<strong>${esc(i.label)}</strong><small>${esc(i.name)} · ${i.damage}${i.nbt?' · NBT':''} · ${formatQuantity(i.quantity)} ${i.kind==='fluid'?'mB':'件'}</small></span><b>${i.monitored?'已监控':'＋ 监控'}</b></button>`).join('')||'<p class="empty">尚未发现匹配库存。后台确认后会自动出现在这里。</p>');
    d.querySelector('[data-prev]').disabled=offset===0;d.querySelector('[data-next]').disabled=offset+50>=page.total;d.querySelector('[data-page]').textContent=`${Math.floor(offset/50)+1} / ${Math.max(1,Math.ceil(page.total/50))} · 已发现 ${page.total} 种`;
   }catch(e){if(d.open)d.querySelector('[data-error]').textContent=e.message}finally{busy=false}}
  const change=()=>{offset=0;serial++;clearTimeout(searchTimer);searchTimer=setTimeout(search,250)};d.querySelector('[data-search]').oninput=change;d.querySelector('[data-kind]').onchange=change;
  d.querySelector('[data-prev]').onclick=()=>{offset=Math.max(0,offset-50);void search()};d.querySelector('[data-next]').onclick=()=>{offset+=50;void search()};
  d.querySelector('[data-results]').onclick=async e=>{const b=e.target.closest('[data-add-id]');if(!b||b.disabled)return;try{b.disabled=true;await request(captured+'/items',{method:'POST',body:{item_id:b.dataset.addId}});await refresh();await search()}catch(e){d.querySelector('[data-error]').textContent=e.message;b.disabled=false}};
  const timer=setInterval(()=>void search(),2000);d.addEventListener('close',()=>{clearInterval(timer);clearTimeout(searchTimer);serial++},{once:true});void search();
 }

 async function editRule(itemID){const item=view.items.find(i=>i.id===itemID),existing=view.rules.filter(r=>r.item_id===itemID&&r.member_id===member().id),captured=base()+'/'+selected;
  const version=generation;const cfg=await request(`/api/v1/monitoring/groups/${encodeURIComponent(group)}/qq`),groups=cfg.groups.filter(g=>g.enabled),current=existing.find(r=>r.enabled)||existing[0];
  if(version!==generation||captured!==base()+'/'+selected)return;const d=dialog('库存预警 · '+item.label,`<form class="qq-chooser"><input type="hidden" name="item_id" value="${itemID}"><label>规则<select name="kind">${Object.entries(rules).map(([k,v])=>`<option value="${k}"${current?.kind===k?' selected':''}>${v}</option>`).join('')}</select></label><label>触发数量（${item.kind==='fluid'?'mB':'件'}）<input name="threshold" inputmode="numeric" value="${current?.threshold||'1000'}" required></label><label>解除数量<input name="recover" inputmode="numeric" value="${current?.recover||'1500'}" required></label><label>持续秒数（0 立即）<input name="duration" type="number" min="0" max="3600" value="${current?.duration_seconds??3}"></label><label>QQ 通知群<select name="group"><option value="">仅站内预警</option>${groups.map(g=>`<option value="${g.group_id}"${current?.notification?.group_id===g.group_id?' selected':''}>${esc(g.name||g.group_id)}</option>`).join('')}</select></label><label>@ QQ（留空普通播报）<input name="recipient" inputmode="numeric" value="${esc(current?.notification?.recipient||'')}"></label><p class="muted">每次越界只预警一次，恢复不播报。失败或过期数据不会判为零。</p><p class="error" data-error></p><button class="primary">保存预警</button></form><div class="subnet-rule-list">${existing.filter(r=>r.enabled).map(r=>`<button class="text-button" data-remove-rule="${r.id}">关闭${rules[r.kind]}预警</button>`).join('')}</div>`);
  d.querySelector('form').onsubmit=async e=>{e.preventDefault();try{const f=new FormData(e.target),body=subnetRuleValues(f),prior=existing.find(r=>r.kind===body.kind);body.revision=prior?.revision||0;body.notification=f.get('group')?{group_id:f.get('group'),recipient:String(f.get('recipient')).trim(),private_copy:false}:null;await request(captured+'/rules'+(prior?'/'+prior.id:''),{method:prior?'PUT':'POST',body});d.close();await refresh();toast('库存预警已保存')}catch(error){d.querySelector('[data-error]').textContent=error.message}};
  d.querySelectorAll('[data-remove-rule]').forEach(b=>b.onclick=async()=>{try{await request(captured+'/rules/'+b.dataset.removeRule,{method:'DELETE'});d.close();await refresh()}catch(e){toast(e.message,true)}});
 }
 root.addEventListener('click',e=>{const b=e.target.closest('button');if(!b)return;if(b.dataset.subnetSelect){generation++;loading=false;selected=b.dataset.subnetSelect;view=null;void refresh()}else if(b.hasAttribute('data-subnet-create'))void openConfig(true);else if(b.hasAttribute('data-subnet-config'))void openConfig();else if(b.hasAttribute('data-subnet-add'))addItems();else if(b.dataset.removeItem){void request(base()+'/'+selected+'/items/'+b.dataset.removeItem,{method:'DELETE'}).then(refresh).catch(e=>toast(e.message,true))}else if(b.dataset.ruleItem)void editRule(b.dataset.ruleItem).catch(e=>toast(e.message,true));else if(b.dataset.historyItem){const captured=base()+'/'+selected,item=view.items.find(i=>i.id===b.dataset.historyItem);void request(captured+'/history/'+item.id).then(xs=>dialog('库存趋势 · '+item.label,subnetHistoryHTML(xs))).catch(e=>toast(e.message,true))}});
 market.addEventListener('click',e=>{if(e.target.closest('[data-subnet-open]'))showView('plugin-subnet');const b=e.target.closest('[data-subnet-toggle]');if(b&&!b.disabled){b.disabled=true;b.textContent=plugin?.enabled?'正在停用…':'正在准备目录…';void request(base()+'/plugin',{method:'PUT',body:{enabled:!plugin?.enabled,revision:plugin?.revision||0}}).then(refresh).catch(e=>toast(e.message,true)).finally(()=>{b.disabled=false;marketRender()})}});
 const observer=new MutationObserver(marketRender);observer.observe(market,{childList:true,subtree:true});
 const timer=setInterval(()=>{if(!root.hidden&&document.visibilityState==='visible')void refresh()},1000);
 function reset(){generation++;loading=false;group='';plugin=null;list=[];view=null;selected='';dialogNode?.close();changed({plugin:{id:'aeorbit.subnet'},enabled:false});render()}
 return{refresh,show:()=>void refresh(),sessionChanged:()=>{reset();void refresh()},networkChanged:()=>{generation++;loading=false;dialogNode?.close();view=null;void refresh()},reset,destroy:()=>{clearInterval(timer);observer.disconnect()}};
}
