import test from "node:test";
import assert from "node:assert/strict";
import { deviceMemoryText, networkNodeModel, networkNodesHTML, networkOverviewHTML, networkSidebarHTML } from "../src/network-monitor.mjs";

const now = Date.parse("2026-11-20T12:00:00Z");
const recent = "2026-11-20T11:59:55Z";
const nodes = [
  { id: "worker", name: "库存助手", role: "quantity_worker", online: true, last_seen: recent, health: { free_memory: "1585933", diagnostics: [{ code: "native_scan_disabled", count: "9" }] } },
  { id: "primary", name: "主 OC", role: "primary", session_id: "session-main", online: true, last_seen: recent, capabilities: { native_scan_disabled: true, events_disabled: true } },
  { id: "stale", name: "慢速助手", role: "quantity_worker", online: true, last_seen: "2026-11-20T11:50:00Z" },
  { id: "offline", role: "quantity_worker", online: false },
  { id: "diagnostic", role: "quantity_worker", online: true, last_seen: recent, health: JSON.stringify({ diagnostics: [{ code: "low_memory", count: "3" }], read_failures: [{ code: "identity_mismatch", item_id: "12" }] }) },
];

test("node attention separates query protection from offline nodes and actual diagnostics", () => {
  const model = networkNodeModel(nodes, {}, now);
  assert.deepEqual(model.counts, { all: 5, online: 3, attention: 3 });
  assert.equal(model.rows.find(row => row.key === "worker").attention, false);
  assert.equal(model.rows.find(row => row.key === "diagnostic").attention, true);
  assert.deepEqual(networkNodeModel(nodes, { filter: "attention" }, now).visible.map(row => row.key), ["stale", "offline", "diagnostic"]);
});

test("node search combines identity, role, and status filters and keeps primary first", () => {
  assert.equal(networkNodeModel(nodes, {}, now).visible[0].key, "primary");
  assert.deepEqual(networkNodeModel(nodes, { search: "SESSION-main", filter: "online", role: "primary" }, now).visible.map(row => row.key), ["primary"]);
  assert.deepEqual(networkNodeModel(nodes, { search: "数量助手 库存", filter: "online" }, now).visible.map(row => row.key), ["worker"]);
  assert.equal(networkNodeModel(nodes, { role: "primary", search: "库存" }, now).visible.length, 0);
});

test("missing health and memory remain unknown and large memory values retain precision", () => {
  assert.equal(deviceMemoryText(undefined), "未上报");
  assert.equal(deviceMemoryText("not-a-number"), "未上报");
  assert.equal(deviceMemoryText("0"), "0 B");
  assert.equal(deviceMemoryText("65536"), "64 KiB");
  assert.equal(deviceMemoryText("1585933"), "1.5 MiB");
  assert.equal(deviceMemoryText("9007199254740993"), "8589934592 MiB");
  assert.deepEqual(networkNodeModel([{ id: "x", health: "bad-json" }], {}, now).rows[0].health, {});
});

test("query protection requires both flags and offline protection is labeled as last reported", () => {
  const devices = [
    { ...nodes[1], online: false },
    { id: "partial", online: true, last_seen: recent, capabilities: { native_scan_disabled: true } },
  ];
  const model = networkNodeModel(devices, {}, now);
  assert.equal(model.rows[0].exact, true);
  assert.equal(model.rows[1].exact, false);
  const html = networkNodesHTML(model, { expanded: new Set(["primary"]) });
  assert.match(html, /data-node-key="primary"[^>]* open/);
  assert.match(html, /上次节点上报/);
  assert.match(html, /查询保护未完整上报/);
});

test("node rendering escapes reported names, IDs and diagnostic values", () => {
  const html = networkNodesHTML(networkNodeModel([{
    id: 'x" onclick="bad', name: "<script>alert(1)</script>", online: true, last_seen: recent,
    session_id: "<iframe>", health: { diagnostics: [{ code: "<img onerror=bad>" }] },
  }], {}, now));
  assert.doesNotMatch(html, /<script>|<iframe>|<img| onclick="bad/);
  assert.match(html, /&lt;script&gt;/);
  assert.match(html, /&quot; onclick=&quot;bad/);
});

test("empty and filtered rosters offer the appropriate recovery without fabricated nodes", () => {
  assert.match(networkNodesHTML(networkNodeModel([], {}, now)), /等待 OC 节点接入/);
  const filtered = networkNodesHTML(networkNodeModel(nodes, { search: "不存在" }, now));
  assert.match(filtered, /清除筛选/);
  assert.doesNotMatch(filtered, /<details/);
});

test("overview and sidebar preserve unknown reader counts and offline capability provenance", () => {
  const network = { id: "network", name: "主基地", online: false, capabilities: { inventory: true } };
  const html = networkOverviewHTML(network, [], "closed", {}, now);
  assert.match(html, /网络离线/);
  assert.match(html, /数量读取器<\/span><strong>—/);
  assert.match(networkSidebarHTML(network, "reconnecting"), /显示上次上报的能力/);
  assert.match(networkSidebarHTML(network, "reconnecting"), /事件流重连中/);
});
