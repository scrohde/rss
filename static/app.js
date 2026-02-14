(() => {
  "use strict";

  const state = {
    activeId: null,
    pendingReadShortcut: null,
  };

  const getItemList = () => document.getElementById("item-list");
  const getFeedList = () => document.getElementById("feed-list");
  const getFeedEditForm = () => document.getElementById("feed-edit-form");
  const getSelectedFeedInput = () => document.getElementById("selected-feed-id");
  const getTopbarShortcuts = () => document.getElementById("topbar-shortcuts");
  const getTopbarShortcutsButton = () =>
    document.getElementById("topbar-shortcuts-button");
  const getTopbarShortcutsPanel = () =>
    document.getElementById("topbar-shortcuts-panel");

  const isTopbarShortcutsOpen = () => {
    const button = getTopbarShortcutsButton();
    return Boolean(button && button.getAttribute("aria-expanded") === "true");
  };

  const setTopbarShortcutsOpen = (isOpen) => {
    const button = getTopbarShortcutsButton();
    const panel = getTopbarShortcutsPanel();
    if (!button || !panel) {
      return;
    }
    button.setAttribute("aria-expanded", isOpen ? "true" : "false");
    button.setAttribute(
      "aria-label",
      isOpen ? "Hide menu" : "Show menu"
    );
    panel.hidden = !isOpen;
  };

  const bindTopbarShortcuts = () => {
    const shortcuts = getTopbarShortcuts();
    const button = getTopbarShortcutsButton();
    const panel = getTopbarShortcutsPanel();
    if (!shortcuts || !button || !panel || button.dataset.bound === "true") {
      return;
    }
    button.dataset.bound = "true";

    button.addEventListener("click", (event) => {
      event.preventDefault();
      setTopbarShortcutsOpen(!isTopbarShortcutsOpen());
    });

    document.addEventListener("click", (event) => {
      if (!isTopbarShortcutsOpen()) {
        return;
      }
      if (shortcuts.contains(event.target)) {
        return;
      }
      setTopbarShortcutsOpen(false);
    });
  };

  const syncTopbarShortcuts = () => {
    const shortcuts = getTopbarShortcuts();
    if (!shortcuts) {
      return;
    }
    shortcuts.hidden = false;
  };

  const isFeedEditMode = () => {
    const feedList = getFeedList();
    if (!feedList) {
      return false;
    }
    return Boolean(feedList.querySelector(".feed-list.edit-mode"));
  };

  const focusFeedEditTitleInput = () => {
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

  const getItemCards = () => {
    const list = getItemList();
    if (!list) {
      return [];
    }
    return Array.from(list.querySelectorAll(".item-card"));
  };

  const setActive = (card, options = {}) => {
    const list = getItemList();
    if (!list || !card) {
      return;
    }
    list.querySelectorAll(".item-card.is-active").forEach((node) => {
      node.classList.remove("is-active");
    });
    card.classList.add("is-active");
    if (card.id) {
      state.activeId = card.id;
    }
    if (options.scroll) {
      card.scrollIntoView({ block: "nearest", behavior: "smooth" });
    }
  };

  const ensureActive = () => {
    const list = getItemList();
    if (!list) {
      state.activeId = null;
      return null;
    }
    let target = null;
    if (state.activeId) {
      target = document.getElementById(state.activeId);
    }
    if (!target || !list.contains(target)) {
      target = list.querySelector(".item-card");
    }
    if (target) {
      setActive(target);
    }
    return target;
  };

  const isTextEntryTarget = (target) => {
    if (!target || !target.closest) {
      return false;
    }
    return Boolean(
      target.closest("input, textarea, select, [contenteditable=\"true\"]")
    );
  };

  const focusItemList = () => {
    const list = getItemList();
    if (!list) {
      return;
    }
    const active = document.activeElement;
    if (active === list || (active && list.contains(active))) {
      return;
    }
    if (isTextEntryTarget(active)) {
      return;
    }
    list.focus({ preventScroll: true });
  };

  const moveActive = (delta) => {
    const cards = getItemCards();
    if (!cards.length) {
      return;
    }
    const current = ensureActive();
    let index = current ? cards.indexOf(current) : 0;
    if (index < 0) {
      index = 0;
    }
    const nextIndex = Math.min(cards.length - 1, Math.max(0, index + delta));
    setActive(cards[nextIndex], { scroll: true });
  };

  const toggleExpanded = (expand) => {
    const current = ensureActive();
    if (!current) {
      return;
    }
    const isExpanded = current.classList.contains("expanded");
    if (expand && !isExpanded) {
      current.click();
      return;
    }
    if (!expand && isExpanded) {
      const toggle = current.querySelector(".item-row.clickable");
      if (toggle) {
        toggle.click();
      }
    }
  };

  const cardItemID = (card) => {
    if (!card || !card.id) {
      return null;
    }
    const match = card.id.match(/^item-(\d+)$/);
    if (!match) {
      return null;
    }
    return match[1];
  };

  const nextCard = (card) => {
    const cards = getItemCards();
    const index = cards.indexOf(card);
    if (index < 0 || index >= cards.length - 1) {
      return null;
    }
    return cards[index + 1];
  };

  const requestToggleRead = (card, view, selectedItemId) => {
    const itemID = cardItemID(card);
    if (!itemID || typeof htmx === "undefined" || !htmx.ajax) {
      return false;
    }
    const selected = selectedItemId || state.activeId;
    htmx.ajax("POST", `/items/${itemID}/toggle`, {
      target: `#${card.id}`,
      swap: "outerHTML",
      values: { view, selected_item_id: selected },
    });
    return true;
  };

  const applyPendingReadShortcut = () => {
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
    if (pending.expandNext && !next.classList.contains("expanded")) {
      next.click();
    }
    return true;
  };

  const isPendingReadSwap = (event, pending) => {
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

  const openActiveLink = () => {
    const current = ensureActive();
    if (!current) {
      return;
    }
    const link = current.querySelector("a.item-title");
    if (link && link.href) {
      window.open(link.href, "_blank", "noopener");
    }
  };

  const toggleRead = () => {
    const current = ensureActive();
    if (!current) {
      return;
    }

    const isRead = current.classList.contains("is-read");
    const isExpanded = current.classList.contains("expanded");

    if (isRead) {
      state.pendingReadShortcut = null;
      const view = isExpanded ? "expanded" : "compact";
      if (requestToggleRead(current, view, current.id)) {
        return;
      }
    } else {
      const next = nextCard(current);
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

  const shouldIgnore = (event) => {
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

  const handleFeedEditModeKeydown = (event) => {
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

  const syncFeedDeleteToggleState = (button, checked) => {
    if (!button) {
      return;
    }
    button.setAttribute("aria-pressed", checked ? "true" : "false");
    const row = button.closest(".feed-row");
    if (row) {
      row.classList.toggle("pending-delete", checked);
    }
  };

  const syncFeedDeleteMarks = () => {
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

  document.addEventListener("click", (event) => {
    const list = getItemList();
    if (!list) {
      return;
    }
    const card = event.target.closest(".item-card");
    if (card && list.contains(card)) {
      setActive(card);
    }
  });

  document.addEventListener("click", (event) => {
    const feedButton = event.target.closest(".feed-link");
    if (!feedButton) {
      return;
    }
    setSelectedFeed(feedButton);
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

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && isTopbarShortcutsOpen()) {
      setTopbarShortcutsOpen(false);
      return;
    }
    if (handleFeedEditModeKeydown(event)) {
      return;
    }
    if (shouldIgnore(event)) {
      return;
    }
    if (!getItemList()) {
      return;
    }

    const key = event.key.toLowerCase();
    const prevent = () => {
      event.preventDefault();
    };

    switch (key) {
      case "j":
      case "arrowdown":
        prevent();
        moveActive(1);
        break;
      case "k":
      case "arrowup":
        prevent();
        moveActive(-1);
        break;
      case "l":
      case "arrowright":
        prevent();
        toggleExpanded(true);
        break;
      case "h":
      case "arrowleft":
        prevent();
        toggleExpanded(false);
        break;
      case "o":
        prevent();
        openActiveLink();
        break;
      case "enter": {
        const main = document.getElementById("main-content");
        if (
          main &&
          event.target &&
          event.target !== document.body &&
          !main.contains(event.target)
        ) {
          break;
        }
        prevent();
        openActiveLink();
        break;
      }
      case "r":
        prevent();
        toggleRead();
        break;
      default:
        break;
    }
  });

  document.addEventListener("DOMContentLoaded", () => {
    bindTopbarShortcuts();
    syncTopbarShortcuts();
    syncFeedDeleteMarks();
    if (isFeedEditMode()) {
      focusFeedEditTitleInput();
      return;
    }
    ensureActive();
    focusItemList();
  });

  document.body.addEventListener("htmx:afterSwap", (event) => {
    bindTopbarShortcuts();
    syncTopbarShortcuts();
    syncFeedDeleteMarks();
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
      focusItemList();
    } else {
      state.activeId = null;
      state.pendingReadShortcut = null;
    }
  });

  document.body.addEventListener("htmx:configRequest", (event) => {
    if (!event || !event.detail || !event.detail.parameters) {
      return;
    }
    if (!event.detail.parameters.selected_item_id) {
      const source = event.detail.elt;
      const sourceCard =
        source && source.closest ? source.closest(".item-card") : null;
      if (sourceCard && sourceCard.id) {
        event.detail.parameters.selected_item_id = sourceCard.id;
        state.activeId = sourceCard.id;
        return;
      }
      if (state.activeId) {
        event.detail.parameters.selected_item_id = state.activeId;
      }
    }
  });
})();
