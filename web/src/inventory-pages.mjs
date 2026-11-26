const CACHE_PAGES = 160;
export const INVENTORY_CACHE_ITEMS = 6000;
const CACHE_TTL = 120_000;
const FRESH_TTL = 10_000;
export const INVENTORY_BLOCK_ITEMS = 200;

function pageKey(spec) {
  const { search, sort, filter, limit, view, scope, kind, page = 0, anchor = "" } = spec;
  return JSON.stringify([search, sort, filter, limit, view, scope, kind, page, anchor]);
}

function pageQuery(spec) {
  const query = new URLSearchParams({
    q: spec.search, sort: spec.sort, limit: String(spec.limit), consistency: "window", pagination: "pages",
    kind: spec.kind || "item",
  });
  if (spec.filter) query.set("craftable", spec.filter);
  if (spec.kind === "item") query.set("grouped", "true");
  if (spec.scope === "nesql_plain") query.set("view", spec.view);
  if (spec.anchor) query.set("anchor", spec.anchor);
  if (spec.page) query.set("page", String(spec.page));
  return query;
}

function pageFromResponse(value) {
  return {
    items: value.items || [], anchor: value.anchor_cursor || "", scope: value.scope || "",
    number: value.page_number, count: value.page_count, total: value.total,
  };
}

export function createInventoryPages({ request, now = Date.now }) {
  const cache = new Map();
  const pending = new Map();
  let epoch = 0, itemCount = 0;

  function remove(key) {
    itemCount -= cache.get(key)?.page.items.length || 0;
    cache.delete(key);
  }

  function get(spec, fresh = false) {
    const key = pageKey(spec);
    const entry = cache.get(key);
    if (!entry) return null;
    const age = now() - entry.cachedAt;
    if (age > CACHE_TTL) { remove(key); return null; }
    if (fresh && age > FRESH_TTL) return null;
    cache.delete(key);
    cache.set(key, entry);
    return entry.page;
  }

  function put(spec, page) {
    const key = pageKey(spec);
    remove(key);
    cache.set(key, { page, number: spec.page, cachedAt: now() });
    itemCount += page.items.length;
    while (cache.size > CACHE_PAGES || (itemCount > INVENTORY_CACHE_ITEMS && cache.size > 1)) remove(cache.keys().next().value);
  }

  function invalidatePositions(total, count) {
    for (const entry of cache.values()) {
      if (!entry.number) continue;
      entry.cachedAt = Math.min(entry.cachedAt, now() - FRESH_TTL - 1);
      entry.page = { ...entry.page, total, count };
    }
  }

  function clear() {
    epoch++;
    cache.clear();
    pending.clear();
    itemCount = 0;
  }

  function read(spec, signal, fresh = false) {
    const key = pageKey(spec);
    const cached = !fresh && get(spec);
    if (cached) return Promise.resolve(cached);
    const current = pending.get(key);
    if (!fresh && current && !current.signal?.aborted) return current.promise;
    const started = epoch;
    const promise = request(`/items?${pageQuery(spec)}`, { signal }).then(value => {
      const page = pageFromResponse(value);
      if (started === epoch && !signal?.aborted && pending.get(key)?.promise === promise) put(spec, page);
      return page;
    }).finally(() => {
      if (pending.get(key)?.promise === promise) pending.delete(key);
    });
    pending.set(key, { promise, signal });
    return promise;
  }

  async function readBlock(start, count, spec, signal, knownTotal) {
    const started = epoch;
    const query = pageQuery({ ...spec, page: start + 1 });
    query.set("limit", String(Math.min(INVENTORY_BLOCK_ITEMS, count * spec.limit)));
    query.set("page_size", String(spec.limit));
    const value = await request(`/items?${query}`, { signal });
    if (started !== epoch || signal.aborted) return value.total;
    if (value.total != null && knownTotal != null && value.total !== knownTotal) invalidatePositions(value.total, value.page_count);
    for (let offset = 0; offset < value.items.length; offset += spec.limit) {
      const index = offset / spec.limit;
      const number = value.page_number + index;
      put({ ...spec, page: number }, {
        ...pageFromResponse(value), items: value.items.slice(offset, offset + spec.limit),
        anchor: value.page_anchors?.[index] || "", number,
      });
    }
    return value.total;
  }

  return { get, put, read, readBlock, invalidatePositions, clear };
}
