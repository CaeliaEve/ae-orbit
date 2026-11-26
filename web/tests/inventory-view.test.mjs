import test from "node:test";
import assert from "node:assert/strict";
import { createInventoryView, inventoryPageLimit } from "../src/inventory-view.mjs";

const item = (id, quantity = "7") => ({ id: String(id), quantity, quantity_known: true });
const response = (number = 1, items = [item(number)], count = 10) => ({
  items, page_number: number, page_count: count, anchor_cursor: `a${items[0]?.id || number}`,
  scope: "nesql_plain", total: String(count * 2),
});

function fixture() {
  let now = 1000, capacity = 2, sequence = 0;
  const frames = new Map(), timers = new Map(), elements = new Map(), requests = [], lookups = [];
  const calls = { renders: 0, focus: 0, scrolls: 0, errors: [], detail: null };
  const state = { network: { id: "n" }, view: "inventory", items: [], itemCache: new Map(), selected: null,
    search: "", sort: "name", filter: "", inventoryScope: "nesql_plain", stockView: "stock", resourceKind: "item" };
  function element() {
    const listeners = new Map();
    return {
      addEventListener(type, listener) { listeners.set(type, listener); },
      emit(type, fields = {}) {
        let prevented = false;
        listeners.get(type)?.({ target: this, preventDefault() { prevented = true; }, ...fields });
        return prevented;
      },
      contains: child => Boolean(child && child === browser.document.activeElement),
      closest: () => null,
      setAttribute(name, value) { this[name] = value; },
      scrollIntoView() { calls.scrolls++; },
      focus() { calls.focus++; },
      scrollTop: 0,
    };
  }
  const $ = selector => {
    if (selector === "dialog[open]") return null;
    if (!elements.has(selector)) elements.set(selector, element());
    return elements.get(selector);
  };
  const browser = {
    document: Object.assign(element(), { visibilityState: "visible", activeElement: null }),
    CSS: { escape: value => value }, performance: { now: () => now }, innerHeight: 800,
    matchMedia: () => ({ matches: false }), scrollX: 0, scrollY: 0,
    scrollTo(x, y) { this.scrollX = x; this.scrollY = y; },
    setTimeout(callback, delay) { timers.set(++sequence, { callback, delay }); return sequence; },
    clearTimeout(id) { timers.delete(id); },
    requestAnimationFrame(callback) { frames.set(++sequence, callback); return sequence; },
    cancelAnimationFrame(id) { frames.delete(id); },
  };
  const view = createInventoryView({
    state, $, browser, now: () => now, measure: () => capacity,
    request: (path, { signal }) => new Promise((resolve, reject) => requests.push({
      network: state.network.id, query: new URL(path, "http://fixture").searchParams, signal, resolve, reject,
    })),
    lookupItems: (ids, signal) => new Promise(resolve => lookups.push({ ids, signal, resolve(items) {
      if (!signal.aborted) for (const item of items) state.itemCache.set(item.id, item);
      resolve({ items });
    } })),
    render() { calls.renders++; browser.scrollY = 0; $("#view-inventory .table-wrap").scrollTop = 0; view.renderPager(); },
    patchRows: () => false,
    updateSelection() { calls.detail = state.selected; }, updateDraft() {}, hideHover() {}, textureAssetURL() {},
    message(text) { if (text) calls.errors.push(text); }, errorText: error => error.message,
  });
  view.bind();
  const settle = async () => { for (let turn = 0; turn < 12; turn++) await Promise.resolve(); };
  return {
    view, state, $, browser, calls, requests, lookups, settle,
    pages: () => requests.filter(({ query }) => !query.has("page_size")),
    prefetches: () => requests.filter(({ query }) => query.has("page_size")),
    frame() { const callbacks = [...frames.values()]; frames.clear(); callbacks.forEach(run => run()); },
    runTimers(delay) {
      for (const [id, timer] of [...timers]) if (delay === timer.delay) { timers.delete(id); timer.callback(); }
    },
    scheduled: () => frames.size + timers.size,
    time: value => { now = value; }, capacity: value => { capacity = value; },
    async start(result = response()) {
      const pending = view.reload();
      requests.at(-1).resolve(result);
      await pending;
    },
  };
}

test("desktop page size uses the available card grid and mobile uses the fixed limit", () => {
  const props = { "--inventory-card-min": "76", "--inventory-card-height": "84" };
  const rows = [];
  const wrap = { clientWidth: 340, clientHeight: 300, style: { setProperty: (key, value) => rows.push([key, value]) } };
  const elements = { "#view-inventory .table-wrap": wrap, "#inventory-empty": { hidden: false },
    "#inventory-rows": { gap: "8", getPropertyValue: name => props[name] },
    "#view-inventory .inventory-table": { paddingLeft: "10", paddingRight: "10", paddingTop: "10", paddingBottom: "10" } };
  const $ = selector => elements[selector];
  const browser = { matchMedia: () => ({ matches: false }), getComputedStyle: element => element };
  assert.equal(inventoryPageLimit($, browser, 50), 9);
  assert.deepEqual(rows, [["--inventory-rows", "3"]]);
  assert.equal(elements["#inventory-empty"].hidden, false);
  browser.matchMedia = () => ({ matches: true });
  assert.equal(inventoryPageLimit($, browser, 9), 50);
});

