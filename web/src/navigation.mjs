export const coreNavigation = Object.freeze([
  { id: 'dashboard', label: '工作台', icon: 'layout-dashboard' },
  { id: 'inventory', label: '库存', icon: 'boxes' },
  { id: 'history', label: '库存历史', icon: 'activity' },
  { id: 'orders', label: '合成中心', icon: 'list-checks' },
  { id: 'cpus', label: 'CPU 监控', icon: 'cpu' },
  { id: 'network', label: '网络管理', icon: 'radio-tower' },
  { id: 'plugins', label: '插件市场', icon: 'puzzle' },
  { id: 'github', label: 'GitHub', icon: 'github', defaultPlacement: 'drawer' },
]);

// Routes come from reviewed frontend modules, never from a server-supplied URL.
export const pluginNavigation = Object.freeze([
  { id: 'plugin-lsc', pluginId: 'gtnh.lsc', label: '储能监控', title: '蓝波顿储能监控', icon: 'battery-charging', defaultPlacement: 'drawer' },
  { id: 'plugin-qq', pluginId: 'aeorbit.qq', label: 'QQ 联动', title: 'QQ 通知联动', icon: 'messages-square', defaultPlacement: 'drawer' },
]);
const known = [...coreNavigation, ...pluginNavigation];
export const navigationKey = memberId => `ae-orbit:navigation:v1:${encodeURIComponent(memberId || 'guest')}`;

export function normalizeNavigation(value) {
  const rows = Array.isArray(value?.entries) ? value.entries : [];
  const seen = new Set();
  const entries = [];
  for (const row of rows) {
    const definition = known.find(entry => entry.id === row?.id);
    if (!definition || seen.has(row.id)) continue;
    seen.add(row.id);
    entries.push({ id: row.id, placement: row.placement === 'drawer' ? 'drawer' : 'primary' });
  }
  for (const entry of known) if (!seen.has(entry.id)) entries.push({ id: entry.id, placement: entry.defaultPlacement || 'primary' });
  return { version: 1, entries };
}

export function loadNavigation(storage, memberId) {
  try { return normalizeNavigation(JSON.parse(storage.getItem(navigationKey(memberId)) || 'null')); }
  catch { return normalizeNavigation(null); }
}
export function saveNavigation(storage, memberId, value) {
  const normalized = normalizeNavigation(value);
  storage.setItem(navigationKey(memberId), JSON.stringify(normalized));
  return normalized;
}
export function availableNavigation(installations = []) {
  const enabled = new Set(installations.filter(v => v.enabled === true).map(v => v.plugin?.id));
  return [...coreNavigation, ...pluginNavigation.filter(v => enabled.has(v.pluginId))];
}
export function orderedNavigation(preferences, available) {
  const entries = new Map(available.map(entry => [entry.id, entry]));
  return normalizeNavigation(preferences).entries.filter(row => entries.has(row.id)).map(row => ({ ...entries.get(row.id), placement: row.placement }));
}
export function partitionNavigation(preferences, available, capacity) {
  const ordered = orderedNavigation(preferences, available);
  const preferred = ordered.filter(row => row.placement === 'primary');
  const slots = Math.max(0, Math.floor(Number(capacity) || 0));
  const primary = preferred.slice(0, slots);
  const visible = new Set(primary.map(row => row.id));
  return { primary, drawer: ordered.filter(row => !visible.has(row.id)), automatic: preferred.slice(slots).map(row => row.id) };
}
export function moveNavigation(preferences, id, direction, available = known) {
  const result = normalizeNavigation(preferences);
  const ids = new Set(available.map(entry => entry.id));
  const positions = result.entries.map((row, index) => ids.has(row.id) ? index : -1).filter(index => index >= 0);
  const at = positions.findIndex(index => result.entries[index].id === id);
  const next = at + (direction < 0 ? -1 : 1);
  if (at < 0 || next < 0 || next >= positions.length) return result;
  [result.entries[positions[at]], result.entries[positions[next]]] = [result.entries[positions[next]], result.entries[positions[at]]];
  return result;
}
export function placeNavigation(preferences, id, placement) {
  const result = normalizeNavigation(preferences);
  const row = result.entries.find(entry => entry.id === id);
  if (row) row.placement = placement === 'drawer' ? 'drawer' : 'primary';
  return result;
}
