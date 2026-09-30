import { bindCatchUpSpinner, syncCatchUpSpinner } from "./catch-up-spinner.js";

const previewRequests = new WeakMap();
const previewTimers = new WeakMap();
const dialogOpeners = new WeakMap();

const getFormElements = (form) => ({
  days: form.querySelector("[data-catch-up-days]"),
  date: form.querySelector("[data-catch-up-date]"),
  cutoff: form.querySelector("[data-catch-up-cutoff]"),
  cutoffLabel: form.querySelector("[data-catch-up-cutoff-label]"),
  preview: form.querySelector("[data-catch-up-preview]"),
  error: form.querySelector("[data-catch-up-error]"),
  submit: form.querySelector("[data-catch-up-submit]"),
});

const formatLocalDate = (date) => {
  const year = String(date.getFullYear()).padStart(4, "0");
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
};

const localMidnightISO = (dateValue) => {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(dateValue || "");
  if (!match) {
    return "";
  }

  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const localMidnight = new Date(year, month - 1, day, 0, 0, 0, 0);
  if (
    localMidnight.getFullYear() !== year ||
    localMidnight.getMonth() !== month - 1 ||
    localMidnight.getDate() !== day
  ) {
    return "";
  }

  return localMidnight.toISOString();
};

const addCalendarDays = (days) => {
  const date = new Date();
  date.setHours(12, 0, 0, 0);
  date.setDate(date.getDate() - days);
  return formatLocalDate(date);
};

const showFormError = (form, message) => {
  const { error } = getFormElements(form);
  if (!error) {
    return;
  }

  error.textContent = message;
  error.hidden = !message;
};

const showStatus = (message) => {
  const status = document.querySelector("[data-catch-up-notice]");
  if (!status) {
    return;
  }

  status.textContent = message;
  status.hidden = !message;
};

const cutoffResultMessage = (count, cutoff) => {
  if (count === 0) {
    return "No unread items matched this cutoff.";
  }

  const itemLabel = count === 1 ? "item" : "items";
  const dateLabel = cutoff ? new Date(cutoff).toLocaleDateString() : "the selected date";
  return `Marked ${count} unread ${itemLabel} published before ${dateLabel} as read. ` +
    "Undo is available until you leave this feed.";
};

const requestPreview = async (form, cutoff) => {
  const { preview, submit } = getFormElements(form);
  const prior = previewRequests.get(form);
  if (prior) {
    prior.controller.abort();
  }

  const controller = new AbortController();
  const request = { controller, cutoff };
  previewRequests.set(form, request);
  form.dataset.previewCutoff = "";
  showFormError(form, "");

  if (submit) {
    submit.disabled = true;
  }
  if (preview) {
    preview.textContent = "Updating the preview…";
  }

  try {
    const url = new URL(
      `/feeds/${form.dataset.feedId}/items/catch-up/preview`,
      window.location.origin
    );
    url.searchParams.set("cutoff", cutoff);
    const response = await fetch(url, {
      method: "GET",
      credentials: "same-origin",
      cache: "no-store",
      headers: { Accept: "text/html" },
      signal: controller.signal,
    });
    if (!response.ok) {
      throw new Error(`Preview returned ${response.status}`);
    }

    const fragment = await response.text();
    if (previewRequests.get(form) !== request || !form.isConnected) {
      return;
    }

    const parsed = new DOMParser().parseFromString(fragment, "text/html");
    const nextPreview = parsed.querySelector("output[data-catch-up-preview]");
    const currentPreview = getFormElements(form).preview;
    const previewInstant = nextPreview ? Date.parse(nextPreview.dataset.cutoff || "") : Number.NaN;
    if (
      !nextPreview ||
      !currentPreview ||
      !Number.isFinite(previewInstant) ||
      previewInstant !== Date.parse(cutoff)
    ) {
      throw new Error("Preview response was missing its count");
    }

    currentPreview.replaceWith(document.importNode(nextPreview, true));
    form.dataset.previewCutoff = cutoff;
    if (submit) {
      submit.disabled = false;
    }
  } catch (error) {
    if (error && error.name === "AbortError") {
      return;
    }
    if (previewRequests.get(form) !== request) {
      return;
    }

    showFormError(form, "The preview could not be loaded. Check your connection or choose another date.");
    const currentPreview = getFormElements(form).preview;
    if (currentPreview) {
      currentPreview.textContent = "Preview unavailable.";
    }
  }
};

