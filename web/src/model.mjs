const MAX_QUANTITY = "9223372036854775807";
const MAX_ENTRIES = 500;
const MAX_RECEIPTS = 2000;
const MAX_STORED_LENGTH = 2 * 1024 * 1024;
const STATUSES = new Set(["draft", "submitting", "uncertain"]);

export function networkQualityLabel(value) {
  if (value === "unverified") return "逐项观测";
  if (value === "degraded") return "存在同步告警";
  return "数据状态未知";
}

export function newId() {
  const crypto = globalThis.crypto;
  if (typeof crypto?.randomUUID === "function") return crypto.randomUUID();
  if (typeof crypto?.getRandomValues !== "function") {
    throw new Error("A cryptographic random source is required.");
  }
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes, byte => byte.toString(16).padStart(2, "0")).join("");
}

function decimal(value) {
  if (typeof value === "number" && !Number.isSafeInteger(value)) {
    throw new Error("Quantity must be an exact integer.");
  }
  if (!["string", "number", "bigint"].includes(typeof value)) {
    throw new Error("Quantity must contain decimal digits.");
  }
  const valueText = String(value).trim();
  if (!/^[0-9]{1,64}$/.test(valueText)) throw new Error("Quantity must contain decimal digits.");
  return BigInt(valueText);
}

export function normalizeQuantity(value, maximum = MAX_QUANTITY) {
  const quantity = decimal(value);
  const limit = decimal(maximum);
  if (quantity < 1n || quantity > limit || limit < 1n || limit > BigInt(MAX_QUANTITY)) {
    throw new Error("Quantity is outside the allowed range.");
  }
  return quantity.toString();
}

export function formatQuantity(value) {
  try {
    return decimal(value).toLocaleString("zh-CN");
  } catch {
    return "-";
  }
}

function text(value, maximum, allowEmpty = false) {
  if (typeof value !== "string" || value.length > maximum ||
      (!allowEmpty && !value.trim()) || /[\u0000-\u001f\u007f]/.test(value)) {
    throw new Error("Invalid draft text.");
  }
  return value;
}

function identifier(value, allowEmpty = false) {
  const result = text(value, 128, allowEmpty);
  if (result && !/^[A-Za-z0-9_.:-]+$/.test(result)) throw new Error("Invalid draft identifier.");
  return result;
}

function snapshot(item) {
  if (!item || typeof item !== "object" || Array.isArray(item)) throw new Error("Invalid draft item.");
  const name = text(item.name, 256);
  if (!Number.isInteger(item.damage) || item.damage < 0 || item.damage > 2147483647) {
    throw new Error("Invalid item metadata.");
  }
  if (typeof item.exact !== "boolean" || typeof item.craftable !== "boolean") {
    throw new Error("Invalid item capability.");
  }
  const kind = item.kind ?? "item";
  if (kind !== "item" && kind !== "fluid") throw new Error("Invalid item kind.");
  return {
    id: normalizeQuantity(item.id),
    kind,
    name,
    damage: item.damage,
    label: text(item.label, 512),
    exact: item.exact,
    craftable: item.craftable,
  };
}

function cpuPolicy(cpuName, waitForCPU) {
  const name = text(cpuName, 256, true);
  if (typeof waitForCPU !== "boolean" || (waitForCPU && !name.trim())) {
    throw new Error("Waiting requires a named CPU.");
  }
  if (name && !name.trim()) throw new Error("Invalid CPU name.");
  return { cpu_name: name, wait_for_cpu: waitForCPU };
}

function entrySnapshot(entry, restoring = false) {
  if (!entry || typeof entry !== "object" || Array.isArray(entry) || !STATUSES.has(entry.status)) {
    throw new Error("Invalid draft state.");
  }
  const result = {
    id: identifier(entry.id),
    item: snapshot(entry.item),
    quantity: normalizeQuantity(entry.quantity),
    ...cpuPolicy(entry.cpu_name, entry.wait_for_cpu),
    status: entry.status,
    request_key: identifier(entry.request_key, true),
    batch_id: identifier(entry.batch_id, true),
  };
  if (result.status === "draft") {
    if (result.request_key || result.batch_id) throw new Error("Editable drafts cannot retain a submitted request.");
  } else if (!result.request_key || !result.batch_id) {
    throw new Error("Pending requests require their original identity.");
  }
  if (entry.error) result.error = text(entry.error, 1024);
  // A page reload cannot prove whether a previous POST reached the service.
  if (restoring && result.status === "submitting") result.status = "uncertain";
  return result;
}

function entriesSnapshot(entries, restoring = false) {
  if (!Array.isArray(entries) || entries.length > MAX_ENTRIES) throw new Error("Draft limit exceeded.");
  const result = entries.map(entry => entrySnapshot(entry, restoring));
  const ids = new Set();
  const requests = new Set();
  for (const entry of result) {
    if (ids.has(entry.id) || (entry.request_key && requests.has(entry.request_key))) {
      throw new Error("Duplicate draft identity.");
    }
    ids.add(entry.id);
    if (entry.request_key) requests.add(entry.request_key);
  }
  return result;
}

