import { unzipSync } from "fflate";
import { createTextureArchiveCache } from "./texture-cache.mjs";

export const TEXTURE_PACK_FORMAT = "ae-orbit.texture-pack";
export const TEXTURE_PACK_VERSION = 1;
export const defaultTexturePackSettings = Object.freeze({
  textureSource: "server",
  texturePackURL: "",
});

const sourceValues = new Set(["server", "url", "local"]);
const MAX_ARCHIVE_BYTES = 512 * 1024 * 1024;
const MAX_ASSET_BYTES = 2 * 1024 * 1024;
const MAX_ASSETS = 200_000;

function text(value) {
  return typeof value === "string" ? value.trim() : "";
}

function safeAssetPath(value) {
  const path = text(value).replaceAll("\\", "/");
  if (!path || path.startsWith("/") || path.includes("\0") || path.includes(":")) {
    return "";
  }
  const parts = path.split("/");
  if (parts.some(part => !part || part === "." || part === "..")) return "";
  if (!path.startsWith("assets/") || !path.endsWith(".png")) return "";
  return path;
}

export function normalizeTexturePackSettings(value) {
  const source = value && typeof value === "object" ? value : {};
  const settings = { ...defaultTexturePackSettings };
  if (sourceValues.has(source.textureSource)) settings.textureSource = source.textureSource;
  if (settings.textureSource === "url") {
    const url = text(source.texturePackURL);
    try {
      const parsed = new URL(url, globalThis.location?.origin || "http://localhost");
      if (parsed.protocol === "http:" || parsed.protocol === "https:") settings.texturePackURL = parsed.href;
      else settings.textureSource = "server";
    } catch {
      settings.textureSource = "server";
    }
  } else if (settings.textureSource === "local") {
    settings.texturePackURL = "";
  }
  return settings;
}

export async function saveCachedTexturePack(input) {
  return createTextureArchiveCache().save(toBytes(input));
}

export async function loadCachedTexturePack() {
  return createTextureArchiveCache().load();
}

export async function clearCachedTexturePack() {
  return createTextureArchiveCache().clear();
}

export function validateTexturePackManifest(value) {
  if (!value || typeof value !== "object"
    || value.format !== TEXTURE_PACK_FORMAT
    || value.version !== TEXTURE_PACK_VERSION
    || !Array.isArray(value.assets)
    || value.assets.length < 1
    || value.assets.length > MAX_ASSETS) {
    throw new Error("纹理包清单格式不受支持。");
  }
  const safeAssets = value.assets.map(safeAssetPath);
  if (safeAssets.some(path => !path)) throw new Error("unsafe texture pack asset path");
  const assets = [...new Set(safeAssets)];
  if (assets.length !== value.assets.length || assets.length === 0) {
    throw new Error("纹理包包含不安全或重复的资源路径。");
  }
  return {
    version: value.version,
    revision: text(value.revision),
    assets,
  };
}

function toBytes(value) {
  if (value instanceof Uint8Array) return value;
  if (value instanceof ArrayBuffer) return new Uint8Array(value);
  if (ArrayBuffer.isView(value)) return new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
  throw new TypeError("纹理包必须是二进制数据。");
}

export function createTexturePackStore({
  createObjectURL = globalThis.URL?.createObjectURL?.bind(globalThis.URL),
  revokeObjectURL = globalThis.URL?.revokeObjectURL?.bind(globalThis.URL),
} = {}) {
  let manifest = null;
  let label = "";
  let entries = new Map();
  let objectURLs = new Map();

  function clear() {
    for (const url of objectURLs.values()) revokeObjectURL?.(url);
    manifest = null;
    label = "";
    entries = new Map();
    objectURLs = new Map();
  }

  async function loadArchive(input, nextLabel = "纹理包") {
    const bytes = toBytes(input);
    if (bytes.byteLength > MAX_ARCHIVE_BYTES) throw new Error("纹理包超过 512 MB 大小限制。");
    const files = unzipSync(bytes);
    const manifestBytes = files["manifest.json"];
    if (!manifestBytes) throw new Error("纹理包缺少 manifest.json。");
    let raw;
    try {
      raw = JSON.parse(new TextDecoder().decode(manifestBytes));
    } catch {
      throw new Error("纹理包清单无法解析。");
    }
    const nextManifest = validateTexturePackManifest(raw);
    const nextEntries = new Map();
    for (const path of nextManifest.assets) {
      const data = files[path];
      if (!(data instanceof Uint8Array) || data.byteLength < 1 || data.byteLength > MAX_ASSET_BYTES) {
        throw new Error(`纹理包缺少有效资源：${path}`);
      }
      nextEntries.set(path, data);
    }
    if (!createObjectURL) throw new Error("当前浏览器不支持本地纹理资源。");
    clear();
    manifest = nextManifest;
    label = text(nextLabel) || "纹理包";
    entries = nextEntries;
    return status();
  }

  function resolve(path) {
    const safe = safeAssetPath(path);
    if (!safe || !entries.has(safe)) return "";
    if (!objectURLs.has(safe)) {
      const blob = new Blob([entries.get(safe)], { type: "image/png" });
      objectURLs.set(safe, createObjectURL(blob));
    }
    return objectURLs.get(safe);
  }

  function status() {
    return {
      loaded: Boolean(manifest),
      label,
      revision: manifest?.revision || "",
      assetCount: entries.size,
    };
  }

  return { clear, loadArchive, resolve, status };
}

export async function loadTexturePackURL(url, fetcher = globalThis.fetch.bind(globalThis)) {
  const target = new URL(url, globalThis.location?.href || "http://localhost");
  const origin = globalThis.location?.origin || "http://localhost";
  const response = await fetcher(url, {
    credentials: target.origin === origin ? "same-origin" : "omit",
    cache: "no-cache",
  });
  if (!response.ok) throw new Error(`纹理包下载失败（HTTP ${response.status}）。`);
  const bytes = await response.arrayBuffer();
  return bytes;
}
