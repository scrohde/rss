(() => {
  "use strict";

  const state = {
    activeId: null,
    pendingReadShortcut: null,
  };

  const getItemList = () => document.getElementById("item-list");
  const getTopbarShortcuts = () => document.getElementById("topbar-shortcuts");

  const syncTopbarShortcuts = () => {
    const shortcuts = getTopbarShortcuts();
    if (!shortcuts) {
      return;
    }
    shortcuts.hidden = !Boolean(getItemList());
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

  const requestToggleRead = (card, view) => {
    const itemID = cardItemID(card);
    if (!itemID || typeof htmx === "undefined" || !htmx.ajax) {
      return false;
    }
    htmx.ajax("POST", `/items/${itemID}/toggle`, {
      target: `#${card.id}`,
      swap: "outerHTML",
      values: { view },
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
      if (requestToggleRead(current, view)) {
        return;
      }
    } else {
      const next = nextCard(current);
      state.pendingReadShortcut = {
        sourceId: current.id,
        nextId: next ? next.id : null,
        expandNext: isExpanded,
      };
      if (requestToggleRead(current, "compact")) {
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
    return Boolean(
      event.target.closest("input, textarea, select, [contenteditable=\"true\"]")
    );
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

  document.addEventListener("keydown", (event) => {
    if (shouldIgnore(event)) {
      return;
    }
    if (!getItemList()) {
      return;
    }
    const main = document.getElementById("main-content");
    if (
      main &&
      event.target &&
      event.target !== document.body &&
      !main.contains(event.target)
    ) {
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
      case "enter":
        prevent();
        openActiveLink();
        break;
      case "r":
        prevent();
        toggleRead();
        break;
      default:
        break;
    }
  });

  document.addEventListener("DOMContentLoaded", () => {
    syncTopbarShortcuts();
    ensureActive();
  });

  document.body.addEventListener("htmx:afterSwap", (event) => {
    syncTopbarShortcuts();
    if (getItemList()) {
      if (
        state.pendingReadShortcut &&
        isPendingReadSwap(event, state.pendingReadShortcut)
      ) {
        applyPendingReadShortcut();
        return;
      }
      ensureActive();
    } else {
      state.activeId = null;
      state.pendingReadShortcut = null;
    }
  });
})();
