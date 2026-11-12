const ACTIVE_STATES = ["queued", "assigned", "computing", "running"];
const FILTER_STATES = {
  active: ACTIVE_STATES,
  attention: ["failed", "needs_review"],
  done: ["done", "failed", "canceled", "expired"],
  all: [""],
};
const PAGE_LIMIT = 200;

function timestamp(value) {
  const milliseconds = Date.parse(value);
  if (!Number.isFinite(milliseconds)) throw new TypeError("Invalid order timestamp.");
  // Date truncates the sub-millisecond precision retained by the service.
  const fraction = String(value).match(/\.(\d+)(?:Z|[+-]\d{2}:\d{2})$/)?.[1] || "";
  return BigInt(milliseconds) * 1000000n + BigInt(fraction.slice(3, 9).padEnd(6, "0"));
}

function compareTime(left, right) {
  const a = timestamp(left), b = timestamp(right);
  return a < b ? -1 : a > b ? 1 : 0;
}

function mergeOrders(pages) {
  const byID = new Map();
  for (const page of pages) {
    for (const order of page) {
      const previous = byID.get(order.id);
      if (!previous || compareTime(order.updated_at, previous.updated_at) > 0) byID.set(order.id, order);
    }
  }
  return [...byID.values()].sort((left, right) =>
    compareTime(right.created_at, left.created_at) || (left.id < right.id ? -1 : left.id > right.id ? 1 : 0));
}

export async function loadOrderSnapshot(fetchOrders, filter = "active") {
  if (typeof filter !== "string" || !Object.hasOwn(FILTER_STATES, filter)) throw new TypeError("Unsupported order filter.");
  if (typeof fetchOrders !== "function") throw new TypeError("An order reader is required.");
  const selectedStates = FILTER_STATES[filter];
  const states = [...new Set([...ACTIVE_STATES, ...selectedStates])];
  const results = await Promise.all(states.map(async state => {
    const orders = await fetchOrders({ state, limit: String(PAGE_LIMIT) });
    if (!Array.isArray(orders)) throw new TypeError("Order state page must be an array.");
    return [state, orders];
  }));
  const pages = new Map(results);
  return {
    orders: mergeOrders(selectedStates.map(state => pages.get(state))),
    active_count: mergeOrders(ACTIVE_STATES.map(state => pages.get(state))).length,
    active_truncated: ACTIVE_STATES.some(state => pages.get(state).length === PAGE_LIMIT),
    limited_states: selectedStates.filter(state => pages.get(state).length === PAGE_LIMIT),
    recent_only: filter === "all",
  };
}
