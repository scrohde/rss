import {
  state,
  contentPanelResizeState,
  contentPanelStorageKey,
  contentPanelFloatingClass,
  contentPanelExpandLabel,
  contentPanelRestoreLabel,
  contentPanelMin,
  contentPanelMax,
} from "./state.js";
import {
  getApp,
  getItemList,
  getContentPanel,
  getContentPanelResizer,
  isTextEntryTarget,
} from "./dom.js";
import {
  setPendingPanelFocus,
  setPanelFocus,
  focusItemList,
} from "./panel-focus.js";

export const getItemRows = () => {
  const list = getItemList();
  if (!list) {
    return [];
  }
  return Array.from(list.querySelectorAll(".item-entry"));
};

const getExpandedRow = () => {
  const list = getItemList();
  if (!list) {
    return null;
  }
  return list.querySelector(".item-entry.is-expanded");
};

export const syncActiveItemOutline = () => {
  const list = getItemList();
  if (!list) {
    return;
  }
  list.classList.toggle("is-keyboard-nav", Boolean(state.itemKeyboardNavActive));
};

export const setItemKeyboardNavActive = (active) => {
  state.itemKeyboardNavActive = Boolean(active);
  syncActiveItemOutline();
};

export const setActive = (row, options = {}) => {
  const list = getItemList();
  if (!list || !row) {
    return;
  }
  list.querySelectorAll(".item-entry.is-active").forEach((node) => {
    node.classList.remove("is-active");
  });
  row.classList.add("is-active");
  if (row.id) {
    state.activeId = row.id;
  }
  if (options.scroll) {
    row.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }
};

export const ensureActive = () => {
  const list = getItemList();
  if (!list) {
    state.activeId = null;
    syncActiveItemOutline();
    return null;
  }
  let target = null;
  if (state.activeId) {
    target = document.getElementById(state.activeId);
  }
  if (!target || !list.contains(target)) {
    target = list.querySelector(".item-entry");
  }
  if (target) {
    setActive(target);
  }
  syncActiveItemOutline();
  return target;
};

export const bindItemRowClickGuards = () => {
  document.querySelectorAll(".item-entry a, .item-entry button").forEach((element) => {
    if (element.dataset.cardClickGuardBound === "true") {
      return;
    }
    element.dataset.cardClickGuardBound = "true";
    element.addEventListener("click", (event) => {
      const row = event.currentTarget.closest(".item-entry");
      if (row) {
        setItemKeyboardNavActive(false);
        setActive(row);
      }
      event.stopPropagation();
    });
  });
};

export const moveActive = (delta) => {
  const rows = getItemRows();
  if (!rows.length) {
    return;
  }
  const current = ensureActive();
  let index = current ? rows.indexOf(current) : 0;
  if (index < 0) {
    index = 0;
  }
  const nextIndex = Math.min(rows.length - 1, Math.max(0, index + delta));
  setActive(rows[nextIndex], { scroll: true });
};

const itemHasContent = (row) => Boolean(row && row.dataset.hasContent === "true");

export const scrollExpandedPanel = (delta) => {
  const panel = getContentPanel();
  if (panel && panel.classList.contains("is-open")) {
    panel.scrollBy({ top: delta });
    return true;
  }
  return false;
};

export const rowItemID = (row) => {
  if (!row || !row.id) {
    return null;
  }
  const match = row.id.match(/^item-(\d+)$/);
  if (!match) {
    return null;
  }
  return match[1];
};

export const toggleExpanded = (expand, options = {}) => {
  const focusPanel = options.focusPanel || "content";
  const current = ensureActive();
  if (!current) {
    return false;
  }
  const isExpanded = current.classList.contains("is-expanded");
  if (expand && !isExpanded) {
    if (!itemHasContent(current)) {
      return false;
    }
    setActive(current, { scroll: true });
    setPendingPanelFocus(focusPanel, { itemID: rowItemID(current) });
    current.click();
    return true;
  }
  if (!expand && isExpanded) {
    const toggle = current.querySelector("[data-item-compact='true']");
    if (toggle) {
      toggle.click();
      return true;
    }
  }
  return false;
};

