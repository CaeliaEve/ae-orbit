import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const compactCSS = await readFile(new URL("../src/compact.css", import.meta.url), "utf8");
const styleCSS = await readFile(new URL("../src/style.css", import.meta.url), "utf8");
const appSource = await readFile(new URL("../src/app.mjs", import.meta.url), "utf8");
const indexHTML = await readFile(new URL("../static/index.html", import.meta.url), "utf8");

test("inventory cards expose a restrained depth and selected-state contract", () => {
  assert.match(compactCSS, /\.inventory-table tr:hover[\s\S]*?transform:\s*translateY\(-1px\)/);
  assert.match(compactCSS, /\.inventory-table tr\[data-selected="true"\][\s\S]*?box-shadow:/);
  assert.match(compactCSS, /\.inventory-table \.missing-image[\s\S]*?background:/);
});

test("inventory detail and hover surfaces keep a visual anchor without requiring textures", () => {
  assert.match(compactCSS, /\.detail-empty/);
  assert.match(compactCSS, /\.inventory-hover-media/);
  assert.match(appSource, /class="detail-empty"/);
  assert.match(appSource, /class="inventory-hover-media"/);
});

test("the shell avoids template kickers and ambient panel shadows", () => {
  assert.doesNotMatch(indexHTML, /class="eyebrow"/);
  assert.match(compactCSS, /\.app-frame\s*\{[^}]*box-shadow:\s*none/);
  assert.match(compactCSS, /\.catalog,\s*\.inspector,\s*\.panel\s*\{[^}]*box-shadow:\s*none/);
});

