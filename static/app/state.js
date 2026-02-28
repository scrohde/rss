export const state = {
  activeId: null,
  pendingReadShortcut: null,
  panelFocus: "items",
  pendingPanelFocus: null,
  pendingContentItemID: null,
};

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
