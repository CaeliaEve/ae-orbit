import { formatQuantity } from "./model.mjs";

const MINUTE = 60_000;
const WINDOW = 24 * 60 * MINUTE;
const MAX_SAMPLES = 1441;
const MAX_STORED_BYTES = 256 * 1024;
const MAX_GAP = 5 * MINUTE;
const units = [[1n, ""], [1000n, "K"], [1000000n, "M"], [1000000000n, "B"], [1000000000000n, "T"], [1000000000000000n, "Qa"], [1000000000000000000n, "Qi"]];
const esc = value => String(value).replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const validQuantity = value => typeof value === "string" && /^(0|[1-9][0-9]{0,39})$/.test(value);

export function trendStorageKey(member, network, scope = "observed") {
  return `ae-orbit:inventory-trend:v1:${JSON.stringify([String(member), String(network), scope])}`;
}

function normalizeSamples(samples, now) {
  if (!Array.isArray(samples) || !Number.isSafeInteger(now)) return [];
  const buckets = new Map();
  for (const sample of samples.slice(-MAX_SAMPLES)) {
    if (!sample || !Number.isSafeInteger(sample.at) || sample.at < now - WINDOW || sample.at > now || !validQuantity(sample.quantity)) continue;
    const bucket = Math.floor(sample.at / MINUTE);
    const previous = buckets.get(bucket);
    if (!previous || sample.at >= previous.at) buckets.set(bucket, { at: sample.at, quantity: sample.quantity });
  }
  return [...buckets.values()].sort((a, b) => a.at - b.at).slice(-MAX_SAMPLES);
}

export function loadTrendHistory(storage, key, now = Date.now()) {
  try {
    const raw = storage.getItem(key);
    if (!raw || raw.length > MAX_STORED_BYTES) return [];
    const value = JSON.parse(raw);
    return value?.version === 1 ? normalizeSamples(value.samples, now) : [];
  } catch { return []; }
}

export function recordTrendSample(samples, quantity, now = Date.now()) {
  const history = normalizeSamples(samples, now);
  if (!validQuantity(quantity)) return history;
  return normalizeSamples([...history, { at: now, quantity }], now);
}

export function saveTrendHistory(storage, key, samples) {
  try { storage.setItem(key, JSON.stringify({ version: 1, samples: samples.slice(-MAX_SAMPLES) })); }
  catch { /* A storage quota failure must not interrupt live inventory. */ }
}

function niceStep(value) {
  const magnitude = 10n ** BigInt(Math.max(0, value.toString().length - 1));
  return [1n, 2n, 5n, 10n].map(factor => factor * magnitude).find(step => step >= value);
}

function quantityAxis(min, max) {
  const spread = max - min;
  const padding = spread ? 0n : (max / 100n || 1n);
  const low = min > padding ? min - padding : 0n;
  const high = max + padding;
  const step = niceStep((high - low + 3n) / 4n || 1n);
  const bottom = low / step * step;
  const top = (high + step - 1n) / step * step;
  const [unit, suffix] = [...units].reverse().find(([divisor]) => top >= divisor);
  // Small changes beside a huge total need a relative axis, otherwise compact
  // labels would round distinct values to the same text.
  const relative = step * 1000n < unit;
  const offset = relative ? bottom : 0n;
  const labelUnit = relative ? 1n : unit;
  const labelSuffix = relative ? "" : suffix;
  let precision = suffix && !relative ? 1 : 0;
  while (step * 10n ** BigInt(precision) < labelUnit && precision < 3) precision++;
  const scale = 10n ** BigInt(precision);
  const ticks = [];
  for (let quantity = top; quantity >= bottom; quantity -= step) {
    const scaled = ((quantity - offset) * scale + labelUnit / 2n) / labelUnit;
    const fraction = precision ? `.${(scaled % scale).toString().padStart(precision, "0")}` : "";
    ticks.push({ quantity: quantity.toString(), label: `${relative ? "+" : ""}${scaled / scale}${fraction}${labelSuffix}`, position: Number((top - quantity) * 10000n / (top - bottom)) / 100 });
  }
  return { bottom, top, ticks, baseline: relative ? formatQuantity(bottom) : "" };
}