const contentPanelMatchesPendingItem = (panel) => {
  if (!panel || !state.pendingContentItemID) {
    return true;
  }
  const article = panel.querySelector(".content-panel-article[data-item-id]");
  if (!article) {
    return false;
  }
  const panelItemID = (article.getAttribute("data-item-id") || "").trim();
  return panelItemID === state.pendingContentItemID;
};

export const focusContentPanel = (options = {}) => {
  const requirePendingItemMatch = Boolean(options.matchPendingItem);
  const panel = getContentPanel();
  if (!panel || !panel.classList.contains("is-open")) {
    return false;
  }
  if (requirePendingItemMatch && !contentPanelMatchesPendingItem(panel)) {
    return false;
  }
  panel.focus({ preventScroll: true });
  setPanelFocus("content");
  return true;
};

export const collapseContentPanelToItems = () => {
  const expandedRow = getExpandedRow();
  if (!expandedRow) {
    focusItemList();
    return false;
  }

  setActive(expandedRow);
  const compactToggle = expandedRow.querySelector("[data-item-compact='true']");
  if (!compactToggle) {
    focusItemList();
    return false;
  }

  setPendingPanelFocus("items");
  compactToggle.click();
  return true;
};

export const expandActiveToContentPanel = () => {
  if (isContentPanelOpen()) {
    return focusContentPanel();
  }

  const current = ensureActive();
  if (!current) {
    return false;
  }
  if (!itemHasContent(current)) {
    return false;
  }

  return toggleExpanded(true, { focusPanel: "content" });
};

const nextRow = (row) => {
  const rows = getItemRows();
  const index = rows.indexOf(row);
  if (index < 0 || index >= rows.length - 1) {
    return null;
  }
  return rows[index + 1];
};

const nextUnreadRow = (row, options = {}) => {
  const requireContent = Boolean(options.requireContent);
  const rows = getItemRows();
  const index = rows.indexOf(row);
  if (index < 0 || index >= rows.length - 1) {
    return null;
  }

  for (let candidateIndex = index + 1; candidateIndex < rows.length; candidateIndex += 1) {
    const candidate = rows[candidateIndex];
    if (candidate.classList.contains("is-read")) {
      continue;
    }
    if (requireContent && !itemHasContent(candidate)) {
      continue;
    }
    return candidate;
  }

  return null;
};

const getReadingModalRow = () => {
  if (!isContentPanelOpen()) {
    return null;
  }

  const panel = getContentPanel();
  if (panel) {
    const article = panel.querySelector(".content-panel-article[data-item-id]");
    if (article) {
      const itemID = (article.getAttribute("data-item-id") || "").trim();
      if (itemID) {
        const row = document.getElementById(`item-${itemID}`);
        if (row && row.classList.contains("is-expanded")) {
          return row;
        }
      }
    }
  }

  return getExpandedRow();
};

const requestExpandRow = (row, options = {}) => {
  const itemID = rowItemID(row);
  if (!itemID || !itemHasContent(row) || typeof htmx === "undefined" || !htmx.ajax) {
    return false;
  }
  const values = { selected_item_id: row.id || state.activeId };
  const collapseRow = options.collapseRow;
  const collapseID = rowItemID(collapseRow);
  if (collapseID && collapseID !== itemID) {
    values.collapse_item_id = collapseID;
  }
  htmx.ajax("GET", `/items/${itemID}`, {
    source: row,
    target: `#${row.id}`,
    swap: "outerHTML",
    values,
  });
  return true;
};

