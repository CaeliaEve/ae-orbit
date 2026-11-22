import {pluginCardHTML} from './plugin-ui.mjs';
import {formatQuantity} from './model.mjs';
const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const rules={low:'低于',high:'高于',zero:'归零'};
export function subnetDiscoveryStatusHTML(status={},subnet={}){
 if(!subnet?.component_address)return '<p class="muted">等待绑定 ME 接口，之后开始发现实际库存。</p>';
 if(!status.capable)return '<p class="muted">OC 已连接；请更新专用子网 OC 以开启库存发现。现有监控继续采集。</p>';
 if(!subnet.online)return '<p class="muted">OC 离线，库存发现暂停。</p>';
 if(status.mode==='native'){const errors={too_many_items:'库存超过 128 种，已暂停清单发现',payload_large:'库存身份数据过大，已暂停清单发现',memory_low:'OC 内存不足，等待重试',unsupported:'OC 不支持清单读取',read_failed:'本次清单读取失败'};const error=status.error?status.error.split(', ').map(e=>{const [kind,code]=e.split(':');return `${kind==='fluid'?'流体':'物品'}：${errors[code]||code}`}).join('；'):'';return `<p class="muted">独立小子网 · 每 30 秒直接刷新库存清单 · 已发现 ${status.found||0} 种${status.progress_at?' · 上次 '+esc(new Date(status.progress_at).toLocaleTimeString('zh-CN')):' · 等待首次清单'}${error?' · '+esc(error):''}</p>`;}
 const total=status.total||0,checked=status.checked||0,percent=total?Math.min(100,checked/total*100).toFixed(1):'0.0';
 return `<p class="muted">子网实际库存：已发现 ${status.found||0} 种 · ${status.completed_at?'本轮已完成':'正在发现'} ${checked}/${total}（${percent}%）${status.failed?` · 查询失败 ${status.failed} 项`:''}</p>`;
}
export function subnetItemsHTML(view,memberID,image=()=>'◇'){
 return (view.items||[]).map(item=>{
  const mine=(view.rules||[]).filter(r=>r.item_id===item.id&&r.member_id===memberID&&r.enabled),active=mine.some(r=>r.active),unit=item.kind==='fluid'?'mB':'件';
  return `<article class="subnet-item" data-item="${item.id}" data-state="${!item.known?'unknown':active?'alert':'normal'}"><span class="subnet-texture">${image(item)}</span><div class="subnet-item-name"><strong>${esc(item.label)}</strong><small title="${esc(item.name)}${item.kind==='item'?` · ${item.damage}`:''}">${item.kind==='fluid'?'流体':'物品'}${item.nbt?' · NBT 变种':''}</small><span>${mine.map(r=>`${rules[r.kind]}${r.kind==='zero'?'':` ${formatQuantity(r.threshold)} ${unit}`} · ${r.duration_seconds} 秒`).join('；')||'尚未设置预警'}</span>${item.keywords?.length?`<div class="subnet-keywords">${item.keywords.map(k=>`<code>${esc(k)}</code>`).join('')}</div>`:''}</div><div class="subnet-item-count"><strong>${item.known?formatQuantity(item.quantity):'未知'}</strong><small>${unit}${item.observed_at?` · ${new Date(item.observed_at).toLocaleTimeString('zh-CN')}`:''}${active?' · 库存预警':''}${!item.known&&item.quantity!==null?` · 最后 ${formatQuantity(item.quantity)}`:''}</small></div><div class="subnet-item-actions"><button class="text-button" data-history-item="${item.id}">趋势</button><button class="text-button" data-item-settings="${item.id}">设置</button></div></article>`;
 }).join('')||'<div class="empty">后台发现实际库存后，点击“从子网库存选择”勾选监控项。已选库存即使为零也会保留。</div>';
}
export function subnetKeywordValues(value){
 const keys=[...new Set(String(value||'').split(/[,，、\r\n]+/).map(k=>k.trim().replace(/\s+/g,' ').toLowerCase()).filter(Boolean))];
 if(keys.length>8||keys.some(k=>[...k].length>32||! /^[\p{L}\p{N} _-]+$/u.test(k)||['电量','蓝波顿','蓝博顿','ae电量','库存'].includes(k)))throw Error('请填写最多 8 个有效关键词，每个 1–32 字，使用文字、数字、空格、下划线或短横线。');
 return keys;
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
export function createSubnetMonitor({root,market,request,member,network,toast,showView,changed,image}){
 let pickerItems=[],pickerPaint=null,itemSearch='',itemFilter='all';
 const textureItem=id=>[...(view?.items||[]),...pickerItems].find(i=>'subnet:'+i.id===id);
 const media=i=>image({...i,id:'subnet:'+i.id});
 let group='',plugin=null,list=[],selected='',view=null,generation=0,loading=false,searchTimer,dialogNode=null;
 const base=()=>`/api/v1/monitoring/groups/${encodeURIComponent(group)}/subnets`;
 const admin=()=>member()?.admin===true;
 // Patch observations in place so one-second updates keep focus, scroll and
 // loaded textures. Structural edits still insert/remove the affected rows.
 const mark=(node,html)=>{if(!node||node.innerHTML===html)return;const t=document.createElement('template');t.innerHTML=html;
  function patch(a,b){if(a.nodeType!==b.nodeType||a.nodeName!==b.nodeName){a.replaceWith(b.cloneNode(true));return}if(a.nodeType===3){if(a.data!==b.data)a.data=b.data;return}if(a.nodeType!==1)return;
   for(const x of [...a.attributes])if(!(a.nodeName==='DETAILS'&&x.name==='open')&&!(x.name==='style'&&a.classList.contains('item-image-sprite'))&&!b.hasAttribute(x.name))a.removeAttribute(x.name);for(const x of b.attributes)if(a.getAttribute(x.name)!==x.value)a.setAttribute(x.name,x.value);
   const xs=[...a.childNodes],ys=[...b.childNodes];for(let i=0;i<Math.max(xs.length,ys.length);i++){if(!ys[i])xs[i].remove();else if(!xs[i])a.append(ys[i].cloneNode(true));else patch(xs[i],ys[i])}
  }const xs=[...node.childNodes],ys=[...t.content.childNodes];for(let i=0;i<Math.max(xs.length,ys.length);i++){if(!ys[i])xs[i].remove();else if(!xs[i])node.append(ys[i].cloneNode(true));else patch(xs[i],ys[i])}};
 function marketRender(){const host=market.querySelector('[data-subnet-market]');if(!host)return;
  mark(host,pluginCardHTML({kind:'subnet',title:'子网库存',description:'关注产线物品和流体，数量越界时提醒，也可在 QQ 查询。',features:['物品 / 流体','库存预警','QQ 关键词'],enabled:plugin?.enabled,status:list.length+' 个子网 · '+list.filter(n=>n.online).length+' 台 OC 在线',actions:`${plugin?.enabled?'<button class="secondary" data-subnet-open>打开子网库存 <span aria-hidden="true">→</span></button>':''}${admin()?`<button class="text-button" data-subnet-toggle>${plugin?.enabled?'停用':'启用插件'}</button>`:'<span class="muted">由管理员启用</span>'}`}));

 }
 function render(){marketRender();if(!member()){root.innerHTML='<div class="empty">登录后查看子网。</div>';return}
  if(!plugin?.enabled){root.innerHTML='<div class="empty">在插件市场启用子网库存监控。</div>';return}
  const n=view?.subnet,ds=view?.discovery,items=view?.items||[],active=new Set((view?.rules||[]).filter(r=>r.member_id===member().id&&r.enabled&&r.active).map(r=>r.item_id)),unknown=items.filter(i=>!i.known).length;
  const term=itemSearch.trim().toLowerCase(),filtered=items.filter(i=>(itemFilter==='all'||itemFilter==='alert'&&active.has(i.id)||itemFilter==='unknown'&&!i.known||i.kind===itemFilter)&&[i.label,i.name,...(i.keywords||[])].join(' ').toLowerCase().includes(term));
  mark(root,`<div class="plugin-page-head"><div><span class="plugin-kicker">SUBNETWORK</span><h2>子网库存</h2><p>把重要的库存留在视线里。</p></div><div class="energy-actions">${n?`<label class="subnet-selector"><span class="sr-only">选择子网</span><select data-subnet-switch>${list.map(v=>`<option value="${v.id}"${selected===v.id?' selected':''}>${esc(v.name)}</option>`).join('')}</select></label>`:''}${admin()?'<button class="text-button" data-subnet-create>新增子网</button>':''}</div></div>${n?`<div class="subnet-summary"><span class="plugin-state" data-tone="${n.online&&n.enabled?'ok':'idle'}">${!n.enabled?'已暂停':n.online?'OC 在线':'OC 离线'}</span><span><b>${items.length}</b> 项监控</span><span class="${active.size?'status-warn':''}"><b>${active.size}</b> 项预警</span>${unknown?`<span><b>${unknown}</b> 项未知</span>`:''}<details class="subnet-diagnostics"><summary>采集详情</summary><div><p>目标 ${n.interval_seconds} 秒 · 实际一轮 ${n.cycle_millis?(n.cycle_millis/1000).toFixed(2)+' 秒':'等待观测'} · 历史 ${n.history_seconds} 秒</p>${subnetDiscoveryStatusHTML(ds,n)}${admin()?'<button class="text-button" data-subnet-config>连接与采集设置 →</button>':''}</div></details></div>${!n.component_address?'<div class="plugin-notice">先在采集详情中选择 ME 组件，再添加监控物品。</div>':''}${ds?.error?`<p class="error">库存发现暂未更新，打开采集详情查看原因。</p>`:''}<div class="subnet-toolbar"><label class="plugin-search"><span class="sr-only">搜索监控物品</span><input data-monitor-search type="search" value="${esc(itemSearch)}" placeholder="搜索物品或 QQ 关键词"></label><div class="plugin-filters" role="group" aria-label="监控库存筛选">${[['all','全部'],['item','物品'],['fluid','流体'],['alert','预警'],['unknown','未知']].map(([key,label])=>`<button data-subnet-filter="${key}" aria-pressed="${itemFilter===key}">${label}</button>`).join('')}</div><button class="primary" data-subnet-add>添加监控</button></div><div class="subnet-list-heading" aria-hidden="true"><span>监控物品</span><span>当前库存</span><span>操作</span></div><div class="subnet-items">${filtered.length?subnetItemsHTML({...view,items:filtered},member().id,media):items.length?'<div class="plugin-empty"><strong>没有匹配的监控物品</strong><p>换一个关键词或筛选条件。</p></div>':'<div class="plugin-empty"><strong>选择你关心的库存</strong><p>点击添加监控，从子网实际库存中选择物品或流体。</p></div>'}</div><details class="subnet-alert-history plugin-disclosure"><summary>我的预警记录 <span>${view.alerts?.length||0} 条</span></summary>${(view.alerts||[]).map(a=>`<p><time>${esc(new Date(a.at).toLocaleString('zh-CN'))}</time> ${esc(items.find(i=>i.id===a.item_id)?.label||'已移除库存')} · ${rules[a.kind]} · ${formatQuantity(a.quantity)} · ${a.state==='active'?'预警中':'已解除'}</p>`).join('')||'<p class="muted">暂无预警。</p>'}</details>`:'<div class="plugin-empty"><strong>开始关注一条产线</strong><p>新增子网，连接专用 OC，再选择需要监控的库存。</p></div>'}`);
 }
 function itemSettings(itemID){
  const item=view.items.find(i=>i.id===itemID),mine=view.rules.filter(r=>r.item_id===itemID&&r.member_id===member().id&&r.enabled);
  const d=dialog(item.label,`<div class="subnet-settings-identity">${media(item)}<div><strong>${esc(item.label)}</strong><small>${esc(item.name)}${item.nbt?' · NBT 变种':''}</small></div><b>${item.known?formatQuantity(item.quantity):'未知'} ${item.kind==='fluid'?'mB':'件'}</b></div><button class="plugin-setting-action" data-rule-item="${item.id}"><span><strong>库存预警</strong><small>${mine.length?mine.map(r=>rules[r.kind]+(r.kind==='zero'?'':' '+formatQuantity(r.threshold))).join('；'):'设置数量阈值与通知群'}</small></span><span>设置 →</span></button>${admin()?`<button class="plugin-setting-action" data-keywords-item="${item.id}"><span><strong>QQ 查询关键词</strong><small>${esc((item.keywords||[]).join('、')||'群消息包含关键词即可查询')}</small></span><span>编辑 →</span></button>`:''}${admin()?`<div class="subnet-remove-action"><button class="text-button danger" data-remove-item="${item.id}">移除监控</button><small>停止采集此项，保留历史。</small></div>`:''}`);
  d.querySelector('[data-rule-item]').onclick=()=>{d.close();void editRule(itemID).catch(e=>toast(e.message,true))};
  d.querySelector('[data-keywords-item]')?.addEventListener('click',()=>{d.close();void editKeywords(itemID)});
  d.querySelector('[data-remove-item]')?.addEventListener('click',()=>{void request(base()+'/'+selected+'/items/'+itemID,{method:'DELETE'}).then(()=>{d.close();return refresh()}).catch(e=>toast(e.message,true))});

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
 function configInput(n={}){return `<label>子网名称<input name="name" value="${esc(n.name||'生产线子网')}" maxlength="128" required></label>${n.id?`<label>ME 组件<select name="component_address"><option value="">请选择</option>${n.components.filter(c=>c.supported).map(c=>`<option value="${c.address}"${n.component_address===c.address?' selected':''}>${esc(c.type)} · ${c.address}</option>`).join('')}</select></label>`:''}<label>维度与位置<input name="location" value="${esc(n.location||'')}" maxlength="128" placeholder="便于辨认这条产线"></label><label class="check"><input type="checkbox" name="enabled"${n.enabled!==false?' checked':''}>启用采集</label><details class="plugin-disclosure"><summary>采集参数 <span>发现方式、间隔与历史</span></summary><div class="qq-chooser"><label>库存发现方式<select name="discovery_mode"><option value="exact"${n.discovery_mode==='exact'?' selected':''}>逐项精确发现</option><option value="native"${n.discovery_mode==='native'?' selected':''}>独立小子网：直接读取清单</option></select></label><p class="muted">直接读取仅用于与主网隔离、库存少于 128 种的小子网；清单构建仍经过 AE 存储接口。</p><label>目标采集间隔（秒）<input name="interval_seconds" type="number" min="1" max="3600" value="${n.interval_seconds||1}" required></label><label>历史保存间隔（秒）<input name="history_seconds" type="number" min="5" max="3600" value="${n.history_seconds||10}" required></label><label>观测有效期（秒）<input name="stale_seconds" type="number" min="10" max="10800" value="${n.stale_seconds||30}" required></label></div></details>`}

 async function openConfig(create=false){const n=create?{}:view.subnet,captured=base(),d=dialog(create?'新增子网 OC':'连接与采集设置',`<form class="qq-chooser">${configInput(n)}<p class="muted">目标最快 1 秒，实际周期受游戏 tick 和服务器总预算限制。一台 OC 只连接一个子 ME 网络。</p><p class="error" data-error></p><button class="primary">${create?'创建并获取安装命令':'保存设置'}</button></form>${!create?'<button class="text-button" data-reset-token>重置 OC 安装令牌</button>':''}`);
  d.querySelector('[data-reset-token]')?.addEventListener('click',async()=>{try{const v=await request(captured+'/'+n.id+'/token',{method:'POST'});d.close();dialog('新的 OC 安装令牌',`<p>旧令牌已失效。在原专用 OC 执行：</p><pre>ae-orbit-subnet-setup ${esc(location.origin)} ${esc(v.token)}
reboot
ae-orbit start</pre>`);await refresh()}catch(e){toast(e.message,true)}});
  d.querySelector('form').onsubmit=async e=>{e.preventDefault();try{if(captured!==base())throw Error('服务器组已变化');const f=new FormData(e.target),body={name:f.get('name'),component_address:f.get('component_address')||'',location:f.get('location'),interval_seconds:Number(f.get('interval_seconds')),history_seconds:Number(f.get('history_seconds')),stale_seconds:Number(f.get('stale_seconds')),enabled:f.has('enabled'),revision:n.revision||0,discovery_mode:f.get('discovery_mode')};const v=await request(captured+(create?'':'/'+n.id),{method:create?'POST':'PUT',body});d.close();if(create){selected=v.id;const origin=location.origin;dialog('安装专用子网 OC',`<p>只在新的专用 OC 执行，令牌仅本次显示。</p><pre class="monitor-install-command">wget -f ${esc(origin)}/oc/install.lua /tmp/ae-orbit-install.lua
lua /tmp/ae-orbit-install.lua ${esc(origin)}
ae-orbit-subnet-setup ${esc(origin)} ${esc(v.token)}
reboot
ae-orbit start</pre><p>启动后回到子网页面，选择实际 ME 组件，再添加库存。</p>`)}await refresh()}catch(error){d.querySelector('[data-error]').textContent=error.message}};
 }
 function addItems(){
  const captured=base()+'/'+selected,version=generation,d=dialog('选择子网实际库存','<div class="subnet-search"><select data-kind><option value="item">物品</option><option value="fluid">流体</option></select><input type="search" data-search placeholder="中文、拼音、首字母或 @modid" aria-label="搜索子网库存"></div><div data-discovery-status></div><p class="muted">这里只显示本子网精确查询成功且数量大于零的物品。首次发现会逐步增加；直接读取模式按 ME 实际身份显示，NESQL 提供中文和纹理。</p><div data-results></div><div class="subnet-picker-pages"><button class="secondary" data-prev>上一页</button><span data-page></span><button class="secondary" data-next>下一页</button></div><p class="error" data-error></p>');let serial=0,offset=0,busy=false;
  async function search(){if(busy||!d.open||version!==generation)return;busy=true;const k=++serial,query=d.querySelector('[data-search]').value,kind=d.querySelector('[data-kind]').value;
   try{const page=await request(captured+'/discovered?kind='+kind+'&q='+encodeURIComponent(query)+'&offset='+offset);if(k!==serial||!d.open||version!==generation)return;
    mark(d.querySelector('[data-discovery-status]'),subnetDiscoveryStatusHTML(page.status,view?.subnet));
    pickerItems=page.items;pickerPaint=()=>mark(d.querySelector('[data-results]'),pickerItems.map(i=>`<button class="subnet-search-row" data-add-id="${i.id}"${i.monitored?' disabled':''}><span>${media(i)}<strong>${esc(i.label)}</strong><small>${esc(i.name)} · ${i.damage}${i.nbt?' · NBT':''} · ${formatQuantity(i.quantity)} ${i.kind==='fluid'?'mB':'件'}</small></span><b>${i.monitored?'已监控':'＋ 监控'}</b></button>`).join('')||'<p class="empty">尚未发现匹配库存。后台确认后会自动出现在这里。</p>');pickerPaint();
    d.querySelector('[data-prev]').disabled=offset===0;d.querySelector('[data-next]').disabled=offset+50>=page.total;d.querySelector('[data-page]').textContent=`${Math.floor(offset/50)+1} / ${Math.max(1,Math.ceil(page.total/50))} · 已发现 ${page.total} 种`;
   }catch(e){if(d.open)d.querySelector('[data-error]').textContent=e.message}finally{busy=false}}
  const change=()=>{offset=0;serial++;clearTimeout(searchTimer);searchTimer=setTimeout(search,250)};d.querySelector('[data-search]').oninput=change;d.querySelector('[data-kind]').onchange=change;
  d.querySelector('[data-prev]').onclick=()=>{offset=Math.max(0,offset-50);void search()};d.querySelector('[data-next]').onclick=()=>{offset+=50;void search()};
  d.querySelector('[data-results]').onclick=async e=>{const b=e.target.closest('[data-add-id]');if(!b||b.disabled)return;try{b.disabled=true;await request(captured+'/items',{method:'POST',body:{item_id:b.dataset.addId}});await refresh();await search()}catch(e){d.querySelector('[data-error]').textContent=e.message;b.disabled=false}};
  const timer=setInterval(()=>void search(),2000);d.addEventListener('close',()=>{clearInterval(timer);clearTimeout(searchTimer);serial++;pickerItems=[];pickerPaint=null},{once:true});void search();
 }

 async function editKeywords(itemID){
  const item=view.items.find(i=>i.id===itemID),captured=base()+'/'+selected,version=generation;
  const d=dialog('QQ 关键词 · '+item.label,`<form class="qq-chooser"><label>查询关键词<textarea name="keywords" rows="4" placeholder="燃料，产线燃料">${esc((item.keywords||[]).join('，'))}</textarea></label><p class="muted">用逗号或换行分隔，最多 8 个，每个 1–32 字。群里直接发送“燃料”或“库存 燃料”即可查询。多个监控项可共用关键词，回复会逐项列出；数秒后生效。</p><p class="muted">消息包含关键词就会回复，例如“硅岩燃料还有多少”或“/硅岩燃料”。电量、蓝波顿、蓝博顿、ae电量、库存保留给已有命令。留空可移除所有关键词。</p><p class="error" data-error></p><button class="primary">保存关键词</button></form>`);
  d.querySelector('form').onsubmit=async e=>{e.preventDefault();const b=e.target.querySelector('button');b.disabled=true;try{
   if(version!==generation||captured!==base()+'/'+selected)throw Error('子网已变化，请重新打开。');
   const keywords=subnetKeywordValues(new FormData(e.target).get('keywords'));
   await request(captured+'/items/'+itemID+'/keywords',{method:'PUT',body:{keywords}});d.close();await refresh();toast('QQ 关键词已保存');
  }catch(error){d.querySelector('[data-error]').textContent=error.message}finally{b.disabled=false}};
 }
 async function editRule(itemID){const item=view.items.find(i=>i.id===itemID),existing=view.rules.filter(r=>r.item_id===itemID&&r.member_id===member().id),captured=base()+'/'+selected;
  const version=generation;const cfg=await request(`/api/v1/monitoring/groups/${encodeURIComponent(group)}/qq`),groups=cfg.groups.filter(g=>g.enabled),current=existing.find(r=>r.enabled)||existing[0];
  if(version!==generation||captured!==base()+'/'+selected)return;const d=dialog('库存预警 · '+item.label,`<form class="qq-chooser"><input type="hidden" name="item_id" value="${itemID}"><label>规则<select name="kind">${Object.entries(rules).map(([k,v])=>`<option value="${k}"${current?.kind===k?' selected':''}>${v}</option>`).join('')}</select></label><label>触发数量（${item.kind==='fluid'?'mB':'件'}）<input name="threshold" inputmode="numeric" value="${current?.threshold||'1000'}" required></label><label>解除数量<input name="recover" inputmode="numeric" value="${current?.recover||'1500'}" required></label><label>持续秒数（0 立即）<input name="duration" type="number" min="0" max="3600" value="${current?.duration_seconds??3}"></label><label>QQ 通知群<select name="group"><option value="">仅站内预警</option>${groups.map(g=>`<option value="${g.group_id}"${current?.notification?.group_id===g.group_id?' selected':''}>${esc(g.name||g.group_id)}</option>`).join('')}</select></label><label>@ QQ（留空普通播报）<input name="recipient" inputmode="numeric" value="${esc(current?.notification?.recipient||'')}"></label><p class="muted">每次越界只预警一次，恢复不播报。失败或过期数据不会判为零。</p><p class="error" data-error></p><button class="primary">保存预警</button></form><div class="subnet-rule-list">${existing.filter(r=>r.enabled).map(r=>`<button class="text-button" data-remove-rule="${r.id}">关闭${rules[r.kind]}预警</button>`).join('')}</div>`);
  d.querySelector('form').onsubmit=async e=>{e.preventDefault();try{const f=new FormData(e.target),body=subnetRuleValues(f),prior=existing.find(r=>r.kind===body.kind);body.revision=prior?.revision||0;body.notification=f.get('group')?{group_id:f.get('group'),recipient:String(f.get('recipient')).trim(),private_copy:false}:null;await request(captured+'/rules'+(prior?'/'+prior.id:''),{method:prior?'PUT':'POST',body});d.close();await refresh();toast('库存预警已保存')}catch(error){d.querySelector('[data-error]').textContent=error.message}};
  d.querySelectorAll('[data-remove-rule]').forEach(b=>b.onclick=async()=>{try{await request(captured+'/rules/'+b.dataset.removeRule,{method:'DELETE'});d.close();await refresh()}catch(e){toast(e.message,true)}});
 }
 root.addEventListener('input',e=>{if(e.target.matches('[data-monitor-search]')){itemSearch=e.target.value;render()}});
 root.addEventListener('change',e=>{if(e.target.matches('[data-subnet-switch]')){generation++;loading=false;selected=e.target.value;view=null;itemSearch='';itemFilter='all';void refresh()}});
 root.addEventListener('click',e=>{const b=e.target.closest('button');if(!b)return;if(b.dataset.itemSettings){itemSettings(b.dataset.itemSettings)}else if(b.dataset.subnetFilter){itemFilter=b.dataset.subnetFilter;render()}else if(b.dataset.subnetSelect){generation++;loading=false;selected=b.dataset.subnetSelect;view=null;void refresh()}else if(b.hasAttribute('data-subnet-create'))void openConfig(true);else if(b.hasAttribute('data-subnet-config'))void openConfig();else if(b.hasAttribute('data-subnet-add'))addItems();else if(b.dataset.removeItem){void request(base()+'/'+selected+'/items/'+b.dataset.removeItem,{method:'DELETE'}).then(refresh).catch(e=>toast(e.message,true))}else if(b.dataset.keywordsItem)void editKeywords(b.dataset.keywordsItem).catch(e=>toast(e.message,true));else if(b.dataset.ruleItem)void editRule(b.dataset.ruleItem).catch(e=>toast(e.message,true));else if(b.dataset.historyItem){const captured=base()+'/'+selected,item=view.items.find(i=>i.id===b.dataset.historyItem);void request(captured+'/history/'+item.id).then(xs=>dialog('库存趋势 · '+item.label,subnetHistoryHTML(xs))).catch(e=>toast(e.message,true))}});
 market.addEventListener('click',e=>{if(e.target.closest('[data-subnet-open]'))showView('plugin-subnet');const b=e.target.closest('[data-subnet-toggle]');if(b&&!b.disabled){b.disabled=true;b.textContent=plugin?.enabled?'正在停用…':'正在准备目录…';void request(base()+'/plugin',{method:'PUT',body:{enabled:!plugin?.enabled,revision:plugin?.revision||0}}).then(refresh).catch(e=>toast(e.message,true)).finally(()=>{b.disabled=false;marketRender()})}});
 const observer=new MutationObserver(marketRender);observer.observe(market,{childList:true,subtree:true});
 const timer=setInterval(()=>{if(!root.hidden&&document.visibilityState==='visible')void refresh()},1000);
 function reset(){pickerItems=[];pickerPaint=null;itemSearch='';itemFilter='all';generation++;loading=false;group='';plugin=null;list=[];view=null;selected='';dialogNode?.close();changed({plugin:{id:'aeorbit.subnet'},enabled:false});render()}
 return{refresh,textureItem,refreshTextures:()=>{render();if(dialogNode?.open)pickerPaint?.()},show:()=>void refresh(),sessionChanged:()=>{reset();void refresh()},networkChanged:()=>{generation++;loading=false;dialogNode?.close();view=null;void refresh()},reset,destroy:()=>{clearInterval(timer);observer.disconnect()}};
}