export function inventoryTrendModel(samples, now = Date.now(), { windowMs = WINDOW } = {}) {
  const window = Number.isSafeInteger(windowMs) && windowMs > 0 && windowMs <= WINDOW ? windowMs : WINDOW;
  const history = normalizeSamples(samples, now).filter(sample => sample.at >= now - window);
  if (!history.length) return null;
  const quantities = history.map(sample => BigInt(sample.quantity));
  const min = quantities.reduce((a, b) => a < b ? a : b);
  const max = quantities.reduce((a, b) => a > b ? a : b);
  const axis = quantityAxis(min, max);
  const start = history.length === 1 ? history[0].at - MINUTE : history[0].at;
  const end = history.at(-1).at;
  const duration = end - start;
  const timeOptions = { hour: "2-digit", minute: "2-digit", hourCycle: "h23", ...(duration < 5 * MINUTE ? { second: "2-digit" } : {}) };
  const timeFormat = new Intl.DateTimeFormat("zh-CN", timeOptions);
  const timeTicks = Array.from({ length: duration < 5 * MINUTE ? 3 : 6 }, (_, index) => index);
  const times = timeTicks.map((_, index) => {
    const at = start + Math.round(duration * index / (timeTicks.length - 1));
    return { at, label: timeFormat.format(new Date(at)), title: new Date(at).toLocaleString("zh-CN", { hourCycle: "h23" }) };
  });
  const points = history.map(sample => ({
    ...sample,
    x: (sample.at - start) / duration * 720,
    y: Number((axis.top - BigInt(sample.quantity)) * 200000n / (axis.top - axis.bottom)) / 1000,
  }));
  const segments = [];
  for (const point of points) {
    if (!segments.length || point.at - segments.at(-1).at(-1).at > MAX_GAP) segments.push([]);
    segments.at(-1).push(point);
  }
  return { times, ticks: axis.ticks, baseline: axis.baseline, segments, first: history[0], latest: history.at(-1), samples: history.length };
}

export function inventoryTrendHTML(samples, { now = Date.now(), empty = "等待库存总量采样", windowMs = WINDOW } = {}) {
  const model = inventoryTrendModel(samples, now, { windowMs });
  if (!model) return `<div class="trend-empty"><i data-lucide="activity"></i><span>${esc(empty)}</span></div>`;
  const grid = model.ticks.map(tick => `<path d="M0 ${(tick.position * 2).toFixed(2)}H720"></path>`).join("");
  const paths = model.segments.map(segment => {
    const line = segment.map((point, index) => `${index ? "L" : "M"}${point.x.toFixed(2)} ${point.y.toFixed(2)}`).join(" ");
    const area = `${line} L${segment.at(-1).x.toFixed(2)} 200 L${segment[0].x.toFixed(2)} 200 Z`;
    return segment.length > 1 ? `<path class="trend-area" d="${area}"></path><path class="trend-line" d="${line}"></path>` : "";
  }).join("");
  const dots = model.segments.map(segment => segment.at(-1)).map(point => `<circle class="trend-point" cx="${point.x.toFixed(2)}" cy="${point.y.toFixed(2)}" r="2.5"><title>${esc(new Date(point.at).toLocaleString("zh-CN", { hourCycle: "h23" }))} · ${esc(formatQuantity(point.quantity))} 件</title></circle>`).join("");
  const ticks = model.ticks.map(tick => `<span title="${esc(formatQuantity(tick.quantity))} 件">${esc(tick.label)}</span>`).join("");
  const times = model.times.map(time => `<time datetime="${new Date(time.at).toISOString()}" title="${esc(time.title)}">${esc(time.label)}</time>`).join("");
  const label = `库存总量时间趋势，最新 ${formatQuantity(model.latest.quantity)} 件，共 ${model.samples} 次分钟采样`;
  return `<div class="trend-plot"><div class="trend-y-axis" aria-label="库存数量">${ticks}</div><svg viewBox="0 0 720 200" preserveAspectRatio="none" role="img" aria-label="${esc(label)}"><g class="trend-grid">${grid}</g>${paths}${dots}</svg><div class="trend-time-axis" aria-label="采样时间">${times}</div></div><div class="trend-note">${model.baseline ? `数量基数 ${esc(model.baseline)} 件 · ` : ""}${model.samples === 1 ? "正在积累时间采样" : `最新 ${esc(formatQuantity(model.latest.quantity))} 件`} · 仅显示本浏览器采样记录</div>`;
}
