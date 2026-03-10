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

const setThemeStatus = (status, message, state) => {
  if (!status) {
    return;
  }

  status.textContent = message;
  if (state) {
    status.dataset.state = state;
    return;
  }

  delete status.dataset.state;
};

const checkedThemeValue = (radios) => {
  const checked = radios.find((radio) => radio.checked);
  if (!checked) {
    return "";
  }
  return checked.value.trim();
};

const applyTheme = (theme) => {
  document.documentElement.dataset.theme = theme || "system";
};

const syncThemeSelection = (radios, theme) => {
  radios.forEach((radio) => {
    radio.checked = radio.value === theme;
  });
};

export const bindThemeForms = () => {
  document
    .querySelectorAll("form[data-theme-form='true']")
    .forEach((form) => {
      if (form.dataset.bound === "true") {
        return;
      }
      form.dataset.bound = "true";

      const status = form.querySelector("[data-theme-status='true']");
      const radios = Array.from(form.querySelectorAll("input[name='theme']"));
      if (radios.length === 0) {
        return;
      }

      let savedTheme = checkedThemeValue(radios) || "system";
      let activeRequestID = 0;
      let activeController = null;

      const revertTheme = () => {
        syncThemeSelection(radios, savedTheme);
        applyTheme(savedTheme);
      };

      radios.forEach((radio) => {
        radio.addEventListener("change", async () => {
          if (!radio.checked) {
            return;
          }

          const nextTheme = radio.value.trim();
          if (nextTheme === "") {
            revertTheme();
            return;
          }

          applyTheme(nextTheme);
          setThemeStatus(status, "Saving...", "pending");

          if (activeController) {
            activeController.abort();
          }

          activeController = new AbortController();
          activeRequestID += 1;
          const requestID = activeRequestID;

          try {
            const response = await fetch(form.action, {
              method: (form.method || "post").toUpperCase(),
              headers: {
                "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
                "X-Requested-With": "fetch",
              },
              body: new URLSearchParams(new FormData(form)),
              credentials: "same-origin",
              redirect: "follow",
              signal: activeController.signal,
            });

            if (requestID !== activeRequestID) {
              return;
            }

            if (response.redirected) {
              const destination = new URL(response.url, window.location.href);
              if (destination.pathname === "/auth/login") {
                window.location.assign(destination.toString());
                return;
              }
            }

            if (!response.ok) {
              throw new Error(`theme update failed with status ${response.status}`);
            }

            savedTheme = nextTheme;
            setThemeStatus(status, "Saved", "saved");
          } catch (error) {
            if (requestID !== activeRequestID) {
              return;
            }
            if (error && error.name === "AbortError") {
              return;
            }

            revertTheme();
            setThemeStatus(status, "Couldn't save", "error");
          } finally {
            if (requestID === activeRequestID) {
              activeController = null;
            }
          }
        });
      });
    });
};
