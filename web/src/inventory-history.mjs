import { formatQuantity } from "./model.mjs";

const esc = value => String(value ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const quantity = value => typeof value === "string" && /^(0|[1-9][0-9]{0,39})$/.test(value);
export const historyTime = value => new Date(value).toLocaleString("zh-CN", { hourCycle: "h23" });
export const historyDelta = value => value === null || value === undefined ? "—" : /^-?[0-9]+$/.test(value) ? `${BigInt(value)>0n?"+":""}${BigInt(value).toLocaleString("zh-CN")}` : "—";

export function historyChartModel(series, metric = "quantity") {
  const points = (series?.points || []).filter(p => quantity(p[metric]) && Number.isFinite(Date.parse(p.at))).map(p => ({...p,time:Date.parse(p.at),value:BigInt(p[metric])}));
  if (!points.length) return null;
  const min = points.reduce((v,p)=>p.value<v?p.value:v,points[0].value), max=points.reduce((v,p)=>p.value>v?p.value:v,points[0].value);
  const span=max-min || (max/100n || 1n), padding=span/10n || 1n;
  const low=min>padding?min-padding:0n, high=max+padding;
  const from=Date.parse(series.from),to=Date.parse(series.to);
  if (!Number.isFinite(from)||!Number.isFinite(to)||to<=from) return null;
  const duration=to-from;
  const mapped=points.map(p=>({...p,x:Math.max(0,Math.min(1000,(p.time-from)/duration*1000)),y:Number((high-p.value)*260000n/(high-low))/1000}));
  const segments=[];
  for(const p of mapped){const last=segments.at(-1)?.at(-1);const gap=series.basis!=="last_known_quantity"&&last&&(p.time-last.time>(series.resolution||60)*2500||!last.all_online||!p.all_online);if(!last||gap)segments.push([]);segments.at(-1).push(p)}
  const ticks=Array.from({length:5},(_,i)=>({y:i*65,value:high-(high-low)*BigInt(i)/4n}));
  return {points:mapped,segments,ticks,from,to,low,high,step:series.basis==="last_known_quantity",metric};
}

export function historyChartHTML(series, metric="quantity") {
  const m=historyChartModel(series,metric);
  if(!m)return `<div class="history-empty"><i data-lucide="activity" aria-hidden="true"></i><strong>这段时间暂无记录</strong><span>从启用历史记录后开始积累，可以扩大时间范围查看。</span></div>`;
  const paths=m.segments.map(segment=>{const path=segment.map((p,i)=>`${i?m.step?`H${p.x.toFixed(2)} V`:'L':'M'}${i&&m.step?p.y.toFixed(2):`${p.x.toFixed(2)} ${p.y.toFixed(2)}`}`).join(' ');return segment.length>1?`<path class="history-line" d="${path}"/>`:''}).join('');
  const dots=m.segments.map(s=>s.at(-1)).map(p=>`<circle cx="${p.x.toFixed(2)}" cy="${p.y.toFixed(2)}" r="2.5" class="history-point"/>`).join('');
  const times=Array.from({length:5},(_,i)=>{const t=new Date(m.from+(m.to-m.from)*i/4);return `<time title="${esc(historyTime(t))}">${esc(t.toLocaleString('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hourCycle:'h23'}))}</time>`}).join('');
  return `<div class="history-chart-grid"><div class="history-y-axis">${m.ticks.map(t=>`<span title="${esc(formatQuantity(t.value))}">${esc(formatQuantity(t.value))}</span>`).join('')}</div><svg id="history-svg" viewBox="0 0 1000 260" preserveAspectRatio="none" role="img" aria-label="${m.step?'单个物品最后已知数量':'后端库存快照'}时间趋势"><g class="history-grid">${m.ticks.map(t=>`<path d="M0 ${t.y} H1000"/>`).join('')}</g>${paths}${dots}<rect id="history-brush" class="history-brush" x="0" y="0" width="0" height="260"/><path id="history-crosshair" class="history-crosshair" d="M0 0 V260" hidden/></svg><div class="history-time-axis">${times}</div></div><output id="history-chart-tooltip" class="history-chart-tooltip" hidden></output>`;
}

export function historyRecordsHTML(records) {
  const names={baseline:"开始记录时的库存",first:"首次观测",change:"数量变化"};
  return records.map(r=>`<tr><td><time title="观测时间 ${esc(historyTime(r.observed_at))}">${esc(historyTime(r.recorded_at))}</time></td><td><button data-history-item="${esc(r.item_id)}" title="查看此物品历史"><strong>${esc(r.label||r.name)}</strong><small>${esc(r.name)} · ${esc(r.damage)}</small></button></td><td class="number">${esc(formatQuantity(r.quantity))}</td><td class="number">${r.previous_quantity===null?'—':esc(formatQuantity(r.previous_quantity))}</td><td class="number" data-direction="${r.delta?.startsWith('-')?'decrease':r.delta&&r.delta!=='0'?'increase':'none'}">${esc(historyDelta(r.delta))}</td><td><span class="history-kind" data-kind="${esc(r.kind)}">${esc(names[r.kind]||r.kind)}</span></td></tr>`).join('');
}

// Keyset pages are frozen at a watermark. Incoming records never reset a page.
export function createHistoryController({request,onChange=()=>{},now=()=>Date.now()}) {
  let generation=0, pageSequence=0, chartAbort, pageAbort, searchAbort, detailQuery='';
  const state={series:null,page:null,pages:[],pageIndex:0,loading:false,tableLoading:false,error:'',tableError:'',searchResults:[],searchLoading:false,metric:'quantity',mode:'trend',epoch:'',item:'',range:'all',from:'',to:'',kind:'',direction:'',newData:false};
  const emit=()=>onChange(state);
  const params=()=>new URLSearchParams(Object.fromEntries(Object.entries({epoch:state.epoch,item:state.item,from:state.from,to:state.to,kind:state.kind,direction:state.direction}).filter(([,v])=>v!=='')));
  function reset(){generation++;chartAbort?.abort();pageAbort?.abort();searchAbort?.abort();detailQuery='';Object.assign(state,{series:null,page:null,pages:[],pageIndex:0,loading:false,tableLoading:false,error:'',tableError:'',searchResults:[],searchLoading:false,epoch:'',item:'',from:'',to:'',range:'all',kind:'',direction:'',newData:false});emit()}
  async function chart(){const token=++generation;chartAbort?.abort();pageAbort?.abort();searchAbort?.abort();state.searchLoading=false;state.tableLoading=false;chartAbort=new AbortController();state.loading=true;state.error='';emit();try{const s=await request('/inventory-history?'+params(),{signal:chartAbort.signal});if(token!==generation)return;state.series=s;state.epoch=s.epoch==='0'?'':s.epoch;state.from=s.from;state.to=s.to;emit()}catch(e){if(token===generation&&e.name!=='AbortError')state.error=e.message||'历史读取失败'}finally{if(token===generation){state.loading=false;emit()}}}
  async function page({next=false,first=false}={}){const token=generation,sequence=++pageSequence;pageAbort?.abort();pageAbort=new AbortController();const current=()=>token===generation&&sequence===pageSequence;state.tableLoading=true;state.tableError='';emit();const filters=next&&state.page?detailQuery:params().toString();const q=new URLSearchParams(filters);q.set('limit','100');if(next&&state.page){q.set('before',state.page.next);q.set('snapshot',state.page.snapshot)}try{const p=await request('/inventory-history/records?'+q,{signal:pageAbort.signal});if(!current())return;if(first){state.pages=[p];state.pageIndex=0;state.newData=false}else if(next){state.pages=state.pages.slice(0,state.pageIndex+1);state.pages.push(p);state.pageIndex++}else{state.pages=[p];state.pageIndex=0}state.page=p;detailQuery=filters}catch(e){if(current()&&e.name!=='AbortError')state.tableError=e.message||'明细读取失败'}finally{if(current()){state.tableLoading=false;emit()}}}
  async function load(){const token=generation+1;await chart();if(generation===token&&!state.error)await page({first:true})}
  async function setRange(range){state.range=range;const end=now();state.to=new Date(end).toISOString();state.from=range==='all'?'':new Date(end-Number(range)*3600000).toISOString();await load()}
  async function setWindow(from,to){if(!Number.isFinite(from)||!Number.isFinite(to)||to-from<60000)return;state.range='custom';state.from=new Date(from).toISOString();state.to=new Date(to).toISOString();await load()}
  async function search(text){searchAbort?.abort();const ctrl=searchAbort=new AbortController();const token=generation;state.searchLoading=true;emit();try{const q=new URLSearchParams({q:text});if(state.epoch)q.set('epoch',state.epoch);const rows=await request('/inventory-history/items?'+q,{signal:ctrl.signal});if(token===generation&&ctrl===searchAbort)state.searchResults=rows}catch(e){if(e.name!=='AbortError'&&token===generation)state.searchResults=[]}finally{if(token===generation&&ctrl===searchAbort){state.searchLoading=false;emit()}}}
  async function selectItem(id){state.item=id;state.metric='quantity';state.searchResults=[];await load()}
  async function refresh(){if(state.range!=='custom'){const end=now();state.to=new Date(end).toISOString();if(state.range!=='all')state.from=new Date(end-Number(state.range)*3600000).toISOString()}const token=generation+1;await chart();if(generation!==token)return;if(!state.error){if(state.pageIndex===0)await page({first:true});else state.newData=true}emit()}
  async function observe(){if(state.loading||state.tableLoading||!state.series||state.range==='custom')return;const end=now();state.to=new Date(end).toISOString();if(state.range!=='all')state.from=new Date(end-Number(state.range)*3600000).toISOString();const token=generation+1;await chart();if(generation===token&&state.page&&state.series?.watermark&&BigInt(state.series.watermark)>BigInt(state.page.snapshot||'0'))state.newData=true;emit()}
  async function next(){if(state.tableLoading||!state.page?.has_more)return;if(state.pages[state.pageIndex+1]){state.page=state.pages[++state.pageIndex];emit()}else await page({next:true})}
  function previous(){if(state.tableLoading||state.pageIndex===0)return;state.page=state.pages[--state.pageIndex];emit()}
  function exportQuery(){const q=state.page?new URLSearchParams(detailQuery):params();if(state.page)q.set('snapshot',state.page.snapshot);return q.toString()}
  return {state,reset,load,setRange,setWindow,search,selectItem,refresh,observe,next,previous,page,exportQuery};
}
