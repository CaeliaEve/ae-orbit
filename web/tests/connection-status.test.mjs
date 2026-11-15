import test from "node:test";
import assert from "node:assert/strict";
import { connectionTestText, connectionTestTone, normalizeConnectionTest } from "../src/connection-status.mjs";

const dateTime = value => value ? `时间:${value}` : "尚无记录";

test("connection test state is bounded and defaults to idle", () => {
  assert.deepEqual(normalizeConnectionTest({ status: "unknown", message: 42 }), {
    status: "idle", checkedAt: "", message: "",
  });
  assert.equal(connectionTestTone("error"), "warn");
  assert.equal(connectionTestTone("checking"), "idle");
});

test("connection test labels remain useful without a timestamp", () => {
  assert.equal(connectionTestText({ status: "checking" }, dateTime), "正在测试当前会话连接…");
  assert.equal(connectionTestText({ status: "error", message: "后端不可用" }, dateTime), "最近测试失败 · 后端不可用 · 尚无记录");
});
