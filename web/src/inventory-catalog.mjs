const text = value => typeof value === "string" ? value.trim() : "";

export function catalogDisplayName(item) {
  return text(item?.label) || text(item?.catalog?.localized_name) || text(item?.name);
}

export function catalogSource(item) {
  const name = text(item?.name);
  return name.includes(":") ? name.split(":", 1)[0] : "";
}

export function catalogFacts(item) {
  const catalog = item?.catalog;
  if (!catalog || typeof catalog !== "object") return [];
  const facts = [];
  const title = catalogDisplayName(item);
  const localizedName = text(catalog.localized_name);
  const modID = text(catalog.mod_id);
  if (localizedName && localizedName !== title) facts.push(["本地化名称", localizedName]);
  if (modID) facts.push(["Mod", modID]);
  if (Number.isInteger(catalog.max_stack_size) && catalog.max_stack_size > 0) {
    facts.push(["最大堆叠", String(catalog.max_stack_size)]);
  }
  if (Number.isInteger(catalog.max_damage) && catalog.max_damage > 0) {
    facts.push(["最大耐久", String(catalog.max_damage)]);
  }
  if (catalog.image_state === "missing") facts.push(["纹理", "暂无已核验图标"]);
  return facts;
}

export function catalogTooltips(item) {
  const values = item?.catalog?.tooltips;
  if (!Array.isArray(values)) return [];
  return values.map(text).filter(Boolean).slice(0, 8);
}

export function catalogAnimation(item) {
  const catalog = item?.catalog;
  if (!catalog || catalog.image_state !== "available" || typeof catalog.image_url !== "string" || !catalog.image_url) return null;
  const width = Number(catalog.width);
  const height = Number(catalog.height);
  if (!Number.isInteger(width) || !Number.isInteger(height) || width < 1 || height < 1) return null;
  if (!Array.isArray(catalog.frames) || catalog.frames.length < 2 || catalog.frames.length > 4096) return null;
  const frames = catalog.frames.filter(frame => {
    const x = Number(frame?.x);
    const y = Number(frame?.y);
    const frameWidth = Number(frame?.width);
    const frameHeight = Number(frame?.height);
    const ticks = Number(frame?.ticks);
    return Number.isInteger(x) && x >= 0 && Number.isInteger(y) && y >= 0
      && Number.isInteger(frameWidth) && frameWidth > 0
      && Number.isInteger(frameHeight) && frameHeight > 0
      && Number.isInteger(ticks) && ticks > 0 && ticks <= 72000
      && x + frameWidth <= width && y + frameHeight <= height;
  });
  return frames.length === catalog.frames.length ? { width, height, frames, interpolate: catalog.interpolate === true } : null;
}
