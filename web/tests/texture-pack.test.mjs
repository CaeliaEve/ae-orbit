import test from "node:test";
import assert from "node:assert/strict";
import { zipSync, strToU8 } from "fflate";
import {
  createTexturePackStore,
  defaultTexturePackSettings,
  loadTexturePackURL,
  normalizeTexturePackSettings,
  validateTexturePackManifest,
} from "../src/texture-pack.mjs";

test("texture pack settings stay bounded and keep the server as the default", () => {
  assert.deepEqual(normalizeTexturePackSettings(), defaultTexturePackSettings);
  assert.deepEqual(normalizeTexturePackSettings({
    textureSource: "url",
    texturePackURL: "  https://cdn.example.test/catalog.zip  ",
  }), {
    textureSource: "url",
    texturePackURL: "https://cdn.example.test/catalog.zip",
  });
  assert.deepEqual(normalizeTexturePackSettings({
    textureSource: "local",
    texturePackURL: "javascript:alert(1)",
  }), { textureSource: "local", texturePackURL: "" });
  assert.deepEqual(normalizeTexturePackSettings({ textureSource: "invalid" }), defaultTexturePackSettings);
});

test("texture pack manifests only admit safe declared PNG assets", () => {
  const manifest = validateTexturePackManifest({
    format: "ae-orbit.texture-pack",
    version: 1,
    assets: ["assets/stone.png"],
  });
  assert.deepEqual(manifest.assets, ["assets/stone.png"]);
  assert.throws(() => validateTexturePackManifest({
    format: "ae-orbit.texture-pack",
    version: 1,
    assets: ["../outside.png"],
  }), /unsafe texture pack asset/);
});

test("texture pack store resolves local assets without changing item metadata", async () => {
  const objectURLs = [];
  const archive = zipSync({
    "manifest.json": strToU8(JSON.stringify({
      format: "ae-orbit.texture-pack",
      version: 1,
      assets: ["assets/stone.png"],
    })),
    "assets/stone.png": new Uint8Array([137, 80, 78, 71]),
  });
  const store = createTexturePackStore({
    createObjectURL: blob => {
      const url = `blob:test-${objectURLs.length}`;
      objectURLs.push({ url, blob });
      return url;
    },
    revokeObjectURL: () => {},
  });
  const summary = await store.loadArchive(archive, "test-pack");
  assert.equal(summary.assetCount, 1);
  assert.equal(store.resolve("assets/stone.png"), "blob:test-0");
  assert.equal(store.resolve("assets/missing.png"), "");
  assert.equal(store.status().label, "test-pack");
});

test("texture pack URL loading keeps same-origin credentials and omits cross-origin credentials", async () => {
  const calls = [];
  const response = { ok: true, arrayBuffer: async () => new Uint8Array([1]).buffer };
  await loadTexturePackURL("https://cdn.example.test/textures.zip", async (url, options) => {
    calls.push({ url, options });
    return response;
  });
  assert.equal(calls[0].options.credentials, "omit");
  calls.length = 0;
  await loadTexturePackURL("http://localhost/textures.zip", async (url, options) => {
    calls.push({ url, options });
    return response;
  });
  assert.equal(calls[0].options.credentials, "same-origin");
});
