import {
  state,
  feedDragState,
  feedPanelResizeState,
  feedPanelStorageKey,
  feedPanelMin,
  feedPanelMax,
} from "./state.js";
import {
  getItemList,
  getDisplayedFeedID,
  getFeedList,
  getFeedPanelResizer,
  getFeedEditForm,
  getSelectedFeedInput,
  isDesktopLayout,
  isVisible,
} from "./dom.js";
import {
  setPendingPanelFocus,
  setPanelFocus,
  focusItemList,
} from "./panel-focus.js";

export const isFeedEditMode = () => {
  const feedList = getFeedList();
  if (!feedList) {
    return false;
  }
  return Boolean(feedList.querySelector(".feed-list.edit-mode"));
};

export const focusFeedEditTitleInput = () => {
  const feedList = getFeedList();
  if (!feedList || !isFeedEditMode()) {
    return;
  }
  const active = document.activeElement;
  if (active && feedList.contains(active)) {
    return;
  }
  const input =
    feedList.querySelector(".feed-edit-title.active") ||
    feedList.querySelector(".feed-edit-title");
  if (!input) {
    return;
  }
  input.focus({ preventScroll: true });
  input.select();
};

export const getFeedLinks = (options = {}) => {
  const visibleOnly = Boolean(options.visibleOnly);
  const feedList = getFeedList();
  if (!feedList || isFeedEditMode()) {
    return [];
  }

  const links = Array.from(feedList.querySelectorAll(".feed-link"));
  if (!visibleOnly) {
    return links;
  }
  return links.filter((link) => isVisible(link));
};

const getFeedMoreButton = () => {
  const feedList = getFeedList();
  if (!feedList || isFeedEditMode()) {
    return null;
  }
  return feedList.querySelector(".feed-more-button[data-feed-more-toggle]");
};

const getFeedZeroList = () => {
  const feedList = getFeedList();
  if (!feedList || isFeedEditMode()) {
    return null;
  }
  return feedList.querySelector("#feed-zero-list");
};

const applyFeedMoreExpandedState = (expanded) => {
  const moreButton = getFeedMoreButton();
  const zeroList = getFeedZeroList();
  if (!moreButton || !zeroList) {
    return false;
  }

  const nextExpanded = Boolean(expanded);
  const activeElement = document.activeElement;
  const activeInsideZeroList =
    !nextExpanded && activeElement && zeroList.contains(activeElement);
  if (activeInsideZeroList) {
    moreButton.focus({ preventScroll: true });
  }
  moreButton.setAttribute("aria-expanded", nextExpanded ? "true" : "false");
  zeroList.hidden = !nextExpanded;

  return true;
};

export const syncFeedMoreState = () => applyFeedMoreExpandedState(state.feedMoreExpanded);

const toggleFeedMoreState = () => {
  state.feedMoreExpanded = !state.feedMoreExpanded;
  applyFeedMoreExpandedState(state.feedMoreExpanded);
};

const isZeroUnreadFeedLink = (feedButton) =>
  Boolean(feedButton && feedButton.closest("#feed-zero-list"));

const getVisibleUnreadFeedLinks = () =>
  getFeedLinks({ visibleOnly: true }).filter((link) => !isZeroUnreadFeedLink(link));

const getVisibleZeroUnreadFeedLinks = () =>
  getFeedLinks({ visibleOnly: true }).filter((link) => isZeroUnreadFeedLink(link));

const getSelectedFeedButton = (options = {}) => {
  const links = getFeedLinks({ visibleOnly: options.visibleOnly });
  if (!links.length) {
    return null;
  }

  const active = links.find((link) => link.classList.contains("active"));
  if (active) {
    return active;
  }

  const selectedFeedInput = getSelectedFeedInput();
  const selectedFeedID = selectedFeedInput
    ? selectedFeedInput.value.trim()
    : "";
  if (selectedFeedID) {
    const selected = links.find((link) => link.dataset.feedId === selectedFeedID);
    if (selected) {
      return selected;
    }
  }

  return links[0];
};

