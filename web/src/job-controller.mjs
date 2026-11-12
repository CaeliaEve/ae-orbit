import { createJobIntent, createJobJournal, applyJobSnapshot, applyJobChanges, validateJobSummary } from "./jobs.mjs";
import { loadDraft, saveDraft, storageKey, newId, normalizeQuantity } from "./model.mjs";

export function overlayJobClaims(draft, intents) {
  return { ...draft, entries: draft.entries.map(row => {
    const intent = intents.find(value => value.draftIds.includes(row.id));
    if (!intent || (row.status !== "draft" && row.batch_id !== `job:${intent.requestKey}`)) return row;
    return { ...row, status: "uncertain", request_key: `draft-${row.id}`, batch_id: `job:${intent.requestKey}` };
  }) };
}

export function validateJobDetail(value, scope, jobId) {
  const summary = validateJobSummary(value?.summary);
  if (summary.id !== jobId || summary.network_id !== scope.networkId || !Array.isArray(value.entries) ||
      value.entries.length !== Number(summary.total)) throw new Error("Invalid job detail.");
  const seen = new Set(), counts = Object.fromEntries(Object.keys(summary.counts).map(key => [key, 0]));
  value.entries.forEach((entry, ordinal) => {
    if (typeof entry.id !== "string" || !/^[A-Za-z0-9_.:-]{1,128}$/.test(entry.id) || seen.has(entry.id) ||
        entry.ordinal !== ordinal || !(entry.state in counts) || typeof entry.entry_key !== "string" ||
        (entry.order_id !== undefined && !/^[A-Za-z0-9_.:-]{1,128}$/.test(entry.order_id))) throw new Error("Invalid job entry.");
    seen.add(entry.id); counts[entry.state]++;
    normalizeQuantity(entry.item_id); normalizeQuantity(entry.requested_item_id); normalizeQuantity(entry.quantity);
  });
  if (Object.keys(counts).some(key => String(counts[key]) !== summary.counts[key])) throw new Error("Invalid entry counts.");
  return { summary, entries: structuredClone(value.entries) };
}

