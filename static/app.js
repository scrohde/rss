(() => {
  "use strict";

  const state = {
    activeId: null,
    pendingReadShortcut: null,
    panelFocus: "items",
    pendingPanelFocus: null,
  };
  const feedDragState = {
    row: null,
    list: null,
  };
  const feedPanelResizeState = {
    active: false,
    pointerId: null,
    startX: 0,
    startWidth: 0,
    hasStoredWidth: false,
  };
  const contentPanelResizeState = {
    active: false,
    pointerId: null,
    startX: 0,
    startWidth: 0,
    hasStoredWidth: false,
  };
  const feedPanelStorageKey = "pulse.feedPanelWidth";
  const contentPanelStorageKey = "pulse.contentPanelWidth";
  const contentPanelFloatingClass = "is-content-panel-floating";
  const contentPanelExpandLabel = "Float article panel";
  const contentPanelRestoreLabel = "Restore docked panel";
  const feedPanelMin = 180;
  const feedPanelMax = 460;
  const contentPanelMin = 360;
  const contentPanelMax = 760;
  const expandedPanelScrollStep = 72;

  const getApp = () => document.querySelector(".app");
  const getItemList = () => document.getElementById("item-list");
  const getDisplayedFeedID = () => {
    const list = getItemList();
    if (!list) {
      return "";
    }
    return (list.dataset.feedId || "").trim();
  };
  const getFeedList = () => document.getElementById("feed-list");
  const getFeedPanelResizer = () => document.getElementById("feed-panel-resizer");
  const getContentPanelResizer = () => document.getElementById("content-panel-resizer");
  const getContentPanel = () => document.getElementById("content-panel");
  const getFeedEditForm = () => document.getElementById("feed-edit-form");
  const getSelectedFeedInput = () => document.getElementById("selected-feed-id");
  const getTopbarShortcuts = () => document.getElementById("topbar-shortcuts");
  const getTopbarShortcutsButton = () =>
    document.getElementById("topbar-shortcuts-button");
  const getTopbarShortcutsPanel = () =>
    document.getElementById("topbar-shortcuts-panel");
  const getCSRFToken = () => {
    const meta = document.querySelector('meta[name="csrf-token"]');
    if (!meta) {
      return "";
    }
    return (meta.getAttribute("content") || "").trim();
  };

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

  const bindSubscribeForms = () => {
    document
      .querySelectorAll("form[data-subscribe-form='true']")
      .forEach((form) => {
        if (form.dataset.bound === "true") {
          return;
        }
        form.dataset.bound = "true";
        form.addEventListener("htmx:afterRequest", (event) => {
          if (!event || !event.detail || !event.detail.successful) {
            return;
          }
          form.reset();
        });
      });
  };

  const bindImportControls = () => {
    document
      .querySelectorAll("button[data-import-button='true']")
      .forEach((button) => {
        if (button.dataset.bound === "true") {
          return;
        }
        button.dataset.bound = "true";
        button.addEventListener("click", () => {
          const form = button.closest("form");
          if (!form) {
            return;
          }
          const input = form.querySelector("input[data-import-file-input='true']");
          if (input) {
            input.click();
          }
        });
      });

    document
      .querySelectorAll("input[data-import-file-input='true']")
      .forEach((input) => {
        if (input.dataset.bound === "true") {
          return;
        }
        input.dataset.bound = "true";
        input.addEventListener("change", () => {
          if (!input.files || input.files.length === 0) {
            return;
          }
          const form = input.closest("form");
          if (!form) {
            return;
          }
          if (typeof form.requestSubmit === "function") {
            form.requestSubmit();
            return;
          }
          form.submit();
        });
      });
  };

  const bindItemRowClickGuards = () => {
    document.querySelectorAll(".item-entry a, .item-entry button").forEach((element) => {
      if (element.dataset.cardClickGuardBound === "true") {
        return;
      }
      element.dataset.cardClickGuardBound = "true";
      element.addEventListener("click", (event) => {
        const row = event.currentTarget.closest(".item-entry");
        if (row) {
          setActive(row);
        }
        event.stopPropagation();
      });
    });
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

  const getItemRows = () => {
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

  const setActive = (row, options = {}) => {
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
      target = list.querySelector(".item-entry");
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

  const isDesktopLayout = () =>
    !window.matchMedia("(max-width: 960px)").matches;

  const isVisible = (element) =>
    Boolean(element && element.getClientRects().length > 0);

  const getFeedLinks = (options = {}) => {
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
      !isFeedEditMode() &&
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

  const setPanelFocus = (panel) => {
    if (panel) {
      state.panelFocus = panel;
    }
  };

  const resolvePanelFocus = (target) => {
    const detected = panelFromTarget(target);
    if (detected) {
      setPanelFocus(detected);
      return detected;
    }

    let panel = state.panelFocus || "items";
    if (panel === "content" && !isContentPanelOpen()) {
      panel = "items";
    }
    if (!isDesktopLayout() || isFeedEditMode()) {
      panel = "items";
    }
    setPanelFocus(panel);
    return panel;
  };

  const syncPanelFocusFromTarget = (target) => {
    const detected = panelFromTarget(target);
    if (detected) {
      setPanelFocus(detected);
    }
  };

  const focusItemList = () => {
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

  const moveActive = (delta) => {
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

  const scrollExpandedPanel = (delta) => {
    const panel = getContentPanel();
    if (panel && panel.classList.contains("is-open")) {
      panel.scrollBy({ top: delta });
      return true;
    }
    return false;
  };

  const toggleExpanded = (expand) => {
    const current = ensureActive();
    if (!current) {
      return false;
    }
    const isExpanded = current.classList.contains("is-expanded");
    if (expand && !isExpanded) {
      if (!itemHasContent(current)) {
        return false;
      }
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

  const focusContentPanel = () => {
    const panel = getContentPanel();
    if (!panel || !panel.classList.contains("is-open")) {
      return false;
    }
    panel.focus({ preventScroll: true });
    setPanelFocus("content");
    return true;
  };

  const collapseContentPanelToItems = () => {
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

    state.pendingPanelFocus = "items";
    compactToggle.click();
    return true;
  };

  const expandActiveToContentPanel = () => {
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

    state.pendingPanelFocus = "content";
    const expanded = toggleExpanded(true);
    if (!expanded) {
      state.pendingPanelFocus = null;
    }
    return expanded;
  };

  const rowItemID = (row) => {
    if (!row || !row.id) {
      return null;
    }
    const match = row.id.match(/^item-(\d+)$/);
    if (!match) {
      return null;
    }
    return match[1];
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

  const openRowInReadingModal = (row) => {
    if (!row || !itemHasContent(row)) {
      return false;
    }
    setActive(row, { scroll: true });
    state.pendingPanelFocus = "content";
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
      target: `#${row.id}`,
      swap: "outerHTML",
      values: { view, selected_item_id: selected },
    });
    return true;
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
      target: `#${row.id}`,
      swap: "outerHTML",
      values,
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
    if (
      pending.expandNext &&
      itemHasContent(next) &&
      !next.classList.contains("is-expanded")
    ) {
      if (pending.keepFloating) {
        setContentPanelFloating(true);
      }
      return openRowInReadingModal(next);
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

  const handleReadingModalReadShortcut = () => {
    const current = getReadingModalRow();
    if (!current) {
      return false;
    }

    const nextUnread = nextUnreadRow(current, { requireContent: true });
    if (current.classList.contains("is-read")) {
      if (nextUnread) {
        return openRowInReadingModal(nextUnread);
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

  const toggleRead = () => {
    if (handleReadingModalReadShortcut()) {
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

  const clearFeedDragState = () => {
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
      state.pendingPanelFocus = pendingPanelFocus;
    }
    feedButton.click();
    return true;
  };

  const focusFeedPanel = () => {
    if (!isDesktopLayout() || isFeedEditMode()) {
      return false;
    }

    const selectedFeed =
      getSelectedFeedButton({ visibleOnly: true }) || getSelectedFeedButton();
    if (!selectedFeed) {
      return false;
    }

    setSelectedFeed(selectedFeed);
    selectedFeed.focus({ preventScroll: true });
    selectedFeed.scrollIntoView({ block: "nearest", behavior: "smooth" });
    setPanelFocus("feed");
    if (!getItemList()) {
      requestFeedItems(selectedFeed, "feed");
    }
    return true;
  };

  const moveSelectedFeed = (delta) => {
    if (!isDesktopLayout() || isFeedEditMode()) {
      return false;
    }

    const feedButtons = getFeedLinks({ visibleOnly: true });
    if (!feedButtons.length) {
      return false;
    }

    const current =
      getSelectedFeedButton({ visibleOnly: true }) || feedButtons[0];
    let index = feedButtons.indexOf(current);
    if (index < 0) {
      index = 0;
    }
    const nextIndex = Math.min(
      feedButtons.length - 1,
      Math.max(0, index + delta)
    );
    const next = feedButtons[nextIndex];
    const selectionChanged = next !== current;
    setSelectedFeed(next);
    next.focus({ preventScroll: true });
    next.scrollIntoView({ block: "nearest", behavior: "smooth" });
    setPanelFocus("feed");
    if (selectionChanged) {
      requestFeedItems(next, "feed");
    }
    return true;
  };

  const openSelectedFeed = () => {
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

  const moveWithinFocusedPanel = (delta, panel) => {
    if (panel === "feed") {
      return moveSelectedFeed(delta);
    }
    if (panel === "content") {
      return scrollExpandedPanel(delta * expandedPanelScrollStep);
    }
    moveActive(delta);
    return true;
  };

  const focusPanelAfterSwap = () => {
    const pendingPanel = state.pendingPanelFocus;
    if (pendingPanel === "content") {
      if (focusContentPanel()) {
        state.pendingPanelFocus = null;
      } else {
        deferFocusContentPanel();
      }
      return;
    }
    state.pendingPanelFocus = null;

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

  const deferFocusContentPanel = (remainingAttempts = 3) => {
    if (state.pendingPanelFocus !== "content") {
      return;
    }
    if (focusContentPanel()) {
      state.pendingPanelFocus = null;
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

  const syncFeedPanelWidth = () => {
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

  const bindFeedPanelResize = () => {
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

  const isContentPanelOpen = () => {
    const panel = getContentPanel();
    return Boolean(panel && panel.classList.contains("is-open"));
  };

  const isContentPanelFloating = () => {
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

  const setContentPanelFloating = (isFloating) => {
    const app = getApp();
    if (!app) {
      return;
    }
    app.classList.toggle(contentPanelFloatingClass, isFloating);
    syncContentPanelToggleButtons(isFloating);
  };

  const closeContentPanel = () => {
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

  const syncContentPanelMode = () => {
    if (!isContentPanelOpen()) {
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

  const syncContentPanelWidth = () => {
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

  const bindContentPanelResize = () => {
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

  document.addEventListener("focusin", (event) => {
    syncPanelFocusFromTarget(event.target);
  });

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
    syncPanelFocusFromTarget(event.target);
  });

  document.addEventListener("click", (event) => {
    const list = getItemList();
    if (!list) {
      return;
    }
    const row = event.target.closest(".item-entry");
    if (row && list.contains(row)) {
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

    const closeButton = target.closest("button[data-content-panel-close='true']");
    if (closeButton) {
      setContentPanelFloating(false);
    }
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

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && isTopbarShortcutsOpen()) {
      setTopbarShortcutsOpen(false);
      return;
    }
    if (event.key === "Escape" && isContentPanelOpen() && isContentPanelFloating()) {
      event.preventDefault();
      closeContentPanel();
      return;
    }
    if (handleFeedEditModeKeydown(event)) {
      return;
    }
    if (shouldIgnore(event)) {
      return;
    }
    if (!getItemList() && !getFeedLinks({ visibleOnly: true }).length) {
      return;
    }

    const key = event.key.toLowerCase();
    const desktopPanelNavigationEnabled = isDesktopLayout() && !isFeedEditMode();
    const panel = desktopPanelNavigationEnabled
      ? resolvePanelFocus(event.target || document.activeElement)
      : "items";
    const prevent = () => {
      event.preventDefault();
    };

    switch (key) {
      case "j":
      case "arrowdown":
        prevent();
        if (desktopPanelNavigationEnabled) {
          moveWithinFocusedPanel(1, panel);
          break;
        }
        if (key === "arrowdown" && scrollExpandedPanel(expandedPanelScrollStep)) {
          break;
        }
        moveActive(1);
        break;
      case "k":
      case "arrowup":
        prevent();
        if (desktopPanelNavigationEnabled) {
          moveWithinFocusedPanel(-1, panel);
          break;
        }
        if (key === "arrowup" && scrollExpandedPanel(-expandedPanelScrollStep)) {
          break;
        }
        moveActive(-1);
        break;
      case "l":
      case "arrowright":
        prevent();
        if (desktopPanelNavigationEnabled) {
          if (panel === "feed") {
            openSelectedFeed();
            break;
          }
          if (panel === "items") {
            expandActiveToContentPanel();
            break;
          }
          focusContentPanel();
          break;
        }
        toggleExpanded(true);
        break;
      case "h":
      case "arrowleft":
        prevent();
        if (desktopPanelNavigationEnabled) {
          if (panel === "content") {
            collapseContentPanelToItems();
            break;
          }
          focusFeedPanel();
          break;
        }
        toggleExpanded(false);
        break;
      case "o":
        prevent();
        openActiveLink();
        break;
      case "enter": {
        if (
          desktopPanelNavigationEnabled &&
          panel === "content" &&
          isContentPanelOpen() &&
          !isContentPanelFloating()
        ) {
          prevent();
          setContentPanelFloating(true);
          break;
        }
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
    bindSubscribeForms();
    bindImportControls();
    bindItemRowClickGuards();
    bindFeedPanelResize();
    bindContentPanelResize();
    syncFeedPanelWidth();
    syncContentPanelWidth();
    syncContentPanelMode();
    syncTopbarShortcuts();
    syncFeedDeleteMarks();
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
    state.pendingPanelFocus = null;
  });

  document.body.addEventListener("htmx:afterSwap", (event) => {
    clearFeedDragState();
    bindTopbarShortcuts();
    bindSubscribeForms();
    bindImportControls();
    bindItemRowClickGuards();
    bindFeedPanelResize();
    bindContentPanelResize();
    syncFeedPanelWidth();
    syncContentPanelWidth();
    syncContentPanelMode();
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
      focusPanelAfterSwap();
    } else {
      state.activeId = null;
      state.pendingReadShortcut = null;
      state.pendingPanelFocus = null;
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
    const sourceRow =
      source && source.closest ? source.closest(".item-entry") : null;
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
})();