test("the final inventory hover surface is not overridden by a legacy duplicate", () => {
  assert.equal((compactCSS.match(/^\.inventory-hover-card\s*\{/gm) || []).length, 1);
  assert.doesNotMatch(compactCSS, /box-shadow:\s*0 12px 28px #0009/);
});

test("desktop workbench stays inside the viewport while mobile keeps natural flow", () => {
  assert.match(compactCSS, /@media\s*\(min-width:\s*901px\)[\s\S]*?html,\s*body\s*\{[\s\S]*?height:\s*100%;[\s\S]*?overflow:\s*hidden/);
  assert.match(compactCSS, /@media\s*\(min-width:\s*901px\)[\s\S]*?\.content,\s*main\s*\{[\s\S]*?overflow:\s*hidden/);
  assert.match(compactCSS, /@media\s*\(min-width:\s*901px\)[\s\S]*?\.view\s*\{[\s\S]*?height:\s*100%;[\s\S]*?overflow:\s*hidden/);
});

test("inventory grid fills available width and supports wheel page navigation", () => {
  assert.match(compactCSS, /\.inventory-table tbody[\s\S]*?grid-template-columns:\s*repeat\(auto-fit,\s*minmax\(/);
  assert.doesNotMatch(compactCSS, /grid-template-columns:\s*repeat\((?:8|7|6|5),/);
  assert.match(appSource, /function inventoryPageLimit\(\)/);
  assert.match(appSource, /const limit = inventoryPageLimit\(\)/);
  assert.match(appSource, /limit: String\(spec\.limit\)/);
  assert.match(appSource, /function refreshInventoryCapacity\(\)/);
  assert.match(appSource, /function changeInventoryPage\(direction\)/);
  assert.match(appSource, /\.catalog[\s\S]*?addEventListener\("wheel"/);
  assert.match(appSource, /event\.preventDefault\(\)/);
});

test("inventory wheel queues NEI-like continuous page changes", () => {
  assert.match(appSource, /let inventoryWheelAccumulation = 0/);
  assert.match(appSource, /function drainInventoryWheel\(\)/);
  assert.match(appSource, /const INVENTORY_WHEEL_THRESHOLD = 80/);
  assert.match(appSource, /const INVENTORY_WHEEL_MAX_BACKLOG = INVENTORY_WHEEL_THRESHOLD \* 48/);
  assert.match(appSource, /inventoryWheelAccumulation \+ delta/);
  assert.match(appSource, /Math\.min\(/);
  assert.match(appSource, /INVENTORY_WHEEL_MAX_BACKLOG,\s*\n?\s*Math\.max\(/);
  assert.match(appSource, /-INVENTORY_WHEEL_MAX_BACKLOG,\s*inventoryWheelAccumulation \+ delta/);
  assert.match(appSource, /const INVENTORY_WHEEL_MAX_PAGES_PER_FRAME = 12/);
  assert.match(appSource, /for \(let pages = 0; pages < INVENTORY_WHEEL_MAX_PAGES_PER_FRAME/);
  assert.match(appSource, /setTimeout\(\(\) => \{[\s\S]*?drainInventoryWheel\(\);[\s\S]*?\}, 0\)/);
  assert.doesNotMatch(appSource, /INVENTORY_WHEEL_PAGE_INTERVAL/);
  assert.doesNotMatch(appSource, /inventoryWheelNextAt/);
  assert.match(appSource, /function clearInventoryWheelDirection\(direction\)/);
  assert.match(appSource, /const INVENTORY_PREFETCH_DEPTH = 12/);
  assert.match(appSource, /function prefetchInventoryPages\(cursor, generation, limit\)/);
  assert.match(appSource, /function applyCachedInventoryPage\(cursor, page\)/);
  assert.match(appSource, /inventoryPageCache\.get\(key\)/);
  assert.match(appSource, /const inventoryPageRequests = new Map\(\)/);
  assert.match(appSource, /function requestInventoryPage\(cursor, limit, signal/);
  assert.match(appSource, /if \(Math\.abs\(inventoryWheelAccumulation\) >= INVENTORY_WHEEL_THRESHOLD\) scheduleInventoryWheelDrain\(\)/);
  assert.match(appSource, /changeInventoryPage\(direction\)/);
  assert.match(compactCSS, /#view-inventory \.table-wrap[\s\S]*?overflow:\s*hidden/);
});

test("inventory hover card is centered over the catalog and only mouse-triggered", () => {
  assert.match(appSource, /const tableRect = wrap\.getBoundingClientRect\(\)/);
  assert.match(appSource, /const left = tableRect\.left \+ \(tableRect\.width - cardRect\.width\) \/ 2/);
  assert.doesNotMatch(appSource, /document\.addEventListener\("focusin", event => \{[\s\S]*?showInventoryHover/);
  assert.match(compactCSS, /\.inventory-hover-card\s*\{[\s\S]*?position:\s*fixed/);
});

test("inventory hover does not add a white card highlight", () => {
  assert.match(compactCSS, /\.inventory-table tr:hover\s*\{[\s\S]*?background:\s*#101215/);
  assert.match(compactCSS, /\.inventory-table tr:hover\s*\{[\s\S]*?border-color:\s*#292d33/);
  assert.match(compactCSS, /\.inventory-table tr:hover\s*\{[\s\S]*?transform:\s*none/);
  assert.match(compactCSS, /\.inventory-table tr:hover td\s*\{[\s\S]*?background:\s*transparent/);
});

test("inventory hover exposes catalog metadata without inventing missing values", () => {
  assert.match(appSource, /from "\.\/inventory-catalog\.mjs"/);
  assert.match(appSource, /catalogFacts\(item\)/);
  assert.match(appSource, /catalogTooltips\(item\)/);
  assert.match(appSource, /inventory-hover-catalog/);
  assert.match(compactCSS, /\.inventory-hover-catalog/);
  assert.match(compactCSS, /\.inventory-hover-tooltip/);
});

test("network management uses one structured operations dashboard", () => {
  assert.match(appSource, /class="network-hero"/);
  assert.match(appSource, /class="network-metrics"/);
  assert.match(appSource, /class="network-dashboard-grid"/);
  assert.match(appSource, /class="network-capabilities"/);
  assert.match(appSource, /network-devices-panel/);
  assert.doesNotMatch(appSource, /<div class="network-table" id="network-table">/);
  assert.match(compactCSS, /\.network-dashboard\s*\{/);
  assert.match(compactCSS, /\.network-metrics\s*\{/);
  assert.match(compactCSS, /\.network-dashboard-grid\s*\{/);
  assert.match(compactCSS, /\.network-capability\s*\{/);
  assert.match(compactCSS, /\.network-devices-panel\s+\.device-cards\s*\{/);
  assert.match(compactCSS, /#view-network #network-info\.network-dashboard\s*\{[\s\S]*?overflow-y:\s*auto/);
});

test("CPU summary has one stable host and desktop lists remain reachable", () => {
  assert.match(indexHTML, /<div id="cpu-overview" class="cpu-overview"/);
  assert.match(appSource, /\$\("#cpu-overview"\)\.innerHTML = overview/);
  assert.doesNotMatch(appSource, /const overview = `<section id="cpu-overview"/);
  assert.match(compactCSS, /#view-cpus #cpu-list\s*\{[\s\S]*?overflow-y:\s*auto/);
  assert.match(compactCSS, /#view-orders #jobs-panel[\s\S]*?overflow-y:\s*auto/);
});

test("dashboard health uses explicit network state semantics", () => {
  assert.match(indexHTML, /id="dashboard-network-card"/);
  assert.match(appSource, /dashboard-network-card/);
  assert.match(appSource, /等待 OC 恢复/);
  assert.match(compactCSS, /#dashboard-network-card\[data-state="offline"\] strong/);
  assert.doesNotMatch(styleCSS, /\.metric-card:first-child strong\s*\{\s*color:\s*var\(--green\)/);
});

test("inventory exposes stale snapshots and names missing textures", () => {
  assert.match(appSource, /missing-texture/);
  assert.match(appSource, /stock-value[\s\S]*current_session[\s\S]*stale/);
  assert.match(compactCSS, /\.inventory-table \.freshness\.stale\s*\{[\s\S]*?display:\s*block/);
  assert.match(compactCSS, /\.item-button\.missing-texture \.item-copy/);
  assert.doesNotMatch(appSource, /aria-describedby="inventory-hover-card"/);
});

test("settings tabs and mobile brand expose valid navigation semantics", () => {
  assert.match(indexHTML, /role="tab"[^>]+aria-selected="true"[^>]+aria-controls="settings-detail"/);
  assert.match(indexHTML, /class="mobile-brand"/);
  assert.match(appSource, /setAttribute\("aria-selected", String\(item === button\)\)/);
  assert.match(compactCSS, /\.mobile-brand\s*\{/);
  assert.match(compactCSS, /\.cpu-overview\s*\{[\s\S]*?grid-template-columns:\s*1fr/);
});
