export const state = {
  activeId: null,
  itemKeyboardNavActive: false,
  pendingReadShortcut: null,
  feedMoreExpanded: false,
  panelFocus: "items",
  pendingPanelFocus: null,
  pendingContentItemID: null,
};

export const itemKeyboardOutlineVisibilityGate =
  "#item-list.is-keyboard-nav:focus-within .item-entry.is-active";

export const itemKeyboardOutlineTransitionTable = Object.freeze([
  Object.freeze({
    id: "keyboard-item-navigation",
    owner: "static/app/keyboard.js",
    trigger: "j/k and ArrowUp/ArrowDown movement in the items panel",
    transition: "itemKeyboardNavActive = true",
  }),
  Object.freeze({
    id: "pointer-item-interaction",
    owner: "static/app/content-panel.js",
    trigger: "row click, link click, and button click in an item row",
    transition: "itemKeyboardNavActive = false",
  }),
  Object.freeze({
    id: "panel-focus-shift",
    owner: "static/app/panel-focus.js + static/styles.css",
    trigger: "focus entering or leaving #item-list",
    transition: "no state change; visibility is gated by :focus-within",
  }),
  Object.freeze({
    id: "htmx-after-swap-hydrate",
    owner: "static/app/htmx.js",
    trigger: "htmx:afterSwap with #item-list present",
    transition: "preserve itemKeyboardNavActive; rehydrate active row",
  }),
  Object.freeze({
    id: "htmx-after-swap-no-item-list",
    owner: "static/app/htmx.js",
    trigger: "htmx:afterSwap without #item-list",
    transition: "itemKeyboardNavActive = false",
  }),
]);

export const feedDragState = {
  row: null,
  list: null,
};

export const feedPanelResizeState = {
  active: false,
  pointerId: null,
  startX: 0,
  startWidth: 0,
  hasStoredWidth: false,
};

export const contentPanelResizeState = {
  active: false,
  pointerId: null,
  startX: 0,
  startWidth: 0,
  hasStoredWidth: false,
};

export const feedPanelStorageKey = "pulse.feedPanelWidth";
export const contentPanelStorageKey = "pulse.contentPanelWidth";

export const contentPanelFloatingClass = "is-content-panel-floating";
export const contentPanelExpandLabel = "Float article panel";
export const contentPanelRestoreLabel = "Restore docked panel";

export const feedPanelMin = 180;
export const feedPanelMax = 460;
export const contentPanelMin = 360;
export const contentPanelMax = 760;

export const expandedPanelScrollStep = 72;
