import { formatQuantity } from "./model.mjs";

export const quantityKnown = item => item?.quantity_known !== false;
export const resourceUnit = item => item?.kind === "fluid" ? "mB" : item?.kind === "essentia" ? "源质" : "";
export const resourceLabel = kind => ({ item:"物品",fluid:"流体",essentia:"源质" }[kind] || "物品");
export const craftingKindSupported = (item, capabilities = {}) => (item?.kind || "item") === "item"
  || item?.kind === "fluid" && capabilities.craft_fluids === true && capabilities.fluids === true && capabilities.craft === true;
export const craftingQuantityLabel = item => item?.kind === "fluid" ? "合成数量（mB，1000 mB = 1 桶）" : "合成数量";
export const stockText = item => quantityKnown(item) ? `${formatQuantity(item.quantity)}${resourceUnit(item) ? " "+resourceUnit(item) : ""}` : "未查询";
export function stockFreshness(item, age) {
  if (!quantityKnown(item)) return "等待精确查询";
  if (!item.current_session) return "库存待同步";
  return age(item.observed_at);
}
export function inventorySummary(stats, age, kind = "item") {
  if (!stats || stats.scope !== "nesql_plain") return "";
  if (kind !== "item") {
    const resource = stats.resources?.find(r => r.kind === kind);
    if (!resource) return `${resourceLabel(kind)}目录尚未启用`;
    return `${resourceLabel(kind)} · 已确认有库存 ${formatQuantity(resource.entries)} 种，共 ${formatQuantity(resource.total_quantity)} ${resource.unit} · 已查询 ${formatQuantity(resource.checked)}/${formatQuantity(resource.candidates)}${resource.supported ? "" : " · 等待支持此类型的 OC 更新接入"}`;
  }
  const total = formatQuantity(stats.candidates);
  const checked = formatQuantity(stats.checked);
  const stock = formatQuantity(stats.entries);
  const units = formatQuantity(stats.total_quantity);
  const freshness = stats.last_observation ? ` · 最近更新 ${age(stats.last_observation)}` : " · 等待库存观测";
  const pending = stats.pending && stats.pending !== "0" ? ` · ${formatQuantity(stats.pending)} 种待确认` : "";
  return `NESQL 无 NBT · 已确认有库存 ${stock} 种，共 ${units} 件 · 已查询 ${checked}/${total}${pending}${freshness}`;
}
