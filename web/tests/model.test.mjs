import test from "node:test";
import assert from "node:assert/strict";
import * as model from "../src/model.mjs";
import {
  newId, normalizeQuantity, formatQuantity, storageKey, loadDraft, saveDraft,
  addDraft, updateDraft, removeDraft, markSubmitting, completeSubmission,
} from "../src/model.mjs";

const item = (id = "1") => ({
  id, name: "minecraft:iron_ingot", damage: 0, label: "Iron ingot",
  exact: true, craftable: true,
});
const memoryStorage = () => {
  const values = new Map();
  return {
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
  };
};
const draft = () => addDraft([], item(), "10");
const pending = () => markSubmitting(draft(), undefined, "batch-1");

test("network quality labels distinguish backend observations from synchronization alerts", () => {
  assert.equal(typeof model.networkQualityLabel, "function");
  for (const [value, expected] of [
    ["unverified", "逐项观测"],
    ["degraded", "存在同步告警"],
    ["current", "数据状态未知"],
    ["fresh", "数据状态未知"],
    ["unknown", "数据状态未知"],
    [null, "数据状态未知"],
    [undefined, "数据状态未知"],
    ["__proto__", "数据状态未知"],
    ["constructor", "数据状态未知"],
    ["toString", "数据状态未知"],
  ]) {
    assert.equal(model.networkQualityLabel(value), expected, `quality: ${String(value)}`);
  }
});

test("quantities remain exact decimal strings and reject unsafe forms", () => {
  assert.equal(normalizeQuantity(" 00012 "), "12");
  assert.equal(normalizeQuantity("9223372036854775807"), "9223372036854775807");
  assert.equal(normalizeQuantity(12), "12");
  for (const value of ["0", "-1", "1.2", "1e3", "", "9223372036854775808", Number.MAX_SAFE_INTEGER + 1]) {
    assert.throws(() => normalizeQuantity(value));
  }
  assert.throws(() => normalizeQuantity("11", "10"));
});

test("quantity formatting does not round large inventories", () => {
  assert.equal(formatQuantity("9223372036854775807"), "9,223,372,036,854,775,807");
  assert.equal(formatQuantity("0"), "0");
  assert.equal(formatQuantity(undefined), "-");
  assert.equal(formatQuantity(Number.MAX_SAFE_INTEGER + 1), "-");
});

test("draft keys are isolated by both member and network", () => {
  assert.notEqual(storageKey("a", "b:c"), storageKey("a:b", "c"));
  assert.notEqual(storageKey("a", "b"), storageKey("other", "b"));
  assert.notEqual(storageKey("a", "b"), storageKey("a", "other"));
});

test("only editable identical item and CPU policies merge", () => {
  const first = draft();
  const merged = addDraft(first, item(), "5");
  assert.equal(first[0].quantity, "10");
  assert.equal(merged.length, 1);
  assert.equal(merged[0].quantity, "15");
  const cpu = addDraft(merged, item(), "3", "CPU A", false);
  const wait = addDraft(cpu, item(), "3", "CPU A", true);
  assert.equal(wait.length, 3);
  const submitting = markSubmitting(first, undefined, "batch-1");
  assert.equal(addDraft(submitting, item(), "1").length, 2);
  assert.throws(() => addDraft([], { ...item(), exact: false }, "1"));
  assert.throws(() => addDraft([], item(), "1", "", true));
});

test("immediate submission can keep its intent separate from an existing draft", () => {
  const existing = addDraft([], item(), "64");
  const entries = addDraft(existing, item(), "1", "", false, { merge: false });
  assert.equal(entries.length, 2);
  assert.equal(entries[0].id, existing[0].id);
  assert.equal(entries[0].quantity, "64");
  assert.equal(entries[1].quantity, "1");
  assert.notEqual(entries[1].id, existing[0].id);
  const submitting = markSubmitting(entries, [entries[1].id], "immediate-batch");
  assert.equal(submitting[0].status, "draft");
  assert.equal(submitting[0].quantity, "64");
  assert.equal(submitting[1].status, "submitting");
  assert.equal(submitting[1].quantity, "1");
});

test("item identities match backend registry and signed-int32 metadata bounds", () => {
  const storage = memoryStorage();
  const identity = { ...item(), name: "unqualified_registry", damage: 2147483647 };
  const entries = addDraft([], identity, "1");
  saveDraft(storage, "key", { entries, receipts: {} });
  const loaded = loadDraft(storage, "key");
  assert.equal(loaded.warning, undefined);
  assert.equal(loaded.entries[0].item.name, identity.name);
  assert.equal(loaded.entries[0].item.damage, identity.damage);
  for (const damage of [-1, 2147483648, 0.5]) {
    assert.throws(() => addDraft([], { ...item(), damage }, "1"));
  }
  assert.throws(() => addDraft([], { ...item(), name: "" }, "1"));
});

test("draft editing validates the entire resulting CPU policy", () => {
  const entries = addDraft([], item(), "1", "CPU A", true);
  const next = updateDraft(entries, entries[0].id, { quantity: "20" });
  assert.equal(next[0].quantity, "20");
  assert.equal(entries[0].quantity, "1");
  assert.throws(() => updateDraft(next, next[0].id, { cpu_name: "" }));
  assert.throws(() => updateDraft(next, next[0].id, { status: "submitting" }));
  assert.equal(removeDraft(next, next[0].id).length, 0);
});

test("uncertain and submitting entries cannot be edited or deleted", () => {
  const entries = pending();
  assert.throws(() => updateDraft(entries, entries[0].id, { quantity: "3" }));
  assert.throws(() => removeDraft(entries, entries[0].id));
  const unknown = completeSubmission(entries, entries[0].id, { uncertain: true });
  assert.equal(unknown[0].status, "uncertain");
  assert.throws(() => updateDraft(unknown, unknown[0].id, { cpu_name: "CPU B" }));
  assert.throws(() => markSubmitting(unknown, [unknown[0].id], "batch-2"));
});

