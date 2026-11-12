import test from "node:test";
import assert from "node:assert/strict";
const jobs = await import("../src/jobs.mjs").catch(error => {
  if (error.code !== "ERR_MODULE_NOT_FOUND") throw error;
  return {};
});
import { saveDraft, storageKey, loadDraft } from "../src/model.mjs";

function storage() {
  const data = new Map();
  return { get length() { return data.size; }, key: i => [...data.keys()][i],
    getItem: k => data.get(k) ?? null, setItem: (k, v) => data.set(k, v), removeItem: k => data.delete(k) };
}
const scope = { memberId: "member-1", networkId: "network-1" };
const row = (id = "row-0001") => ({ id, item: { id: "12", name: "stone", label: "Stone", damage: 0, exact: true, craftable: true },
  quantity: "64", cpu_name: "", wait_for_cpu: false, status: "draft", request_key: "", batch_id: "" });
const intent = () => jobs.createJobIntent({ ...scope, requestKey: "request-0001", entries: [row()] });
const summary = (version = "1") => ({ id: "job-1", network_id: "network-1", owner_id: "member-1", name: "",
  manifest_digest: "a".repeat(64), control: "active", version, control_version: "1", total: "1",
  counts: { pending: "1", rejected: "0", queued: "0", assigned: "0", computing: "0", running: "0", needs_review: "0", done: "0", failed: "0", canceled: "0", expired: "0" },
  created_at: "2026-11-11T00:00:00Z", updated_at: "2026-11-11T00:00:00Z" });
const response = () => ({ receipt: { job_id: "job-1", manifest_digest: "a".repeat(64), archived: false },
  job: { summary: summary(), entries: [{ id: "entry-1", entry_key: "draft-row-0001", ordinal: 0, requested_item_id: "12", item_id: "12", quantity: "64", state: "pending" }] } });

test("manifest captures immutable row order and stable legacy-derived source keys without CPU fields", () => {
  assert.equal(typeof jobs.createJobIntent, "function");
  const entries = [row(), row("row-0002")];
  const value = jobs.createJobIntent({ ...scope, requestKey: "request-0001", entries, ttlSeconds: 0 });
  entries[0].quantity = "99";
  assert.deepEqual(value.manifest, { request_key: "request-0001", name: "", ttl_seconds: 3600,
    entries: [{ entry_key: "draft-row-0001", item_id: "12", quantity: "64" }, { entry_key: "draft-row-0002", item_id: "12", quantity: "64" }] });
  assert.ok(Object.isFrozen(value.manifest.entries[0]));
  assert.throws(() => jobs.createJobIntent({ ...scope, requestKey: "request-0001", entries: [{ ...row(), cpu_name: "named" }] }));
});

test("journal and compatible claims persist and reread before POST eligibility; restored intent is uncertain", () => {
  const store = storage();
  saveDraft(store, storageKey(scope.memberId, scope.networkId), { entries: [row()], receipts: {} });
  const journal = jobs.createJobJournal(store, scope);
  const prepared = journal.prepare(intent());
  assert.equal(prepared.transportReady, true);
  const draft = loadDraft(store, storageKey(scope.memberId, scope.networkId));
  assert.equal(draft.entries[0].status, "uncertain");
  assert.equal(draft.entries[0].request_key, "draft-row-0001");
  assert.ok(jobs.isJobClaim(draft.entries[0]));
  assert.equal(journal.list()[0].state, "uncertain");
  assert.equal(jobs.createJobJournal(store, { ...scope, memberId: "member-2" }).list().length, 0);
  assert.throws(() => journal.prepare(intent()), /claim|locked|draft/i);
});

test("failed storage and failed reread never grant transport eligibility", () => {
  const store = storage();
  saveDraft(store, storageKey(scope.memberId, scope.networkId), { entries: [row()], receipts: {} });
  store.setItem = () => { throw new Error("full"); };
  assert.throws(() => jobs.createJobJournal(store, scope).prepare(intent()), /full/);
  const silent = storage();
  saveDraft(silent, storageKey(scope.memberId, scope.networkId), { entries: [row()], receipts: {} });
  silent.setItem = () => {};
  assert.throws(() => jobs.createJobJournal(silent, scope).prepare(intent()), /persist/i);
});

test("journal replacement between its write and claim reread prevents transport", () => {
  const store = storage();
  saveDraft(store, storageKey(scope.memberId, scope.networkId), { entries: [row()], receipts: {} });
  const write = store.setItem;
  let journalKey;
  store.setItem = (k, v) => {
    write(k, v);
    if (k.includes(":intent:")) journalKey = k;
    if (journalKey && k.includes(":draft:")) {
      const replaced = JSON.parse(store.getItem(journalKey));
      replaced.manifest.entries[0].quantity = "65";
      write(journalKey, JSON.stringify(replaced));
    }
  };
  assert.throws(() => jobs.createJobJournal(store, scope).prepare(intent()), /journal|persist/i);
});