const submitFeedEditForm = () => {
  const form = getFeedEditForm();
  if (!form) {
    return false;
  }
  if (typeof form.requestSubmit === "function") {
    form.requestSubmit();
    return true;
  }
  const submit =
    form.querySelector('button[type="submit"]') ||
    form.querySelector('input[type="submit"]');
  if (submit && typeof submit.click === "function") {
    submit.click();
    return true;
  }
  return false;
};

const cancelFeedEditMode = () => {
  const cancelButton = document.querySelector("#feed-list .feed-edit-cancel");
  if (!cancelButton) {
    return false;
  }
  cancelButton.click();
  return true;
};

export const handleFeedEditModeKeydown = (event) => {
  if (!isFeedEditMode()) {
    return false;
  }

  if (event.key === "Escape") {
    event.preventDefault();
    return cancelFeedEditMode();
  }

  if (event.key !== "Enter") {
    return false;
  }
  if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) {
    return false;
  }

  const target = event.target;
  if (!target || !target.closest || !target.closest(".feed-edit-title")) {
    return false;
  }

  event.preventDefault();
  return submitFeedEditForm();
};

export const syncFeedDeleteToggleState = (button, checked) => {
  if (!button) {
    return;
  }
  button.setAttribute("aria-pressed", checked ? "true" : "false");
  const row = button.closest(".feed-row");
  if (row) {
    row.classList.toggle("pending-delete", checked);
  }
};

export const syncFeedDeleteMarks = () => {
  const feedList = getFeedList();
  if (!feedList) {
    return;
  }
  feedList
    .querySelectorAll(".feed-remove-mark[data-feed-delete-toggle]")
    .forEach((button) => {
      const inputID = button.dataset.feedDeleteToggle;
      const input = inputID ? document.getElementById(inputID) : null;
      syncFeedDeleteToggleState(button, Boolean(input && input.checked));
    });
};

export const clearFeedDragState = () => {
  if (feedDragState.row) {
    feedDragState.row.classList.remove("dragging");
  }
  feedDragState.row = null;
  feedDragState.list = null;
};

const rowFromDragHandle = (target) => {
  if (!target || !target.closest) {
    return null;
  }
  const handle = target.closest(".feed-drag-handle");
  if (!handle) {
    return null;
  }
  const row = handle.closest(".feed-row[data-feed-id]");
  const list = row ? row.closest(".feed-list.edit-mode") : null;
  if (!row || !list) {
    return null;
  }
  return row;
};

const rowAfterPointer = (list, clientY) => {
  const rows = Array.from(
    list.querySelectorAll(".feed-row[data-feed-id]:not(.dragging)")
  );
  let closestRow = null;
  let closestOffset = Number.NEGATIVE_INFINITY;
  rows.forEach((row) => {
    const rect = row.getBoundingClientRect();
    const offset = clientY - rect.top - rect.height / 2;
    if (offset < 0 && offset > closestOffset) {
      closestOffset = offset;
      closestRow = row;
    }
  });
  return closestRow;
};

const setSelectedFeed = (feedButton) => {
  const list = getFeedList();
  if (!list || !feedButton || !list.contains(feedButton)) {
    return;
  }
  list.querySelectorAll(".feed-link.active").forEach((node) => {
    node.classList.remove("active");
  });
  feedButton.classList.add("active");

  const selectedFeedInput = getSelectedFeedInput();
  const feedID = feedButton.dataset.feedId;
  if (selectedFeedInput && feedID) {
    selectedFeedInput.value = feedID;
  }
};

const requestFeedItems = (feedButton, pendingPanelFocus) => {
  if (!feedButton || typeof feedButton.click !== "function") {
    return false;
  }
  if (pendingPanelFocus) {
    setPendingPanelFocus(pendingPanelFocus);
  }
  feedButton.click();
  return true;
};

const focusFeedLink = (feedButton, options = {}) => {
  if (!feedButton) {
    return false;
  }

  const shouldRequestItems = Boolean(options.shouldRequestItems);
  const currentSelection =
    getSelectedFeedButton({ visibleOnly: true }) || getSelectedFeedButton();
  const selectionChanged = currentSelection !== feedButton;
  setSelectedFeed(feedButton);
  feedButton.focus({ preventScroll: true });
  feedButton.scrollIntoView({ block: "nearest", behavior: "smooth" });
  setPanelFocus("feed");
  if (shouldRequestItems && selectionChanged) {
    requestFeedItems(feedButton, "feed");
  }
  return true;
};

