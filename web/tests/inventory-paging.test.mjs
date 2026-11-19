import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import vm from "node:vm";
import { createInventoryWheel, inventoryKeyboardDirection } from "../src/inventory-paging.mjs";

test("each wheel detent changes a page without any cooldown", () => {
  const wheel = createInventoryWheel();
  for (let n=0;n<20;n++) assert.equal(wheel.consume(120,n),1);
  assert.equal(wheel.consume(2400,21),1);
  assert.equal(wheel.consume(-120,22),-1);
});

test("fine trackpad movement accumulates within the gesture and reverses immediately", () => {
  const wheel = createInventoryWheel();
  assert.equal(wheel.consume(15,0),0);
  assert.equal(wheel.consume(15,10),0);
  assert.equal(wheel.consume(15,20),1);
  assert.equal(wheel.consume(-40,21),-1);
  assert.equal(wheel.consume(20,400),0);
  assert.equal(wheel.consume(20,600),0);
});

test("page keys do not interfere with typing, inspector controls, modifiers or held keys", () => {
  assert.equal(inventoryKeyboardDirection({ key: "PageDown" }), 1);
  assert.equal(inventoryKeyboardDirection({ key: "PageUp" }), -1);
  assert.equal(inventoryKeyboardDirection({ key: "Home" }), 0);
  for (const extra of [{ repeat: true }, { ctrlKey: true }, { shiftKey: true }, { isComposing: true }, { target: { closest: () => ({}) } }]) {
    assert.equal(inventoryKeyboardDirection({ key: "PageDown", ...extra }), null);
  }
  assert.equal(inventoryKeyboardDirection({ key: "ArrowRight" }), null);
});

const source = await readFile(new URL("../src/app.mjs", import.meta.url), "utf8");
function fixture(request) {
  let now = 1000, capacity = 2, frameId = 0;
  const frames = new Map();
  const state = { network: { id: "n" }, page: 0, cursor: "", anchorCursor: "a1", nextCursor: "b2", cursors: [{ cursor: "", anchor: "" }],
    items: [{ id: "1", quantity: "1" }, { id: "2", quantity: "2" }], inventoryPageCount: 10, inventoryDesiredPage: null, inventoryLimit: 2, inventoryLoading: false, inventoryTarget: null,
    generation: 0, inventoryScope: "nesql_plain", stockView: "stock", search: "", sort: "name", filter: "", selected: { id: "1", quantity: "1" }, inspector: "detail", itemCache: new Map(), view: "inventory" };
  const elements = new Map();
  const calls = { renders: 0, prefetch: 0, requests: [], errors: [], scrolls: 0 };
  const $ = key => {
    if (!elements.has(key)) elements.set(key, { style: { setProperty() {} }, setAttribute(name, value) { this[name] = value; }, contains: () => false, scrollTop: 120, focus() {}, scrollIntoView() { calls.scrolls++; } });
    return elements.get(key);
  };
  const context = vm.createContext({ state, $, AbortController, URLSearchParams, Map, Set,
    Date: { now: () => now }, window: { scrollX: 0, scrollY: 0, innerHeight: 800, scrollTo() {} }, document: { activeElement: {} }, CSS: { escape: x => x },
    matchMedia: () => ({ matches: false }), inventoryPageLimit: () => capacity,
    inventoryCacheEpoch: 0, inventoryLiveRequest: null, inventoryPrefetchControllers: new Set(), inventoryPageCache: new Map(), inventoryPageRequests: new Map(), queryController: null,
    inventoryPrefetchJob: null, inventoryPrefetchTarget: null, inventoryNavigationFrame: 0, inventoryVisibleTimer: 0, inventoryImageTimer: 0, inventoryDirection: 1,
    INVENTORY_PAGE_CACHE_LIMIT: 48, INVENTORY_PAGE_CACHE_TTL: 15000, INVENTORY_PAGE_FRESH_TTL: 1000, INVENTORY_PAGE_CACHE_ITEMS: 6000, INVENTORY_PREFETCH_DEPTH: 0,
    requestAnimationFrame: fn => { frames.set(++frameId,fn); return frameId; }, cancelAnimationFrame: id => frames.delete(id),
    setTimeout: () => 1, clearTimeout() {}, scheduleInventoryImageWarmup() {}, inventoryWheel: createInventoryWheel(),
    networkPath: x => x, api: { request(path) { calls.requests.push(path); return request(path); } },
    renderInventory() { calls.renders++; context.renderInventoryPager(); }, hideInventoryHover() {}, patchVisibleInventory() {}, patchSelectedDetail() {}, patchDraftAppearance() {}, refreshHeader() {},
    message: (key, text) => { if (text) calls.errors.push(text); }, errorText: e => e.message,
  });
  vm.runInContext(source.slice(source.indexOf("function renderInventoryPager()"), source.indexOf("function patchSelectedDetail(")), context);
  vm.runInContext(source.slice(source.indexOf("function refreshInventoryCapacity()"), source.indexOf("async function patchVisibleInventory()")), context);
  context.prefetchInventoryPages = () => { calls.prefetch++; };
  context.scheduleInventoryImageWarmup = () => {};
  const frame = () => { const callbacks = [...frames.values()]; frames.clear(); callbacks.forEach(fn => fn()); };
  const settle = async () => { frame(); for (let n = 0; n < 12; n++) await Promise.resolve(); };
  return { state, calls, elements, context, settle, frame, time: value => { now = value; }, capacity: value => { capacity = value; } };
}
const page = (id = "3") => ({ items: [{ id, quantity: "7" }], anchor_cursor: `a${id}`, next_cursor: `b${id}`, scope: "nesql_plain" });

