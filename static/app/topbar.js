import {
  getTopbarShortcuts,
  getTopbarShortcutsButton,
  getTopbarShortcutsPanel,
} from "./dom.js";

export const isTopbarShortcutsOpen = () => {
  const button = getTopbarShortcutsButton();
  return Boolean(button && button.getAttribute("aria-expanded") === "true");
};

export const setTopbarShortcutsOpen = (isOpen) => {
  const button = getTopbarShortcutsButton();
  const panel = getTopbarShortcutsPanel();
  if (!button || !panel) {
    return;
  }
  button.setAttribute("aria-expanded", isOpen ? "true" : "false");
  button.setAttribute("aria-label", isOpen ? "Hide menu" : "Show menu");
  panel.hidden = !isOpen;
};

export const bindTopbarShortcuts = () => {
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

export const syncTopbarShortcuts = () => {
  const shortcuts = getTopbarShortcuts();
  if (!shortcuts) {
    return;
  }
  shortcuts.hidden = false;
};

export const bindSubscribeForms = () => {
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

export const bindImportControls = () => {
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
