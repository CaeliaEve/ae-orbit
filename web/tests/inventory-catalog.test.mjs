import test from "node:test";
import assert from "node:assert/strict";
import {
  animationFrameAt,
  catalogAnimation,
  catalogDisplayName,
  catalogFacts,
  catalogImageURL,
  catalogSource,
  catalogTooltips,
  spriteLayout,
} from "../src/inventory-catalog.mjs";

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

test("Chinese catalog names replace English dedicated-server labels", () => {
  const live = { name: "minecraft:stone", label: "Stone", catalog: { localized_name: "石头" } };
  assert.equal(catalogDisplayName(live), "石头");
  assert.equal(live.label, "Stone", "display localization mutated the live observation");
  assert.deepEqual(catalogFacts(live), [], "primary Chinese name was repeated as a secondary fact");
  assert.equal(catalogDisplayName({ ...live, label: "自定义石头" }), "自定义石头");
  assert.equal(catalogDisplayName({ ...live, catalog: { localized_name: "Rock" } }), "Stone");
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

test("catalog image identity stays stable across inventory quantity updates", () => {
  const before = { id: "1226", catalog: { image_state: "available", image_url: "/catalog/1226.png" } };
  const after = { ...before, quantity: "9007199254740993" };
  assert.equal(catalogImageURL(before), "/catalog/1226.png");
  assert.equal(catalogImageURL(after), catalogImageURL(before));
  assert.equal(catalogImageURL({ id: "1226", catalog: { image_state: "missing" } }), "");
});

test("catalog animation selects frames by game ticks and exposes interpolation progress", () => {
  const animation = {
    width: 48,
    height: 16,
    interpolate: true,
    frames: [
      { x: 0, y: 0, width: 16, height: 16, ticks: 2 },
      { x: 16, y: 0, width: 16, height: 16, ticks: 3 },
      { x: 32, y: 0, width: 16, height: 16, ticks: 1 },
    ],
  };
  assert.equal(animationFrameAt(animation, 0).index, 0);
  assert.equal(animationFrameAt(animation, 50).tick, 1);
  assert.equal(animationFrameAt(animation, 75).tick, 1.5);
  assert.equal(animationFrameAt(animation, 75).progress, 0.75);
  assert.equal(animationFrameAt(animation, 100).index, 1);
  assert.equal(animationFrameAt(animation, 200).index, 1);
  assert.equal(animationFrameAt(animation, 250).index, 2);
  assert.equal(animationFrameAt(animation, 300).index, 0);
  assert.equal(animationFrameAt(animation, 200).progress, 2 / 3);
});

test("catalog animation layout contains and centers any sprite in its rendered box", () => {
  const animation = {
    width: 16,
    height: 128,
    frames: [{ x: 0, y: 0, width: 16, height: 16, ticks: 2 }, { x: 0, y: 16, width: 16, height: 16, ticks: 2 }],
  };
  assert.deepEqual(spriteLayout(animation, 34, 34), {
    scale: 2.125,
    backgroundWidth: 34,
    backgroundHeight: 272,
    offsetX: 0,
    offsetY: 0,
  });
  assert.equal(spriteLayout(animation, 0, 34), null);
});

test("catalog animation layout can adapt when adjacent frames use different dimensions", () => {
  const animation = {
    width: 48,
    height: 16,
    frames: [
      { x: 0, y: 0, width: 16, height: 16, ticks: 1 },
      { x: 16, y: 0, width: 8, height: 8, ticks: 1 },
    ],
  };
  const next = spriteLayout(animation, 32, 32, animation.frames[1]);
  assert.equal(next.scale, 4);
  assert.equal(next.offsetX, 0);
  assert.equal(next.offsetY, 0);
  assert.equal(next.backgroundWidth, 192);
  assert.equal(next.backgroundHeight, 64);
});
