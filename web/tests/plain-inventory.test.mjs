import test from "node:test";
import assert from "node:assert/strict";
import { stockText, stockFreshness, inventorySummary } from "../src/plain-inventory.mjs";

test("unqueried candidates are distinct from confirmed zero and stale stock", () => {
  assert.equal(stockText({ quantity: "0", quantity_known: false }), "未查询");
  assert.equal(stockText({ quantity: "0", quantity_known: true }), "0");
  assert.equal(stockText({ quantity: "42" }), "42");
  assert.equal(stockFreshness({ quantity_known: false }, () => "wrong"), "等待精确查询");
  assert.equal(stockFreshness({ quantity_known: true, current_session: false }, () => "wrong"), "库存待同步");
});
test("summary describes export scope, partial progress and observation freshness", () => {
  assert.equal(inventorySummary(null, () => ""), "");
  const summary = inventorySummary({ scope: "nesql_plain", candidates: "53689", checked: "12", entries: "3", total_quantity: "9007199254740993", oldest_observation: "old" }, () => "20分钟前");
  assert.match(summary, /无 NBT/);
  assert.match(summary, /有库存 3 种/);
  assert.match(summary, /12\/53,689/);
  assert.match(summary, /9,007,199,254,740,993 件/);
  assert.match(summary, /最早观测 20分钟前/);
});
