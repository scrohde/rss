import { state } from "./state.js";
import {
  getItemList,
  getFeedList,
  getContentPanel,
  isTextEntryTarget,
  isDesktopLayout,
} from "./dom.js";

let isFeedEditModeResolver = () => false;
let isContentPanelOpenResolver = () => false;

export const configurePanelFocus = (options = {}) => {
  if (typeof options.isFeedEditMode === "function") {
    isFeedEditModeResolver = options.isFeedEditMode;
  }
  if (typeof options.isContentPanelOpen === "function") {
    isContentPanelOpenResolver = options.isContentPanelOpen;
  }
};

export const setPendingPanelFocus = (panel, options = {}) => {
  if (panel === "items") {
    state.pendingPanelFocus = "items";
    state.pendingContentItemID = null;
    return;
  }
  if (panel === "content") {
    state.pendingPanelFocus = "content";
    state.pendingContentItemID = options.itemID || null;
    return;
  }
  if (panel === "feed") {
    state.pendingPanelFocus = "feed";
    state.pendingContentItemID = null;
    return;
  }
  state.pendingPanelFocus = null;
  state.pendingContentItemID = null;
};

export const setPanelFocus = (panel) => {
  if (panel) {
    state.panelFocus = panel;
  }
};

const panelFromTarget = (target) => {
  const resolvedTarget = target || document.activeElement;
  if (!resolvedTarget || !resolvedTarget.closest) {
    return null;
  }

  const contentPanel = getContentPanel();
  if (
    contentPanel &&
    contentPanel.classList.contains("is-open") &&
    contentPanel.contains(resolvedTarget)
  ) {
    return "content";
  }

  const feedList = getFeedList();
  if (
    feedList &&
    !isFeedEditModeResolver() &&
    feedList.contains(resolvedTarget) &&
    resolvedTarget.closest(".feed-link")
  ) {
    return "feed";
  }

  const itemList = getItemList();
  if (
    itemList &&
    (resolvedTarget === itemList ||
      itemList.contains(resolvedTarget) ||
      resolvedTarget.closest("#main-content"))
  ) {
    return "items";
  }

  return null;
};

export const resolvePanelFocus = (target) => {
  const detected = panelFromTarget(target);
  if (detected) {
    setPanelFocus(detected);
    return detected;
  }

  let panel = state.panelFocus || "items";
  if (panel === "content" && !isContentPanelOpenResolver()) {
    panel = "items";
  }
  if (!isDesktopLayout() || isFeedEditModeResolver()) {
    panel = "items";
  }
  setPanelFocus(panel);
  return panel;
};

export const syncPanelFocusFromTarget = (target) => {
  const detected = panelFromTarget(target);
  if (detected) {
    setPanelFocus(detected);
  }
};

export const focusItemList = () => {
  const list = getItemList();
  if (!list) {
    return;
  }
  const active = document.activeElement;
  if (active === list || (active && list.contains(active))) {
    setPanelFocus("items");
    return;
  }
  if (isTextEntryTarget(active)) {
    return;
  }
  list.focus({ preventScroll: true });
  setPanelFocus("items");
};
