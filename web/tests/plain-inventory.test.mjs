import test from "node:test";
import assert from "node:assert/strict";
import { stockText, stockFreshness, inventorySummary, resourceUnit } from "../src/plain-inventory.mjs";

test("unqueried candidates are distinct from confirmed zero and stale stock", () => {
  assert.equal(stockText({ quantity: "0", quantity_known: false }), "未查询");
  assert.equal(stockText({ quantity: "0", quantity_known: true }), "0");
  assert.equal(stockText({ quantity: "42" }), "42");
  assert.equal(stockFreshness({ quantity_known: false }, () => "wrong"), "等待精确查询");
  assert.equal(stockFreshness({ quantity_known: true, current_session: false }, () => "wrong"), "库存待同步");
});
test("resource quantities keep units and remain unknown until observed",()=>{
  assert.equal(stockText({kind:"fluid",quantity:"125000"}),"125,000 mB");
  assert.equal(stockText({kind:"essentia",quantity:"69"}),"69 源质");
  assert.equal(stockText({kind:"fluid",quantity:"0",quantity_known:false}),"未查询");
  assert.equal(resourceUnit({kind:"item"}),"");
  const stats={scope:"nesql_plain",resources:[{kind:"fluid",entries:"2",checked:"4",candidates:"10",total_quantity:"2500",unit:"mB",supported:false}]};
  assert.match(inventorySummary(stats,()=>"","fluid"),/2,500 mB/);
  assert.match(inventorySummary(stats,()=>"","fluid"),/等待支持此类型的 OC/);
  assert.match(inventorySummary(stats,()=>"","essentia"),/目录尚未启用/);
});
test("summary describes export scope, partial progress and observation freshness", () => {
  assert.equal(inventorySummary(null, () => ""), "");
  const summary = inventorySummary({ scope: "nesql_plain", candidates: "53689", checked: "12", entries: "3", total_quantity: "9007199254740993", last_observation: "recent", pending: "1" }, () => "20分钟前");
  assert.match(summary, /无 NBT/);
  assert.match(summary, /有库存 3 种/);
  assert.match(summary, /12\/53,689/);
  assert.match(summary, /9,007,199,254,740,993 件/);
  assert.match(summary, /最近更新 20分钟前/);
});