test("navigation keeps the old page and rows until the new response succeeds", async () => {
  let resolve;
  const f = fixture(() => new Promise(r => { resolve = r; }));
  assert.equal(f.context.changeInventoryPage(1), true);
  f.frame();
  assert.equal(f.state.page, 0);
  assert.equal(f.state.items[0].id, "1");
  assert.equal(f.calls.renders, 0);
  assert.equal(f.elements.get("#page-label").textContent, "1 / 10 页 · 正在读取第 2 页…");
  assert.equal(f.state.inventoryTarget.page, 1);
  resolve(page()); await f.settle();
  assert.equal(f.state.page, 1);
  assert.equal(f.state.items[0].id, "3");
  assert.equal(f.state.inventoryLoading, false);
  assert.equal(f.calls.renders, 1);
});

test("a failed next page preserves cursor, items and history, allowing a retry", async () => {
  const f = fixture(async () => { throw new Error("timeout"); });
  f.context.changeInventoryPage(1); await f.settle();
  assert.equal(f.state.page, 0); assert.equal(f.state.cursor, "");
  assert.equal(f.state.items[0].id, "1"); assert.equal(f.state.nextCursor, "b2");
  assert.equal(f.state.cursors.length, 1); assert.equal(f.state.inventoryLoading, false);
  assert.deepEqual(f.calls.errors, ["timeout"]);
  assert.equal(f.context.changeInventoryPage(1), true); await f.settle();
});

test("a late response cannot replace a newer search", async () => {
  const pending = [];
  const f = fixture(() => new Promise(r => pending.push(r)));
  f.context.changeInventoryPage(1); f.frame();
  f.state.search = "new";
  const reset = f.context.loadInventory(true);
  pending[1](page("99")); await reset;
  pending[0](page("3")); await f.settle();
  assert.equal(f.state.page, 0); assert.equal(f.state.items[0].id, "99");
  assert.equal(f.state.inventoryLoading, false);
});

test("cached pages load immediately and previous page jumps directly", async () => {
  const f = fixture(async () => page("5"));
  const spec = f.context.inventoryQuerySpec(2);
  f.context.cacheInventoryPage("", { ...spec, page: 2 }, { items: [{ id: "3", quantity: "3" }], anchorCursor: "a3", nextCursor: "b4", scope: "nesql_plain" });
  assert.equal(f.context.changeInventoryPage(1), true);
  assert.equal(f.state.page, 1); assert.equal(f.calls.requests.length, 0);
  f.context.changeInventoryPage(1); await f.settle();
  assert.equal(f.state.page, 2);
  f.time(17000);
  f.context.changeInventoryPage(-1); await f.settle();
  const query = new URL(f.calls.requests.at(-1), "http://fixture").searchParams;
  assert.equal(query.get("page"), "2"); assert.equal(query.has("cursor"), false);
  assert.equal(f.state.page, 1);
});