const focusFeedMoreButton = () => {
  const moreButton = getFeedMoreButton();
  if (!moreButton) {
    return false;
  }
  moreButton.focus({ preventScroll: true });
  moreButton.scrollIntoView({ block: "nearest", behavior: "smooth" });
  setPanelFocus("feed");
  return true;
};

const queueFeedMoreButtonFocus = () => {
  if (typeof window.requestAnimationFrame !== "function") {
    return;
  }
  window.requestAnimationFrame(() => {
    focusFeedMoreButton();
  });
};

export const focusFeedPanel = () => {
  if (!isDesktopLayout() || isFeedEditMode()) {
    return false;
  }

  const visibleSelection = getSelectedFeedButton({ visibleOnly: true });
  if (visibleSelection) {
    focusFeedLink(visibleSelection, { shouldRequestItems: !getItemList() });
    return true;
  }

  if (focusFeedMoreButton()) {
    return true;
  }

  const selectedFeed = getSelectedFeedButton();
  if (!selectedFeed) {
    return false;
  }

  return focusFeedLink(selectedFeed, { shouldRequestItems: !getItemList() });
};

export const moveSelectedFeed = (delta) => {
  if (!isDesktopLayout() || isFeedEditMode()) {
    return false;
  }

  const step = Math.sign(delta);
  if (step === 0) {
    return false;
  }

  const moreButton = getFeedMoreButton();
  const feedButtons = getFeedLinks({ visibleOnly: true });
  const unreadFeedButtons = getVisibleUnreadFeedLinks();
  const zeroFeedButtons = getVisibleZeroUnreadFeedLinks();
  if (moreButton && document.activeElement === moreButton) {
    if (step > 0) {
      if (!state.feedMoreExpanded) {
        state.feedMoreExpanded = true;
        applyFeedMoreExpandedState(true);
        return focusFeedMoreButton();
      }
      if (zeroFeedButtons.length) {
        return focusFeedLink(zeroFeedButtons[0], { shouldRequestItems: true });
      }
      return focusFeedMoreButton();
    }

    if (unreadFeedButtons.length) {
      return focusFeedLink(unreadFeedButtons[unreadFeedButtons.length - 1], {
        shouldRequestItems: true,
      });
    }

    return focusFeedMoreButton();
  }

  if (!feedButtons.length) {
    if (!moreButton) {
      return false;
    }
    if (step > 0 && !state.feedMoreExpanded) {
      state.feedMoreExpanded = true;
      applyFeedMoreExpandedState(true);
    }
    return focusFeedMoreButton();
  }

  const current =
    getSelectedFeedButton({ visibleOnly: true }) || feedButtons[0];

  if (moreButton && step > 0 && unreadFeedButtons.length && !isZeroUnreadFeedLink(current)) {
    const lastUnread = unreadFeedButtons[unreadFeedButtons.length - 1];
    if (current === lastUnread) {
      state.feedMoreExpanded = true;
      applyFeedMoreExpandedState(true);
      return focusFeedMoreButton();
    }
  }

  if (moreButton && step < 0 && zeroFeedButtons.length && isZeroUnreadFeedLink(current)) {
    const firstZero = zeroFeedButtons[0];
    if (current === firstZero) {
      state.feedMoreExpanded = false;
      applyFeedMoreExpandedState(false);
      const focused = focusFeedMoreButton();
      queueFeedMoreButtonFocus();
      return focused;
    }
  }

  let index = feedButtons.indexOf(current);
  if (index < 0) {
    index = 0;
  }
  const nextIndex = Math.min(
    feedButtons.length - 1,
    Math.max(0, index + step)
  );
  const next = feedButtons[nextIndex];
  return focusFeedLink(next, { shouldRequestItems: true });
};

