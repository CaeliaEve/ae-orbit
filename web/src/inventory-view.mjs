import { createInventoryPages, INVENTORY_CACHE_ITEMS, INVENTORY_BLOCK_ITEMS } from "./inventory-pages.mjs";
import { createInventoryWheel, inventoryKeyboardDirection } from "./inventory-paging.mjs";
import { quantityKnown } from "./plain-inventory.mjs";

const LIVE_REFRESH = 5000;
const PREFETCH_DEPTH = 24;

export function inventoryPageLimit($, browser, previousLimit) {
  const fallback = 50;
  if (browser.matchMedia("(max-width: 900px)").matches) return fallback;
  const wrap = $("#view-inventory .table-wrap");
  if (!wrap || wrap.clientWidth < 100 || wrap.clientHeight < 100) return previousLimit || fallback;
  const empty = $("#inventory-empty");
  const wasEmptyVisible = empty && !empty.hidden;
  if (wasEmptyVisible) empty.hidden = true;
  const width = wrap.clientWidth, height = wrap.clientHeight;
  if (wasEmptyVisible) empty.hidden = false;
  const grid = browser.getComputedStyle($("#inventory-rows"));
  const table = browser.getComputedStyle($("#view-inventory .inventory-table"));
  const gap = parseFloat(grid.gap) || 8;
  const cardWidth = parseFloat(grid.getPropertyValue("--inventory-card-min")) || 76;
  const cardHeight = parseFloat(grid.getPropertyValue("--inventory-card-height")) || 84;
  const paddingX = parseFloat(table.paddingLeft) + parseFloat(table.paddingRight);
  const paddingY = parseFloat(table.paddingTop) + parseFloat(table.paddingBottom);
  const columns = Math.max(1, Math.floor((width - paddingX + gap) / (cardWidth + gap)));
  const rows = Math.max(1, Math.floor((height - paddingY + gap) / (cardHeight + gap)));
  wrap.style.setProperty("--inventory-rows", String(rows));
  return Math.min(INVENTORY_BLOCK_ITEMS, columns * rows);
}

