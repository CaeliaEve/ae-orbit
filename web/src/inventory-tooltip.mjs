import { catalogDisplayName, catalogSource, catalogTooltips } from "./inventory-catalog.mjs";
import { stockText } from "./plain-inventory.mjs";

const escapeHTML = value => String(value ?? "").replace(/[&<>"']/g, character => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[character]));
const styles = { l: "bold", m: "strike", n: "underline", o: "italic" };

// NEI also embeds renderer IDs and hotkey markers. They cannot be rendered as text.
export function tooltipText(value) {
  return String(value ?? "").replace(/§z[^§]*§Z/g, "").replace(/§!\S+/g, "").replace(/§x\d+/g, "");
}

export function plainTooltipText(value) {
  return tooltipText(value).replace(/§./g, "").trim();
}

export function minecraftTextHTML(value) {
  const text = tooltipText(value);
  let color = "", formatting = new Set(), buffer = "", html = "";
  const flush = () => {
    if (!buffer) return;
    const classes = [color ? `mc-${color}` : "", ...[...formatting].map(style => `mc-${style}`)].filter(Boolean).join(" ");
    html += classes ? `<span class="${classes}">${escapeHTML(buffer)}</span>` : escapeHTML(buffer);
    buffer = "";
  };
  for (let index = 0; index < text.length; index += 1) {
    if (text[index] !== "§" || index + 1 === text.length) { buffer += text[index]; continue; }
    flush();
    const code = text[++index].toLowerCase();
    if (/^[0-9a-f]$/.test(code)) { color = code; formatting = new Set(); }
    else if (styles[code]) formatting.add(styles[code]);
    else if (code === "r") { color = ""; formatting = new Set(); }
    // §h is NEI's line spacing marker; §k's randomized glyphs stay readable here.
  }
  flush();
  return html;
}

export function inventoryTooltipModel(item, advanced = false) {
  const title = catalogDisplayName(item);
  const source = item?.catalog?.mod_id || catalogSource(item);
  const names = [title, item?.label, item?.catalog?.localized_name, item?.name].filter(Boolean).map(plainTooltipText);
  const exported = catalogTooltips(item).flatMap(value => value.split(/\r?\n/));
  let titleText = title;
  const lines = [];
  for (const value of exported) {
    const visible = plainTooltipText(value);
    const name = visible.replace(/\s*\(#\d+(?:\/\d+)?\)\s*$/, "");
    if (names.includes(name)) {
      // Preserve a supplied rarity color without displaying the exported world's numeric ID.
      if (name === title && /§[0-9a-f]/i.test(value)) titleText = tooltipText(value).replace(/\s*\(#\d+(?:\/\d+)?\)\s*(?=(?:§.)*$)/, "");
      continue;
    }
    if (!visible || visible.toLowerCase() === String(source).toLowerCase()) continue;
    lines.push({ text: tooltipText(value), kind: "description", gap: /§h$/.test(value) });
  }
  const footers = [{ text: `库存：${stockText(item)}${item?.quantity_known !== false && item?.current_session === false ? " · 待同步" : ""}`, kind: "stock" }];
  if(item.group)footers.push({text:`${item.group.stock} 个有库存变种 · 已查 ${item.group.checked}/${item.group.candidates}`,kind:'muted'});
  if(item.has_tag)footers.push({text:`NBT 变种 ${item.variant?.hash?.slice(0,10) || ''}`,kind:'muted'});
  if (!item?.group && (item?.craftable_observed_at || item?.craftable_current_session === true)) {
    footers.push({ text: item.craftable_current_session === false ? "合成能力待核验" : item.craftable ? "可合成" : "不可合成", kind: item.craftable && item.craftable_current_session !== false ? "craftable" : "muted" });
  }
  if (advanced) {
    if (item?.name) lines.push({ text: `${item.name}${Number.isInteger(item.damage) ? ` @ ${item.damage}` : ""}`, kind: "muted" });
    const catalog = item?.catalog;
    if (Number.isInteger(catalog?.max_stack_size) && catalog.max_stack_size > 0) lines.push({ text: `最大堆叠：${catalog.max_stack_size}`, kind: "muted" });
    if (Number.isInteger(catalog?.max_damage) && catalog.max_damage > 0) lines.push({ text: `最大耐久：${catalog.max_damage}`, kind: "muted" });
  }
  if (source) footers.push({ text: source, kind: "mod" });
  footers.push({ text: advanced ? "松开 Shift 收起详细信息" : "Shift 查看详细信息", kind: "hint" });
  return { title: { text: titleText, kind: "title" }, lines, footers };
}

export function tooltipLineHTML(line) {
  return `<div class="nei-tooltip-line nei-tooltip-${escapeHTML(line.kind)}${line.gap ? " nei-tooltip-gap" : ""}">${minecraftTextHTML(line.text)}</div>`;
}

export function splitTooltipLine(line, fits) {
  if (fits(line)) return [line];
  const tokens = tooltipText(line.text).match(/§.|[^]/gu) || [];
  const fragments = [];
  let start = 0, active = "";
  while (start < tokens.length) {
    let low = start + 1, high = tokens.length, end = low;
    while (low <= high) {
      const middle = Math.floor((low + high) / 2);
      if (fits({ ...line, text: active + tokens.slice(start, middle).join(""), gap: false })) { end = middle; low = middle + 1; }
      else high = middle - 1;
    }
    const text = active + tokens.slice(start, end).join("");
    fragments.push({ ...line, text, gap: end === tokens.length && line.gap });
    for (const token of tokens.slice(start, end)) {
      if (/^§[0-9a-fr]$/i.test(token)) active = token.toLowerCase() === "§r" ? "" : token;
      else if (/^§[lmno]$/i.test(token)) active += token;
    }
    start = end;
  }
  return fragments;
}

// Measure wrapped lines once when content changes; cursor movement only changes position.
export function paginateTooltipLines(lines, heights, availableHeight) {
  const pages = [[]];
  let height = 0;
  lines.forEach((line, index) => {
    const nextHeight = Math.max(1, Number(heights[index]) || 18);
    if (height + nextHeight > availableHeight && pages.at(-1).length) { pages.push([]); height = 0; }
    pages.at(-1).push(line);
    height += nextHeight;
  });
  return pages;
}

export function tooltipPosition(pointer, size, viewport) {
  const margin = 8, offset = 12;
  let left = pointer.x + offset;
  if (left + size.width > viewport.width - margin) left = pointer.x - offset - size.width;
  return {
    left: Math.max(margin, Math.min(left, viewport.width - size.width - margin)),
    top: Math.max(margin, Math.min(pointer.y - offset, viewport.height - size.height - margin)),
  };
}
