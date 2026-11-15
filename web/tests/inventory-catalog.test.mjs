import test from "node:test";
import assert from "node:assert/strict";
import { catalogAnimation, catalogDisplayName, catalogFacts, catalogSource, catalogTooltips } from "../src/inventory-catalog.mjs";

const item = {
  name: "gregtech:gt.metaitem.01",
  label: "游戏内名称",
  catalog: {
    localized_name: "NESQL 名称",
    mod_id: "gregtech",
    max_stack_size: 64,
    max_damage: 1000,
    image_state: "available",
    tooltips: ["第一行", "第二行"],
  },
};

test("catalog metadata keeps the game label as the primary display name", () => {
  assert.equal(catalogDisplayName(item), "游戏内名称");
  assert.equal(catalogSource(item), "gregtech");
  assert.deepEqual(catalogFacts(item), [
    ["本地化名称", "NESQL 名称"],
    ["Mod", "gregtech"],
    ["最大堆叠", "64"],
    ["最大耐久", "1000"],
  ]);
  assert.deepEqual(catalogTooltips(item), ["第一行", "第二行"]);
});

test("missing catalog fields do not create unknown placeholders", () => {
  const sparse = { name: "minecraft:stone", label: "石头", catalog: { image_state: "missing" } };
  assert.deepEqual(catalogFacts(sparse), [["纹理", "暂无已核验图标"]]);
  assert.deepEqual(catalogTooltips(sparse), []);
});

test("localized name matching the game label is not repeated", () => {
  assert.deepEqual(catalogFacts({ label: "石头", name: "minecraft:stone", catalog: { localized_name: "石头", max_stack_size: 0, max_damage: 0 } }), []);
});

test("localized name is a fallback only when the game label is absent", () => {
  assert.equal(catalogDisplayName({ name: "minecraft:stone", catalog: { localized_name: "石头" } }), "石头");
});

test("catalog animation validates the source sheet and timeline", () => {
  const animation = catalogAnimation({
    catalog: {
      image_state: "available",
      image_url: "/catalog.png",
      width: 32,
      height: 16,
      interpolate: true,
      frames: [
        { x: 0, y: 0, width: 16, height: 16, ticks: 2 },
        { x: 16, y: 0, width: 16, height: 16, ticks: 3 },
      ],
    },
  });
  assert.equal(animation.interpolate, true);
  assert.deepEqual(animation.frames.map(frame => frame.ticks), [2, 3]);
  assert.equal(catalogAnimation({ catalog: { image_state: "available", image_url: "/x.png", width: 16, height: 16, frames: [{ x: 0, y: 0, width: 16, height: 16, ticks: 1 }] } }), null);
});
