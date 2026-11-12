import { normalizeQuantity, storageKey, saveDraft } from "./model.mjs";

const STATES = ["pending", "rejected", "queued", "assigned", "computing", "running", "needs_review", "done", "failed", "canceled", "expired"];
const KEY = /^[A-Za-z0-9_.:-]{8,128}$/;
const ID = /^[A-Za-z0-9_.:-]{1,128}$/;
const MAX_INT = 9223372036854775807n;
const CLAIM = "job:";
function requireValue(ok, message = "Invalid production job data.") { if (!ok) throw new Error(message); }
function id(value) { requireValue(typeof value === "string" && ID.test(value)); return value; }
function key(value) { requireValue(typeof value === "string" && KEY.test(value)); return value; }
function decimal(value) {
  requireValue(typeof value === "string" && /^(0|[1-9][0-9]{0,18})$/.test(value) && BigInt(value) <= MAX_INT);
  return value;
}
function clone(value) { return JSON.parse(JSON.stringify(value)); }
function freeze(value) {
  if (value && typeof value === "object") { Object.values(value).forEach(freeze); Object.freeze(value); }
  return value;
}
function sameScope(a, b) { return a.memberId === b.memberId && a.networkId === b.networkId; }

export function isJobClaim(entry) { return typeof entry?.batch_id === "string" && entry.batch_id.startsWith(CLAIM); }

export function createJobIntent({ memberId, networkId, requestKey, entries, name = "", ttlSeconds = 3600 }) {
  id(memberId); id(networkId); key(requestKey);
  requireValue(requestKey.length <= 124, "Local job request keys must fit the v1 claim (8-124 characters).");
  requireValue(typeof name === "string" && !/[\u0000-\u001f\u007f]/.test(name) && new TextEncoder().encode(name).length <= 128);
  requireValue(Number.isInteger(ttlSeconds) && ttlSeconds >= 0 && ttlSeconds <= 86400);
  requireValue(Array.isArray(entries) && entries.length > 0 && entries.length <= 500);
  const seen = new Set();
  const manifestEntries = entries.map(row => {
    requireValue(row.status === "draft" && row.cpu_name === "" && row.wait_for_cpu === false, "Job rows require editable default CPU policy.");
    requireValue(row.item?.exact === true && row.item.craftable === true);
    id(row.id);
    const entryKey = key(`draft-${row.id}`);
    requireValue(!seen.has(entryKey)); seen.add(entryKey);
    return { entry_key: entryKey, item_id: normalizeQuantity(row.item.id), quantity: normalizeQuantity(row.quantity) };
  });
  const manifest = { request_key: requestKey, name, ttl_seconds: ttlSeconds || 3600, entries: manifestEntries };
  requireValue(new TextEncoder().encode(JSON.stringify(manifest)).length <= 262144);
  return freeze({ memberId, networkId, requestKey, manifest, draftIds: entries.map(row => row.id), state: "prepared" });
}

function validateIntent(value) {
  requireValue(value && ["prepared", "uncertain", "accepted", "rejected"].includes(value.state), "Invalid persisted intent state.");
  if (value.state === "rejected") requireValue(isDefiniteRejection(value.rejection), "Invalid rejection evidence.");
  requireValue(Array.isArray(value.draftIds) && value.manifest && Array.isArray(value.manifest.entries) &&
    value.draftIds.length === value.manifest.entries.length, "Missing persisted manifest associations.");
  const canonical = createJobIntent({ memberId: value.memberId, networkId: value.networkId,
    requestKey: value.requestKey, name: value.manifest.name, ttlSeconds: value.manifest.ttl_seconds,
    entries: value.draftIds.map((rowId, i) => ({ id: rowId, status: "draft", cpu_name: "", wait_for_cpu: false,
      item: { id: value.manifest.entries[i]?.item_id, exact: true, craftable: true }, quantity: value.manifest.entries[i]?.quantity })) });
  requireValue(JSON.stringify(canonical.manifest) === JSON.stringify(value.manifest), "Invalid persisted immutable manifest.");
  if (value.state === "accepted" || value.jobId !== undefined || value.digest !== undefined || value.archived !== undefined) {
    id(value.jobId);
    requireValue(typeof value.digest === "string" && /^[a-f0-9]{64}$/.test(value.digest) && typeof value.archived === "boolean",
      "Missing persisted acceptance identity.");
  }
  if (value.conflictJobIds !== undefined) {
    requireValue(Array.isArray(value.conflictJobIds) && value.conflictJobIds.length <= 500);
    value.conflictJobIds.forEach(id);
  }
  return value;
}