const openRowInReadingModal = (row, options = {}) => {
  const focusPanel = options.focusPanel || "content";
  if (!row || !itemHasContent(row)) {
    return false;
  }
  setActive(row, { scroll: true });
  setPendingPanelFocus(focusPanel, { itemID: rowItemID(row) });
  if (requestExpandRow(row, { collapseRow: getExpandedRow() })) {
    return true;
  }
  row.click();
  return true;
};

const requestToggleRead = (row, view, selectedItemId) => {
  const itemID = rowItemID(row);
  if (!itemID || typeof htmx === "undefined" || !htmx.ajax) {
    return false;
  }
  const selected = selectedItemId || state.activeId;
  htmx.ajax("POST", `/items/${itemID}/toggle`, {
    source: row,
    target: `#${row.id}`,
    swap: "outerHTML",
    values: { view, selected_item_id: selected },
  });
  return true;
};

export const applyPendingReadShortcut = () => {
  const pending = state.pendingReadShortcut;
  if (!pending) {
    return false;
  }
  state.pendingReadShortcut = null;

  if (!pending.nextId) {
    ensureActive();
    return true;
  }

  const list = getItemList();
  const next = document.getElementById(pending.nextId);
  if (!list || !next || !list.contains(next)) {
    ensureActive();
    return true;
  }

  setActive(next, { scroll: true });
  if (
    pending.expandNext &&
    itemHasContent(next) &&
    !next.classList.contains("is-expanded")
  ) {
    if (pending.keepFloating) {
      setContentPanelFloating(true);
    }
    return openRowInReadingModal(next, { focusPanel: "content" });
  }
  return true;
};

export const isPendingReadSwap = (event, pending) => {
  if (!event || !event.detail || !pending) {
    return false;
  }
  const detail = event.detail;
  const target = detail.target;
  if (target && target.id === pending.sourceId) {
    return true;
  }
  const elt = detail.elt;
  return Boolean(elt && elt.id === pending.sourceId);
};

export const openActiveLink = () => {
  const current = ensureActive();
  if (!current) {
    return;
  }
  const link = current.querySelector("a.item-title");
  if (link && link.href) {
    window.open(link.href, "_blank", "noopener");
  }
};

export const markContentPanelArticleReadAndAdvance = () => {
  const current = getReadingModalRow();
  if (!current) {
    return false;
  }

  const nextUnread = nextUnreadRow(current, { requireContent: true });
  if (current.classList.contains("is-read")) {
    if (nextUnread) {
      return openRowInReadingModal(nextUnread, { focusPanel: "content" });
    }
    closeContentPanel();
    return true;
  }

  const selectedAfterToggle = nextUnread ? nextUnread.id : current.id;
  state.pendingReadShortcut = {
    sourceId: current.id,
    nextId: nextUnread ? nextUnread.id : null,
    expandNext: Boolean(nextUnread),
    keepFloating: Boolean(nextUnread && isContentPanelFloating()),
  };
  if (requestToggleRead(current, "compact", selectedAfterToggle)) {
    return true;
  }
  state.pendingReadShortcut = null;
  return false;
};

export const toggleRead = () => {
  if (markContentPanelArticleReadAndAdvance()) {
    return;
  }

  const current = ensureActive();
  if (!current) {
    return;
  }

  const isRead = current.classList.contains("is-read");
  const isExpanded = current.classList.contains("is-expanded");

  if (isRead) {
    state.pendingReadShortcut = null;
    const view = isExpanded ? "expanded" : "compact";
    if (requestToggleRead(current, view, current.id)) {
      return;
    }
  } else {
    const next = nextRow(current);
    const selectedAfterToggle = next ? next.id : current.id;
    state.pendingReadShortcut = {
      sourceId: current.id,
      nextId: next ? next.id : null,
      expandNext: isExpanded,
    };
    if (requestToggleRead(current, "compact", selectedAfterToggle)) {
      return;
    }
  }

  const button = current.querySelector('button[hx-post*="/toggle"]');
  if (button) {
    button.click();
  }
};

