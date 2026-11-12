import test from "node:test";
import assert from "node:assert/strict";
import { loadOrderSnapshot } from "../src/orders.mjs";

const order = (id, state, changes = {}) => ({
  id, state, network_id: "network-1", user_id: "member-1", item_id: "1",
  quantity: "64", attempt_id: "", reason: "", batch_id: "batch-1",
  created_at: "2026-11-11T00:00:00Z", updated_at: "2026-11-11T00:00:00Z",
  expires_at: "2026-11-11T01:00:00Z", ...changes,
});

function source(pages) {
  const calls = [];
  return {
    calls,
    async fetch(query) {
      calls.push(query);
      return pages[query.state] ?? [];
    },
  };
}

test("active tasks are read by state instead of being hidden behind recent completed orders", async () => {
  const api = source({
    "": Array.from({ length: 200 }, (_, index) => order(`done-${index}`, "done")),
    queued: [order("old-queued", "queued", { created_at: "2026-11-01T00:00:00Z" })],
    running: [order("running-1", "running")],
  });
  const result = await loadOrderSnapshot(api.fetch);
  assert.deepEqual(result.orders.map(value => value.id), ["running-1", "old-queued"]);
  assert.equal(result.active_count, 2);
  assert.equal(result.active_truncated, false);
  assert.deepEqual(result.limited_states, []);
  assert.equal(result.recent_only, false);
  assert.deepEqual(api.calls, ["queued", "assigned", "computing", "running"].map(state => ({ state, limit: "200" })));
});

test("each filter retains independent active counts and only displays its own result pages", async () => {
  const pages = {
    queued: [order("q", "queued")], running: [order("r", "running")],
    failed: [order("f", "failed")], needs_review: [order("n", "needs_review")],
    done: [order("d", "done")], canceled: [order("c", "canceled")],
    expired: [order("e", "expired")], "": [order("recent", "done")],
  };
  for (const [filter, expected, requested] of [
    ["attention", ["f", "n"], ["queued", "assigned", "computing", "running", "failed", "needs_review"]],
    ["done", ["c", "d", "e", "f"], ["queued", "assigned", "computing", "running", "done", "failed", "canceled", "expired"]],
    ["all", ["recent"], ["queued", "assigned", "computing", "running", ""]],
  ]) {
    const api = source(pages);
    const result = await loadOrderSnapshot(api.fetch, filter);
    assert.deepEqual(result.orders.map(value => value.id), expected, filter);
    assert.equal(result.active_count, 2, filter);
    assert.equal(result.recent_only, filter === "all", filter);
    assert.deepEqual(api.calls, requested.map(state => ({ state, limit: "200" })), filter);
  }
});

test("overlapping state pages deduplicate the newest revision and preserve deterministic creation order", async () => {
  const older = order("x", "queued", { updated_at: "2026-11-11T00:00:01.000100Z" });
  const newer = order("x", "running", { updated_at: "2026-11-11T00:00:01.000900Z" });
  const pages = {
    queued: [older, order("b", "queued"), order("last", "queued", { created_at: "2026-11-10T23:59:59Z" })],
    running: [newer, order("a", "running"), order("first", "running", { created_at: "2026-11-11T00:00:00.000100Z" })],
  };
  const before = structuredClone(pages);
  const result = await loadOrderSnapshot(source(pages).fetch);
  assert.deepEqual(result.orders.map(value => value.id), ["first", "a", "b", "x", "last"]);
  assert.equal(result.orders.find(value => value.id === "x").state, "running");
  assert.equal(result.active_count, 5);
  assert.deepEqual(pages, before, "loading must not mutate supplied pages");
});

test("state-page caps report truncation separately for navigation and the selected view", async () => {
  const queued = Array.from({ length: 200 }, (_, index) => order(`queued-${index}`, "queued"));
  const failed = Array.from({ length: 200 }, (_, index) => order(`failed-${index}`, "failed"));
  const api = source({ queued, failed, "": failed });
  const attention = await loadOrderSnapshot(api.fetch, "attention");
  assert.equal(attention.orders.length, 200);
  assert.equal(attention.active_count, 200);
  assert.equal(attention.active_truncated, true);
  assert.deepEqual(attention.limited_states, ["failed"]);
  assert.equal(attention.recent_only, false);
  const all = await loadOrderSnapshot(api.fetch, "all");
  assert.deepEqual(all.limited_states, [""]);
  assert.equal(all.recent_only, true);
  const belowCap = await loadOrderSnapshot(source({ queued: queued.slice(0, 199) }).fetch);
  assert.equal(belowCap.active_truncated, false);
  assert.deepEqual(belowCap.limited_states, []);
});

test("a failed or malformed state page never becomes an apparently empty snapshot", async () => {
  const failure = new Error("backend unavailable");
  await assert.rejects(loadOrderSnapshot(async ({ state }) => {
    if (state === "running") throw failure;
    return [];
  }), error => error === failure);
  for (const malformed of [null, undefined, {}, { orders: [] }]) {
    await assert.rejects(loadOrderSnapshot(async ({ state }) => state === "assigned" ? malformed : []), TypeError);
  }
});

test("unsupported filters are rejected before any state requests", async () => {
  let calls = 0;
  for (const filter of ["unknown", "queued", "", null, "__proto__", "constructor", ["active"]]) {
    await assert.rejects(loadOrderSnapshot(async () => { calls++; return []; }, filter), /filter/i);
  }
  assert.equal(calls, 0);
});