function validateControl(operation) {
  requireValue(operation && typeof operation === "object", "Invalid saved control.");
  id(operation.jobId); key(operation.operationKey); decimal(operation.expectedControlVersion);
  requireValue(BigInt(operation.expectedControlVersion) > 0n && ["pause", "resume", "stop"].includes(operation.action),
    "Invalid saved control action or version.");
  requireValue(Object.keys(operation).length === 4, "Unexpected control parameters.");
  return operation;
}

function isDefiniteRejection(error) {
  return error?.uncertain === false && (
    error.status === 429 && error.code === "capacity_exceeded" ||
    error.status === 403 && ["forbidden", "capability_unavailable"].includes(error.code) ||
    error.status === 400 && ["invalid_request", "invalid_reference", "capability_unavailable"].includes(error.code));
}

function readDraft(storage, draftKey) {
  const raw = storage.getItem(draftKey);
  requireValue(typeof raw === "string", "Draft storage is missing.");
  const draft = JSON.parse(raw);
  requireValue(draft.version === 1 && Array.isArray(draft.entries) && !draft.warning, "Invalid draft storage.");
  return draft;
}

export function createJobJournal(storage, scope) {
  id(scope.memberId); id(scope.networkId);
  const prefix = `ae-orbit:jobs:v1:${encodeURIComponent(scope.memberId)}:${encodeURIComponent(scope.networkId)}:`;
  const draftKey = storageKey(scope.memberId, scope.networkId);
  const intentKey = request => `${prefix}intent:${key(request)}`;
  const controlKey = (job, operation) => `${prefix}control:${id(job)}:${key(operation)}`;
  function persist(k, value) {
    const raw = JSON.stringify(value);
    storage.setItem(k, raw);
    requireValue(storage.getItem(k) === raw, "Job journal persistence verification failed.");
  }
  function read(request) {
    const value = JSON.parse(storage.getItem(intentKey(request)));
    requireValue(value && sameScope(value, scope) && value.requestKey === request, "Missing scoped intent.");
    return validateIntent(value);
  }
  function listControls(jobId) {
    if (jobId !== undefined) id(jobId);
    const results = [];
    for (let i = 0; i < storage.length; i++) {
      const k = storage.key(i);
      if (!k?.startsWith(`${prefix}control:`)) continue;
      const saved = JSON.parse(storage.getItem(k));
      requireValue(saved && sameScope(saved, scope), "Missing scoped control.");
      const operation = validateControl(saved.operation);
      requireValue(k === controlKey(operation.jobId, operation.operationKey), "Invalid persisted control identity.");
      if (jobId === undefined || operation.jobId === jobId) results.push(freeze(clone(operation)));
    }
    return results.sort((a, b) => a.jobId.localeCompare(b.jobId) || a.operationKey.localeCompare(b.operationKey));
  }
  return {
    list() {
      const results = [];
      for (let i = 0; i < storage.length; i++) {
        const k = storage.key(i);
        if (!k?.startsWith(`${prefix}intent:`)) continue;
        const value = read(k.slice(`${prefix}intent:`.length));
        results.push({ ...value, state: ["accepted", "rejected"].includes(value.state) ? value.state : "uncertain" });
      }
      return results.sort((a, b) => a.requestKey.localeCompare(b.requestKey));
    },
    prepare(intent) {
      requireValue(sameScope(intent, scope), "Wrong job scope.");
      validateIntent(intent);
      requireValue(!storage.getItem(intentKey(intent.requestKey)), "Intent already locked.");
      const draft = readDraft(storage, draftKey);
      for (let i = 0; i < intent.draftIds.length; i++) {
        const row = draft.entries.find(row => row.id === intent.draftIds[i]);
        const expected = intent.manifest.entries[i];
        requireValue(row?.status === "draft" && row.cpu_name === "" && row.wait_for_cpu === false &&
          row.item.id === expected.item_id && row.quantity === expected.quantity, "Draft claim is locked or changed.");
      }
      // Per-request entries survive independent writers; claims retain the exact old order key.
      const savedIntent = { ...clone(intent), state: "uncertain" };
      persist(intentKey(intent.requestKey), savedIntent);
      for (const row of draft.entries) {
        if (!intent.draftIds.includes(row.id)) continue;
        Object.assign(row, { status: "submitting", request_key: `draft-${row.id}`, batch_id: `${CLAIM}${intent.requestKey}` });
      }
      saveDraft(storage, draftKey, draft);
      const reread = readDraft(storage, draftKey);
      for (const rowId of intent.draftIds) {
        const claimed = reread.entries.find(row => row.id === rowId);
        requireValue(claimed?.status === "submitting" && claimed.batch_id === `${CLAIM}${intent.requestKey}` &&
          claimed.request_key === `draft-${rowId}`, "Draft claim persistence verification failed.");
      }
      requireValue(JSON.stringify(read(intent.requestKey)) === JSON.stringify(savedIntent), "Job journal persistence changed.");
      return { intent: freeze(clone(intent)), transportReady: true };
    },
    markUncertain(request, error = {}) {
      const value = read(request);
      requireValue(value.state !== "accepted");
      persist(intentKey(request), { ...value, state: "uncertain", conflictJobIds: Array.isArray(error.jobIds) ? error.jobIds.filter(v => typeof v === "string" && ID.test(v)).slice(0, 500) : [] });
    },
    markDefiniteRejected(request, error) {
      const value = read(request);
      requireValue(value.state === "uncertain" && isDefiniteRejection(error), "No definite rejection.");
      persist(intentKey(request), { ...value, state: "rejected",
        rejection: { status: error.status, code: error.code, uncertain: false } });
    },
    retryRejected(request) {
      const value = read(request);
      requireValue(value.state === "rejected" && isDefiniteRejection(value.rejection), "Only definite rejection can be explicitly retried.");
      const next = { ...value, state: "uncertain" };
      delete next.rejection;
      persist(intentKey(request), next);
      return freeze(clone(next));
    },
    accept(request, response, context) {
      const value = read(request);
      const receipt = verifyJobAcceptance(value, response, context);
      persist(intentKey(request), { ...value, ...receipt, state: "accepted" });
      const draft = readDraft(storage, draftKey);
      draft.entries = draft.entries.filter(row => !(value.draftIds.includes(row.id) && row.batch_id === `${CLAIM}${request}` &&
        row.request_key === `draft-${row.id}`));
      saveDraft(storage, draftKey, draft);
      return receipt;
    },
    listControls,
    prepareControl(operation) {
      validateControl(operation);
      requireValue(listControls(operation.jobId).length === 0, "A control is already uncertain.");
      // Contending tabs can both pass the check; separate keys retain both exact operations.
      persist(controlKey(operation.jobId, operation.operationKey), { ...scope, operation: clone(operation) });
      return freeze(clone(operation));
    },
    replayControl(jobId, { explicit = false, operationKey } = {}) {
      requireValue(explicit, "Exact replay requires explicit confirmation.");
      const operations = listControls(id(jobId));
      requireValue(operationKey !== undefined || operations.length === 1, "Select an exact operation key when multiple controls exist.");
      const operation = operations.find(value => operationKey === undefined || value.operationKey === operationKey);
      requireValue(operation, "Missing scoped control operation.");
      return operation;
    },
    completeControl(jobId, operationKey) {
      key(operationKey);
      const operation = listControls(id(jobId)).find(value => value.operationKey === operationKey);
      requireValue(operation, "Missing scoped control operation.");
      const k = controlKey(jobId, operationKey);
      storage.removeItem(k);
      requireValue(storage.getItem(k) === null, "Control journal removal failed.");
    },
  };
}