test("a burst of cold navigation keeps the existing grid interactive and requests only the last page", async () => {
  const f = fixture();
  await f.start();
  const catalog = f.$("#view-inventory .catalog");
  for (let n = 0; n < 5; n++) assert.equal(catalog.emit("wheel", { deltaX: 0, deltaY: 120 }), true);
  assert.equal(f.pages().length, 1);
  f.frame();
  const pending = f.pages().at(-1);
  assert.equal(pending.query.get("page"), "6");
  assert.equal(f.state.items[0].id, "1");
  assert.equal(f.$("#inventory-rows")["aria-busy"], "false");
  assert.match(f.$("#page-label").textContent, /^1 \/ 10.*第 6 页/);
  pending.resolve(response(6)); await f.settle();
  assert.equal(f.state.items[0].id, "6");
  assert.match(f.$("#page-label").textContent, /^6 \/ 10/);
});

test("a slow intermediate page cannot render once a newer wheel target is queued", async () => {
  const f = fixture();
  await f.start();
  const catalog = f.$("#view-inventory .catalog");
  const wheel = () => catalog.emit("wheel", { deltaX: 0, deltaY: 120 });
  wheel(); f.frame();
  const intermediate = f.pages().at(-1);
  wheel(); wheel();
  intermediate.resolve(response(2)); await f.settle();
  assert.equal(f.state.items[0].id, "1");
  f.frame();
  const last = f.pages().at(-1);
  assert.equal(last.query.get("page"), "4");
  assert.equal(f.pages().length, 3);
  last.resolve(response(4)); await f.settle();
  assert.equal(f.state.items[0].id, "4");
});

test("navigation loops, and reversing to a cached page aborts a slow request", async () => {
  const f = fixture();
  await f.start();
  f.$("#prev-page").emit("click"); f.frame();
  const last = f.pages().at(-1);
  assert.equal(last.query.get("page"), "10");
  f.$("#next-page").emit("click");
  assert.equal(last.signal.aborted, true);
  assert.equal(f.view.loading, false);
  last.resolve(response(10)); await f.settle();
  assert.equal(f.state.items[0].id, "1");
  f.$("#prev-page").emit("click"); f.frame();
  f.pages().at(-1).resolve(response(10)); await f.settle();
  assert.equal(f.state.items[0].id, "10");
  f.$("#next-page").emit("click");
  assert.equal(f.state.items[0].id, "1");
});

test("a failed next page preserves the current window and can be retried", async () => {
  const f = fixture();
  await f.start();
  f.$("#next-page").emit("click"); f.frame();
  f.pages().at(-1).reject(Error("timeout")); await f.settle();
  assert.deepEqual(f.calls.errors, ["timeout"]);
  assert.equal(f.state.items[0].id, "1");
  assert.equal(f.view.loading, false);
  f.$("#next-page").emit("click"); f.frame();
  assert.equal(f.pages().at(-1).query.get("page"), "2");
  f.pages().at(-1).resolve(response(2)); await f.settle();
  assert.equal(f.state.items[0].id, "2");
});

test("committed search supersedes navigation and a failed query can be retried", async () => {
  const f = fixture();
  await f.start();
  f.$("#next-page").emit("click"); f.frame();
  const old = f.pages().at(-1);
  f.$("#search").value = "new";
  f.$("#search").emit("input", { isComposing: true });
  f.runTimers(220);
  assert.equal(f.pages().at(-1), old);
  f.$("#search").emit("compositionend"); f.runTimers(220);
  const search = f.pages().at(-1);
  assert.equal(old.signal.aborted, true);
  assert.equal(search.query.get("q"), "new");
  assert.equal(search.query.has("page"), false);
  search.reject(Error("timeout")); old.resolve(response(2)); await f.settle();
  assert.equal(f.state.items[0].id, "1");
  assert.equal(f.$("#next-page").disabled, true);
  assert.equal(f.$("#refresh-inventory").disabled, false);
  const retry = f.view.refresh();
  f.pages().at(-1).resolve(response(1, [item(99)])); await retry;
  assert.equal(f.state.items[0].id, "99");
});