test("per-intent keys preserve independent tab writes and source conflicts stay locked", () => {
  const store = storage();
  saveDraft(store, storageKey(scope.memberId, scope.networkId), { entries: [row(), row("row-0002")], receipts: {} });
  const a = jobs.createJobJournal(store, scope), b = jobs.createJobJournal(store, scope);
  a.prepare(intent());
  b.prepare(jobs.createJobIntent({ ...scope, requestKey: "request-0002", entries: [row("row-0002")] }));
  assert.equal(a.list().length, 2);
  a.markUncertain("request-0001", { code: "entry_already_accepted", jobIds: ["job-1"] });
  assert.equal(a.list()[0].state, "uncertain");
  assert.ok(jobs.isJobClaim(loadDraft(store, storageKey(scope.memberId, scope.networkId)).entries[0]));
});

test("receipt verification binds original-key scoped transport and complete detail identity", () => {
  const context = { ...scope, requestKey: "request-0001", originalKey: true };
  assert.equal(jobs.verifyJobAcceptance(intent(), response(), context).jobId, "job-1");
  for (const mutate of [
    r => { r.job.entries[0].quantity = "63"; },
    r => { r.job.entries[0].entry_key = "different"; },
    r => { r.job.entries[0].ordinal = 1; },
    r => { r.job.entries[0].requested_item_id = "13"; },
    r => { r.job.summary.manifest_digest = "b".repeat(64); },
    r => { r.job.summary.id = "other"; },
    r => { r.job.entries = []; },
  ]) {
    const bad = response(); mutate(bad);
    assert.throws(() => jobs.verifyJobAcceptance(intent(), bad, context));
  }
  assert.throws(() => jobs.verifyJobAcceptance(intent(), response(), { ...context, networkId: "other" }));
  assert.throws(() => jobs.verifyJobAcceptance(intent(), response(), { ...context, originalKey: false }));
});

test("authenticated original-key archive receipts are accepted without inventing editable detail", () => {
  const archived = { receipt: { job_id: "job-1", manifest_digest: "a".repeat(64), archived: true } };
  assert.deepEqual(jobs.verifyJobAcceptance(intent(), archived, { ...scope, requestKey: "request-0001", originalKey: true, method: "GET" }),
    { jobId: "job-1", digest: "a".repeat(64), archived: true });
  assert.throws(() => jobs.verifyJobAcceptance({ ...intent(), digest: "b".repeat(64) }, archived,
    { ...scope, requestKey: "request-0001", originalKey: true, method: "GET" }));
  assert.throws(() => jobs.verifyJobAcceptance(intent(), archived,
    { ...scope, requestKey: "request-0001", originalKey: true, method: "POST" }));
});

test("verified receipt is persisted before associated claims are removed", () => {
  const store = storage();
  saveDraft(store, storageKey(scope.memberId, scope.networkId), { entries: [row()], receipts: {} });
  const journal = jobs.createJobJournal(store, scope);
  journal.prepare(intent());
  journal.accept("request-0001", response(), { ...scope, requestKey: "request-0001", originalKey: true });
  assert.equal(loadDraft(store, storageKey(scope.memberId, scope.networkId)).entries.length, 0);
  assert.equal(journal.list()[0].state, "accepted");
  assert.equal(journal.list()[0].jobId, "job-1");
});

test("decimal versions, atomic page validation, removal fences and reset history invalidation", () => {
  let state = jobs.applyJobSnapshot(undefined, { jobs: [summary("9007199254740993")], watermark: "2" }, scope);
  state.history = [{ id: "old" }];
  const change = { revision: "3", job_id: "job-1", version: "9007199254740994", removed: true };
  assert.throws(() => jobs.applyJobChanges(state, { changes: [change, { revision: "4" }], next_after: "4", watermark: "4", has_more: false, reset: false }));
  assert.equal(state.cursor, "2");
  state = jobs.applyJobChanges(state, { changes: [change], next_after: "3", watermark: "3", has_more: false, reset: false });
  assert.equal(state.jobs.size, 0);
  state = jobs.applyJobChanges(state, { changes: [{ revision: "4", job_id: "job-1", version: "9007199254740993", removed: false, summary: summary("9007199254740993") }], next_after: "4", watermark: "4", has_more: false, reset: false });
  assert.equal(state.jobs.size, 0);
  assert.equal(state.versions.get("job-1"), "9007199254740994");
  const reset = jobs.applyJobChanges(state, { changes: [], next_after: "4", watermark: "5", has_more: false, reset: true });
  assert.equal(reset.needsSnapshot, true);
  assert.deepEqual(reset.history, []);
  assert.throws(() => jobs.applyJobSnapshot(undefined, { jobs: [summary(2)], watermark: "2" }, scope));
});

test("control journal requires explicit replay of exactly the saved action/key/CAS", () => {
  const journal = jobs.createJobJournal(storage(), scope);
  const operation = { jobId: "job-1", action: "pause", operationKey: "operation-0001", expectedControlVersion: "9007199254740993" };
  journal.prepareControl(operation);
  assert.throws(() => journal.prepareControl({ ...operation, action: "stop" }));
  assert.throws(() => journal.replayControl("job-1"));
  assert.deepEqual(journal.replayControl("job-1", { explicit: true }), operation);
});