export const openSelectedFeed = () => {
  if (!isDesktopLayout() || isFeedEditMode()) {
    return false;
  }

  const selectedFeed =
    getSelectedFeedButton({ visibleOnly: true }) || getSelectedFeedButton();
  if (!selectedFeed) {
    return false;
  }

  const selectedFeedID = (selectedFeed.dataset.feedId || "").trim();
  setSelectedFeed(selectedFeed);
  if (getItemList() && selectedFeedID && getDisplayedFeedID() === selectedFeedID) {
    focusItemList();
    return true;
  }
  return requestFeedItems(selectedFeed, "items");
};

const clampFeedPanelWidth = (width) =>
  Math.min(feedPanelMax, Math.max(feedPanelMin, Math.round(width)));

const currentFeedPanelWidth = () => {
  const computed = getComputedStyle(document.documentElement);
  const parsed = parseFloat(computed.getPropertyValue("--feed-panel-width"));
  if (Number.isFinite(parsed)) {
    return parsed;
  }

  const panel = document.querySelector(".feed-panel");
  if (panel) {
    return panel.getBoundingClientRect().width;
  }

  return 260;
};

const setFeedPanelWidth = (width, persist) => {
  const clamped = clampFeedPanelWidth(width);
  document.documentElement.style.setProperty("--feed-panel-width", `${clamped}px`);
  if (!persist) {
    return clamped;
  }

  try {
    window.localStorage.setItem(feedPanelStorageKey, String(clamped));
    feedPanelResizeState.hasStoredWidth = true;
  } catch (_error) {
    // Ignore localStorage failures.
  }

  return clamped;
};

const readStoredFeedPanelWidth = () => {
  try {
    const raw = window.localStorage.getItem(feedPanelStorageKey);
    if (!raw) {
      return null;
    }
    const parsed = parseInt(raw, 10);
    if (!Number.isFinite(parsed)) {
      return null;
    }

    return clampFeedPanelWidth(parsed);
  } catch (_error) {
    return null;
  }
};

const measuredFeedPanelWidth = () => {
  const feedList = getFeedList();
  if (!feedList) {
    return null;
  }

  const candidates = feedList.querySelectorAll(".feed-link, .feed-edit-title");
  if (!candidates.length) {
    return null;
  }

  let widest = 220;
  candidates.forEach((element) => {
    widest = Math.max(widest, element.scrollWidth + 54);
  });

  return clampFeedPanelWidth(widest);
};

export const syncFeedPanelWidth = () => {
  if (window.matchMedia("(max-width: 960px)").matches) {
    return;
  }

  const storedWidth = readStoredFeedPanelWidth();
  if (storedWidth !== null) {
    setFeedPanelWidth(storedWidth, false);
    feedPanelResizeState.hasStoredWidth = true;

    return;
  }

  if (feedPanelResizeState.hasStoredWidth) {
    return;
  }

  const measuredWidth = measuredFeedPanelWidth();
  if (measuredWidth !== null) {
    setFeedPanelWidth(measuredWidth, false);
  }
};

const stopFeedPanelResize = (persist) => {
  if (!feedPanelResizeState.active) {
    return;
  }
  feedPanelResizeState.active = false;
  feedPanelResizeState.pointerId = null;
  document.body.classList.remove("is-resizing-feed-panel");
  if (persist) {
    setFeedPanelWidth(currentFeedPanelWidth(), true);
  }
};

export const bindFeedPanelResize = () => {
  const resizer = getFeedPanelResizer();
  if (!resizer || resizer.dataset.bound === "true") {
    return;
  }
  resizer.dataset.bound = "true";

  resizer.addEventListener("pointerdown", (event) => {
    if (event.button !== 0 || window.matchMedia("(max-width: 960px)").matches) {
      return;
    }

    feedPanelResizeState.active = true;
    feedPanelResizeState.pointerId = event.pointerId;
    feedPanelResizeState.startX = event.clientX;
    feedPanelResizeState.startWidth = currentFeedPanelWidth();
    document.body.classList.add("is-resizing-feed-panel");
    if (typeof resizer.setPointerCapture === "function") {
      resizer.setPointerCapture(event.pointerId);
    }
    event.preventDefault();
  });

  resizer.addEventListener("pointermove", (event) => {
    if (!feedPanelResizeState.active || feedPanelResizeState.pointerId !== event.pointerId) {
      return;
    }

    const delta = event.clientX - feedPanelResizeState.startX;
    setFeedPanelWidth(feedPanelResizeState.startWidth + delta, false);
  });

  resizer.addEventListener("pointerup", (event) => {
    if (feedPanelResizeState.pointerId !== event.pointerId) {
      return;
    }
    stopFeedPanelResize(true);
  });

  resizer.addEventListener("pointercancel", (event) => {
    if (feedPanelResizeState.pointerId !== event.pointerId) {
      return;
    }
    stopFeedPanelResize(false);
  });
};

