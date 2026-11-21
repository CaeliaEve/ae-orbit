const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const uint = value => /^(0|[1-9][0-9]{0,127})$/.test(String(value)) ? BigInt(value) : null;
const signed = value => value === null ? '—' : `${value > 0n ? '+' : ''}${value.toLocaleString('zh-CN')}`;
const exact = value => value === null ? '—' : value.toLocaleString('zh-CN');
const date = value => value ? new Date(value).toLocaleString('zh-CN',{hour12:false}) : '等待观测';
const states={online:'实时连接',offline:'OC 离线',paused:'监控已暂停',revoked:'连接已撤销',waiting_sample:'等待首次观测',waiting_collector:'等待 OC 连接',upgrade_required:'等待 OC 更新',read_error:'读取异常',stale:'数据待更新'};
export function shortEnergy(value) {
  if(value===null)return {value:'—',unit:'EU'};
  const scales=[[10n**24n,'Y EU'],[10n**21n,'Z EU'],[10n**18n,'E EU'],[10n**15n,'P EU'],[10n**12n,'T EU'],[10n**9n,'G EU'],[10n**6n,'M EU'],[1000n,'k EU']];
  const abs=value<0n?-value:value;
  const [scale,unit]=scales.find(([n])=>abs>=n)||[1n,'EU'];
  const hundredths=abs*100n/scale;
  return {value:`${value<0n?'-':''}${(hundredths/100n).toLocaleString('zh-CN')}${scale>1n?'.'+String(hundredths%100n).padStart(2,'0'):''}`,unit};
}
function eta(seconds) {
  if(seconds===null)return '等待稳定趋势';
  if(seconds>86400n*9999n)return '超过 9,999 天';
  if(seconds>=86400n)return `${seconds/86400n} 天 ${seconds%86400n/3600n} 小时`;
  if(seconds>=3600n)return `${seconds/3600n} 小时 ${seconds%3600n/60n} 分钟`;
  if(seconds>=60n)return `${seconds/60n} 分钟 ${seconds%60n} 秒`;
  return `${seconds} 秒`;
}
export function lscConsoleModel(object,points=[],now=Date.now(),staleSeconds=60) {
  const stored=uint(object?.stored_eu),capacity=uint(object?.capacity_eu);
  const known=stored!==null&&capacity!==null&&capacity>0n&&stored<=capacity;
  const current=object?.status==='online'&&Number.isFinite(Date.parse(object.observed_at))&&now-Date.parse(object.observed_at)<=staleSeconds*1000;
  const f=object?.flow;
  const values=[f?.input_eut,f?.output_eut,f?.loss_eut,f?.avg_input_eut,f?.avg_output_eut].map(uint);
  const flow=current&&f?.source==='lsc_sensor_v1'&&f.window_seconds===5&&values.every(v=>v!==null)?{input:values[0],output:values[1],loss:values[2],averageInput:values[3],averageOutput:values[4],net:values[0]-values[1]-values[2]}:null;
  const xs=points.filter(p=>uint(p.stored_eu)!==null&&uint(p.capacity_eu)!==null&&Number.isFinite(Date.parse(p.at))).slice(-6);
  let rate=null,delta=null,seconds=null;
  if(current&&known&&xs.length>=3){
    const first=xs[0],last=xs.at(-1),elapsed=Date.parse(last.at)-Date.parse(first.at);
    if(elapsed>=20000&&xs.every(p=>p.epoch===object.epoch&&p.capacity_eu===object.capacity_eu)&&now-Date.parse(last.at)<=staleSeconds*1000&&xs.every((p,i)=>i===0||Date.parse(p.at)>Date.parse(xs[i-1].at))){
      delta=uint(last.stored_eu)-uint(first.stored_eu);seconds=Math.round(elapsed/1000);rate=delta*1000n/BigInt(elapsed);
    }
  }
  const remaining=known?capacity-stored:null;
  const duration=rate===null||rate===0n?null:rate>0n?(remaining+rate-1n)/rate:(stored-rate-1n)/(-rate);
  return {known,current,stored,capacity,remaining,percent:known?Number(stored*10000n/capacity)/100:null,flow,rate,delta,seconds,direction:rate===null?'等待趋势':rate>0n?'正在充能':rate<0n?'正在放能':'储能平稳',eta:eta(duration),etaLabel:rate!==null&&rate<0n?'预计耗尽':'预计充满'};
}
export function lscHistoryChart(points=[],metric='storage',width=980) {
  if(!points.length)return '<div class="lsc-chart-empty">等待记录积累，新的观测会自动出现在这里。</div>';
  const valid=points.filter(p=>Number.isFinite(Date.parse(p.at)));
  if(!valid.length)return '<div class="lsc-chart-empty">暂无有效时间记录。</div>';
  const min=Date.parse(valid[0].at),max=Date.parse(valid.at(-1).at);
  width=Math.max(350,Math.min(980,width));const right=width-24;
  const x=p=>56+(Date.parse(p.at)-min)/(max-min||1)*(right-56);
  const series=metric==='flow'?[['avg_input_eut','输入','in'],['avg_output_eut','输出','out']]:[['stored_eu','储能','storage']];
  const numbers=valid.flatMap(p=>series.map(([key])=>uint(metric==='flow'?p.flow?.[key]:p[key]))).filter(v=>v!==null);
  if(!numbers.length)return '<div class="lsc-chart-empty">该时间段尚未记录输入输出。更新监控 OC 后开始积累。</div>';
  const top=numbers.reduce((a,b)=>a>b?a:b,1n),bottom=metric==='flow'?0n:numbers.reduce((a,b)=>a<b?a:b,top);
  const span=top-bottom||1n;
  const y=n=>30+170-Number((n-bottom)*10000n/span)/10000*170;
  const ticks=Array.from({length:5},(_,i)=>bottom+span*BigInt(4-i)/4n);
  const paths=series.map(([key,label,tone])=>{
    let epoch=null,capacity=null;const segments=[];let segment=[];
    for(const p of valid){
      const n=uint(metric==='flow'?p.flow?.[key]:p[key]);
      if(n===null||epoch!==null&&(p.epoch!==epoch||p.capacity_eu!==capacity)){if(segment.length)segments.push(segment);segment=[];}
      if(n!==null)segment.push([x(p),y(n),p,n]);epoch=p.epoch;capacity=p.capacity_eu;
    }
    if(segment.length)segments.push(segment);
    return segments.map(xs=>`<polyline class="lsc-line-${tone}" points="${xs.map(([px,py])=>`${px},${py}`).join(' ')}"/>${xs.length<=120?xs.map(([px,py,p,n])=>`<circle class="lsc-dot-${tone}" cx="${px}" cy="${py}" r="2"><title>${esc(date(p.at))} · ${label} ${esc(exact(n))} ${metric==='flow'?'EU/t':'EU'}</title></circle>`).join(''):''}`).join('');
  }).join('');
  return `<svg class="lsc-chart" viewBox="0 0 ${width} 240" role="img" aria-label="${metric==='flow'?'五秒平均输入输出':'储能数量'}随时间变化，观测中断后分段"><g>${ticks.map((n,i)=>`<line x1="56" x2="${right}" y1="${30+i*42.5}" y2="${30+i*42.5}"/><text x="47" y="${34+i*42.5}" text-anchor="end">${esc(shortEnergy(n).value+' '+shortEnergy(n).unit.replace(' EU','').replace('EU',''))}</text>`).join('')}</g>${paths}<text x="56" y="232">${esc(date(valid[0].at))}</text><text x="${right}" y="232" text-anchor="end">${esc(date(valid.at(-1).at))}</text></svg>`;
}
export function lscConsoleHTML(object,{history,livePoints=[],objects=[],collectors=[],alerts=[],admin=false,pinned=false,metric='storage',hours=1,from='',to='',historyError='',now=Date.now(),staleSeconds=60,chartWidth=980}={}) {
  if(!object)return '<div class="lsc-chart-empty">连接监控 OC 并绑定电容库后，这里将成为它的储能控制台。<button class="text-button" data-tab="collectors">连接设备</button></div>';
  const m=lscConsoleModel(object,livePoints,now,staleSeconds),amount=shortEnergy(m.stored),collector=collectors.find(c=>c.id===object.collector_id);
  const displayState=!m.current&&object.status==='online'?'stale':object.status;
  const localAlerts=alerts.filter(a=>a.object_id===object.id),active=localAlerts.filter(a=>a.state==='active');
  const flow=m.flow;
  const number=(n,unit='EU/t',sign=false)=>`<strong title="${esc(sign?signed(n):exact(n))} ${unit}">${esc(sign?signed(n):exact(n))}<small>${unit}</small></strong>`;
  const unavailable=object.flow_error==='invalid_format'?'当前接口格式未支持':object.flow_error==='read_failed'?'输入输出读取失败':object.flow_error==='unsupported'?'机器未提供输入输出接口':'更新监控 OC 后开始采集';
  const range=history?.points||[];
  return `<div class="lsc-console" data-state="${m.current?'online':esc(displayState)}">
    <div class="lsc-heading"><div><span class="lsc-eyebrow">LAPOTRONIC · ENERGY STORAGE</span><h2>${esc(object.name)}</h2><p>${esc(object.location||'基地储能')}<span class="lsc-state">${esc(states[m.current?'online':displayState]||'等待连接')}</span></p></div><div class="lsc-heading-actions">${objects.length>1?`<select data-lsc-object aria-label="选择电容库">${objects.map(o=>`<option value="${esc(o.id)}" ${o.id===object.id?'selected':''}>${esc(o.name)}</option>`).join('')}</select>`:''}<button class="text-button" data-pin="${esc(object.id)}">${pinned?'移出工作台':'添加到工作台'}</button>${admin?`<button class="text-button" data-pause-object="${esc(object.id)}">${object.enabled?'暂停监控':'恢复监控'}</button>`:''}</div></div>
    ${!m.current?`<p class="lsc-stale" role="status">${esc(states[displayState]||'数据待更新')} · 保留最后一次储能观测，输入输出与预计时间暂不可用。${object.error?` ${esc(object.error)}`:''}</p>`:''}
    <div class="lsc-overview"><section class="lsc-reservoir" aria-label="当前储能"><span class="lsc-label">当前储能</span><div class="lsc-stored" title="${esc(exact(m.stored))} EU"><strong>${esc(amount.value)}</strong><span>${amount.unit}</span></div><div class="lsc-capacity-line"><span>占用 ${m.percent===null?'—':m.percent.toFixed(2)+'%'}</span><span>总容量 <b title="${esc(exact(m.capacity))} EU">${esc(shortEnergy(m.capacity).value)} ${shortEnergy(m.capacity).unit}</b></span></div><div class="lsc-reservoir-track" role="meter" aria-label="电容库电量" aria-valuemin="0" aria-valuemax="100"${m.percent!==null?` aria-valuenow="${m.percent}"`:''}><div class="lsc-reservoir-fill" data-lsc-fill="${m.percent??0}"></div>${Array.from({length:19},()=>'<i></i>').join('')}</div><div class="lsc-reservoir-foot"><span>剩余可充空间<strong title="${esc(exact(m.remaining))} EU">${esc(shortEnergy(m.remaining).value)} ${shortEnergy(m.remaining).unit}</strong></span><span>${m.etaLabel}<strong>${m.eta}</strong></span></div><small class="lsc-observation">最后观测 ${esc(date(object.observed_at))}</small></section>
    <section class="lsc-flow" aria-label="电容库输入输出"><div class="lsc-flow-grid"><div class="lsc-flow-in"><span class="lsc-label">输入 <span>→</span></span>${number(flow?.input??null)}<small>5 秒均值 ${esc(exact(flow?.averageInput??null))} EU/t</small></div><div class="lsc-flow-out"><span class="lsc-label">输出 <span>→</span></span>${number(flow?.output??null)}<small>5 秒均值 ${esc(exact(flow?.averageOutput??null))} EU/t</small></div><div class="lsc-flow-loss"><span class="lsc-label">被动损耗</span>${number(flow?.loss??null)}<small>维护状态会影响自放电</small></div><div class="lsc-flow-net" data-direction="${flow===null?'unknown':flow.net>=0n?'in':'out'}"><span class="lsc-label">净流入 · 输入 − 输出 − 损耗</span>${number(flow?.net??null,'EU/t',true)}<small>${flow===null?esc(unavailable):flow.net>0n?'输入覆盖负载与损耗':flow.net<0n?'负载与损耗超过输入':'输入输出保持平衡'}</small></div></div><div class="lsc-observed-rate"><span>${m.direction}<small>最近 ${m.seconds??'—'} 秒储能实际变化</small></span>${number(m.rate,'EU/s',true)}</div></section></div>
    <section class="lsc-timeline"><div class="lsc-timeline-heading"><div><h3>能量轨迹</h3><p>${history?`${history.resolution==='raw'?'原始观测':history.resolution==='hour'?'小时汇总':'日汇总'} · 开始记录 ${esc(date(history.started_at))}`:'正在读取历史'}</p></div><div class="lsc-chart-switch" role="group" aria-label="历史指标"><button data-lsc-metric="storage" aria-pressed="${metric==='storage'}">储能</button><button data-lsc-metric="flow" aria-pressed="${metric==='flow'}">输入 / 输出</button></div></div><div class="lsc-timeline-tools"><div class="lsc-range-presets">${[[1,'1 小时'],[24,'24 小时'],[168,'7 天'],[720,'30 天']].map(([v,t])=>`<button data-energy-hours="${v}" aria-pressed="${hours===v&&!from}">${t}</button>`).join('')}</div><button class="text-button" data-energy-export ${!range.length?'disabled':''}>导出明细</button><details data-lsc-disclosure="range"><summary>自定义时间</summary><form data-energy-range class="energy-range"><label>开始<input type="datetime-local" name="from" value="${esc(from)}" required></label><label>结束<input type="datetime-local" name="to" value="${esc(to)}" required></label><button class="secondary">查看</button></form></details></div>${historyError?`<p class="error" role="status">历史读取失败，保留已有曲线。${esc(historyError)}</p>`:''}${lscHistoryChart(range,metric,chartWidth)}<div class="lsc-chart-note">${metric==='flow'?'<span class="lsc-legend-in">输入 · 5 秒均值</span><span class="lsc-legend-out">输出 · 5 秒均值</span><span>汇总曲线取区间最后观测，不能作为区间平均功率。</span>':'<span>储能数量 · EU</span><span>中断、容量变化或重启后分段显示。</span>'}</div>${history?.truncated?'<p class="error">超过 2,000 个点，请缩小时间范围查看完整记录。</p>':''}</section>
    <div class="lsc-footer"><section class="lsc-alert-summary"><h3>设备状态</h3><p><span class="lsc-state">${active.length?'当前 '+active.length+' 项告警':'暂无当前告警'}</span><button class="text-button" data-tab="alerts">查看告警历史 →</button></p>${active.slice(0,3).map(a=>`<p class="lsc-alert-text">${esc(a.detail)}</p>`).join('')}<small>采集 OC：${esc(collector?.name||'等待连接')} · ${collector?.online?'在线':'离线'}</small></section><details class="lsc-details" data-lsc-disclosure="records"><summary>精确观测明细 <span>最近 ${Math.min(range.length,200)} 条</span></summary><div class="energy-table"><table><thead><tr><th>时间</th><th>储能 EU</th><th>输入 EU/t</th><th>输出 EU/t</th><th>损耗 EU/t</th><th>净流入 EU/t</th></tr></thead><tbody>${range.slice(-200).reverse().map(p=>{const pf=p.flow,input=uint(pf?.input_eut),output=uint(pf?.output_eut),loss=uint(pf?.loss_eut);return `<tr><td>${esc(date(p.at))}</td><td>${esc(exact(uint(p.stored_eu)))}</td><td>${esc(exact(input))}</td><td>${esc(exact(output))}</td><td>${esc(exact(loss))}</td><td>${esc(signed(input!==null&&output!==null&&loss!==null?input-output-loss:null))}</td></tr>`;}).join('')}</tbody></table></div></details></div><p class="lsc-method-note">输入输出是游戏 tick 的观测值；储能实际变化按现实时间计算，包含无线转移。预计时间按近期趋势估算。</p></div>`;
}