export function verifyJobAcceptance(intent, response, context) {
  requireValue(context?.originalKey === true && sameScope(intent, context) && context.requestKey === intent.requestKey,
    "Receipt requires scoped original-key transport.");
  const receipt = response?.receipt;
  requireValue(receipt && typeof receipt.archived === "boolean" && /^[a-f0-9]{64}$/.test(receipt.manifest_digest));
  id(receipt.job_id);
  requireValue(!intent.digest || intent.digest === receipt.manifest_digest);
  requireValue(!intent.jobId || intent.jobId === receipt.job_id);
  if (receipt.archived) {
    requireValue(response.job === undefined && (intent.digest || context.method === "GET"), "Archived identity requires original-key GET.");
  } else {
    const detail = response.job;
    const s = validateJobSummary(detail?.summary);
    requireValue(s.id === receipt.job_id && s.manifest_digest === receipt.manifest_digest &&
      s.owner_id === intent.memberId && s.network_id === intent.networkId && s.name === intent.manifest.name &&
      s.total === String(intent.manifest.entries.length));
    requireValue(Array.isArray(detail.entries) && detail.entries.length === intent.manifest.entries.length);
    const ids = new Set();
    detail.entries.forEach((entry, ordinal) => {
      const expected = intent.manifest.entries[ordinal];
      id(entry.id); requireValue(!ids.has(entry.id)); ids.add(entry.id);
      normalizeQuantity(entry.item_id);
      requireValue(entry.ordinal === ordinal && entry.entry_key === expected.entry_key &&
        entry.requested_item_id === expected.item_id && entry.quantity === expected.quantity && STATES.includes(entry.state));
    });
  }
  return { jobId: receipt.job_id, digest: receipt.manifest_digest, archived: receipt.archived };
}