test("capacity changes keep the current page and first item as anchor", async () => {
  const f = fixture(async () => page("7"));
  f.state.page = 4; f.state.anchorCursor = "a7";
  f.capacity(3);
  assert.equal(f.context.refreshInventoryCapacity(), true); await f.settle();
  const query = new URL(f.calls.requests[0], "http://fixture").searchParams;
  assert.equal(query.get("anchor"), "a7"); assert.equal(query.get("limit"), "3");
  assert.equal(f.state.page, 4); assert.equal(f.state.inventoryLimit, 3);
});

test("cache is bounded, expires and a fresh read bypasses it", async () => {
  const f = fixture(async () => page("9"));
  const spec = f.context.inventoryQuerySpec(2);
  for (let i = 0; i < 60; i++) f.context.cacheInventoryPage(String(i), spec, { items: [{ id: String(i) }] });
  assert.equal(f.context.inventoryPageCache.size, 48);
  assert.equal(f.context.cachedInventoryPage("0", spec), null);
  assert.equal(f.context.cachedInventoryPage("59", spec).items[0].id, "59");
  const result = await f.context.requestInventoryPage("59", 2, undefined, spec, true);
  assert.equal(result.items[0].id, "9");
  f.time(17000);
  assert.equal(f.context.cachedInventoryPage("59", spec), null);
});

test("a failed new search disables old navigation until the search is retried", async () => {
  let fail = true;
  const f = fixture(async () => { if (fail) throw new Error("timeout"); return page("8"); });
  f.state.search = "new";
  await f.context.loadInventory(true);
  assert.equal(f.state.items[0].id, "1");
  assert.equal(f.state.inventoryQueryPending, true);
  assert.equal(f.context.changeInventoryPage(1), false);
  assert.equal(f.elements.get("#refresh-inventory").disabled, false);
  fail = false;
  await f.context.loadInventory(true);
  assert.equal(f.state.inventoryQueryPending, false);
  assert.equal(f.state.items[0].id, "8");
});

test("a cancelled request cannot be reused by another request for the same boundary", async () => {
  const pending = [];
  const f = fixture(() => new Promise(r => pending.push(r)));
  const controller = new AbortController(), spec = f.context.inventoryQuerySpec(2);
  const old = f.context.requestInventoryPage("b2", 2, controller.signal, spec);
  controller.abort();
  const current = f.context.requestInventoryPage("b2", 2, undefined, spec);
  assert.equal(f.calls.requests.length, 2);
  pending[0](page("3")); await old;
  assert.equal(f.context.inventoryPageRequests.size, 1);
  pending[1](page("4")); await current;
  assert.equal(f.context.cachedInventoryPage("b2", spec).items[0].id, "4");
});


test("first and last pages loop in both directions", async () => {
  const f=fixture(async()=>page());
  f.context.changeInventoryPage(-1); await f.settle();
  assert.equal(f.state.page,9);
  assert.equal(new URL(f.calls.requests[0],"http://fixture").searchParams.get("page"),"10");
  f.context.changeInventoryPage(1); await f.settle();
  assert.equal(f.state.page,0);
});

test("continuous input during a slow request jumps to the final page without replaying intermediate pages", async () => {
  const pending=[];
  const f=fixture(()=>new Promise(r=>pending.push(r)));
  f.context.changeInventoryPage(1); f.frame();
  f.context.changeInventoryPage(1);
  f.context.changeInventoryPage(1); f.frame();
  assert.equal(f.state.inventoryDesiredPage,3);
  assert.equal(f.calls.requests.length,2);
  pending[0](page("3")); await f.settle();
  assert.equal(f.calls.renders,0);
  assert.equal(f.calls.requests.length,2);
  assert.equal(new URL(f.calls.requests[1],"http://fixture").searchParams.get("page"),"4");
  pending[1](page("7")); await f.settle();
  assert.equal(f.state.page,3);assert.equal(f.calls.renders,1);
});