export const bindFeedPanelInteractions = () => {
  if (document.body.dataset.feedPanelInteractionsBound === "true") {
    return;
  }
  document.body.dataset.feedPanelInteractionsBound = "true";

  document.addEventListener("click", (event) => {
    const moreButton = event.target.closest(".feed-more-button[data-feed-more-toggle]");
    if (!moreButton) {
      return;
    }
    const feedList = getFeedList();
    if (!feedList || !feedList.contains(moreButton)) {
      return;
    }
    event.preventDefault();
    toggleFeedMoreState();
    setPanelFocus("feed");
  });

  document.addEventListener("click", (event) => {
    const feedButton = event.target.closest(".feed-link");
    if (!feedButton) {
      return;
    }
    setSelectedFeed(feedButton);
    setPanelFocus("feed");
  });

  document.addEventListener("click", (event) => {
    const deleteToggleButton = event.target.closest(
      ".feed-remove-mark[data-feed-delete-toggle]"
    );
    if (deleteToggleButton) {
      const inputID = deleteToggleButton.dataset.feedDeleteToggle;
      const input = inputID ? document.getElementById(inputID) : null;
      if (input) {
        input.checked = !input.checked;
        syncFeedDeleteToggleState(deleteToggleButton, input.checked);
      }
      return;
    }

    const revertButton = event.target.closest(".feed-title-revert");
    if (!revertButton) {
      return;
    }
    const inputID = revertButton.dataset.feedTitleInput;
    const originalTitle = revertButton.dataset.originalTitle || "";
    if (!inputID) {
      return;
    }
    const input = document.getElementById(inputID);
    if (!input) {
      return;
    }
    input.value = originalTitle;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });

  document.addEventListener("dragstart", (event) => {
    if (!isFeedEditMode()) {
      return;
    }
    const feedList = getFeedList();
    if (!feedList || !feedList.contains(event.target)) {
      return;
    }

    const row = rowFromDragHandle(event.target);
    if (!row) {
      event.preventDefault();
      return;
    }

    clearFeedDragState();
    feedDragState.row = row;
    feedDragState.list = row.parentElement;
    row.classList.add("dragging");

    if (event.dataTransfer) {
      event.dataTransfer.effectAllowed = "move";
      event.dataTransfer.setData("text/plain", row.dataset.feedId || "");
    }
  });

  document.addEventListener("dragover", (event) => {
    if (!isFeedEditMode()) {
      return;
    }
    if (!feedDragState.row || !feedDragState.list) {
      return;
    }
    const targetList =
      event.target && event.target.closest
        ? event.target.closest(".feed-list.edit-mode")
        : null;
    if (!targetList || targetList !== feedDragState.list) {
      return;
    }

    event.preventDefault();
    const nextRow = rowAfterPointer(targetList, event.clientY);
    const dragRow = feedDragState.row;
    if (!dragRow) {
      return;
    }
    if (!nextRow) {
      if (targetList.lastElementChild !== dragRow) {
        targetList.appendChild(dragRow);
      }
      return;
    }
    if (nextRow !== dragRow && nextRow.previousElementSibling !== dragRow) {
      targetList.insertBefore(dragRow, nextRow);
    }
  });

  document.addEventListener("drop", (event) => {
    if (!feedDragState.row || !feedDragState.list) {
      return;
    }
    const targetList =
      event.target && event.target.closest
        ? event.target.closest(".feed-list.edit-mode")
        : null;
    if (targetList && targetList === feedDragState.list) {
      event.preventDefault();
    }
    clearFeedDragState();
  });

  document.addEventListener("dragend", () => {
    clearFeedDragState();
  });
};
