const text = value => typeof value === "string" ? value.trim() : "";
const GAME_TICK_MS = 1000 / 20;

export function catalogDisplayName(item) {
  const label = text(item?.label);
  const localized = text(item?.catalog?.localized_name);
  // Dedicated servers commonly report English labels even to Chinese clients.
  // Use the verified catalog translation while preserving existing Chinese labels.
  const chinese = /[\u3400-\u9fff]/;
  if (chinese.test(localized) && !chinese.test(label)) return localized;
  return label || localized || text(item?.name);
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

export function catalogImageURL(item) {
  const catalog = item?.catalog;
  return catalog?.image_state === "available" && typeof catalog.image_url === "string" && catalog.image_url
    ? catalog.image_url
    : "";
}

export function animationFrameAt(animation, elapsedMs) {
  if (!animation || !Array.isArray(animation.frames) || animation.frames.length < 2) return null;
  const cycleTicks = animation.frames.reduce((total, frame) => total + frame.ticks, 0);
  if (!Number.isInteger(cycleTicks) || cycleTicks < 1) return null;
  const elapsed = Math.max(0, Number(elapsedMs) || 0);
  const cycleMs = cycleTicks * GAME_TICK_MS;
  const elapsedTicks = (elapsed % cycleMs) / GAME_TICK_MS;
  let remaining = elapsedTicks;
  for (let index = 0; index < animation.frames.length; index += 1) {
    const frame = animation.frames[index];
    if (remaining < frame.ticks) {
      return {
        index,
        frame,
        next: animation.frames[(index + 1) % animation.frames.length],
        tick: remaining,
        progress: remaining / frame.ticks,
        cycleTicks,
      };
    }
    remaining -= frame.ticks;
  }
  return null;
}

export function spriteLayout(animation, targetWidth, targetHeight, frame = animation?.frames?.[0]) {
  if (!animation || !Array.isArray(animation.frames) || !animation.frames.length) return null;
  const width = Number(targetWidth);
  const height = Number(targetHeight);
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0
    || !frame?.width || !frame?.height) return null;
  const scale = Math.min(width / frame.width, height / frame.height);
  const renderedWidth = frame.width * scale;
  const renderedHeight = frame.height * scale;
  return {
    scale,
    backgroundWidth: animation.width * scale,
    backgroundHeight: animation.height * scale,
    offsetX: (width - renderedWidth) / 2,
    offsetY: (height - renderedHeight) / 2,
  };
}