test("reverse while loading returns to a cached current page immediately", async () => {
  let resolve;
  const f=fixture(()=>new Promise(r=>resolve=r));
  f.context.cacheInventoryPage("",{...f.context.inventoryQuerySpec(2),page:1},{items:f.state.items,anchorCursor:"a1",nextCursor:"b2",scope:"nesql_plain"});
  f.context.changeInventoryPage(1); f.frame();
  f.context.changeInventoryPage(-1);
  assert.equal(f.state.inventoryLoading,false);assert.equal(f.state.page,0);
  resolve(page("3"));await f.settle();
  assert.equal(f.state.page,0);
});

test("a burst of cold page input starts only the final request in that frame", async () => {
  const f = fixture(async () => page("7"));
  for (let n = 0; n < 5; n++) f.context.changeInventoryPage(1);
  assert.equal(f.calls.requests.length, 0);
  f.frame(); await f.settle();
  assert.equal(f.calls.requests.length, 1);
  assert.equal(new URL(f.calls.requests[0], "http://fixture").searchParams.get("page"), "6");
  assert.equal(f.state.page, 5);
});

test("cold navigation leaves the visible grid interactive at full brightness", async () => {
  let resolve;
  const f = fixture(() => new Promise(r => resolve = r));
  f.context.changeInventoryPage(1); f.frame();
  assert.equal(f.elements.get("#inventory-rows")["aria-busy"], "false");
  assert.equal(f.elements.get("#first-page").disabled, true);
  resolve(page()); await f.settle();
});

test("warm but outdated pages can be shown immediately and marked for background revalidation", async () => {
  const f = fixture(async () => page());
  const spec = { ...f.context.inventoryQuerySpec(2), page: 2 };
  f.context.cacheInventoryPage("", spec, { items: [{ id: "3" }] });
  f.time(2500);
  assert.equal(f.context.cachedInventoryPage("", spec, true), null);
  assert.equal(f.context.cachedInventoryPage("", spec).items[0].id, "3");
});

test("a contiguous server block is split into pages with independent anchors", async () => {
  const f = fixture(async () => ({ items: [1,2,3,4,5].map(id => ({ id: String(id) })), page_number: 3, page_count: 5,
    page_anchors: ["a1","a3","a5"], total: "9", scope: "nesql_plain", next_cursor: "" }));
  const spec = f.context.inventoryQuerySpec(2), controller = new AbortController();
  await f.context.requestInventoryBlock(2, 3, spec, controller.signal, 0);
  const query = new URL(f.calls.requests[0], "http://fixture").searchParams;
  assert.equal(query.get("page_size"), "2"); assert.equal(query.get("limit"), "6");
  assert.equal(f.context.cachedInventoryPage("", { ...spec, page: 3 }).anchorCursor, "a1");
  assert.equal(f.context.cachedInventoryPage("", { ...spec, page: 4 }).anchorCursor, "a3");
  assert.equal(f.context.cachedInventoryPage("", { ...spec, page: 5 }).items.length, 1);
});

test("a cancelled or replaced block cannot warm another query's cache", async () => {
  let resolve;
  const f = fixture(() => new Promise(r => resolve = r));
  const controller = new AbortController(), spec = f.context.inventoryQuerySpec(2);
  const pending = f.context.requestInventoryBlock(0, 2, spec, controller.signal, 0);
  f.context.clearInventoryPages();
  resolve({ items: [{id:"1"}], page_number: 1, page_count: 1 }); await pending;
  assert.equal(f.context.inventoryPageCache.size, 0);
});

test("stock membership changes retain warm pages but invalidate their position freshness", () => {
  const f = fixture(async () => page()), spec = { ...f.context.inventoryQuerySpec(2), page: 2 };
  f.context.cacheInventoryPage("", spec, { items: [{id:"3"}], total: "9", pageCount: 5 });
  f.context.invalidateInventoryPageNumbers("10", 5);
  assert.equal(f.context.cachedInventoryPage("", spec, true), null);
  assert.equal(f.context.cachedInventoryPage("", spec).total, "10");
});