export const shouldIgnore = (event) => {
  if (event.defaultPrevented) {
    return true;
  }
  if (event.metaKey || event.ctrlKey || event.altKey) {
    return true;
  }
  if (!event.target) {
    return false;
  }
  return isTextEntryTarget(event.target);
};

export const deferFocusContentPanel = (remainingAttempts = 24) => {
  if (state.pendingPanelFocus !== "content") {
    return;
  }
  if (focusContentPanel({ matchPendingItem: true })) {
    setPendingPanelFocus(null);
    return;
  }
  if (remainingAttempts <= 0) {
    return;
  }

  const retry = () => deferFocusContentPanel(remainingAttempts - 1);
  if (typeof window.requestAnimationFrame === "function") {
    window.requestAnimationFrame(retry);
    return;
  }
  window.setTimeout(retry, 16);
};

export const isContentPanelOpen = () => {
  const panel = getContentPanel();
  return Boolean(panel && panel.classList.contains("is-open"));
};

export const isContentPanelFloating = () => {
  const app = getApp();
  return Boolean(app && app.classList.contains(contentPanelFloatingClass));
};

const syncContentPanelToggleButtons = (isFloating) => {
  document
    .querySelectorAll("button[data-content-panel-full-toggle='true']")
    .forEach((button) => {
      const label = isFloating ? contentPanelRestoreLabel : contentPanelExpandLabel;
      button.setAttribute("aria-pressed", isFloating ? "true" : "false");
      button.setAttribute("aria-label", label);
      button.title = label;
    });
};

export const setContentPanelFloating = (isFloating) => {
  const app = getApp();
  if (!app) {
    return;
  }
  app.classList.toggle(contentPanelFloatingClass, isFloating);
  syncContentPanelToggleButtons(isFloating);
};

export const closeContentPanel = () => {
  const panel = getContentPanel();
  if (!panel || !panel.classList.contains("is-open")) {
    return false;
  }
  const closeButton = panel.querySelector("button[data-content-panel-close='true']");
  if (!closeButton || typeof closeButton.click !== "function") {
    return false;
  }
  closeButton.click();
  return true;
};

export const syncContentPanelMode = () => {
  if (!isContentPanelOpen()) {
    // Preserve floating mode across intermediate panel-close swaps while a
    // content-panel reopen request is still pending.
    if (state.pendingPanelFocus === "content" && isContentPanelFloating()) {
      syncContentPanelToggleButtons(true);
      return;
    }
    setContentPanelFloating(false);
    return;
  }
  syncContentPanelToggleButtons(isContentPanelFloating());
};

const clampContentPanelWidth = (width) =>
  Math.min(contentPanelMax, Math.max(contentPanelMin, Math.round(width)));

const currentContentPanelWidth = () => {
  const computed = getComputedStyle(document.documentElement);
  const parsed = parseFloat(computed.getPropertyValue("--content-panel-width"));
  if (Number.isFinite(parsed)) {
    return parsed;
  }

  const panel = getContentPanel();
  if (panel) {
    return panel.getBoundingClientRect().width;
  }

  return 520;
};

const setContentPanelWidth = (width, persist) => {
  const clamped = clampContentPanelWidth(width);
  document.documentElement.style.setProperty("--content-panel-width", `${clamped}px`);
  if (!persist) {
    return clamped;
  }

  try {
    window.localStorage.setItem(contentPanelStorageKey, String(clamped));
    contentPanelResizeState.hasStoredWidth = true;
  } catch (_error) {
    // Ignore localStorage failures.
  }

  return clamped;
};

const readStoredContentPanelWidth = () => {
  try {
    const raw = window.localStorage.getItem(contentPanelStorageKey);
    if (!raw) {
      return null;
    }
    const parsed = parseInt(raw, 10);
    if (!Number.isFinite(parsed)) {
      return null;
    }

    return clampContentPanelWidth(parsed);
  } catch (_error) {
    return null;
  }
};

