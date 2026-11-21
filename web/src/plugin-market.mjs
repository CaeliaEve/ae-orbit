import { lscConsoleHTML } from "./lsc-console.mjs";
const esc = v => String(v ?? '').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
export const energyValue = s => /^(0|[1-9][0-9]{0,127})$/.test(String(s)) ? BigInt(s) : null;
export function energyText(s) { const n=energyValue(s);return n===null?'—':n.toLocaleString('zh-CN'); }
export function energyPercent(stored,capacity) {const a=energyValue(stored),b=energyValue(capacity);return a===null||b===null||b===0n?null:Number(a*10000n/b)/100;}
export function energyAggregate(objects) {let stored=0n,capacity=0n,count=0;for(const o of objects){if(o.status!=='online')continue;const a=energyValue(o.stored_eu),b=energyValue(o.capacity_eu);if(a===null||b===null||b===0n)continue;stored+=a;capacity+=b;count++;}return{count,stored:String(stored),capacity:String(capacity),percent:energyPercent(stored,capacity)};}
const statusLabels={online:'正在采集',offline:'OC 离线',revoked:'采集器已撤销',paused:'已暂停',waiting_collector:'等待 OC 连接',upgrade_required:'等待 OC 更新',waiting_sample:'等待首次观测',read_error:'读取异常',stale:'数据待更新'};
const alertLabels={low_energy:'低电量',collector_offline:'OC 离线',read_error:'读取异常',capacity_changed:'容量变化',falling:'持续下降'};
const dt=v=>v?new Date(v).toLocaleString('zh-CN',{hour12:false}):'等待观测';
export function energyChart(points=[]) {
 const valid=points.filter(p=>energyPercent(p.stored_eu,p.capacity_eu)!==null&&Number.isFinite(Date.parse(p.at)));
 if(!valid.length)return '<div class="empty">尚未积累储能记录。连接监控 OC 后自动开始。</div>';
 const min=Date.parse(valid[0].at),max=Date.parse(valid.at(-1).at),width=800,height=190;
 let lastEpoch=null;const lines=[];let line=[];
 for(const p of valid){if(lastEpoch!==null&&p.epoch!==lastEpoch){lines.push(line);line=[];}line.push([40+(Date.parse(p.at)-min)/(max-min||1)*740,20+(100-energyPercent(p.stored_eu,p.capacity_eu))/100*140,p]);lastEpoch=p.epoch;}if(line.length)lines.push(line);
 return `<svg class="energy-chart" viewBox="0 0 ${width} ${height}" role="img" aria-label="储能占比随时间变化；断线和容量变化分段显示">${[0,25,50,75,100].map(v=>`<line x1="40" x2="780" y1="${20+(100-v)*1.4}" y2="${20+(100-v)*1.4}" class="energy-grid"/><text x="2" y="${24+(100-v)*1.4}">${v}%</text>`).join('')}${lines.map(xs=>`<polyline points="${xs.map(([x,y])=>`${x},${y}`).join(' ')}"/>${xs.length<100?xs.map(([x,y,p])=>`<circle cx="${x}" cy="${y}" r="2"><title>${esc(dt(p.at))} · ${esc(energyText(p.stored_eu))} EU · ${energyPercent(p.stored_eu,p.capacity_eu)}%</title></circle>`).join(''):''}`).join('')}<text x="40" y="185">${esc(dt(valid[0].at))}</text><text x="780" y="185" text-anchor="end">${esc(dt(valid.at(-1).at))}</text></svg>`;
}
export function energyTrend(points=[]){
 const xs=points.filter(p=>energyValue(p.stored_eu)!==null).slice(-6);if(xs.length<3)return null;
 const first=xs[0],last=xs.at(-1),seconds=(Date.parse(last.at)-Date.parse(first.at))/1000;
 if(seconds<20||xs.some(p=>p.epoch!==last.epoch||p.capacity_eu!==last.capacity_eu))return null;
 const difference=energyValue(last.stored_eu)-energyValue(first.stored_eu);
 return {direction:difference>0n?'充能':difference<0n?'放能':'平稳',perSecond:(difference*1000n/BigInt(Math.round(seconds*1000))).toString()};
}
export function createPluginMarket({root,runtimeRoot,widget,request,member,network,showView,toast,onPluginsChanged=()=>{},storage=localStorage}){
 const state={group:'',groups:[],installations:[],collectors:[],objects:[],alerts:[],tab:'monitor',managementTab:'market',selected:'',history:null,liveHistory:null,historyError:'',metric:'storage',historyGeneration:0,hours:1,from:'',to:'',loading:false,error:'',generation:0,busy:false};
 let owner='',timer=null,lastRender=new Map();
 const path=s=>`/api/v1/monitoring/groups/${encodeURIComponent(state.group)}${s}`;
 const isAdmin=()=>!!member()?.admin;
 const pinKey=()=>`ae-orbit:monitor-pins:${member()?.id}:${state.group}`;
 function pins(){try{return JSON.parse(storage.getItem(pinKey())||'[]').filter(x=>typeof x==='string');}catch{return[];}}
 function renderWidget(){
  if(!widget)return;const enabled=state.installations.some(v=>v.plugin.id==='gtnh.lsc'&&v.enabled),pinned=new Set(pins()),xs=state.objects.filter(o=>enabled&&pinned.has(o.id)).map(o=>o.status==='online'&&Date.now()-Date.parse(o.observed_at)>(state.installations[0]?.settings.offline_seconds||60)*1000?{...o,status:'stale'}:o);widget.hidden=!xs.length;
  widget.innerHTML=xs.map(o=>`<button class="energy-widget" data-monitor-open="${esc(o.id)}"><span>${esc(o.name)} <small>${esc(statusLabels[o.status]||o.status)}</small></span><strong>${o.percent||'—'}<small>%</small></strong><span>${esc(energyText(o.stored_eu))} EU</span><progress max="100" value="${energyPercent(o.stored_eu,o.capacity_eu)||0}"></progress></button>`).join('');
 }
 function render(){renderWidget();renderSurface(root,state.managementTab,false);if(runtimeRoot)renderSurface(runtimeRoot,state.tab,true);}
 function renderSurface(target,tab,runtime){
  if(!target)return;
  const active=state.alerts.filter(a=>a.state==='active'),installation=state.installations[0];const aggregate=energyAggregate(state.objects);
  let content='';
  if(tab==='market') content=state.installations.map(v=>`<article class="market-plugin"><div class="market-plugin-symbol">EU</div><div><span class="plugin-kicker">官方 · 只读 · v${esc(v.plugin.version)}</span><h2>${esc(v.plugin.name)}</h2><p>${esc(v.plugin.description)}</p><div class="plugin-facts"><span>独立监控 OC</span><span>默认每 ${v.settings.interval_seconds} 秒采集</span><span>${state.objects.length} 座已绑定</span></div><p class="muted">需要因特网卡和适配器，适配器连接超级电容库控制器。最多监控 16 座，按队列轮询；超过 8 座时实际更新间隔可能高于 10 秒。</p><p>${!v.enabled?'尚未启用':!state.collectors.some(c=>c.online)?'已启用 · 等待监控 OC 连接':!state.objects.length?'已启用 · 等待绑定电容库':'已启用 · 在监控页查看采集状态'}</p><div class="energy-actions">${isAdmin()?`<button class="primary" data-toggle-plugin>${v.enabled?'停用插件':'启用插件'}</button>`:'<span class="muted">由管理员启用和配置</span>'}<button class="secondary" data-tab="collectors">连接监控 OC</button>${v.enabled?'<button class="secondary" data-open-monitor>打开储能监控</button>':''}</div></div></article>${isAdmin()?settingsHTML(v):''}`).join('');
  if(tab==='collectors')content=`<div class="plugin-section-heading"><div><h2>独立监控采集器</h2><p class="muted">每台 OC 使用自己的监控令牌，无需 ME 连接。</p></div>${isAdmin()?'<button class="primary" data-create-collector>新增监控 OC</button>':''}</div>${state.collectors.map(c=>`<article class="collector-row"><div><strong>${esc(c.name)}</strong><small>${c.revoked?'已撤销':c.online?'在线':'等待连接／离线'} · ${esc(dt(c.last_seen))} · 空闲 ${Math.round(c.free_memory/1024)} KiB</small><small>${c.components.length} 个 LSC 组件 · ${c.modules.includes('gtnh.lsc')?'支持蓝波顿插件':'等待新版监控程序'}</small></div>${isAdmin()&&!c.revoked?`<div class="energy-actions"><button class="text-button" data-bind="${esc(c.id)}" ${!c.components.some(x=>x.supported)?'disabled':''}>绑定电容库</button><button class="text-button" data-rotate="${esc(c.id)}">更换令牌</button><button class="text-button" data-revoke="${esc(c.id)}">撤销</button></div>`:''}</article>`).join('')||'<div class="empty">尚无监控 OC。管理员创建后，在新 OC 安装并填写独立令牌。</div>'}`;
  if(tab==='monitor') content=lscConsoleHTML(state.objects.find(o=>o.id===state.selected),{history:state.history,livePoints:state.liveHistory?.points||[],objects:state.objects,collectors:state.collectors,alerts:state.alerts,admin:isAdmin(),pinned:pins().includes(state.selected),metric:state.metric,chartWidth:target.clientWidth-64,hours:state.hours,from:state.from,to:state.to,historyError:state.historyError,staleSeconds:installation?.settings.offline_seconds||60});
  if(tab==='alerts')content=`<div class="plugin-section-heading"><div><h2>站内告警</h2><p class="muted">服务端持续判断 · 确认表示已读，恢复由实际观测决定 · 显示最近 200 条</p></div></div>${state.alerts.map(a=>`<article class="monitor-alert" data-state="${esc(a.state)}"><div><strong>${esc(a.object_name)} · ${esc(alertLabels[a.kind]||a.kind)}</strong><p>${esc(a.detail)}</p><small>触发 ${esc(dt(a.triggered_at))}${a.resolved_at?` · 结束 ${esc(dt(a.resolved_at))}`:''}${a.acknowledged_at?` · ${esc(a.acknowledged_by)} 已确认`:''}</small></div><span>${a.state==='active'?'当前告警':a.resolve_reason==='monitoring_disabled'?'监控已停用':a.resolve_reason==='observation_unavailable'?'监控中断':a.resolve_reason==='acknowledged'?'已确认':'已恢复'}</span>${!a.acknowledged_at?`<button class="secondary" data-ack="${esc(a.id)}">确认已读</button>`:''}</article>`).join('')||'<div class="empty">暂无告警</div>'}`;
  const tabs=runtime?[['monitor','电容库'],['alerts',`告警${active.length?' · '+active.length:''}`]]:[['market','插件与配置'],['collectors','连接设备']];
  const title=runtime?'蓝波顿储能监控':'插件市场';
  const contentBody=!member()?'<div class="empty">登录后查看。</div>':!state.group?'<div class="empty">暂无可访问的服务器组。管理员可先在网络管理中创建游戏服务器组。</div>':runtime&&!installation?.enabled?'<div class="empty">此服务器组尚未启用储能插件。<button class="text-button" data-open-market>前往插件市场</button></div>':content;
  const html=`<div class="plugin-top"><div><h1 class="sr-only">${title}</h1><p class="muted">${runtime?'单台设备控制台':'按需启用、配置插件并连接设备。'}</p></div><label>游戏服务器组<select data-monitor-group>${state.groups.map(g=>`<option value="${esc(g)}" ${g===state.group?'selected':''}>${esc(g)}</option>`).join('')}</select></label></div><div class="plugin-tabs" role="group" aria-label="${title}">${tabs.map(([k,t])=>`<button data-tab="${k}" aria-pressed="${tab===k}">${t}</button>`).join('')}${runtime?'<button class="text-button" data-open-market>管理插件与设备</button>':''}</div>${state.error?`<p class="error" role="alert">${esc(state.error)} · 保留上次数据</p>`:''}${contentBody}`;
  if(html===lastRender.get(target))return;
  if(target.contains(document.activeElement)&&document.activeElement.matches('input,select,textarea')&&!state.busy)return;
  const scroll=target.scrollTop;
  const open=[...target.querySelectorAll('[data-lsc-disclosure][open]')].map(e=>e.dataset.lscDisclosure);
  target.innerHTML=html;lastRender.set(target,html);
  for(const name of open)target.querySelector(`[data-lsc-disclosure="${name}"]`)?.setAttribute('open','');
  target.querySelectorAll('[data-lsc-fill]').forEach(e=>e.style.width=`${Math.max(0,Math.min(100,Number(e.dataset.lscFill)))}%`);
  target.scrollTop=scroll;
 }

 function settingsHTML(v){const s=v.settings;return `<details class="plugin-settings"><summary>采集、告警与历史设置</summary><form data-plugin-settings><div class="plugin-form-grid">${[['interval_seconds','采集间隔（秒）',10,3600],['low_percent','低电量阈值（%）',1,95],['recover_percent','恢复阈值（%）',2,100],['low_duration_seconds','低电量持续时间（秒）',10,3600],['offline_seconds','离线／过期阈值（秒）',30,10800],['falling_minutes','持续下降告警（分钟，0 关闭）',0,1440],['raw_retention_days','原始记录天数（0 永久保留）',0,3650]].map(([k,label,min,max])=>`<label>${label}<input type="number" name="${k}" min="${min}" max="${max}" value="${s[k]}" required></label>`).join('')}<label><span>站内告警</span><input type="checkbox" name="alerts" ${s.alerts?'checked':''}></label></div><p class="muted">默认原始记录保留 30 天，小时／日汇总长期保留。选择 0 保留全部原始记录。此设置仅影响机器监控历史。</p><button class="primary" type="submit">保存设置</button></form></details>`;}
 async function loadHistory(){
  if(!state.selected)return;
  const generation=state.generation,requestGeneration=++state.historyGeneration,id=state.selected;
  const now=new Date(),to=state.to?new Date(state.to):now,from=state.from?new Date(state.from):new Date(to.getTime()-state.hours*3600000);
  if(!Number.isFinite(from.getTime())||!Number.isFinite(to.getTime())||from>=to)throw Error('请选择有效的开始和结束时间。');
  const historyPath=(a,b)=>path(`/objects/${id}/history?from=${encodeURIComponent(a.toISOString())}&to=${encodeURIComponent(b.toISOString())}`);
  const viewed=historyPath(from,to),live=state.hours===1&&!state.from&&!state.to?viewed:historyPath(new Date(now.getTime()-300000),now);
  try{
   const [h,l]=await Promise.all([request(viewed),live===viewed?null:request(live)]);
   if(generation===state.generation&&state.selected===id&&requestGeneration===state.historyGeneration){state.history=h;state.liveHistory=l||h;state.historyError='';render();}
  }catch(e){if(generation===state.generation&&state.selected===id&&requestGeneration===state.historyGeneration){state.historyError=e.message;render();}}
 }

 async function refresh(){
  if(!member()){reset();render();return;}if(state.loading)return;
  const generation=state.generation,identity=member().id;state.loading=true;
  try{
   const groups=await request('/api/v1/monitoring/groups');if(generation!==state.generation||member()?.id!==identity)return;state.groups=groups;
   if(!groups.includes(state.group))state.group=groups.includes(network()?.server_group)?network().server_group:groups[0]||'';
   if(!state.group){state.installations=[];state.collectors=[];state.objects=[];state.alerts=[];onPluginsChanged([]);render();return;}
   const [installations,collectors,objects,alerts]=await Promise.all([request(path('/plugins')),request(path('/collectors')),request(path('/objects')),request(path('/alerts'))]);
   if(generation!==state.generation||member()?.id!==identity)return;
   Object.assign(state,{installations:installations.filter(v=>v.plugin?.id==='gtnh.lsc'),collectors,objects,alerts,error:''});if(!objects.some(o=>o.id===state.selected)){state.selected=objects[0]?.id||'';state.history=null;state.liveHistory=null;}onPluginsChanged(state.installations);render();if(state.selected&&runtimeRoot&&!runtimeRoot.hidden&&state.tab==='monitor')await loadHistory();
  }catch(e){if(generation===state.generation){state.error=e.message;render();}}
  finally{if(generation===state.generation)state.loading=false;}
 }
 function reset(){state.generation++;state.group='';state.groups=[];state.installations=[];state.collectors=[];state.objects=[];state.alerts=[];state.selected='';state.history=null;state.liveHistory=null;state.historyError='';state.historyGeneration++;state.error='';state.loading=false;lastRender.clear();onPluginsChanged([]);document.querySelector('#plugin-dialog')?.close();render();}
 function sessionChanged(){const id=member()?.id||'';if(id!==owner){owner=id;reset();}void refresh();}
 async function mutate(fn){if(state.busy)return;state.busy=true;try{await fn();await refresh();}catch(e){toast(e.message,true);}finally{state.busy=false;render();}}
 function dialog(title,html){let d=document.querySelector('#plugin-dialog');if(!d){d=document.createElement('dialog');d.id='plugin-dialog';document.body.append(d);}d.innerHTML=`<div class="dialog-heading"><h2>${esc(title)}</h2><button class="icon-button" data-plugin-close aria-label="关闭">×</button></div>${html}`;d.querySelector('[data-plugin-close]').onclick=()=>d.close();d.showModal();return d;}
 function showToken(token){const origin=location.origin;const d=dialog('连接专用监控 OC',`<p>令牌仅本次显示。请保存后在新 OC 执行以下命令。适配器连接电容库控制器，随后回到此页绑定。</p><pre class="monitor-install-command">wget -f ${esc(origin)}/oc/install.lua /tmp/ae-orbit-install.lua &amp;&amp; lua /tmp/ae-orbit-install.lua ${esc(origin)}\nae-orbit-monitor-setup ${esc(origin)} ${esc(token)}\nreboot</pre><p>重启后执行 <code>ae-orbit start</code>。已有开机启动配置的机器会自动运行。</p><button class="secondary" data-copy-monitor>复制安装命令</button>`);d.querySelector('[data-copy-monitor]').onclick=async()=>{try{await navigator.clipboard.writeText(d.querySelector('pre').textContent);toast('已复制。');}catch{toast('请选中命令手动复制。',true);}};d.addEventListener('close',()=>{d.innerHTML='';},{once:true});}
 for(const surface of [root,runtimeRoot].filter(Boolean)){
 surface.addEventListener('change',e=>{if(e.target.matches('[data-lsc-object]')){state.selected=e.target.value;state.history=null;state.liveHistory=null;render();void loadHistory();}else if(e.target.matches('[data-monitor-group]')){state.generation++;state.loading=false;state.group=e.target.value;state.selected='';state.history=null;state.liveHistory=null;state.historyError='';state.historyGeneration++;state.objects=[];state.collectors=[];state.installations=[];state.alerts=[];render();void refresh();}});
 surface.addEventListener('submit',e=>{if(e.target.matches('[data-plugin-settings]')){e.preventDefault();const f=new FormData(e.target),v=state.installations[0],settings={};for(const k of Object.keys(v.settings))settings[k]=k==='alerts'?f.has(k):Number(f.get(k));void mutate(()=>request(path(`/plugins/${v.plugin.id}`),{method:'PUT',body:{enabled:v.enabled,revision:v.revision,settings}}));}else if(e.target.matches('[data-energy-range]')){e.preventDefault();const f=new FormData(e.target);state.from=f.get('from');state.to=f.get('to');void loadHistory().catch(e=>toast(e.message,true));}});
 surface.addEventListener('click',e=>{const b=e.target.closest('button');if(!b)return;const v=state.installations[0];
  if(b.dataset.tab){if(['monitor','alerts'].includes(b.dataset.tab)){state.tab=b.dataset.tab;if(surface===root)showView('plugin-lsc');}else{state.managementTab=b.dataset.tab;if(surface===runtimeRoot)showView('plugins');}render();void refresh();}
  else if(b.hasAttribute('data-open-monitor'))showView('plugin-lsc');
  else if(b.hasAttribute('data-open-market')){state.managementTab='market';showView('plugins');}
  else if(b.hasAttribute('data-plugin-refresh'))void refresh();
  else if(b.hasAttribute('data-toggle-plugin')&&v)void mutate(async()=>{await request(path(`/plugins/${v.plugin.id}`),{method:'PUT',body:{enabled:!v.enabled,revision:v.revision,settings:v.settings}});if(!v.enabled)toast('已启用。储能监控入口在“更多”中，可在设置 → 导航布局调整。');});
  else if(b.dataset.lscMetric){state.metric=b.dataset.lscMetric==='flow'?'flow':'storage';render();}
  else if(b.dataset.object){state.selected=b.dataset.object;state.history=null;state.liveHistory=null;state.tab='monitor';render();void loadHistory();}
  else if(b.dataset.pin){const xs=pins(),id=b.dataset.pin;try{storage.setItem(pinKey(),JSON.stringify(xs.includes(id)?xs.filter(x=>x!==id):[...xs,id]));render();}catch{toast('主页设置无法保存。',true);}}
  else if(b.dataset.pauseObject){const o=state.objects.find(x=>x.id===b.dataset.pauseObject);if(o)void mutate(()=>request(path(`/objects/${o.id}`),{method:'PUT',body:{plugin_id:o.plugin_id,collector_id:o.collector_id,component_address:o.component_address,name:o.name,location:o.location,revision:o.revision,enabled:!o.enabled}}));}
  else if(b.dataset.ack)void mutate(()=>request(path(`/alerts/${b.dataset.ack}/acknowledge`),{method:'POST',body:{}}));
  else if(b.dataset.energyHours){state.hours=Number(b.dataset.energyHours);state.from='';state.to='';void loadHistory().catch(e=>toast(e.message,true));}
  else if(b.hasAttribute('data-energy-export')){const xs=state.history?.points||[];const csv='时间,储能EU,容量EU,最小储能EU,最大储能EU,采样次数,输入EU每tick,输出EU每tick,损耗EU每tick,五秒平均输入EU每tick,五秒平均输出EU每tick\n'+xs.map(p=>[p.at,p.stored_eu,p.capacity_eu,p.min_eu,p.max_eu,p.samples,p.flow?.input_eut||'',p.flow?.output_eut||'',p.flow?.loss_eut||'',p.flow?.avg_input_eut||'',p.flow?.avg_output_eut||''].join(',')).join('\n');const url=URL.createObjectURL(new Blob(['\ufeff'+csv],{type:'text/csv;charset=utf-8'}));const a=document.createElement('a');a.href=url;a.download='储能趋势.csv';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);}
  else if(b.hasAttribute('data-create-collector')){const d=dialog('新增监控 OC','<form><label>采集器名称<input name="name" value="储能监控 OC" maxlength="128" required></label><button class="primary" type="submit">创建并获取安装命令</button></form>');d.querySelector('form').onsubmit=e=>{e.preventDefault();const name=new FormData(e.target).get('name');void mutate(async()=>{const x=await request(path('/collectors'),{method:'POST',body:{name}});d.close();showToken(x.token);});};}
  else if(b.dataset.rotate){const id=b.dataset.rotate;const d=dialog('更换采集器令牌','<p>旧令牌会失效。需要在该 OC 更新配置后重新连接。</p><button class="primary" data-confirm>更换令牌</button>');d.querySelector('[data-confirm]').onclick=()=>void mutate(async()=>{const x=await request(path(`/collectors/${id}/token`),{method:'POST',body:{}});d.close();showToken(x.token);});}
  else if(b.dataset.revoke){const d=dialog('撤销采集器','<p>该 OC 将无法继续上报，历史数据保留。</p><button class="primary" data-confirm>撤销采集器</button>');d.querySelector('[data-confirm]').onclick=()=>void mutate(async()=>{await request(path(`/collectors/${b.dataset.revoke}`),{method:'DELETE'});d.close();});}
  else if(b.dataset.bind){const c=state.collectors.find(x=>x.id===b.dataset.bind);if(!c)return;const used=new Set(state.objects.filter(o=>o.collector_id===c.id).map(o=>o.component_address));const xs=c.components.filter(x=>x.supported&&!used.has(x.address));const d=dialog('绑定超级电容库',`<form><label>控制器组件<select name="address" required>${xs.map(x=>`<option>${esc(x.address)}</option>`).join('')}</select></label><label>显示名称<input name="name" value="基地储能" maxlength="128" required></label><label>维度与坐标（建议填写，防止重复统计）<input name="location" placeholder="维度180 / 41,49,-341" maxlength="128"></label><button class="primary" type="submit" ${xs.length?'':'disabled'}>开始监控</button></form>`);d.querySelector('form').onsubmit=e=>{e.preventDefault();const f=new FormData(e.target);void mutate(async()=>{await request(path('/objects'),{method:'POST',body:{plugin_id:'gtnh.lsc',collector_id:c.id,component_address:f.get('address'),name:f.get('name'),location:f.get('location'),enabled:true,revision:0}});d.close();state.tab='monitor';showView('plugin-lsc');});};}
 });
 }
 widget?.addEventListener('click',e=>{const b=e.target.closest('[data-monitor-open]');if(b){state.tab='monitor';state.selected=b.dataset.monitorOpen;showView('plugin-lsc');void loadHistory().catch(e=>toast(e.message,true));}});
 timer=setInterval(()=>{if(member()&&document.visibilityState==='visible'&&(!root.hidden||runtimeRoot&&!runtimeRoot.hidden||widget&&!widget.hidden&&!widget.closest('.view')?.hidden))void refresh();},10000);
 window.addEventListener('resize',render);
 render();return{refresh,sessionChanged,reset,show:()=>{render();void refresh();},showPlugin:()=>{render();if(state.selected)void loadHistory();void refresh();},state,destroy:()=>{clearInterval(timer);window.removeEventListener('resize',render);}};
}