export function validateJobSummary(value) {
  requireValue(value && typeof value === "object");
  id(value.id); id(value.network_id); id(value.owner_id);
  decimal(value.version); decimal(value.control_version); decimal(value.total);
  requireValue(BigInt(value.version) > 0n && BigInt(value.control_version) > 0n && BigInt(value.total) > 0n);
  requireValue(typeof value.name === "string" && /^[a-f0-9]{64}$/.test(value.manifest_digest) &&
    ["active", "paused", "stopped"].includes(value.control));
  requireValue(Number.isFinite(Date.parse(value.created_at)) && Number.isFinite(Date.parse(value.updated_at)));
  let total = 0n;
  for (const state of STATES) total += BigInt(decimal(value.counts?.[state]));
  requireValue(total === BigInt(value.total));
  return clone(value);
}

export function applyJobSnapshot(previous, snapshot, scope) {
  decimal(snapshot?.watermark);
  requireValue(Array.isArray(snapshot.jobs));
  const scopedPrevious = previous && sameScope(previous, scope) ? previous : undefined;
  if (scopedPrevious?.cursor !== undefined && !scopedPrevious.needsSnapshot) {
    requireValue(BigInt(snapshot.watermark) >= BigInt(decimal(scopedPrevious.cursor)), "Stale job snapshot.");
  }
  const jobs = new Map(), versions = new Map(scopedPrevious?.versions ?? []);
  for (const raw of snapshot.jobs) {
    const summary = validateJobSummary(raw);
    requireValue(summary.network_id === scope.networkId && !jobs.has(summary.id));
    const prior = versions.get(summary.id);
    if (!prior || BigInt(summary.version) > BigInt(prior) ||
      (summary.version === prior && scopedPrevious?.jobs.has(summary.id))) {
      jobs.set(summary.id, summary); versions.set(summary.id, summary.version);
    } else if (scopedPrevious?.jobs.has(summary.id)) {
      jobs.set(summary.id, clone(scopedPrevious.jobs.get(summary.id)));
    }
  }
  return { ...scope, jobs, versions, cursor: snapshot.watermark, history: [], needsSnapshot: false };
}

export function applyJobChanges(state, page) {
  requireValue(!state.needsSnapshot, "A reset requires a fresh snapshot.");
  decimal(page?.next_after); decimal(page.watermark);
  requireValue(Array.isArray(page.changes) && page.changes.length <= 100 && typeof page.has_more === "boolean" && typeof page.reset === "boolean");
  if (page.reset) {
    requireValue(page.changes.length === 0);
    return { ...state, history: [], needsSnapshot: true };
  }
  let revision = BigInt(decimal(state.cursor));
  for (const change of page.changes) {
    decimal(change.revision); decimal(change.version); id(change.job_id);
    requireValue(BigInt(change.revision) > revision && BigInt(change.revision) <= BigInt(page.watermark) &&
      BigInt(change.version) > 0n && typeof change.removed === "boolean");
    revision = BigInt(change.revision);
    if (change.removed) requireValue(change.summary === undefined);
    else {
      const summary = validateJobSummary(change.summary);
      requireValue(summary.id === change.job_id && summary.version === change.version && summary.network_id === state.networkId);
    }
  }
  requireValue(page.next_after === revision.toString() && BigInt(page.next_after) <= BigInt(page.watermark));
  requireValue(!page.has_more || page.changes.length > 0);
  const jobs = new Map(state.jobs), versions = new Map(state.versions);
  for (const change of page.changes) {
    const prior = versions.get(change.job_id);
    if (prior && BigInt(prior) >= BigInt(change.version)) continue;
    versions.set(change.job_id, change.version);
    if (change.removed) jobs.delete(change.job_id);
    else jobs.set(change.job_id, clone(change.summary));
  }
  return { ...state, jobs, versions, cursor: page.next_after };
}
