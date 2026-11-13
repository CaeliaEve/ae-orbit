import test from "node:test";
import assert from "node:assert/strict";
import { clearSession, loadSession, normalizeSession, saveSession, SESSION_KEY } from "../src/session.mjs";

function storage(initial = {}) {
  const values = new Map(Object.entries(initial));
  return {
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: key => values.delete(key),
    value: key => values.get(key),
  };
}

test("session normalization rejects malformed or oversized credentials", () => {
  assert.equal(normalizeSession(null), null);
  assert.equal(normalizeSession({ member: "m", csrf: "short" }), null);
  assert.equal(normalizeSession({ member: "m", csrf: "x".repeat(513) }), null);
  assert.deepEqual(normalizeSession({ member: "m", csrf: "x".repeat(32), expiresAt: "" }), {
    version: 2, member: "m", csrf: "x".repeat(32), expiresAt: "",
  });
});

test("remembered sessions persist in local storage without copying the legacy key", () => {
  const local = storage();
  const session = storage();
  const saved = saveSession(local, session, { member: "member-1", csrf: "x".repeat(32), expiresAt: "2026-11-14T00:00:00Z" });
  assert.deepEqual(loadSession(local, session), saved);
  assert.equal(local.value(SESSION_KEY), JSON.stringify(saved));
  assert.equal(session.value(SESSION_KEY), undefined);
});

test("temporary sessions stay in session storage and clear both stores", () => {
  const local = storage();
  const session = storage();
  const saved = saveSession(local, session, { member: "member-1", csrf: "x".repeat(32) }, false);
  assert.deepEqual(loadSession(local, session, false), saved);
  assert.deepEqual(loadSession(local, session, true), saved);
  clearSession(local, session);
  assert.equal(local.value(SESSION_KEY), undefined);
  assert.equal(session.value(SESSION_KEY), undefined);
});

test("legacy session storage remains readable during migration", () => {
  const local = storage();
  const session = storage({ "ae-orbit:session": JSON.stringify({ member: "member-1", csrf: "x".repeat(32) }) });
  assert.equal(loadSession(local, session).member, "member-1");
});
