import test from "node:test";
import assert from "node:assert/strict";
import {
  normalizeNetworkDraft,
  networkCreateResult,
  networkRefreshState,
  networkDashboardSummary,
  networkCapabilityRows,
} from "../src/network-ui.mjs";

test("network draft trims fields and defaults to the OC bridge", () => {
  assert.deepEqual(normalizeNetworkDraft({ name: "  主基地  ", server_group: "  survival  " }), {
    ok: true,
    value: { name: "主基地", server_group: "survival", source: "oc" },
  });
});

test("network draft rejects missing or oversized fields", () => {
  assert.equal(normalizeNetworkDraft({ name: "", server_group: "survival" }).error, "请输入网络名称。");
  assert.equal(normalizeNetworkDraft({ name: "main", server_group: "" }).error, "请输入 Minecraft 服务组。");
  assert.equal(normalizeNetworkDraft({ name: "x".repeat(129), server_group: "survival" }).error, "网络名称不能超过 128 个字符。");
  assert.equal(normalizeNetworkDraft({ name: "main", server_group: "x".repeat(129) }).error, "Minecraft 服务组不能超过 128 个字符。");
});

test("network draft rejects unknown sources", () => {
  assert.deepEqual(normalizeNetworkDraft({ name: "main", server_group: "survival", source: "remote" }), {
    ok: false,
    error: "网络来源无效。",
  });
});

test("network create result keeps the network and one-time device token", () => {
  const network = { id: "network-1", name: "主基地" };
  assert.deepEqual(networkCreateResult({ network, device_token: "secret-token" }), {
    network,
    deviceToken: "secret-token",
  });
  assert.deepEqual(networkCreateResult({ network }), { network, deviceToken: "" });
  assert.deepEqual(networkCreateResult({}), { network: null, deviceToken: "" });
});

test("network refresh state distinguishes loading, recoverable failure, and clear", () => {
  assert.deepEqual(networkRefreshState({ loading: true }), { hidden: false, tone: "loading", text: "正在刷新网络状态…", retry: false });
  assert.deepEqual(networkRefreshState({ error: "后端不可用。", hasSnapshot: true }), { hidden: false, tone: "error", text: "刷新失败，已保留上次数据。后端不可用。", retry: true });
  assert.deepEqual(networkRefreshState(), { hidden: true, tone: "", text: "", retry: false });
});

test("network dashboard summary stays useful with partial or empty device data", () => {
  const now = Date.parse("2026-11-14T12:00:00Z");
  assert.deepEqual(networkDashboardSummary({ online: true, last_seen: "2026-11-14T11:59:30Z" }, [], now), {
    total: 0,
    online: 0,
    offline: 0,
    stale: 0,
    health: "waiting",
    lastCommunication: "2026-11-14T11:59:30Z",
    serviceCount: 0,
  });

  const summary = networkDashboardSummary({
    online: true,
    last_seen: "2026-11-14T11:59:00Z",
    capabilities: { inventory: true, craft: true, cpu_selection: true },
  }, [
    { online: true, last_seen: "2026-11-14T11:59:40Z" },
    { online: false, last_seen: "2026-11-14T11:50:00Z" },
  ], now);
  assert.equal(summary.total, 2);
  assert.equal(summary.online, 1);
  assert.equal(summary.offline, 1);
  assert.equal(summary.health, "degraded");
  assert.equal(summary.serviceCount, 3);
});

test("network capability rows expose stable labels and tones", () => {
  assert.deepEqual(networkCapabilityRows({
    inventory: true,
    recipes: false,
    craft: true,
    cpu_output: true,
    cpu_selection: false,
  }, { quantity_readers: 2 }), [
    { key: "inventory", label: "库存同步", value: "可用", tone: "ok", detail: "2 个数量读取器" },
    { key: "recipes", label: "配方读取", value: "不可用", tone: "idle", detail: "当前网络未声明" },
    { key: "craft", label: "合成下单", value: "可用", tone: "ok", detail: "支持生产任务" },
    { key: "cpu_output", label: "CPU 输出", value: "可用", tone: "ok", detail: "可读取最终产物" },
    { key: "cpu_selection", label: "严格指定 CPU", value: "不可用", tone: "idle", detail: "需 OC 支持唯一 CPU 名称" },
  ]);
});
