import { formatQuantity } from "./model.mjs";
const esc = value => String(value ?? "").replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]));
export const detailKinds = [
  {id:"active", title:"处理中 · 等待返回", description:"AE 已派发，等待加工产物回到网络。"},
  {id:"pending", title:"待执行", description:"后续尚未执行的合成步骤产物。"},
  {id:"stored", title:"CPU 内材料", description:"本次任务暂存在 CPU 内的材料。"},
];
const errors = {unsupported:"OC 未提供此类数据",target_expired:"CPU 引用已过期，请关闭并重新选择",target_changed:"CPU 任务已变化，请关闭并重新选择",read_failed:"读取失败，保留上次观测",memory_low:"OC 可用内存不足，已跳过读取",invalid_items:"数据无法完整识别，保留上次观测",packet_budget:"数据超过传输预算，已跳过本次上报"};
export function cpuDetailStatus(target,now=Date.now()) {
  if(target.error)return target.error;
  const parts=target.data?.parts || [];
  const observed=parts.filter(part=>part.observed_at).length;
  const failed=parts.filter(part=>part.error).length;
  if(failed)return `已观测 ${observed}/3 栏 · ${failed} 栏本次读取未完成，原因见下方`;
  if(!target.receivedAt)return target.loading ? "正在连接详情服务…" : "等待连接详情服务";
  const elapsed=Math.max(0,Math.floor((now-target.receivedAt)/1000));
  if(elapsed>=15)return `详情连接已 ${elapsed} 秒未更新 · 可重新连接，保留最后观测`;
  return observed===3 ? "持续更新 · 各栏读取时间如下" : `已观测 ${observed}/3 栏 · 已连接，等待主 OC 读取其余清单`;
}
export function detailError(error) {
  return ({capability_unavailable:"请先更新主 OC；详情需要新的采集接口。",capacity_exceeded:"这个网络正在查看另一台 CPU，请稍后再试。",view_changed:"CPU 快照已变化，请关闭后重新选择。",conflict:"这台 CPU 已空闲，请选择正在合成的 CPU。",not_found:"CPU 观测已失效，请刷新列表后重新选择。"})[error?.code] || error?.message || "暂时无法读取合成详情";
}
export function cpuDetailColumnsHTML(view, {search="", image=()=>'<i data-lucide="package" aria-hidden="true"></i>', age=()=>"刚刚"}={}) {
  const tokens=String(search).normalize("NFKC").toLocaleLowerCase().trim().split(/\s+/).filter(Boolean);
  return detailKinds.map(kind=>{
    const part=view?.parts?.find(part=>part.kind===kind.id);
    const observed=!!part?.observed_at;
    const items=Array.isArray(part?.items)?part.items:[];
    const visible=items.filter(item=>tokens.every(token=>[item.label,item.name,item.catalog?.localized_name].join(" ").normalize("NFKC").toLocaleLowerCase().includes(token)));
    const note=part?.error ? errors[part.error] || "读取不可用" : !observed ? "等待本栏首次读取" : !items.length ? "本次观测为空" : part.truncated ? `仅展示前 ${items.length} 种，共 ${formatQuantity(part.total)} 种` : `${formatQuantity(part.total)} 种`;
    const html=visible.map(item=>{
      const label=item.catalog?.base_fallback ? item.label || item.name : item.catalog?.localized_name || item.label || item.name;
      const unit=item.kind==="fluid"?" mB":item.kind==="essentia"?" 源质":"";
      return `<li class="cpu-process-item"><span class="cpu-process-media">${image(item)}</span><span class="cpu-process-label"><strong title="${esc(label)}">${esc(label)}</strong><small title="${esc(item.name)}">${esc(item.name)}${item.has_tag ? ' · NBT' : ''}</small>${item.catalog?.base_fallback?'<small class="cpu-process-fallback">基础纹理</small>':''}</span><span class="cpu-process-quantity number">${esc(formatQuantity(item.quantity))}<small>${esc(unit)}</small></span></li>`;
    }).join("");
    return `<section class="cpu-process-column" aria-labelledby="cpu-process-${kind.id}"><header><h3 id="cpu-process-${kind.id}">${kind.title}</h3><p>${kind.description}</p><span>${observed ? `观测于 ${esc(age(part.observed_at))}` : "尚未观测"}</span></header><p class="cpu-process-note${part?.error?' has-error':''}" role="status">${esc(note)}</p><ul data-cpu-process-scroll="${kind.id}">${html || `<li class="cpu-process-empty">${observed&&items.length ? "没有匹配的物品" : observed&&!part?.error ? "暂无物品" : "等待采集"}</li>`}</ul></section>`;
  }).join("");
}

