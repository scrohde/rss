import { state } from "./state.js";
import { getItemList, getCSRFToken } from "./dom.js";
import {
  setPendingPanelFocus,
  resolvePanelFocus,
  focusItemList,
} from "./panel-focus.js";

export const bindHTMXLifecycle = ({ topbar, feed, content }) => {
  const bindCommonInteractions = () => {
    topbar.bindTopbarShortcuts();
    topbar.bindSubscribeForms();
    topbar.bindImportControls();
    content.bindItemRowClickGuards();
    feed.bindFeedPanelResize();
    content.bindContentPanelResize();
    feed.syncFeedPanelWidth();
    content.syncContentPanelWidth();
    content.syncContentPanelMode();
    content.syncActiveItemOutline();
    topbar.syncTopbarShortcuts();
    feed.syncFeedDeleteMarks();
  };

  const focusFeedPanel = () => feed.focusFeedPanel();
  const getFeedLinks = (options = {}) => feed.getFeedLinks(options);
  const isFeedEditMode = () => feed.isFeedEditMode();
  const focusFeedEditTitleInput = () => feed.focusFeedEditTitleInput();
  const clearFeedDragState = () => feed.clearFeedDragState();
  const ensureActive = () => content.ensureActive();
  const applyPendingReadShortcut = () => content.applyPendingReadShortcut();
  const isPendingReadSwap = (event, pending) => content.isPendingReadSwap(event, pending);
  const focusContentPanel = (options = {}) => content.focusContentPanel(options);
  const deferFocusContentPanel = (remainingAttempts = 24) =>
    content.deferFocusContentPanel(remainingAttempts);
  const rowItemID = (row) => content.rowItemID(row);
  const setActive = (row, options = {}) => content.setActive(row, options);

  const focusPanelAfterSwap = () => {
    const pendingPanel = state.pendingPanelFocus;
    if (pendingPanel === "content") {
      if (focusContentPanel({ matchPendingItem: true })) {
        setPendingPanelFocus(null);
      } else {
        deferFocusContentPanel(24);
      }
      return;
    }
    setPendingPanelFocus(null);

    if (pendingPanel === "feed" && focusFeedPanel()) {
      return;
    }
    if (pendingPanel === "items") {
      focusItemList();
      return;
    }

    const panel = resolvePanelFocus();
    if (panel === "feed" && focusFeedPanel()) {
      return;
    }
    if (panel === "content" && focusContentPanel()) {
      return;
    }
    focusItemList();
  };

  document.addEventListener("DOMContentLoaded", () => {
    bindCommonInteractions();
    if (isFeedEditMode()) {
      focusFeedEditTitleInput();
      return;
    }
    if (getItemList()) {
      ensureActive();
      focusItemList();
    } else if (getFeedLinks({ visibleOnly: true }).length) {
      focusFeedPanel();
    }
    setPendingPanelFocus(null);
  });

  document.body.addEventListener("htmx:afterSwap", (event) => {
    clearFeedDragState();
    bindCommonInteractions();
    const swapTarget = event && event.detail ? event.detail.target : null;
    if (swapTarget && swapTarget.id === "feed-list" && isFeedEditMode()) {
      focusFeedEditTitleInput();
      return;
    }
    if (getItemList()) {
      if (
        state.pendingReadShortcut &&
        isPendingReadSwap(event, state.pendingReadShortcut)
      ) {
        applyPendingReadShortcut();
      } else {
        ensureActive();
      }
      focusPanelAfterSwap();
    } else {
      state.activeId = null;
      content.setItemKeyboardNavActive(false);
      state.pendingReadShortcut = null;
      setPendingPanelFocus(null);
      if (getFeedLinks({ visibleOnly: true }).length) {
        focusFeedPanel();
      }
    }
  });

  document.body.addEventListener("htmx:configRequest", (event) => {
    if (!event || !event.detail || !event.detail.parameters) {
      return;
    }
    const source = event.detail.elt;
    const sourceRow = source && source.closest ? source.closest(".item-entry") : null;
    const csrfToken = getCSRFToken();
    if (csrfToken) {
      if (!event.detail.headers) {
        event.detail.headers = {};
      }
      event.detail.headers["X-CSRF-Token"] = csrfToken;
    }
    if (!event.detail.parameters.selected_item_id) {
      if (sourceRow && sourceRow.id) {
        event.detail.parameters.selected_item_id = sourceRow.id;
        state.activeId = sourceRow.id;
      } else if (state.activeId) {
        event.detail.parameters.selected_item_id = state.activeId;
      }
    }

    if (
      sourceRow &&
      sourceRow.dataset.itemExpand === "true" &&
      !sourceRow.classList.contains("is-expanded")
    ) {
      setActive(sourceRow, { scroll: true });
      setPendingPanelFocus("content", { itemID: rowItemID(sourceRow) });
      const list = getItemList();
      if (!list || !list.contains(sourceRow)) {
        return;
      }
      const expandedRow = list.querySelector(".item-entry.is-expanded");
      if (!expandedRow || expandedRow === sourceRow) {
        return;
      }
      const collapseID = expandedRow.dataset.itemId;
      if (collapseID) {
        event.detail.parameters.collapse_item_id = collapseID;
      }
    }
  });

  document.body.addEventListener("htmx:afterSettle", () => {
    if (state.pendingPanelFocus !== "content") {
      return;
    }
    if (focusContentPanel({ matchPendingItem: true })) {
      setPendingPanelFocus(null);
      return;
    }
    deferFocusContentPanel(24);
  });
};
