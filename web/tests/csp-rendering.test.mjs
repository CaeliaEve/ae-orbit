import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

test("dynamic CPU markup stays compatible with the strict style CSP", async () => {
  const source = await readFile(new URL("../src/app.mjs", import.meta.url), "utf8");
  assert.doesNotMatch(source, /<[^>]+\sstyle\s*=/i);
});
