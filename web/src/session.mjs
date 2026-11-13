export const SESSION_KEY = "ae-orbit:session:v2";
export const LEGACY_SESSION_KEY = "ae-orbit:session";

function read(storage, key) {
  try { return JSON.parse(storage?.getItem(key) || "null"); } catch { return null; }
}

export function normalizeSession(value) {
  if (!value || typeof value !== "object") return null;
  if (typeof value.member !== "string" || !value.member || value.member.length > 128) return null;
  if (typeof value.csrf !== "string" || value.csrf.length < 16 || value.csrf.length > 512) return null;
  const expiresAt = typeof value.expiresAt === "string" && value.expiresAt ? value.expiresAt : "";
  return { version: 2, member: value.member, csrf: value.csrf, expiresAt };
}

export function loadSession(localStorage, sessionStorage, remember = true) {
  const sources = remember
    ? [[localStorage, SESSION_KEY], [sessionStorage, SESSION_KEY], [sessionStorage, LEGACY_SESSION_KEY]]
    : [[sessionStorage, SESSION_KEY], [sessionStorage, LEGACY_SESSION_KEY]];
  for (const [storage, key] of sources) {
    const session = normalizeSession(read(storage, key));
    if (session) return session;
  }
  return null;
}

export function saveSession(localStorage, sessionStorage, value, remember = true) {
  const session = normalizeSession(value);
  if (!session) throw new TypeError("Invalid browser session.");
  clearSession(localStorage, sessionStorage);
  const storage = remember ? localStorage : sessionStorage;
  storage.setItem(SESSION_KEY, JSON.stringify(session));
  return session;
}

export function clearSession(localStorage, sessionStorage) {
  for (const [storage, key] of [[localStorage, SESSION_KEY], [localStorage, LEGACY_SESSION_KEY], [sessionStorage, SESSION_KEY], [sessionStorage, LEGACY_SESSION_KEY]]) {
    try { storage?.removeItem(key); } catch {}
  }
}