// The app owns shared inventory rows and selection. This view owns page
// positions, requests and timers, which all stop when the view becomes inactive.
export function createInventoryView({ state, request, lookupItems, $, render, patchRows,
  updateSelection, updateDraft, hideHover, textureAssetURL, message, errorText,
  browser = window, now = Date.now, measure = limit => inventoryPageLimit($, browser, limit) }) {
  const pages = createInventoryPages({ request, now });
  const wheel = createInventoryWheel();
  const images = new Map();
  const position = { page: 0, count: 1, total: null, anchor: "", anchored: false, limit: 50 };
  let hasPage = false, queryPending = false, searchValue, direction = 1, desired = null, target;
  let pageRequest, liveRequest, visibleRequest, prefetch;
  let navigationFrame = 0, searchTimer, resizeTimer, liveTimer, visibleTimer, imageTimer;
  let checkedAt = -Infinity;

  const active = () => Boolean(state.network && state.view === "inventory" && browser.document.visibilityState === "visible");
  const scroller = () => $("#view-inventory .table-wrap");
  const anchor = () => position.page > 0 || position.anchored ? position.anchor : "";
  const specFor = (limit = position.limit) => ({ search: state.search, sort: state.sort,
    filter: state.filter, limit, view: state.stockView, scope: state.inventoryScope, kind: state.resourceKind });
  const currentWindow = (limit = position.limit) => ({ page: position.page, anchor: anchor(), limit });

  function renderPager() {
    const loading = Boolean(pageRequest);
    $("#first-page").disabled = queryPending || (position.page === 0 && !position.anchored);
    $("#prev-page").disabled = $("#next-page").disabled = queryPending || position.count <= 1;
    $("#refresh-inventory").disabled = loading;
    $("#inventory-rows").setAttribute("aria-busy", String(loading && !target?.absolute));
    const targetPage = desired ?? target?.page;
    const label = `${position.page + 1} / ${position.count}`;
    $("#page-label").textContent = loading
      ? `${label} 页 · 正在读取${targetPage != null && targetPage !== position.page ? `第 ${targetPage + 1} 页` : ""}…`
      : queryPending ? "读取失败 · 点击刷新重试"
      : `${label} 页 · ${state.items.length} 项`;
  }

  function focusedItem() {
    const { item, quickAdd } = browser.document.activeElement?.dataset || {};
    const id = quickAdd || item;
    return id ? `[${quickAdd ? "data-quick-add" : "data-item"}="${browser.CSS.escape(id)}"]` : "";
  }

  function syncItems(items) {
    state.items = items;
    for (const item of items) if (!item.group) state.itemCache.set(String(item.id), item);
    const selected = state.selected?.group ? items.find(item => item.id === state.selected.id) : state.itemCache.get(String(state.selected?.id));
    if (selected) state.selected = selected;
    updateDraft();
  }

  function updatePosition(page) {
    if (page.total != null && position.total != null && page.total !== position.total) pages.invalidatePositions(page.total, page.count);
    position.count = page.count || position.count;
    position.total = page.total ?? position.total;
  }

  function abortRefresh() {
    liveRequest?.abort();
    visibleRequest?.abort();
    liveRequest = visibleRequest = null;
  }

  function stopPrefetch() {
    prefetch?.controller.abort();
    prefetch = null;
    browser.clearTimeout(imageTimer);
  }

  function scheduleImages() {
    browser.clearTimeout(imageTimer);
    if (!active()) return;
    imageTimer = browser.setTimeout(() => {
      if (!active()) return;
      const spec = specFor();
      for (const distance of [1, -1, 2, 3]) {
        const page = (position.page + direction * distance + position.count) % position.count;
        for (const item of pages.get({ ...spec, page: page + 1 })?.items || []) {
          const url = textureAssetURL(item);
          if (!url || images.has(url)) continue;
          const image = new browser.Image();
          image.decoding = "async";
          image.src = url;
          images.set(url, image);
          if (images.size > 320) images.delete(images.keys().next().value);
        }
      }
    }, 120);
  }

  async function prefetchNearby() {
    if (!active() || position.count <= 1 || prefetch) return;
    const controller = new AbortController();
    prefetch = { controller };
    const { signal } = controller;
    try {
      while (!signal.aborted && active() && !pageRequest && !queryPending) {
        const spec = specFor(), { count, total, page } = position;
        const depth = Math.min(count - 1, PREFETCH_DEPTH, Math.floor(INVENTORY_CACHE_ITEMS / spec.limit / 2));
        const candidates = [];
        for (let n = 1; n <= depth; n++) {
          candidates.push({ index: (page + direction * n + count) % count, step: direction });
          if (n <= 4) candidates.push({ index: (page - direction * n + count) % count, step: -direction });
        }
        const missing = candidates.find(({ index }) => !pages.get({ ...spec, page: index + 1 }, true));
        if (!missing) break;
        const batch = Math.max(1, Math.min(Math.floor(INVENTORY_BLOCK_ITEMS / spec.limit), depth, missing.step < 0 ? missing.index + 1 : count - missing.index));
        const start = missing.step < 0 ? missing.index - batch + 1 : missing.index;
        const nextTotal = await pages.readBlock(start, batch, spec, signal, total);
        if (nextTotal != null && total != null && nextTotal !== total) break;
      }
    } catch {} finally {
      if (prefetch?.controller === controller) {
        prefetch = null;
        scheduleImages();
      }
    }
  }

  function applyPage(destination, page) {
    const nextPage = page.number ? page.number - 1 : destination.page;
    const navigated = nextPage !== position.page;
    const hadItemFocus = $("#inventory-rows").contains(browser.document.activeElement);
    const focus = focusedItem();
    const { scrollX, scrollY } = browser;
    const selected = state.selected;
    if (navigated) hideHover();
    updatePosition(page);
    position.page = nextPage;
    position.anchor = page.anchor;
    position.anchored = Boolean(destination.anchor);
    state.inventoryScope = page.scope;
    syncItems(page.items);
    const spec = specFor();
    pages.put({ ...spec, anchor: anchor() }, page);
    if (!destination.anchor) pages.put({ ...spec, page: position.page + 1 }, page);
    hasPage = true;
    message("");
    $("#stock-view-label").hidden = state.inventoryScope !== "nesql_plain";
    render();
    if (navigated) {
      const wrap = scroller();
      wrap.scrollTop = 0;
      if (browser.matchMedia("(max-width: 900px)").matches) wrap.scrollIntoView({ block: "start" });
      if (hadItemFocus) $("[data-item]", wrap)?.focus({ preventScroll: true });
    } else {
      if (browser.scrollY !== scrollY) browser.scrollTo(scrollX, scrollY);
      if (focus) $(focus)?.focus({ preventScroll: true });
    }
    updateSelection(selected, { navigation: true });
    void prefetchNearby();
    scheduleImages();
    browser.clearTimeout(visibleTimer);
    visibleTimer = browser.setTimeout(() => {
      if (active() && !pageRequest) void refreshWindow();
    }, 180);
  }

  async function load(reset, destination) {
    if (reset) {
      desired = null;
      destination = { page: 0, anchor: "", limit: measure(position.limit) };
      queryPending = true;
      stopPrefetch();
      pages.clear();
      wheel.reset();
      browser.cancelAnimationFrame(navigationFrame);
      navigationFrame = 0;
    }
    if (!active()) return;
    abortRefresh();
    pageRequest?.abort();
    const controller = pageRequest = new AbortController();
    const { limit } = destination;
    const spec = { ...specFor(limit), anchor: destination.anchor, page: destination.absolute ? destination.page + 1 : 0 };
    target = destination;
    renderPager();
    if (!state.items.length) render();
    try {
      const page = await pages.read(spec, controller.signal);
      if (pageRequest !== controller || !active() || (destination.absolute && desired !== destination.page)) return;
      queryPending = false;
      if (limit !== position.limit) { stopPrefetch(); pages.clear(); }
      position.limit = limit;
      desired = null;
      pageRequest = target = null;
      applyPage(destination, page);
    } catch (error) {
      if (pageRequest !== controller || error.name === "AbortError" || (destination.absolute && desired !== destination.page)) return;
      wheel.reset();
      desired = null;
      message(errorText(error));
    } finally {
      if (pageRequest === controller) {
        pageRequest = target = null;
        renderPager();
        if (!state.items.length) render();
      }
      if (!pageRequest && !navigationFrame && active() && hasPage && !queryPending) {
        const nextLimit = measure(position.limit);
        if (nextLimit !== limit && nextLimit !== position.limit) void load(false, currentWindow(nextLimit));
      }
    }
  }

  function navigate(page) {
    const limit = measure(position.limit);
    const destination = { page, anchor: "", absolute: true, limit };
    const cached = pages.get({ ...specFor(limit), page: page + 1 });
    if (!cached) { void load(false, destination); return; }
    pageRequest?.abort();
    pageRequest = target = null;
    abortRefresh();
    position.limit = limit;
    desired = null;
    applyPage(destination, cached);
  }

  function changePage(step) {
    if (!active() || !hasPage || queryPending || (step && position.count <= 1)) return false;
    const current = desired ?? target?.page ?? position.page;
    desired = step ? (current + step + position.count) % position.count : 0;
    if (step) direction = step;
    if (pages.get({ ...specFor(), page: desired + 1 })) {
      browser.cancelAnimationFrame(navigationFrame);
      navigationFrame = 0;
      navigate(desired);
    } else if (!navigationFrame) {
      navigationFrame = browser.requestAnimationFrame(() => {
        navigationFrame = 0;
        if (desired != null) navigate(desired);
      });
    }
    return true;
  }

  function refreshCapacity() {
    const limit = measure(position.limit);
    if (limit === position.limit || pageRequest || navigationFrame || !active()) return false;
    void load(queryPending || !hasPage, currentWindow(limit));
    return true;
  }

  function patchLive(items, previous) {
    const focus = focusedItem();
    const wrap = scroller();
    const scrollTop = wrap.scrollTop;
    const { scrollX, scrollY } = browser;
    state.items = items;
    if (!patchRows()) render();
    wrap.scrollTop = scrollTop;
    if (browser.scrollY !== scrollY) browser.scrollTo(scrollX, scrollY);
    renderPager();
    if (focus) $(focus)?.focus({ preventScroll: true });
    updateSelection(previous);
  }

  async function patchVisible() {
    if (!state.items.length || pageRequest || liveRequest || visibleRequest || queryPending) return;
    const controller = visibleRequest = new AbortController();
    try {
      const value = await lookupItems(state.items.map(item => item.id).concat(state.selected?.id || []), controller.signal);
      if (!value || visibleRequest !== controller || !active()) return;
      const previous = state.selected;
      const live = new Map(value.items.map(item => [String(item.id), item]));
      let items = state.items.map(item => live.get(String(item.id)) || item);
      if (state.inventoryScope === "nesql_plain" && state.stockView === "stock") {
        items = items.filter(item => quantityKnown(item) && BigInt(item.quantity) > 0n);
      }
      state.selected = live.get(String(state.selected?.id)) || state.selected;
      patchLive(items, previous);
    } catch {} finally {
      if (visibleRequest === controller) visibleRequest = null;
    }
  }

  async function refreshWindow(force = false) {
    if (!active() || pageRequest || queryPending || liveRequest || !hasPage) return;
    abortRefresh();
    if (force) { stopPrefetch(); pages.clear(); }
    const controller = liveRequest = new AbortController();
    const current = () => liveRequest === controller && active();
    const spec = { ...specFor(), anchor: anchor() };
    checkedAt = now();
    try {
      const page = await pages.read(spec, controller.signal, true);
      if (!current()) return;
      const selectedID = state.selected?.id;
      if (selectedID && !page.items.some(item => String(item.id) === String(selectedID))) {
        try { await lookupItems([selectedID], controller.signal); } catch {}
        if (!current()) return;
      }
      const previous = state.selected;
      updatePosition(page);
      // Follow the visible window's anchor. Only clamp if pages were removed.
      position.page = Math.min(position.page, position.count - 1);
      position.anchor = page.anchor || position.anchor;
      pages.put({ ...specFor(), anchor: anchor() }, page);
      syncItems(page.items);
      patchLive(page.items, previous);
      message("");
      if (!spec.anchor) pages.put({ ...specFor(), page: position.page + 1 }, page);
      void prefetchNearby();
    } catch (error) {
      if (current() && error.name !== "AbortError") message(errorText(error));
    } finally {
      if (liveRequest === controller) liveRequest = null;
    }
  }

  function observe() {
    if (!active() || !hasPage || queryPending || navigationFrame) return;
    if (!pageRequest && !liveRequest && now() - checkedAt >= LIVE_REFRESH) {
      browser.clearTimeout(liveTimer);
      liveTimer = null;
      void refreshWindow();
    } else {
      void patchVisible();
      if (liveTimer == null) liveTimer = browser.setTimeout(() => {
        liveTimer = null;
        observe();
      }, Math.max(500, LIVE_REFRESH - (now() - checkedAt)));
    }
  }

  function hide() {
    pageRequest?.abort();
    pageRequest = target = desired = null;
    abortRefresh();
    stopPrefetch();
    browser.cancelAnimationFrame(navigationFrame);
    navigationFrame = 0;
    for (const timer of [searchTimer, resizeTimer, liveTimer, visibleTimer]) browser.clearTimeout(timer);
    liveTimer = null;
    wheel.reset();
    if (searchValue !== undefined && searchValue !== state.search) {
      state.search = searchValue;
      queryPending = true;
    }
    searchValue = undefined;
    renderPager();
  }

  function reset() {
    searchValue = undefined;
    hide();
    pages.clear();
    images.clear();
    Object.assign(position, { page: 0, count: 1, total: null, anchor: "", anchored: false });
    hasPage = queryPending = false;
    checkedAt = -Infinity;
    direction = 1;
    $("#search").value = state.search;
  }

  function show() {
    if (!active() || pageRequest) return;
    if (!hasPage || queryPending) { void load(true); return; }
    if (!refreshCapacity()) observe();
  }

  function resize() {
    browser.clearTimeout(resizeTimer);
    if (active()) resizeTimer = browser.setTimeout(refreshCapacity, 120);
  }

  function bind() {
    const search = event => {
      browser.clearTimeout(searchTimer);
      if (event.isComposing) { searchValue = undefined; return; }
      searchValue = event.target.value.trim();
      searchTimer = browser.setTimeout(() => {
        state.search = searchValue;
        searchValue = undefined;
        void load(true);
      }, 220);
    };
    $("#search").addEventListener("input", search);
    $("#search").addEventListener("compositionend", search);
    $("#sort").addEventListener("change", event => { state.sort = event.target.value; void load(true); });
    $("#stock-view").addEventListener("change", event => { state.stockView = event.target.value; void load(true); });
    for (const [button, step] of [["#first-page", 0], ["#prev-page", -1], ["#next-page", 1]]) {
      $(button).addEventListener("click", () => { wheel.reset(); changePage(step); });
    }
    $("#view-inventory .catalog").addEventListener("wheel", event => {
      if (!active() || browser.matchMedia("(max-width: 900px)").matches) return;
      if (event.ctrlKey || event.target.closest("input, select, textarea")) return;
      const raw = Math.abs(event.deltaX) > Math.abs(event.deltaY) ? event.deltaX : event.deltaY;
      if (!raw) return;
      event.preventDefault();
      const multiplier = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? browser.innerHeight : 1;
      const step = wheel.consume(raw * multiplier, browser.performance.now());
      if (step) changePage(step);
    }, { passive: false });
    browser.document.addEventListener("keydown", event => {
      if (!active() || $("dialog[open]")) return;
      const step = inventoryKeyboardDirection(event);
      if (step === null) return;
      event.preventDefault();
      wheel.reset();
      changePage(step);
    });
  }

  return { bind, show, hide, reset, resize, observe, reload: () => load(true),
    refresh: () => queryPending || !hasPage ? load(true) : refreshWindow(true),
    renderPager, get loading() { return Boolean(pageRequest); } };
}
