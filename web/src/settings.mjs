export const SETTINGS_KEY = "ae-orbit:settings:v1";

export const defaultSettings = Object.freeze({
  density: "compact",
  inventoryLayout: "auto",
  taskNotifications: true,
  networkNotifications: true,
  syncNotifications: false,
  reconnect: true,
  rememberSession: true,
});

const booleanKeys = ["taskNotifications", "networkNotifications", "syncNotifications", "reconnect", "rememberSession"];
const densityValues = new Set(["compact", "comfortable"]);
const layoutValues = new Set(["auto", "dense", "wide"]);

export function normalizeSettings(value) {
  const source = value && typeof value === "object" ? value : {};
  const settings = { ...defaultSettings };
  if (densityValues.has(source.density)) settings.density = source.density;
  if (layoutValues.has(source.inventoryLayout)) settings.inventoryLayout = source.inventoryLayout;
  for (const key of booleanKeys) if (typeof source[key] === "boolean") settings[key] = source[key];
  return settings;
}

export function loadSettings(storage = globalThis.localStorage) {
  try { return normalizeSettings(JSON.parse(storage.getItem(SETTINGS_KEY) || "null")); }
  catch { return { ...defaultSettings }; }
}

export function saveSettings(storage, value) {
  const settings = normalizeSettings(value);
  storage.setItem(SETTINGS_KEY, JSON.stringify(settings));
  return settings;
}