const syncCutoff = (form) => {
  const { days, date, cutoff, cutoffLabel } = getFormElements(form);
  if (!date || !cutoff) {
    return "";
  }

  if (date.disabled) {
    const count = Number(days.value);
    date.value = days.validity.valid && Number.isSafeInteger(count) && count >= 1 ? addCalendarDays(count) : "";
  }
  const instant = localMidnightISO(date.value);
  cutoff.value = instant;
  if (cutoffLabel) {
    cutoffLabel.textContent = instant
      ? `Cutoff: ${date.value} at midnight local time.`
      : "Choose a valid number of days or a date for the cutoff.";
  }

  return instant;
};

const cancelPreview = (form) => {
  clearTimeout(previewTimers.get(form));
  previewTimers.delete(form);
  previewRequests.get(form)?.controller.abort();
  previewRequests.delete(form);
};

const previewForm = (form, delay = 0) => {
  cancelPreview(form);
  form.dataset.previewCutoff = "";
  const { submit, preview } = getFormElements(form);
  if (submit) submit.disabled = true;
  const instant = syncCutoff(form);
  if (!instant) {
    showFormError(form, "Choose a positive whole number of days or a valid date.");
    if (preview) {
      preview.textContent = "Choose a valid date to preview matching unread items.";
    }
    return;
  }

  showFormError(form, "");
  if (delay) {
    if (preview) preview.textContent = "Updating the preview…";
    previewTimers.set(form, setTimeout(() => {
      previewTimers.delete(form);
      if (form.isConnected) void requestPreview(form, instant);
    }, delay));
  } else {
    void requestPreview(form, instant);
  }
};

const initializeForm = (form) => {
  if (form.dataset.initialized === "true") {
    previewForm(form);
    return;
  }

  const { days, date } = getFormElements(form);
  if (!days || !date) {
    return;
  }

  days.value = "7";
  date.value = addCalendarDays(7);
  date.disabled = true;
  form.dataset.initialized = "true";
  syncCatchUpSpinner(form);
  previewForm(form);
};

const closestCatchUpForm = (target) =>
  target && target.closest ? target.closest("form[data-catch-up-form]") : null;

const getEventDetail = (event) => {
  const detail = event && event.detail;
  if (!detail) {
    return {};
  }
  if (detail["pulse:catch-up-applied"]) {
    return detail["pulse:catch-up-applied"];
  }
  return detail;
};

