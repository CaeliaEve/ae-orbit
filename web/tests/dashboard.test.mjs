import test from "node:test";
import assert from "node:assert/strict";
import { dashboardTasks, dashboardInventory, dashboardTasksHTML } from "../src/dashboard.mjs";

const job = (id, fields = {}) => ({ id, created_at: "2026-11-20T00:00:00Z", control: "active", counts: { running: "1", failed: "0", needs_review: "0", rejected: "0" }, ...fields });
const snapshot = rows => ({ jobs: new Map(rows.map(row => [row.id, row])) });

test("missing, loading and failed task observations do not claim an empty queue", () => {
  for (const state of [{}, { loading: true }, { error: { status: 503 } }, { error: { status: 403 } }]) {
    const model = dashboardTasks(state, { activeCount: 9 });
    assert.equal(model.count, null);
    assert.ok(model.notice);
    assert.doesNotMatch(model.empty, /没有未结束/);
  }
  assert.match(dashboardTasks({ error: { status: 403 } }).empty, /无权/);
});

test("production jobs exclude settled rows and never add child order counts", () => {
  const model = dashboardTasks({ sync: snapshot([job("b"), job("a"), job("settled", { settled_at: "2026-11-20T01:00:00Z" })]) }, { activeCount: 500 });
  assert.equal(model.count, 2);
  assert.deepEqual(model.rows.map(row => row.id), ["a", "b"]);
  assert.equal(model.legacy, false);
  assert.equal(dashboardTasks({ sync: snapshot([]) }).count, 0);
});

test("stale task snapshots retain counts and disclose refresh failures", () => {
  const model = dashboardTasks({ sync: snapshot([job("a")]), error: { status: 503 } });
  assert.equal(model.count, 1);
  assert.match(model.notice, /显示上次记录/);
});

test("attention counts distinguish paused, rejected and review tasks using exact counts", () => {
  const model = dashboardTasks({ sync: snapshot([job("a", { control: "paused" }), job("b", { counts: { rejected: "9007199254740993" } }), job("c"), job("d", { counts: { needs_review: "1" } })]) });
  assert.equal(model.attention, 3);
  assert.equal(model.count, 4);
});

test("unsupported production jobs fall back to observed active orders and mark limits", () => {
  assert.equal(dashboardTasks({ support: "unsupported" }).count, null);
  const model = dashboardTasks({ support: "unsupported" }, { snapshot: {}, rows: [{ id: "done", state: "done" }, { id: "running", state: "running" }], activeCount: 200, truncated: true });
  assert.equal(model.count, 200);
  assert.equal(model.lowerBound, true);
  assert.deepEqual(model.rows.map(row => row.id), ["running"]);
  assert.match(dashboardTasks({ support: "unsupported" }, { snapshot: {}, rows: [], activeCount: 2 }).empty, /前往合成中心/);
});

test("inventory uses whole-network exact totals and preserves pending candidates", () => {
  const model = dashboardInventory({ scope: "nesql_plain", candidates: "53689", checked: "53688", entries: "3096", pending: "1", total_quantity: "9007199254740993" });
  assert.equal(model.total, "9007199254740993");
  assert.equal(model.entries, "3096");
  assert.equal(model.pending, "1");
  assert.match(model.coverage, /53,688 \/ 53,689/);
  assert.equal(dashboardInventory(null).known, false);
  assert.equal(dashboardInventory({ pending: "0" }).pending, null);
});

test("task preview is bounded, newest first, escaped and opens the actual task", () => {
  const rows = Array.from({ length: 5 }, (_, i) => job(`job-${i}`, { name: '<script>"', created_at: `2026-11-20T0${i}:00:00Z` }));
  const model = dashboardTasks({ sync: snapshot(rows) });
  const html = dashboardTasksHTML(model, { age: () => "1 分钟前" });
  assert.equal((html.match(/class="dashboard-task-row"/g) || []).length, 3);
  assert.match(html, /data-job="job-4"/);
  assert.doesNotMatch(html, /data-job="job-0"|<script>/);
  assert.match(html, /&lt;script&gt;&quot;/);
  assert.match(html, /还有 2 组任务/);
});
