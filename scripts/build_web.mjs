import { build } from "esbuild";
import { mkdir, copyFile } from "node:fs/promises";

await mkdir("web/static", { recursive: true });
await build({
  entryPoints: ["web/src/app.mjs"],
  outfile: "web/static/app.js",
  bundle: true,
  format: "esm",
  target: ["es2022"],
  minify: true,
  legalComments: "eof",
});
await copyFile("web/src/style.css", "web/static/style.css");
await copyFile("web/src/compact.css", "web/static/compact.css");
console.log("Built web/static/app.js, style.css and compact.css.");