export const bindCatchUpControls = () => {
  bindCatchUpSpinner((form) => previewForm(form, 200));
  document.addEventListener("click", (event) => {
    const opener = event.target.closest("[data-catch-up-open]");
    if (opener) {
      const dialog = document.getElementById(opener.getAttribute("aria-controls"));
      if (!dialog || typeof dialog.showModal !== "function") {
        return;
      }

      dialogOpeners.set(dialog, opener);
      if (dialog.dataset.focusReturnBound !== "true") {
        dialog.dataset.focusReturnBound = "true";
        dialog.addEventListener("close", () => {
          const form = dialog.querySelector("[data-catch-up-form]");
          if (form) cancelPreview(form);
          const returnTarget = dialogOpeners.get(dialog);
          if (returnTarget && document.contains(returnTarget)) {
            returnTarget.focus({ preventScroll: true });
          }
        });
      }
      dialog.showModal();
      const form = dialog.querySelector("form[data-catch-up-form]");
      if (form) {
        initializeForm(form);
        const { days, date } = getFormElements(form);
        (date.disabled ? days : date)?.focus({ preventScroll: true });
      }
      return;
    }

    const closeButton = event.target.closest("[data-catch-up-close]");
    if (closeButton) {
      const dialog = closeButton.closest("dialog[data-catch-up-dialog]");
      dialog?.close();
      const returnTarget = dialog ? dialogOpeners.get(dialog) : null;
      if (returnTarget && document.contains(returnTarget)) {
        returnTarget.focus({ preventScroll: true });
      }
    }

    const mode = event.target.closest("[data-catch-up-mode]");
    if (mode) {
      const form = closestCatchUpForm(mode);
      const { days, date } = getFormElements(form);
      const customDate = date.disabled;
      date.disabled = !customDate;
      days.disabled = customDate;
      form.querySelector("[data-catch-up-days-picker]").hidden = customDate;
      form.querySelector("[data-catch-up-custom]").hidden = !customDate;
      mode.setAttribute("aria-expanded", String(customDate));
      mode.textContent = customDate ? "Choose a number of days" : "Choose a specific date";
      (customDate ? date : days).focus({ preventScroll: true });
      previewForm(form);
    }
  });

  document.addEventListener("change", (event) => {
    const form = closestCatchUpForm(event.target);
    if (!form) {
      return;
    }

    if (event.target.matches("[data-catch-up-days], [data-catch-up-date]")) previewForm(form);
  });

  document.addEventListener("input", (event) => {
    if (event.target.matches("[data-catch-up-days], [data-catch-up-date]")) {
      const form = closestCatchUpForm(event.target);
      if (form) {
        if (event.target.matches("[data-catch-up-days]")) syncCatchUpSpinner(form);
        previewForm(form, 200);
      }
    }
  });

  document.body.addEventListener("htmx:configRequest", (event) => {
    const form = closestCatchUpForm(event.detail && event.detail.elt);
    if (!form) {
      return;
    }

    const { cutoff } = getFormElements(form);
    const instant = cutoff ? cutoff.value : "";
    if (!instant || form.dataset.previewCutoff !== instant) {
      event.preventDefault();
      showFormError(form, "Wait for the preview to finish before applying Catch up.");
      return;
    }

    event.detail.parameters.cutoff = instant;
  });

  document.body.addEventListener("htmx:responseError", (event) => {
    const source = event.detail && event.detail.elt;
    const form = closestCatchUpForm(source);
    if (form) {
      showFormError(form, "Catch up could not be completed. Review the feed before trying again.");
      return;
    }

    const undoButton =
      source && source.matches && source.matches("[data-mark-all-read-undo-button]")
        ? source
        : source && source.querySelector
          ? source.querySelector("[data-mark-all-read-undo-button]")
          : null;
    if (undoButton) {
      showStatus("Undo failed. It is still available, so you can try again.");
    }
  });

  document.body.addEventListener("pulse:catch-up-applied", (event) => {
    const detail = getEventDetail(event);
    const count = Number(detail.affectedCount);
    showStatus(cutoffResultMessage(Number.isFinite(count) ? count : 0, detail.cutoff));
  });

  document.body.addEventListener("pulse:mark-all-read-applied", () => {
    showStatus("Mark all read is complete. Undo is available until you leave this feed.");
  });

  document.body.addEventListener("pulse:bulk-read-undone", () => {
    showStatus("Undo complete. The affected items are unread again.");
  });

  const showRedirectResult = () => {
    const params = new URLSearchParams(window.location.search);
    const rawCount = params.get("catch_up_count");
    const cutoff = params.get("catch_up_cutoff");
    if (rawCount === null || !cutoff || Number.isNaN(Number(rawCount))) {
      return;
    }

    const count = Number(rawCount);
    const requestedFeedID = params.get("catch_up_feed_id") || params.get("selected_feed_id");
    const activeFeedID =
      document.querySelector("#item-list[data-feed-id]")?.dataset.feedId ||
      document.querySelector("[data-mobile-feed-actions][data-feed-id]")?.dataset.feedId ||
      "";
    if (!requestedFeedID || requestedFeedID !== activeFeedID) {
      return;
    }

    showStatus(cutoffResultMessage(count, cutoff));
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", showRedirectResult, { once: true });
  } else {
    showRedirectResult();
  }
};
