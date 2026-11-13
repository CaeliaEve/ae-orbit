import test from "node:test";
import assert from "node:assert/strict";
import { defaultSettings, loadSettings, normalizeSettings, saveSettings, SETTINGS_KEY } from "../src/settings.mjs";

function storage(initial = {}) {
  const values = new Map(Object.entries(initial));
  return {
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    value: key => values.get(key),
  };
}

test("settings normalize unknown values without changing the product defaults", () => {
  assert.deepEqual(normalizeSettings({ density: "huge", reconnect: "yes", inventoryLayout: "dense" }), {
    ...defaultSettings, inventoryLayout: "dense",
  });
});

test("settings persist and reload as a bounded preference record", () => {
  const memory = storage();
  const saved = saveSettings(memory, { density: "comfortable", taskNotifications: false });
  assert.equal(memory.value(SETTINGS_KEY), JSON.stringify(saved));
  assert.deepEqual(loadSettings(memory), { ...defaultSettings, density: "comfortable", taskNotifications: false });
});

test("corrupt settings fall back to defaults", () => {
  assert.deepEqual(loadSettings(storage({ [SETTINGS_KEY]: "{" })), { ...defaultSettings });
});
