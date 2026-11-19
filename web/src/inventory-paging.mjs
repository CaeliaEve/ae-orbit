// Wheel input is consumed as it arrives; it never queues future page changes.
export function createInventoryWheel({ threshold = 80, interval = 220, idle = 160 } = {}) {
  let amount = 0, lastAt = -Infinity, turnedAt = -Infinity, direction = 0;
  return {
    reset() { amount = 0; lastAt = turnedAt = -Infinity; direction = 0; },
    consume(delta, now, loading = false) {
      if (!Number.isFinite(delta) || !delta) return 0;
      const nextDirection = Math.sign(delta);
      const reversed = direction && nextDirection !== direction;
      if (reversed || now - lastAt > idle) amount = 0;
      direction = nextDirection;
      lastAt = now;
      if (loading || (!reversed && now - turnedAt < interval)) { amount = 0; return 0; }
      amount += delta;
      if (Math.abs(amount) < threshold) return 0;
      amount = 0;
      turnedAt = now;
      return nextDirection;
    },
  };
}

export function inventoryKeyboardDirection(event) {
  if (event.defaultPrevented || event.ctrlKey || event.altKey || event.metaKey || event.shiftKey || event.isComposing || event.repeat) return null;
  if (event.target?.closest?.('input, textarea, select, [contenteditable="true"], dialog, #inspector')) return null;
  return event.key === "PageDown" ? 1 : event.key === "PageUp" ? -1 : event.key === "Home" ? 0 : null;
}
