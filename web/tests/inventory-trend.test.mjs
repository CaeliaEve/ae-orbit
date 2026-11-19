import test from "node:test";
import assert from "node:assert/strict";
import { inventoryTrendHTML, inventoryTrendModel, loadTrendHistory, recordTrendSample, saveTrendHistory, trendStorageKey } from "../src/inventory-trend.mjs";

const minute = 60_000;
const at = Math.floor(Date.now() / minute) * minute;

test("selected time range excludes older samples without filling gaps or rounding quantities", () => {
  const samples = [{ at: at - 120 * minute, quantity: "9007199254740993" }, { at: at - 59 * minute, quantity: "9007199254740994" }, { at, quantity: "9007199254740995" }];
  const hour = inventoryTrendModel(samples, at, { windowMs: 60 * minute });
  assert.equal(hour.samples, 2);
  assert.equal(hour.first.quantity, "9007199254740994");
  assert.deepEqual(hour.segments.map(segment => segment.length), [1, 1]);
  assert.equal(inventoryTrendModel(samples, at, { windowMs: 6 * 60 * minute }).samples, 3);
  assert.equal(inventoryTrendModel(samples, at, { windowMs: -1 }).samples, 3);
  assert.equal(inventoryTrendModel(samples.slice(0, 1), at, { windowMs: 60 * minute }), null);
});

test("minute samples retain exact totals and replace only the current minute", () => {
  let samples = recordTrendSample([], "9007199254740993", at);
  samples = recordTrendSample(samples, "9007199254740994", at + 5000);
  samples = recordTrendSample(samples, "0", at + minute);
  assert.deepEqual(samples, [{ at: at + 5000, quantity: "9007199254740994" }, { at: at + minute, quantity: "0" }]);
  assert.deepEqual(recordTrendSample(samples, "unknown", at + minute), samples);
  assert.deepEqual(recordTrendSample(samples, 9007199254740994, at + minute), samples);
});

test("history is bounded to a day and storage is isolated by member, network and scope", () => {
  const samples = Array.from({ length: 1500 }, (_, index) => ({ at: at - (1499 - index) * minute, quantity: String(index) }));
  const values = new Map();
  const storage = { getItem: key => values.get(key), setItem: (key, value) => values.set(key, value) };
  const key = trendStorageKey("member", "network", "nesql_plain");
  saveTrendHistory(storage, key, samples);
  const loaded = loadTrendHistory(storage, key, at);
  assert.equal(loaded.length, 1441);
  assert.equal(loaded[0].at, at - 24 * 60 * minute);
  assert.equal(loaded.at(-1).quantity, "1499");
  for (const other of [trendStorageKey("other", "network", "nesql_plain"), trendStorageKey("member", "other", "nesql_plain"), trendStorageKey("member", "network", "observed")]) {
    assert.deepEqual(loadTrendHistory(storage, other, at), []);
  }
});

test("corrupt, oversized, future and invalid cached samples do not create observations", () => {
  assert.deepEqual(loadTrendHistory({ getItem: () => "{" }, "key", at), []);
  assert.deepEqual(loadTrendHistory({ getItem: () => "x".repeat(256 * 1024 + 1) }, "key", at), []);
  const raw = JSON.stringify({ version: 1, samples: [
    { at: at + minute, quantity: "99" }, { at, quantity: "-1" }, { at: at - minute, quantity: "42" },
  ] });
  assert.deepEqual(loadTrendHistory({ getItem: () => raw }, "key", at), [{ at: at - minute, quantity: "42" }]);
  assert.doesNotThrow(() => saveTrendHistory({ setItem: () => { throw new Error("quota"); } }, "key", []));
});

test("x coordinates follow elapsed time and labels describe clock times", () => {
  const model = inventoryTrendModel([{ at: at - 60 * minute, quantity: "47000000" }, { at: at - 50 * minute, quantity: "47500000" }, { at, quantity: "49000000" }], at);
  const points = model.segments.flat();
  assert.equal(points[0].x, 0);
  assert.equal(points[1].x, 120);
  assert.equal(points[2].x, 720);
  assert.equal(model.times.length, 6);
  assert.ok(model.times.every(time => /^\d{2}:\d{2}$/.test(time.label)));
  assert.ok(model.ticks.some(tick => tick.label.endsWith("M")));
});

test("large totals preserve small changes without rounded duplicate axis labels", () => {
  const model = inventoryTrendModel([{ at: at - minute, quantity: "9007199254740993" }, { at, quantity: "9007199254740994" }], at);
  assert.ok(model.baseline);
  assert.equal(new Set(model.ticks.map(tick => tick.label)).size, model.ticks.length);
  assert.notEqual(model.segments[0][0].y, model.segments[0][1].y);
  assert.match(inventoryTrendHTML([{ at, quantity: "9007199254740993" }], { now: at }), /9,007,199,254,740,993/);
});

test("flat and single observations render finite geometry without invented history", () => {
  for (const quantity of ["0", "47000000"]) {
    const samples = [{ at: at - minute, quantity }, { at, quantity }];
    const model = inventoryTrendModel(samples, at);
    assert.equal(model.segments[0][0].y, model.segments[0][1].y);
    assert.ok(model.segments.flat().every(point => Number.isFinite(point.x) && Number.isFinite(point.y)));
  }
  const html = inventoryTrendHTML([{ at, quantity: "47000000" }], { now: at });
  assert.match(html, /正在积累时间采样/);
  assert.match(html, /trend-point/);
  assert.doesNotMatch(html, /class="trend-line"/);
});

test("periods without sampling break the line rather than inventing a continuous series", () => {
  const model = inventoryTrendModel([{ at: at - 20 * minute, quantity: "47" }, { at: at - 19 * minute, quantity: "48" }, { at, quantity: "49" }], at);
  assert.deepEqual(model.segments.map(segment => segment.length), [2, 1]);
});

test("chart markup exposes quantity and time axes, escapes text and respects the style CSP", () => {
  const html = inventoryTrendHTML([{ at: at - minute, quantity: "47000000" }, { at, quantity: "49000000" }], { now: at });
  assert.match(html, /aria-label="库存数量"/);
  assert.match(html, /aria-label="采样时间"/);
  assert.match(html, /datetime="/);
  assert.doesNotMatch(html, /style=|样本 1|Math\.log10/);
  assert.match(inventoryTrendHTML([], { now: at, empty: "<unknown>" }), /&lt;unknown&gt;/);
});