export function createJobController({ storage, request, makeKey = newId, lock = (_key, work) => work(), onChange = () => {} }) {
  let scope, journal, generation = 0, syncing, historySequence = 0, detailSequence = 0;
  let state = empty();
  function empty() { return { support: "unknown", loading: false, error: null, sync: null, history: [], historyCursor: "", historyMore: true, historyLoaded: false, historyLoading: false, detail: null, detailId: "", detailLoading: false, detailError: null, busy: false }; }
  const emit = () => onChange(state);
  const base = () => `/api/v1/networks/${encodeURIComponent(scope.networkId)}`;
  const current = token => token === generation;
  const draftKey = () => storageKey(scope.memberId, scope.networkId);
  function retainFailure(savedJournal, requestKey, error) {
    savedJournal.markUncertain(requestKey, error);
    if (error.uncertain === false && (
      error.status === 429 && error.code === "capacity_exceeded" ||
      error.status === 403 && ["forbidden", "capability_unavailable"].includes(error.code) ||
      error.status === 400 && ["invalid_request", "invalid_reference", "capability_unavailable"].includes(error.code)
    )) savedJournal.markDefiniteRejected(requestKey, error);
  }
  function local() {
    if (!journal) return { intents: [], controls: [], draft: { version: 1, entries: [], receipts: {} } };
    const intents = journal.list();
    return { intents, controls: journal.listControls(), draft: overlayJobClaims(loadDraft(storage, draftKey()), intents) };
  }
  async function sync() {
    if (!scope) return;
    if (syncing) return syncing;
    const token = generation, path = base();
    state.loading = true; emit();
    const work = (async () => {
      try {
        if (!state.sync || state.sync.needsSnapshot) {
          const snapshot = await request(`${path}/jobs`);
          if (!current(token)) return;
          state.sync = applyJobSnapshot(state.sync, snapshot, scope);
          state.support = "supported"; state.history = []; state.historyLoaded = false; state.historyCursor = ""; state.historyMore = true; state.historyLoading = false;
          historySequence++;
        }
        let resets = 0;
        for (let pages = 0; pages < 100; pages++) {
          const page = await request(`${path}/jobs/changes?after=${state.sync.cursor}`);
          if (!current(token)) return;
          state.sync = applyJobChanges(state.sync, page);
          state.history = state.history.filter(row => !page.changes.some(change => change.removed && change.job_id === row.id &&
            BigInt(change.version) >= BigInt(row.version)));
          if (state.sync.needsSnapshot) {
            if (++resets > 2) throw new Error("Job feed keeps resetting.");
            const snapshot = await request(`${path}/jobs`);
            if (!current(token)) return;
            state.sync = applyJobSnapshot(state.sync, snapshot, scope);
            state.history = []; state.historyLoaded = false; state.historyCursor = ""; state.historyMore = true; state.historyLoading = false; historySequence++;
            continue;
          }
          if (!page.has_more) break;
          if (pages === 99) throw new Error("Job changes are still catching up; refresh again.");
        }
        state.error = null;
      } catch (error) {
        if (!current(token)) return;
        if (!state.sync && [404, 405, 501].includes(error.status)) state.support = "unsupported";
        state.error = error;
      } finally {
        if (current(token)) { state.loading = false; syncing = null; emit(); }
      }
    })();
    syncing = work;
    return work;
  }
  async function submit(ids, { name = "", ttlSeconds = 3600 } = {}) {
    if (!scope || state.support !== "supported" || state.busy) throw new Error("Jobs unavailable.");
    const token = generation, path = base(), savedJournal = journal, key = draftKey();
    state.busy = true; emit();
    try {
      const prepared = await lock(key, () => {
        if (!current(token)) return;
        const draft = local().draft;
        if (draft.warning) throw new Error("Draft storage unavailable.");
        const rows = ids.map(id => draft.entries.find(row => row.id === id));
        const intent = createJobIntent({ ...scope, requestKey: makeKey(), entries: rows, name, ttlSeconds });
        // Materialize journal overlays before prepare so a prior interrupted claim cannot be reused.
        saveDraft(storage, key, draft);
        return savedJournal.prepare(intent);
      });
      if (!current(token) || !prepared?.transportReady) return;
      emit();
      try {
        const response = await request(`${path}/jobs`, { method: "POST", body: prepared.intent.manifest });
        if (!current(token)) return;
        await lock(key, () => { if (current(token)) savedJournal.accept(prepared.intent.requestKey, response,
          { ...scope, requestKey: prepared.intent.requestKey, originalKey: true, method: "POST" }); });
      } catch (error) {
        if (!current(token)) return;
        retainFailure(savedJournal, prepared.intent.requestKey, error);
        throw error;
      }
    } finally {
      if (current(token)) { state.busy = false; emit(); }
    }
  }
  async function reconcile(requestKey) {
    if (!scope) return;
    const token = generation, path = base(), savedJournal = journal, key = draftKey(), captured = { ...scope };
    const response = await request(`${path}/jobs/by-key/${encodeURIComponent(requestKey)}`);
    if (!current(token)) return;
    const receipt = await lock(key, () => {
      if (!current(token)) return;
      return savedJournal.accept(requestKey, response, { ...captured, requestKey, originalKey: true, method: "GET" });
    });
    if (current(token)) emit();
    return receipt;
  }
  async function history() {
    if (!scope || state.support !== "supported" || state.historyLoading || !state.historyMore) return;
    const token = generation, sequence = ++historySequence, path = base(), cursor = state.historyCursor;
    state.historyLoading = true; emit();
    try {
      const page = await request(`${path}/jobs/history?${new URLSearchParams({ limit: "100", ...(cursor ? {cursor} : {}) })}`);
      if (!current(token) || sequence !== historySequence) return;
      if (!Array.isArray(page.jobs) || page.jobs.length > 100 || typeof page.has_more !== "boolean" ||
          typeof page.next_cursor !== "string" || (page.has_more && (!page.next_cursor || page.next_cursor === cursor))) throw new Error("Invalid job history.");
      const rows = page.jobs.map(validateJobSummary);
      if (rows.some(row => row.network_id !== scope.networkId || !row.settled_at)) throw new Error("Invalid job history scope.");
      const merged = new Map(state.history.map(row => [row.id, row]));
      rows.forEach(row => {
        const fence = state.sync.versions.get(row.id), live = state.sync.jobs.get(row.id);
        if (fence && !live && BigInt(fence) >= BigInt(row.version)) return;
        const next = live && BigInt(live.version) >= BigInt(row.version) ? live : row;
        const prior = merged.get(row.id);
        if (!prior || BigInt(next.version) >= BigInt(prior.version)) merged.set(row.id, next);
      });
      state.history = [...merged.values()]; state.historyCursor = page.next_cursor; state.historyMore = page.has_more; state.historyLoaded = true; state.error = null;
    } catch (error) { if (current(token) && sequence === historySequence) state.error = error; }
    finally { if (current(token) && sequence === historySequence) { state.historyLoading = false; emit(); } }
  }
  async function detail(jobId = state.detailId) {
    if (!scope || !jobId) return;
    const token = generation, sequence = ++detailSequence, path = base();
    if (state.detailId !== jobId) state.detail = null;
    state.detailId = jobId; state.detailLoading = true; state.detailError = null; emit();
    try {
      const value = await request(`${path}/jobs/${encodeURIComponent(jobId)}`);
      if (!current(token) || sequence !== detailSequence) return;
      const next = validateJobDetail(value, scope, jobId);
      if (!state.detail || BigInt(next.summary.version) >= BigInt(state.detail.summary.version)) state.detail = next;
      return { jobId, generation: token, sequence, detail: state.detail };
    } catch (error) { if (current(token) && sequence === detailSequence) state.detailError = error; }
    finally { if (current(token) && sequence === detailSequence) { state.detailLoading = false; emit(); } }
  }
  async function sendControl(operation) {
    const token = generation, path = base(), savedJournal = journal;
    const summary = validateJobSummary(await request(`${path}/jobs/${encodeURIComponent(operation.jobId)}/${operation.action}`, {
      method: "POST", body: { operation_key: operation.operationKey, expected_control_version: operation.expectedControlVersion },
    }));
    if (!current(token)) return;
    if (summary.id !== operation.jobId || summary.network_id !== scope.networkId ||
        BigInt(summary.control_version) <= BigInt(operation.expectedControlVersion) ||
        summary.control !== ({pause:"paused",resume:"active",stop:"stopped"})[operation.action]) throw new Error("Invalid control acknowledgement.");
    savedJournal.completeControl(operation.jobId, operation.operationKey); emit();
    await sync(); await detail();
  }
  return {
    get state() { return state; }, local, sync, submit, reconcile, history, detail,
    async retryRejected(requestKey) {
      if (!scope || state.busy || state.support !== "supported") throw new Error("Jobs unavailable.");
      const token = generation, path = base(), key = draftKey(), savedJournal = journal;
      state.busy = true; emit();
      try {
        const intent = await lock(key, () => current(token) ? savedJournal.retryRejected(requestKey) : null);
        if (!intent || !current(token)) return;
        emit();
        try {
          const response = await request(`${path}/jobs`, { method: "POST", body: intent.manifest });
          if (!current(token)) return;
          await lock(key, () => { if (current(token)) savedJournal.accept(requestKey, response,
            { ...scope, requestKey, originalKey: true, method: "POST" }); });
        } catch (error) {
          if (!current(token)) return;
          retainFailure(savedJournal, requestKey, error);
          throw error;
        }
      } finally { if (current(token)) { state.busy = false; emit(); } }
    },
    reset(next) { generation++; historySequence++; detailSequence++; scope = next; syncing = null;
      journal = next ? createJobJournal(storage, next) : null; state = empty(); emit(); },
    closeDetail() { detailSequence++; state.detailId = ""; state.detail = null; state.detailError = null; state.detailLoading = false; emit(); },
    async control(summary, action) {
      if (!scope || state.busy) throw new Error("Control unavailable.");
      const token = generation, key = draftKey();
      const operation = await lock(key, () => {
        if (!current(token)) return;
        return journal.prepareControl({ jobId: summary.id, action, operationKey: makeKey(), expectedControlVersion: summary.control_version });
      });
      if (operation && current(token)) { emit(); return sendControl(operation); }
    },
    replayControl(jobId, operationKey, refreshed) {
      if (!refreshed || !current(refreshed.generation) || refreshed.sequence !== detailSequence ||
          refreshed.jobId !== jobId || state.detailId !== jobId || refreshed.detail !== state.detail ||
          state.detailLoading || state.detailError) throw new Error("Refresh this job before replaying its control.");
      return sendControl(journal.replayControl(jobId, { explicit: true, operationKey }));
    },
  };
}
