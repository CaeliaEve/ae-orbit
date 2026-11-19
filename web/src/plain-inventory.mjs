import { formatQuantity } from "./model.mjs";

export const quantityKnown = item => item?.quantity_known !== false;
export const stockText = item => quantityKnown(item) ? formatQuantity(item.quantity) : "未查询";
export function stockFreshness(item, age) {
  if (!quantityKnown(item)) return "等待精确查询";
  if (!item.current_session) return "库存待同步";
  return age(item.observed_at);
}
export function inventorySummary(stats, age) {
  if (!stats || stats.scope !== "nesql_plain") return "";
  const total = formatQuantity(stats.candidates);
  const checked = formatQuantity(stats.checked);
  const stock = formatQuantity(stats.entries);
  const units = formatQuantity(stats.total_quantity);
  const freshness = stats.last_observation ? ` · 最近更新 ${age(stats.last_observation)}` : " · 等待库存观测";
  const pending = stats.pending && stats.pending !== "0" ? ` · ${formatQuantity(stats.pending)} 种待确认` : "";
  return `NESQL 无 NBT · 已确认有库存 ${stock} 种，共 ${units} 件 · 已查询 ${checked}/${total}${pending}${freshness}`;
}
