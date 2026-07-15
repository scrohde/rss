export const getApp = () => document.querySelector(".app");

export const getItemList = () => document.getElementById("item-list");

export const getDisplayedFeedID = () => {
  const list = getItemList();
  if (!list) {
    return "";
  }
  return (list.dataset.feedId || "").trim();
};

export const getFeedList = () => document.getElementById("feed-list");

export const getFeedPanelResizer = () =>
  document.getElementById("feed-panel-resizer");

export const getContentPanelResizer = () =>
  document.getElementById("content-panel-resizer");

export const getContentPanel = () => document.getElementById("content-panel");

export const getFeedEditForm = () => document.getElementById("feed-edit-form");

export const getSelectedFeedInput = () =>
  document.getElementById("selected-feed-id");

export const getTopbarShortcuts = () =>
  document.getElementById("topbar-shortcuts");

export const getTopbarShortcutsButton = () =>
  document.getElementById("topbar-shortcuts-button");

export const getTopbarShortcutsPanel = () =>
  document.getElementById("topbar-shortcuts-panel");

export const getCSRFToken = () => {
  const meta = document.querySelector('meta[name="csrf-token"]');
  if (!meta) {
    return "";
  }
  return (meta.getAttribute("content") || "").trim();
};

export const isTextEntryTarget = (target) => {
  if (!target || !target.closest) {
    return false;
  }
  return Boolean(target.closest('input, textarea, select, [contenteditable="true"]'));
};

export const mobileLayoutQuery = "(max-width: 960px)";

export const isDesktopLayout = () =>
  !window.matchMedia(mobileLayoutQuery).matches;

export const isVisible = (element) =>
  Boolean(element && element.getClientRects().length > 0);
