import test from "node:test";
import assert from "node:assert/strict";
import { connectionDiagnostics, streamStateTone, streamStateText } from "../src/connection-diagnostics.mjs";

const now = Date.parse("2026-11-13T00:00:00Z");

test("connection diagnostics summarize session, stream and OC health", () => {
  const result = connectionDiagnostics({
    origin: "http://127.0.0.1:56607",
    member: { id: "member-1" },
    session: { expiresAt: "2026-11-13T01:00:00Z" },
    network: {
      id: "net-1",
      name: "主基地",
      online: true,
      last_seen: "2026-11-12T23:59:00Z",
      last_inventory: "2026-11-12T23:58:00Z",
    },
    devices: [
      { online: true, last_seen: "2026-11-13T00:00:00Z" },
      { online: true, last_seen: "2026-11-12T23:57:00Z" },
      { online: false },
    ],
    streamState: "connected",
    connectionTest: { status: "ok", checkedAt: "2026-11-12T23:59:30Z" },
  }, now);

  assert.equal(result.origin, "http://127.0.0.1:56607");
  assert.equal(result.session.key, "active");
  assert.equal(result.network.label, "主基地 · 在线");
  assert.equal(result.stream.key, "connected");
  assert.deepEqual(result.devices, { total: 3, online: 1, offline: 1, stale: 1 });
  assert.equal(result.lastCommunication, "2026-11-13T00:00:00Z");
  assert.equal(result.lastInventory, "2026-11-12T23:58:00Z");
  assert.equal(result.connectionTest.status, "ok");
});

test("connection diagnostics distinguish expired sessions and unavailable services", () => {
  const result = connectionDiagnostics({
    member: { id: "member-1" },
    session: { expiresAt: "2026-11-12T23:59:59Z" },
    network: { name: "主基地", online: false },
    devices: [],
    streamState: "closed",
  }, now);

  assert.equal(result.session.key, "expired");
  assert.equal(result.network.key, "offline");
  assert.equal(result.stream.key, "closed");
  assert.equal(result.lastCommunication, "");
  assert.equal(result.lastInventory, "");
  assert.equal(result.origin, "当前页面");
});

test("stream state labels stay bounded and readable", () => {
  assert.equal(streamStateText("connected"), "事件流已连接");
  assert.equal(streamStateText("reconnecting"), "事件流重连中");
  assert.equal(streamStateText("closed"), "事件流已关闭");
  assert.equal(streamStateText("other"), "事件流已关闭");
  assert.equal(streamStateTone("connected"), "ok");
  assert.equal(streamStateTone("reconnecting"), "warn");
  assert.equal(streamStateTone("closed"), "idle");
});
