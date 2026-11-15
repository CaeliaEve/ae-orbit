import test from "node:test";
import assert from "node:assert/strict";
import { networkErrorKind, networkRecoveryState, recoveryEvidence } from "../src/network-recovery.mjs";

const now = Date.parse("2026-11-13T00:00:00Z");

test("network recovery reports a healthy cooperative network", () => {
  assert.deepEqual(networkRecoveryState({
    network: { name: "主基地", online: true },
    devices: [
      { online: true, last_seen: "2026-11-13T00:00:00Z" },
      { online: true, last_seen: "2026-11-13T00:00:00Z" },
    ],
    streamState: "connected",
  }, now), {
    key: "healthy",
    tone: "ok",
    label: "网络状态正常",
    detail: "2 / 2 台 OC 在线，事件流已连接。",
    action: "",
    actionLabel: "",
  });
});

test("network recovery prioritizes unavailable and stale states", () => {
  assert.equal(networkRecoveryState({ network: null }, now).key, "unavailable");
  assert.equal(networkRecoveryState({
    network: { name: "主基地", online: false },
    devices: [{ online: false }],
    streamState: "closed",
  }, now).key, "heartbeat_waiting");
  const stale = networkRecoveryState({
    network: { name: "主基地", online: true },
    devices: [{ online: true, last_seen: "2026-11-12T23:57:00Z" }],
    streamState: "connected",
  }, now);
  assert.equal(stale.key, "stale");
  assert.equal(stale.action, "refresh");
  assert.match(stale.detail, /数据滞后/);
});

test("stream recovery offers a reconnect action without hiding a refresh failure", () => {
  const reconnecting = networkRecoveryState({
    network: { name: "主基地", online: true },
    devices: [{ online: true, last_seen: "2026-11-13T00:00:00Z" }],
    streamState: "reconnecting",
  }, now);
  assert.equal(reconnecting.key, "reconnecting");
  assert.equal(reconnecting.label, "事件流等待重连");
  assert.equal(reconnecting.action, "reconnect");
  assert.match(reconnecting.detail, /实时事件尚未恢复/);

  const failed = networkRecoveryState({
    network: { name: "主基地", online: true },
    devices: [],
    streamState: "connected",
    loading: false,
    error: "后端暂时不可用。",
    hasSnapshot: true,
  }, now);
  assert.equal(failed.key, "backend_error");
  assert.equal(failed.action, "refresh");
  assert.match(failed.detail, /保留上次数据/);
});

test("network recovery separates backend reachability from OC heartbeat states", () => {
  const unavailable = networkRecoveryState({
    network: { name: "主基地", online: true },
    devices: [],
    error: "无法连接后端。",
    errorKind: "backend_unavailable",
    hasSnapshot: true,
  }, now);
  assert.deepEqual(unavailable, {
    key: "backend_unavailable",
    tone: "warn",
    label: "后端不可达",
    detail: "无法读取最新网络状态，已保留上次数据；请检查服务器进程或 HTTPS 入口。",
    action: "refresh",
    actionLabel: "重试",
  });

  const waiting = networkRecoveryState({
    network: { name: "主基地", online: false },
    devices: [{ online: false }, { online: false }],
    streamState: "closed",
  }, now);
  assert.equal(waiting.key, "heartbeat_waiting");
  assert.equal(waiting.label, "等待游戏心跳");
  assert.match(waiting.detail, /存档|区块|OC/);

  const offline = networkRecoveryState({
    network: { name: "主基地", online: false, last_seen: "2026-11-12T23:50:00Z" },
    devices: [{ online: false, last_seen: "2026-11-12T23:50:00Z" }],
    streamState: "closed",
  }, now);
  assert.equal(offline.key, "oc_offline");
  assert.equal(offline.label, "OC 桥接器离线");
  assert.match(offline.detail, /最近心跳/);
});

test("network recovery exposes bounded evidence timestamps and error classification", () => {
  assert.equal(networkErrorKind({ status: 0, code: "network_error" }), "backend_unavailable");
  assert.equal(networkErrorKind({ status: 503, code: "request_failed" }), "backend_error");
  assert.equal(networkErrorKind(null), "backend_error");
  assert.deepEqual(recoveryEvidence({
    network: { last_seen: "2026-11-13T00:00:00Z", last_inventory: "2026-11-12T23:58:00Z" },
    devices: [
      { last_seen: "2026-11-12T23:59:00Z", last_inventory: "2026-11-12T23:57:00Z" },
      { last_seen: "2026-11-13T00:01:00Z" },
    ],
  }), {
    lastSeen: "2026-11-13T00:01:00Z",
    lastInventory: "2026-11-12T23:58:00Z",
  });
});
