import test from "node:test";
import assert from "node:assert/strict";
import { inventoryTooltipModel, minecraftTextHTML, paginateTooltipLines, plainTooltipText, splitTooltipLine, tooltipPosition } from "../src/inventory-tooltip.mjs";

const item = { id: "42", label: "碳化铌杆", name: "miscutils:itemRodNiobiumCarbide", damage: 0, quantity: "9007199254740993", quantity_known: true, current_session: true, catalog: { mod_id: "miscutils", max_stack_size: 64, tooltips: ["碳化铌杆 (#8515)", "NbC", "§!ctrl", "§z738§Z", "§8长按 [§7G§8] 打开指南"] } };

test("real NESQL++ tooltip markers and world numeric ID do not leak into the displayed description", () => {
  const model = inventoryTooltipModel(item);
  assert.equal(model.title.text, "碳化铌杆");
  assert.deepEqual(model.lines.map(line => plainTooltipText(line.text)), ["NbC", "长按 [G] 打开指南"]);
  assert.equal(model.footers.find(line => line.kind === "stock").text, "库存：9,007,199,254,740,993");
  assert.equal(model.footers.find(line => line.kind === "mod").text, "miscutils");
  assert.ok(!JSON.stringify(model).includes("8515"));
  assert.ok(!JSON.stringify(model).includes('"42"'));
});

test("Minecraft color resets clear previous styles and untrusted text stays escaped", () => {
  assert.equal(minecraftTextHTML('§6§l金色§r普通<svg onload="x">§7灰'), '<span class="mc-6 mc-bold">金色</span>普通&lt;svg onload=&quot;x&quot;&gt;<span class="mc-7">灰</span>');
  assert.equal(minecraftTextHTML("§l粗§a绿§o斜"), '<span class="mc-bold">粗</span><span class="mc-a">绿</span><span class="mc-a mc-italic">斜</span>');
});

test("formatted rarity title stays colored and untranslated duplicate title is removed", () => {
  const model = inventoryTooltipModel({ ...item, label: "Steel Ingot", catalog: { localized_name: "钢锭", tooltips: ["§e钢锭 (#123/4)§r", "Steel Ingot", "Fe"] } });
  assert.equal(model.title.text, "§e钢锭§r");
  assert.deepEqual(model.lines.map(line => line.text), ["Fe"]);
});

test("Shift details use the stable registry identity and verified catalog facts", () => {
  const simple = inventoryTooltipModel(item), detail = inventoryTooltipModel(item, true);
  assert.ok(!simple.lines.some(line => line.text.includes("miscutils:")));
  assert.ok(detail.lines.some(line => line.text === "miscutils:itemRodNiobiumCarbide @ 0"));
  assert.ok(detail.lines.some(line => line.text === "最大堆叠：64"));
  assert.ok(!detail.lines.some(line => line.text.includes("耐久")));
});

test("unknown inventory and stale crafting observations never become confirmed values", () => {
  const model = inventoryTooltipModel({ ...item, quantity_known: false, craftable: true, craftable_observed_at: "old", craftable_current_session: false });
  assert.equal(model.footers[0].text, "库存：未查询");
  assert.ok(model.footers.some(line => line.text === "合成能力待核验"));
  assert.ok(!model.footers.some(line => line.kind === "craftable"));
});

test("all exported description lines survive pagination in their original order", () => {
  const descriptions = Array.from({ length: 35 }, (_, index) => `说明 ${index}`);
  const model = inventoryTooltipModel({ ...item, catalog: { tooltips: descriptions } });
  const pages = paginateTooltipLines(model.lines, model.lines.map((_, i) => i % 2 ? 36 : 18), 130);
  assert.ok(pages.length > 1);
  assert.deepEqual(pages.flat().map(line => line.text), descriptions);
  assert.deepEqual(paginateTooltipLines([], [], 10), [[]]);
});

test("NEI cursor offset flips at the right edge and clamps to every viewport corner", () => {
  const size = { width: 240, height: 180 }, viewport = { width: 1000, height: 600 };
  assert.deepEqual(tooltipPosition({ x: 300, y: 200 }, size, viewport), { left: 312, top: 188 });
  assert.deepEqual(tooltipPosition({ x: 990, y: 598 }, size, viewport), { left: 738, top: 412 });
  assert.deepEqual(tooltipPosition({ x: 0, y: 0 }, size, viewport), { left: 12, top: 8 });
  for (const x of [0, 10, 500, 999]) for (const y of [0, 300, 599]) {
    const position = tooltipPosition({ x, y }, size, viewport);
    assert.ok(position.left >= 8 && position.left + size.width <= viewport.width - 8);
    assert.ok(position.top >= 8 && position.top + size.height <= viewport.height - 8);
  }
});

test("an oversized single description splits without losing Unicode characters or Minecraft styles", () => {
  const line = { text: "§6§l超长说明😀第一段§a绿色第二段§r普通结尾", kind: "description", gap: true };
  const fragments = splitTooltipLine(line, fragment => [...plainTooltipText(fragment.text)].length <= 6);
  assert.equal(fragments.map(fragment => plainTooltipText(fragment.text)).join(""), plainTooltipText(line.text));
  assert.ok(fragments.every(fragment => [...plainTooltipText(fragment.text)].length <= 6));
  assert.match(fragments[1].text, /^§6§l/);
  assert.ok(fragments.some(fragment => /^§a/.test(fragment.text)));
  assert.ok(fragments.at(-1).gap);
  assert.ok(fragments.slice(0, -1).every(fragment => !fragment.gap));
});
