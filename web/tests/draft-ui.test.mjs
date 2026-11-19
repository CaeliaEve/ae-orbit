import test from "node:test";
import assert from "node:assert/strict";
import { draftDisplayItem, draftSelectionSummary } from "../src/draft-ui.mjs";
import { addDraft, saveDraft, loadDraft } from "../src/model.mjs";

const item = { id: "42", kind: "item", name: "gregtech:ingot", damage: 0, label: "Steel Ingot", exact: true, craftable: true };

test("restored draft gets current catalog textures without changing saved intent", () => {
  const values = new Map();
  const storage = { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) };
  const current = { ...item, label: "钢锭", catalog: { image_state: "available", image_url: "/textures/steel.png", image_key: "steel" } };
  saveDraft(storage, "draft", { version: 1, entries: addDraft([], current, "4096", "CPU 1", true), receipts: {} });
  const entry = loadDraft(storage, "draft").entries[0];
  assert.equal(entry.item.catalog, undefined);
  const before = structuredClone(entry);
  assert.equal(draftDisplayItem(entry, new Map([["42", current]])), current);
  assert.deepEqual(entry, before);
  assert.equal(entry.quantity, "4096");
  assert.equal(entry.cpu_name, "CPU 1");
});

test("display hydration rejects conflicting item identities and keeps missing data readable", () => {
  const entry = { item };
  for (const changed of [{ name: "gregtech:dust" }, { damage: 1 }, { kind: "fluid" }]) {
    assert.equal(draftDisplayItem(entry, new Map([["42", { ...item, ...changed }]])), item);
  }
  assert.equal(draftDisplayItem(entry, new Map()), item);
});

test("draft summary counts only selected editable intent with exact integer totals", () => {
  const entries = [
    { id: "a", status: "draft", quantity: "9223372036854775807" },
    { id: "b", status: "draft", quantity: "9223372036854775807" },
    { id: "c", status: "draft", quantity: "2" },
    { id: "locked", status: "uncertain", quantity: "9" },
  ];
  assert.deepEqual(draftSelectionSummary(entries, new Set(["c"])), { editable: 3, selected: 2, quantity: "18446744073709551614" });
  assert.deepEqual(draftSelectionSummary([], new Set()), { editable: 0, selected: 0, quantity: "0" });
});