export const syncContentPanelWidth = () => {
  if (window.matchMedia("(max-width: 960px)").matches) {
    return;
  }

  const storedWidth = readStoredContentPanelWidth();
  if (storedWidth !== null) {
    setContentPanelWidth(storedWidth, false);
    contentPanelResizeState.hasStoredWidth = true;
  }
};

const stopContentPanelResize = (persist) => {
  if (!contentPanelResizeState.active) {
    return;
  }
  contentPanelResizeState.active = false;
  contentPanelResizeState.pointerId = null;
  document.body.classList.remove("is-resizing-content-panel");
  if (persist) {
    setContentPanelWidth(currentContentPanelWidth(), true);
  }
};

export const bindContentPanelResize = () => {
  const resizer = getContentPanelResizer();
  if (!resizer || resizer.dataset.bound === "true") {
    return;
  }
  resizer.dataset.bound = "true";

  resizer.addEventListener("pointerdown", (event) => {
    if (
      event.button !== 0 ||
      window.matchMedia("(max-width: 960px)").matches ||
      !isContentPanelOpen() ||
      isContentPanelFloating()
    ) {
      return;
    }

    contentPanelResizeState.active = true;
    contentPanelResizeState.pointerId = event.pointerId;
    contentPanelResizeState.startX = event.clientX;
    contentPanelResizeState.startWidth = currentContentPanelWidth();
    document.body.classList.add("is-resizing-content-panel");
    if (typeof resizer.setPointerCapture === "function") {
      resizer.setPointerCapture(event.pointerId);
    }
    event.preventDefault();
  });

  resizer.addEventListener("pointermove", (event) => {
    if (
      !contentPanelResizeState.active ||
      contentPanelResizeState.pointerId !== event.pointerId
    ) {
      return;
    }

    const delta = event.clientX - contentPanelResizeState.startX;
    setContentPanelWidth(contentPanelResizeState.startWidth - delta, false);
  });

  resizer.addEventListener("pointerup", (event) => {
    if (contentPanelResizeState.pointerId !== event.pointerId) {
      return;
    }
    stopContentPanelResize(true);
  });

  resizer.addEventListener("pointercancel", (event) => {
    if (contentPanelResizeState.pointerId !== event.pointerId) {
      return;
    }
    stopContentPanelResize(false);
  });
};

export const bindContentPanelInteractions = () => {
  if (document.body.dataset.contentPanelInteractionsBound === "true") {
    return;
  }
  document.body.dataset.contentPanelInteractionsBound = "true";

  document.addEventListener(
    "click",
    (event) => {
      if (!isContentPanelOpen() || !isContentPanelFloating()) {
        return;
      }
      const panel = getContentPanel();
      const target = event.target;
      if (!panel || !(target instanceof Node) || panel.contains(target)) {
        return;
      }
      event.preventDefault();
      event.stopPropagation();
      closeContentPanel();
    },
    true
  );

  document.addEventListener("click", (event) => {
    const list = getItemList();
    if (!list) {
      return;
    }
    const row = event.target.closest(".item-entry");
    if (row && list.contains(row)) {
      if (event.detail > 0) {
        setItemKeyboardNavActive(false);
      }
      setActive(row);
      setPanelFocus("items");
    }
  });

  document.addEventListener("click", (event) => {
    const target = event.target;
    if (!target || !target.closest) {
      return;
    }

    const fullToggle = target.closest("button[data-content-panel-full-toggle='true']");
    if (fullToggle) {
      event.preventDefault();
      setContentPanelFloating(!isContentPanelFloating());
      return;
    }

    const markReadButton = target.closest("button[data-content-panel-mark-read='true']");
    if (markReadButton) {
      event.preventDefault();
      markContentPanelArticleReadAndAdvance();
      return;
    }

    const closeButton = target.closest("button[data-content-panel-close='true']");
    if (closeButton) {
      setContentPanelFloating(false);
    }
  });
};