test("summary accepts backend active admission state, not invented running control", () => {
  assert.equal(jobs.validateJobSummary(summary()).control, "active");
  assert.throws(() => jobs.validateJobSummary({ ...summary(), control: "running" }));
});

test("snapshot never revives an equal-version tombstone or carries another network fences", () => {
  const previous = { ...scope, jobs: new Map(), versions: new Map([["job-1", "3"]]) };
  assert.equal(jobs.applyJobSnapshot(previous, { jobs: [summary("3")], watermark: "5" }, scope).jobs.size, 0);
  const foreign = { ...previous, networkId: "elsewhere" };
  assert.equal(jobs.applyJobSnapshot(foreign, { jobs: [summary("2")], watermark: "5" }, scope).jobs.size, 1);
});

test("interleaved tabs retain both unresolved control operations and require an exact replay selection", () => {
  const store = storage(), a = jobs.createJobJournal(store, scope), b = jobs.createJobJournal(store, scope);
  const pause = { jobId: "job-1", action: "pause", operationKey: "operation-0001", expectedControlVersion: "1" };
  const stop = { ...pause, action: "stop", operationKey: "operation-0002" };
  const write = store.setItem;
  let competing = false;
  store.setItem = (k, v) => {
    if (!competing && k.includes(":control:")) {
      competing = true;
      assert.deepEqual(b.prepareControl(stop), stop);
    }
    write(k, v);
  };
  assert.deepEqual(a.prepareControl(pause), pause);
  assert.deepEqual(a.listControls("job-1"), [pause, stop]);
  assert.throws(() => a.replayControl("job-1", { explicit: true }), /operation|multiple|select/i);
  assert.deepEqual(a.replayControl("job-1", { explicit: true, operationKey: "operation-0002" }), stop);
  assert.throws(() => a.completeControl("job-1"));
  a.completeControl("job-1", "operation-0002");
  assert.deepEqual(a.listControls(), [pause]);
});

test("stale snapshot cannot erase a newer live summary followed by equal-version catchup", () => {
  const previous = jobs.applyJobSnapshot(undefined, { jobs: [summary("3")], watermark: "3" }, scope);
  assert.throws(() => jobs.applyJobSnapshot(previous, { jobs: [summary("2")], watermark: "2" }, scope), /stale/i);
  let state = jobs.applyJobSnapshot(previous, { jobs: [summary("2")], watermark: "3" }, scope);
  state = jobs.applyJobChanges(state, { changes: [{ revision: "4", job_id: "job-1", version: "3", removed: false,
    summary: summary("3") }], next_after: "4", watermark: "4", has_more: false, reset: false });
  assert.equal(state.jobs.get("job-1").version, "3");
});

test("restored intent and control corruption fails closed instead of granting accepted or replay state", () => {
  const store = storage();
  const prefix = "ae-orbit:jobs:v1:member-1:network-1:";
  const journal = jobs.createJobJournal(store, scope);
  for (const corrupted of [
    { ...scope, requestKey: "request-0001", state: "accepted" },
    { ...intent(), state: "accepted" },
    { ...intent(), state: "uncertain", draftIds: ["different"] },
    { ...intent(), state: "prepared", manifest: { ...intent().manifest, entries: [] } },
  ]) {
    store.setItem(`${prefix}intent:request-0001`, JSON.stringify(corrupted));
    assert.throws(() => journal.list());
  }
  store.removeItem(`${prefix}intent:request-0001`);
  const operation = { jobId: "job-1", action: "pause", operationKey: "operation-0001", expectedControlVersion: "1" };
  for (const corrupted of [{ ...operation, action: "retry" }, { ...operation, expectedControlVersion: 1 },
    { ...operation, jobId: "other" }, { ...operation, operationKey: "different-0001" }, {}]) {
    store.setItem(`${prefix}control:job-1:operation-0001`, JSON.stringify({ ...scope, operation: corrupted }));
    assert.throws(() => journal.replayControl("job-1", { explicit: true, operationKey: "operation-0001" }));
    assert.throws(() => journal.listControls());
  }
});

test("v1 compatible request-key bound is checked before any journal or claim write", () => {
  for (const length of [125, 128]) {
    assert.throws(() => jobs.createJobIntent({ ...scope, requestKey: "r".repeat(length), entries: [row()] }));
    const store = storage();
    saveDraft(store, storageKey(scope.memberId, scope.networkId), { entries: [row()], receipts: {} });
    const oversized = { ...intent(), requestKey: "r".repeat(length),
      manifest: { ...intent().manifest, request_key: "r".repeat(length) } };
    assert.throws(() => jobs.createJobJournal(store, scope).prepare(oversized));
    assert.equal(store.length, 1);
    assert.equal(loadDraft(store, storageKey(scope.memberId, scope.networkId)).entries[0].status, "draft");
  }
  const store = storage();
  saveDraft(store, storageKey(scope.memberId, scope.networkId), { entries: [row()], receipts: {} });
  assert.equal(jobs.createJobJournal(store, scope).prepare(jobs.createJobIntent({
    ...scope, requestKey: "r".repeat(124), entries: [row()],
  })).transportReady, true);
});