test("live refresh keeps a later page, restores scroll and focus, and updates selection", async () => {
  const f = fixture();
  await f.start(response(5, [item(42, "5")]));
  f.state.selected = f.state.items[0];
  f.browser.document.activeElement = { dataset: { item: "42" } };
  f.browser.scrollY = 480;
  f.$("#view-inventory .table-wrap").scrollTop = 120;
  const refresh = f.view.refresh();
  const live = f.pages().at(-1);
  assert.equal(live.query.get("anchor"), "a42");
  live.resolve(response(4, [item(42, "15"), item(43)])); await refresh;
  assert.equal(f.state.selected.quantity, "15");
  assert.equal(f.calls.focus, 1);
  assert.equal(f.browser.scrollY, 480);
  assert.equal(f.$("#view-inventory .table-wrap").scrollTop, 120);
  assert.equal(f.$("#page-label").textContent, "5 / 10 页 · 2 项");
});

test("first-page refresh admits new entries and keeps a selected item that falls to zero", async () => {
  const f = fixture();
  await f.start(response(1, [item(42, "5")]));
  f.state.selected = f.state.items[0];
  const refresh = f.view.refresh();
  const live = f.pages().at(-1);
  assert.equal(live.query.has("anchor"), false);
  live.resolve(response(1, [item(43)], 1)); await f.settle();
  assert.deepEqual(f.lookups[0].ids, ["42"]);
  f.lookups[0].resolve([item(42, "0")]); await refresh;
  assert.deepEqual(f.state.items, [item(43)]);
  assert.equal(f.state.selected.id, "42");
  assert.equal(f.calls.detail.quantity, "0");
  assert.equal(f.$("#next-page").disabled, true);
});

test("capacity changes use the current anchor even if the resized window becomes page one", async () => {
  const f = fixture();
  await f.start(response(5, [item(42)]));
  f.capacity(3); f.view.resize(); f.runTimers(120);
  const resized = f.pages().at(-1);
  assert.equal(resized.query.get("limit"), "3");
  assert.equal(resized.query.get("anchor"), "a42");
  resized.resolve(response(1, [item(42)])); await f.settle();
  const refresh = f.view.refresh();
  assert.equal(f.pages().at(-1).query.get("anchor"), "a42");
  f.pages().at(-1).resolve(response(1, [item(42, "9")])); await refresh;
  assert.equal(f.state.items[0].quantity, "9");
});

test("leaving the page cancels prefetch and timers, and reentering refreshes the same window", async () => {
  const f = fixture();
  await f.start(response(4, [item(42)]));
  const prefetched = f.prefetches()[0];
  f.view.observe();
  const live = f.pages().at(-1);
  f.view.hide(); f.state.view = "dashboard";
  assert.equal(prefetched.signal.aborted, true);
  assert.equal(live.signal.aborted, true);
  assert.equal(f.scheduled(), 0);
  prefetched.resolve(response(5)); live.resolve(response(4, [item(93)])); await f.settle();
  f.view.observe();
  assert.equal(f.pages().at(-1), live);
  assert.equal(f.state.items[0].id, "42");
  f.time(7000);
  f.state.view = "inventory"; f.view.show();
  const reopened = f.pages().at(-1);
  assert.notEqual(reopened, live);
  assert.equal(reopened.query.get("anchor"), "a42");
  reopened.resolve(response(4, [item(42, "9")])); await f.settle();
  assert.match(f.$("#page-label").textContent, /^4 \/ 10/);
  assert.equal(f.state.items[0].quantity, "9");
});

test("switching networks aborts old lookups, discards delayed input, and loads a fresh first page", async () => {
  const f = fixture();
  await f.start(response(4, [item(42)]));
  const refresh = f.view.refresh();
  f.pages().at(-1).resolve(response(4, [item(42)])); await refresh;
  f.view.observe();
  const old = f.lookups[0];
  f.$("#search").value = "not committed"; f.$("#search").emit("input");
  f.state.network = { id: "other" }; f.state.items = []; f.state.itemCache.clear();
  f.view.reset(); f.view.show();
  const first = f.pages().at(-1);
  assert.equal(old.signal.aborted, true);
  assert.equal(first.network, "other");
  assert.equal(first.query.get("q"), "");
  assert.equal(first.query.has("anchor"), false);
  first.resolve(response(1, [item(99)])); await f.settle();
  old.resolve([item(42, "100")]); f.runTimers(220); await f.settle();
  assert.equal(f.pages().at(-1), first);
  assert.equal(f.state.items[0].id, "99");
  assert.equal(f.state.itemCache.has("42"), false);
});

test("hiding the browser tab preserves a committed search and suspends all inventory requests", async () => {
  const f = fixture();
  await f.start();
  f.$("#search").value = "新搜索";
  f.$("#search").emit("input");
  f.browser.document.visibilityState = "hidden";
  f.view.hide();
  f.view.observe();
  f.runTimers(220);
  assert.equal(f.pages().length, 1);
  assert.equal(f.scheduled(), 0);
  f.browser.document.visibilityState = "visible";
  f.view.show();
  const pending = f.pages().at(-1);
  assert.equal(pending.query.get("q"), "新搜索");
  pending.resolve(response(1, [item(8)])); await f.settle();
  assert.equal(f.state.items[0].id, "8");
});
