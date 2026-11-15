import test from "node:test";
import assert from "node:assert/strict";
import { deviceState, healthSummary, networkDeviceSummary, parseHealth, roleLabel } from "../src/device-status.mjs";

test("device status distinguishes offline, stale and online nodes", () => {
  const now = Date.parse("2026-11-13T00:00:00Z");
  assert.equal(deviceState({ online: true, last_seen: "2026-11-12T23:57:00Z" }, now).key, "stale");
  assert.equal(deviceState({ online: false }, now).key, "offline");
  assert.equal(deviceState({ online: true, last_seen: "2026-11-13T00:00:00Z" }, now).key, "online");
  assert.equal(deviceState({ online: true, revoked: true }, now).key, "revoked");
});

test("device health parsing is tolerant and bounded to visible counts", () => {
  assert.deepEqual(parseHealth("not-json"), {});
  assert.deepEqual(healthSummary({ free_memory: "65536", diagnostics: [{ code: "x" }], read_failures: [{ code: "y" }], inventory: { batches: "4" } }), {
    diagnostics: 1, readFailures: 1, memory: "65536", inventoryBatches: "4",
  });
  assert.equal(roleLabel("quantity_worker"), "数量助手");
  assert.equal(roleLabel("other"), "other");
});

test("network device summary counts stale nodes separately", () => {
  const now = Date.parse("2026-11-13T00:00:00Z");
  assert.deepEqual(networkDeviceSummary([
    { online: true, last_seen: "2026-11-13T00:00:00Z" },
    { online: true, last_seen: "2026-11-12T23:57:00Z" },
    { online: false },
  ], now), { total: 3, online: 1, offline: 1, stale: 1 });
});