// Leases are serial: close waits for a late POST before deleting its viewer.
export function createCPUDetails({request, changed=()=>{}, setTimer=setTimeout, clearTimer=clearTimeout, now=Date.now, requestTimeout=8000, id=()=>Array.from(crypto.getRandomValues(new Uint8Array(16)),v=>v.toString(16).padStart(2,"0")).join("")}) {
  let current=null;
  let transition=Promise.resolve();
  function publish(target){if(current===target)changed(target);}
  function boundedRequest(path,options) {
    const controller=new AbortController();
    let timer;
    const timeout=new Promise((_,reject)=>{
      timer=setTimer(()=>{controller.abort();reject(new Error("详情连接超时，正在重连；保留最后观测。"));},requestTimeout);
    });
    const work=Promise.resolve().then(()=>request(path,{...options,signal:controller.signal}));
    return Promise.race([work,timeout]).finally(()=>clearTimer(timer));
  }
  async function release(target){
    if(!target)return;
    target.stopped=true;clearTimer(target.timer);
    await target.pending;
    try{await boundedRequest(`${target.path}/${target.viewer}`,{method:"DELETE"});}catch{}
  }
  async function renew(target){
    if(target.stopped||current!==target)return;
    target.loading=true;
    const pending=boundedRequest(target.path,{method:"POST",body:{session_id:target.session,cpu_id:target.cpu.id,viewer_id:target.viewer}});
    target.pending=pending.catch(()=>{});
    try{
      const data=await pending;
      if(target.stopped||current!==target)return;
      target.data=data;target.error="";target.receivedAt=now();
      if(["target_changed","stale"].includes(data.status)){target.error="CPU 任务或连接已变化，请关闭后重新选择。";target.stopped=true;}
    }catch(error){
      if(target.stopped||current!==target)return;
      target.error=detailError(error);
      if(["capability_unavailable","view_changed","conflict","not_found","capacity_exceeded","unauthorized","forbidden"].includes(error.code)){target.stopped=true;}
    }
    target.loading=false;
    try{publish(target);}finally{if(!target.stopped)target.timer=setTimer(()=>renew(target),5000);}
  }
  return {
    get current(){return current;},
    open({network,session,cpu,name}){
      const old=current;
      const target={network,session,cpu,name,viewer:id(),path:`/api/v1/networks/${encodeURIComponent(network)}/cpu-details`,data:null,error:"",stopped:false,loading:true};
      if(old){old.stopped=true;clearTimer(old.timer);}
      current=target;publish(target);
      transition=transition.catch(()=>{}).then(()=>release(old)).then(()=>renew(target));
      return transition;
    },
    refresh(){const target=current;if(!target||target.pendingRefresh||target.stopped)return Promise.resolve();clearTimer(target.timer);target.pendingRefresh=Promise.resolve(target.pending).then(()=>{clearTimer(target.timer);return renew(target);}).finally(()=>{target.pendingRefresh=null;});return target.pendingRefresh;},
    close(){const old=current;current=null;if(old){old.stopped=true;clearTimer(old.timer);}transition=transition.catch(()=>{}).then(()=>release(old));return transition;},
  };
}
