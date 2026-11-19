import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import vm from "node:vm";

const source = await readFile(new URL("../src/app.mjs", import.meta.url), "utf8");
function fixture(request) {
  const old = { id: "42", quantity: "5" };
  const state = { network: { id: "n" }, inventoryLoading: false, context: 1, generation: 3, page: 4,
    inventoryPageCount: 10, cursors: [], anchorCursor: "anchor-42", cursor: "boundary", inventoryLimit: 2, selected: old, items: [old],
    inspector: "detail", itemCache: new Map() };
  const elements = new Map([[".table-wrap", { scrollTop: 120 }], ["#page-label", {}], ["#next-page", {}]]);
  const focused = { dataset: { item: "42" } };
  const calls = { renders: 0, focused: 0, patches: 0, requests: [] };
  const window = { scrollX: 0, scrollY: 480, scrollTo(x,y) { this.scrollX=x;this.scrollY=y; } };
  const context = vm.createContext({ state, AbortController, inventoryCacheEpoch: 0, inventoryLiveRequest: null,
    inventoryPrefetchControllers: new Set(), inventoryPageCache: new Map(), inventoryPageRequests: new Map(),
    inventoryQuerySpec: limit => ({ search: "", sort: "name", limit }),
    requestInventoryPage: (cursor, limit, signal, spec) => { calls.requests.push({cursor,limit,spec}); return request(); },
    lookupItems: async ids => { state.itemCache.set(ids[0], {id:ids[0],quantity:"0"}); },
    document: { activeElement: focused }, CSS: { escape: v => v }, window,
    $: selector => selector.startsWith("[data-item=") ? { focus() { calls.focused++; } } : elements.get(selector),
    patchInventoryRows: () => { calls.patches++; return false; }, renderInventory: () => { calls.renders++; window.scrollY=0; },
    detailNeedsRender: () => false, patchSelectedDetail: item => { calls.detail=item; }, renderDetail() {},
    message() {}, errorText: String, cacheInventoryPage() {}, clearInventoryPages() {}, prefetchInventoryPages() {}, invalidateInventoryPageNumbers() {},
    renderInventoryPager() { elements.get("#page-label").textContent = `第 ${state.page + 1} 页 · ${state.items.length} 项`; elements.get("#next-page").disabled = !state.nextCursor; },
  });
  const start = source.indexOf("async function refreshInventoryWindow(");
  const end = source.indexOf("function refreshInventoryObservation()",start);
  const anchorStart = source.indexOf("function inventoryWindowAnchor(");
  const anchorEnd = source.indexOf("function inventoryQuerySpec(", anchorStart);
  vm.runInContext(source.slice(anchorStart, anchorEnd),context);
  vm.runInContext(source.slice(start,end),context);
  return { state, context, calls, elements, window, refresh: () => context.refreshInventoryWindow() };
}

test("live window keeps page, selected item, focus and mobile scroll while updating membership",async()=>{
  const f=fixture(async()=>({items:[{id:"42",quantity:"15"},{id:"43",quantity:"3"}],anchorCursor:"new-42",nextCursor:"after-43"}));
  await f.refresh();
  assert.equal(f.state.page,4);
  assert.equal(f.state.selected.quantity,"15");
  assert.equal(f.state.anchorCursor,"new-42");
  assert.equal(f.calls.requests[0].cursor,"");
  assert.equal(f.calls.requests[0].spec.anchor,"anchor-42");
  assert.equal(f.calls.focused,1);
  assert.equal(f.window.scrollY,480);
  assert.equal(f.elements.get(".table-wrap").scrollTop,120);
  assert.equal(f.elements.get("#page-label").textContent,"第 5 页 · 2 项");
});

test("selected item leaving the stock view stays selected with confirmed zero detail",async()=>{
  const f=fixture(async()=>({items:[{id:"43",quantity:"3"}],anchorCursor:"a-43",nextCursor:""}));
  await f.refresh();
  assert.equal(f.state.selected.id,"42");
  assert.equal(f.state.selected.quantity,"0");
  assert.equal(f.calls.detail.quantity,"0");
  assert.equal(f.elements.get("#next-page").disabled,true);
});

test("an old refresh cannot overwrite navigation or a changed query",async()=>{
  let resolve;
  const f=fixture(()=>new Promise(r=>resolve=r));
  const pending=f.refresh();
  f.state.generation++;
  f.state.items=[{id:"99",quantity:"1"}];
  resolve({items:[{id:"42",quantity:"15"}],anchorCursor:"a",nextCursor:"b"});
  await pending;
  assert.equal(f.state.items[0].id,"99");
  assert.equal(f.calls.renders,0);
  assert.equal(f.context.inventoryLiveRequest,null);
});

test("first-page refresh admits new entries using the initial boundary",async()=>{
  const f=fixture(async()=>({items:[{id:"1",quantity:"8"}],anchorCursor:"a-1",nextCursor:"b-1"}));
  f.state.page=0;f.state.cursor="";
  await f.refresh();
  assert.equal(f.calls.requests[0].spec.anchor,"");
  assert.equal(f.state.page,0);
  assert.equal(f.state.items[0].id,"1");
});

test("a resized window keeps its item anchor when its new page number becomes one",async()=>{
  const f=fixture(async()=>({items:[{id:"42",quantity:"9"}],anchorCursor:"anchor-42",nextCursor:"b-42",pageCount:5}));
  f.state.page=0;
  f.state.inventoryWindowAnchored=true;
  await f.refresh();
  assert.equal(f.calls.requests[0].spec.anchor,"anchor-42");
  assert.equal(f.state.items[0].id,"42");
  assert.equal(f.state.cursors[0].anchor,"anchor-42");
});