test("submitting assigns separate persistent idempotency keys before network use", () => {
  const entries = addDraft(draft(), item("2"), "4");
  const next = markSubmitting(entries, undefined, "batch-1");
  assert.equal(entries[0].status, "draft");
  assert.equal(next[0].status, "submitting");
  assert.ok(next[0].request_key);
  assert.notEqual(next[0].request_key, next[1].request_key);
  assert.equal(next[0].batch_id, "batch-1");
  assert.throws(() => markSubmitting(entries, ["missing"], "batch-1"));
});

test("the same persisted draft always derives the same idempotency key across tabs", () => {
  const entries = draft();
  const tabA = markSubmitting(entries, undefined, "batch-a");
  const tabB = markSubmitting(structuredClone(entries), undefined, "batch-b");
  assert.equal(tabA[0].request_key, `draft-${entries[0].id}`);
  assert.equal(tabB[0].request_key, tabA[0].request_key);
});

test("IDs prefer randomUUID and use only cryptographic random bytes on HTTP LAN", () => {
  const original = Object.getOwnPropertyDescriptor(globalThis, "crypto");
  try {
    Object.defineProperty(globalThis, "crypto", {
      configurable: true,
      value: {
        randomUUID: () => "preferred-uuid",
        getRandomValues() { throw new Error("Should prefer randomUUID"); },
      },
    });
    assert.equal(newId(), "preferred-uuid");
    Object.defineProperty(globalThis, "crypto", {
      configurable: true,
      value: {
        getRandomValues(bytes) {
          assert.equal(bytes.length, 16);
          bytes.fill(0xab);
          return bytes;
        },
      },
    });
    assert.equal(newId(), "ab".repeat(16));
    const entries = addDraft([], item(), "1");
    assert.equal(entries[0].id, "ab".repeat(16));
    Object.defineProperty(globalThis, "crypto", { configurable: true, value: {} });
    assert.throws(() => newId(), /random/i);
  } finally {
    if (original) Object.defineProperty(globalThis, "crypto", original);
    else delete globalThis.crypto;
  }
});

test("accepted submissions remove only the acknowledged entry", () => {
  const entries = markSubmitting(addDraft(draft(), item("2"), "4"), undefined, "batch-1");
  const next = completeSubmission(entries, entries[0].id, { order: { id: "order-1" } });
  assert.equal(next.length, 1);
  assert.equal(next[0].id, entries[1].id);
  assert.equal(entries.length, 2);
});

test("explicit rejection becomes editable but timeouts and server errors remain locked", () => {
  const entries = pending();
  const rejected = completeSubmission(entries, entries[0].id, {
    error: { status: 409, message: "CPU busy" },
  });
  assert.equal(rejected[0].status, "draft");
  assert.equal(rejected[0].request_key, "");
  assert.equal(rejected[0].batch_id, "");
  for (const status of [0, 408, 499, 500, 503]) {
    const unknown = completeSubmission(entries, entries[0].id, {
      error: { status, message: "No confirmed result" },
    });
    assert.equal(unknown[0].status, "uncertain");
    assert.equal(unknown[0].request_key, entries[0].request_key);
    assert.equal(unknown[0].batch_id, "batch-1");
  }
});

test("reload recovers an in-flight request as uncertain without changing its identity", () => {
  const storage = memoryStorage();
  const entries = pending();
  saveDraft(storage, "key", { entries, receipts: {} });
  const loaded = loadDraft(storage, "key");
  assert.equal(loaded.entries[0].status, "uncertain");
  assert.equal(loaded.entries[0].request_key, entries[0].request_key);
  assert.equal(loaded.entries[0].batch_id, "batch-1");
});

test("draft snapshots roundtrip and ignore unrelated sensitive properties", () => {
  const storage = memoryStorage();
  const entries = addDraft([], { ...item(), nbt: "private", token: "secret" }, "12");
  saveDraft(storage, "key", {
    entries, receipts: { "order-1": { batch_id: "batch-1", item: item() } },
    token: "secret",
  });
  assert.ok(!storage.getItem("key").includes("secret"));
  assert.ok(!storage.getItem("key").includes("private"));
  const loaded = loadDraft(storage, "key");
  assert.equal(loaded.version, 1);
  assert.equal(loaded.entries[0].item.name, item().name);
  assert.equal(loaded.receipts["order-1"].batch_id, "batch-1");
});

test("corrupt, oversized, or unknown draft states cannot become submit-ready entries", () => {
  const storage = memoryStorage();
  for (const text of [
    "{",
    JSON.stringify({ version: 99, entries: [], receipts: {} }),
    JSON.stringify({ version: 1, entries: [{ ...draft()[0], status: "mystery" }], receipts: {} }),
    JSON.stringify({ version: 1, entries: [{ ...draft()[0], quantity: "-1" }], receipts: {} }),
    JSON.stringify({ version: 1, entries: Array.from({ length: 501 }, () => draft()[0]), receipts: {} }),
  ]) {
    storage.setItem("key", text);
    const loaded = loadDraft(storage, "key");
    assert.deepEqual(loaded.entries, []);
    assert.ok(loaded.warning);
  }
});

test("a full browser storage fails loudly instead of pretending a request was persisted", () => {
  const storage = { setItem() { throw new Error("Quota exceeded"); } };
  assert.throws(() => saveDraft(storage, "key", { entries: pending(), receipts: {} }), /Quota exceeded/);
});