function receiptsSnapshot(receipts) {
  if (!receipts || typeof receipts !== "object" || Array.isArray(receipts)) {
    throw new Error("Invalid order receipts.");
  }
  const entries = Object.entries(receipts);
  if (entries.length > MAX_RECEIPTS) throw new Error("Receipt limit exceeded.");
  const result = {};
  for (const [id, receipt] of entries) {
    identifier(id);
    if (!receipt || typeof receipt !== "object" || Array.isArray(receipt)) throw new Error("Invalid order receipt.");
    const value = { batch_id: identifier(receipt.batch_id, true) };
    if (receipt.item) value.item = snapshot(receipt.item);
    Object.defineProperty(result, id, { value, enumerable: true, configurable: true, writable: true });
  }
  return result;
}

function stateSnapshot(state, restoring = false) {
  if (!state || typeof state !== "object" || (state.version !== undefined && state.version !== 1)) {
    throw new Error("Unsupported draft version.");
  }
  return {
    version: 1,
    entries: entriesSnapshot(state.entries, restoring),
    receipts: receiptsSnapshot(state.receipts ?? {}),
  };
}

export function storageKey(memberId, networkId) {
  return `ae-orbit:draft:v1:${encodeURIComponent(identifier(memberId))}:${encodeURIComponent(identifier(networkId))}`;
}

export function loadDraft(storage, key) {
  try {
    const raw = storage.getItem(key);
    if (raw === null) return { version: 1, entries: [], receipts: {} };
    if (typeof raw !== "string" || raw.length > MAX_STORED_LENGTH) throw new Error("Invalid draft size.");
    const value = JSON.parse(raw);
    if (value?.version !== 1) throw new Error("Unsupported draft version.");
    return stateSnapshot(value, true);
  } catch {
    return {
      version: 1, entries: [], receipts: {},
      warning: "Saved drafts could not be restored. Check existing orders before submitting again.",
    };
  }
}

export function saveDraft(storage, key, state) {
  const serialized = JSON.stringify(stateSnapshot(state));
  if (serialized.length > MAX_STORED_LENGTH) throw new Error("Draft limit exceeded.");
  storage.setItem(key, serialized);
}

export function addDraft(entries, item, quantity, cpuName = "", waitForCPU = false, { merge = true } = {}) {
  const result = entriesSnapshot(entries);
  const selected = snapshot(item);
  if (!selected.exact || !selected.craftable) throw new Error("This item cannot be ordered.");
  const amount = normalizeQuantity(quantity);
  const policy = cpuPolicy(cpuName, waitForCPU);
  const existing = merge && result.find(entry => entry.status === "draft" && entry.item.id === selected.id &&
    entry.cpu_name === policy.cpu_name && entry.wait_for_cpu === policy.wait_for_cpu);
  if (existing) {
    existing.quantity = normalizeQuantity(BigInt(existing.quantity) + BigInt(amount));
    existing.item = selected;
    delete existing.error;
  } else {
    if (result.length >= MAX_ENTRIES) throw new Error("Draft limit exceeded.");
    result.push({
      id: newId(),
      item: selected,
      quantity: amount,
      ...policy,
      status: "draft",
      request_key: "",
      batch_id: "",
    });
  }
  return result;
}

function requireEntry(entries, id, status) {
  const entry = entries.find(value => value.id === id);
  if (!entry || (status && entry.status !== status)) throw new Error("This draft cannot be changed.");
  return entry;
}

export function updateDraft(entries, id, changes) {
  const result = entriesSnapshot(entries);
  const entry = requireEntry(result, id, "draft");
  if (!changes || typeof changes !== "object" ||
      Object.keys(changes).some(key => !["quantity", "cpu_name", "wait_for_cpu"].includes(key))) {
    throw new Error("Invalid draft change.");
  }
  const quantity = normalizeQuantity(changes.quantity ?? entry.quantity);
  const policy = cpuPolicy(changes.cpu_name ?? entry.cpu_name, changes.wait_for_cpu ?? entry.wait_for_cpu);
  Object.assign(entry, { quantity, ...policy });
  delete entry.error;
  return result;
}

export function removeDraft(entries, id) {
  const result = entriesSnapshot(entries);
  requireEntry(result, id, "draft");
  return result.filter(entry => entry.id !== id);
}

export function markSubmitting(entries, ids, batchId) {
  const result = entriesSnapshot(entries);
  identifier(batchId);
  const selected = ids === undefined ? result.filter(entry => entry.status === "draft").map(entry => entry.id) : ids;
  if (!Array.isArray(selected) || selected.length > MAX_ENTRIES) throw new Error("Invalid draft selection.");
  for (const id of new Set(selected)) {
    const entry = requireEntry(result, id, "draft");
    Object.assign(entry, {
      status: "submitting",
      request_key: identifier(`draft-${entry.id}`),
      batch_id: batchId,
    });
    delete entry.error;
  }
  return result;
}

export function completeSubmission(entries, id, { order, error, uncertain = false } = {}) {
  const result = entriesSnapshot(entries);
  const entry = requireEntry(result, id);
  if (entry.status === "draft") throw new Error("This draft has not been submitted.");
  if (!uncertain && order && typeof order.id === "string" && order.id) {
    identifier(order.id);
    return result.filter(value => value.id !== id);
  }
  const status = error?.status;
  const rejected = !uncertain && Number.isInteger(status) && status >= 400 && status < 500 &&
    ![408, 425, 499].includes(status);
  if (rejected) {
    Object.assign(entry, { status: "draft", request_key: "", batch_id: "" });
  } else {
    entry.status = "uncertain";
  }
  entry.error = typeof error?.message === "string" && error.message.trim()
    ? error.message.replace(/[\u0000-\u001f\u007f]/g, " ").slice(0, 1024)
    : "The submission result is not confirmed.";
  return result;
}
