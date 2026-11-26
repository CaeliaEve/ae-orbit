import test from "node:test";
import assert from "node:assert/strict";
import { createInventoryPages } from "../src/inventory-pages.mjs";
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

const spec = { search: "", sort: "name", filter: "", kind: "item", limit: 2, scope: "nesql_plain", view: "stock" };
const item = id => ({ id: String(id) });

test("page cache enforces its page and item budgets and distinguishes warm from fresh", async () => {
  let now = 1000;
  const pages = createInventoryPages({ now: () => now, request: async () => ({ items: [item(999)] }) });
  for (let page = 1; page <= 161; page++) pages.put({ ...spec, page }, { items: [item(page)] });
  assert.equal(pages.get({ ...spec, page: 1 }), null);
  assert.equal(pages.get({ ...spec, page: 2 }).items[0].id, "2");

  const broad = { ...spec, limit: 200 };
  const items = Array.from({ length: 200 }, (_, index) => item(index));
  for (let page = 1; page <= 31; page++) pages.put({ ...broad, page }, { items });
  assert.equal(pages.get({ ...broad, page: 1 }), null);
  assert.equal(pages.get({ ...broad, page: 2 }).items.length, 200);
  now += 10_001;
  assert.equal(pages.get({ ...broad, page: 31 }, true), null);
  assert.equal(pages.get({ ...broad, page: 31 }).items.length, 200);
  const fresh = await pages.read({ ...broad, page: 31 }, undefined, true);
  assert.equal(fresh.items[0].id, "999");
  now += 120_001;
  assert.equal(pages.get({ ...broad, page: 31 }), null);
});

test("in-flight requests share the same query, and an aborted result cannot replace its successor", async () => {
  const updates = [];
  const pages = createInventoryPages({ request: path => new Promise(resolve => updates.push({ path, resolve })) });
  const controller = new AbortController(), query = { ...spec, anchor: "a2" };
  const old = pages.read(query, controller.signal);
  assert.equal(pages.read(query, controller.signal), old);
  controller.abort();
  const next = pages.read(query);
  assert.equal(updates.length, 2);
  const params = new URL(updates[1].path, "http://fixture").searchParams;
  assert.equal(params.get("anchor"), "a2");
  assert.equal(params.get("grouped"), "true");
  assert.equal(params.get("view"), "stock");
  assert.equal(params.get("consistency"), "window");
  updates[0].resolve({ items: [item(3)] });
  await old;
  assert.equal(pages.read(query), next);
  updates[1].resolve({ items: [item(4)] });
  await next;
  assert.equal(pages.get(query).items[0].id, "4");
});

test("contiguous blocks retain each page's anchor and invalidate old positions on stock changes", async () => {
  const calls = [];
  const pages = createInventoryPages({ request: async path => {
    calls.push(path);
    return { items: [1, 2, 3, 4, 5].map(item), page_number: 3, page_count: 5,
      page_anchors: ["a1", "a3", "a5"], total: "9", scope: "nesql_plain" };
  } });
  pages.put({ ...spec, page: 2 }, { items: [item(8)], total: "10", count: 5 });
  await pages.readBlock(2, 3, spec, new AbortController().signal, "10");
  const query = new URL(calls[0], "http://fixture").searchParams;
  assert.equal(query.get("page"), "3");
  assert.equal(query.get("page_size"), "2");
  assert.equal(query.get("limit"), "6");
  assert.equal(pages.get({ ...spec, page: 3 }).anchor, "a1");
  assert.equal(pages.get({ ...spec, page: 4 }).anchor, "a3");
  assert.equal(pages.get({ ...spec, page: 5 }).items.length, 1);
  assert.equal(pages.get({ ...spec, page: 2 }, true), null);
  assert.equal(pages.get({ ...spec, page: 2 }).total, "9");
});

test("cancelled prefetch and retired cache epochs never warm the current query", async () => {
  const updates = [];
  const pages = createInventoryPages({ request: () => new Promise(resolve => updates.push(resolve)) });
  const controller = new AbortController();
  const aborted = pages.readBlock(0, 2, spec, controller.signal);
  controller.abort();
  const retired = pages.readBlock(0, 2, spec, new AbortController().signal);
  pages.clear();
  for (const resolve of updates) resolve({ items: [item(1)], page_number: 1, page_count: 1 });
  await Promise.all([aborted, retired]);
  assert.equal(pages.get({ ...spec, page: 1 }), null);
});
