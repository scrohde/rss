const previewRequests = new WeakMap();
const dialogOpeners = new WeakMap();

const getFormElements = (form) => ({
  range: form.querySelector("[data-catch-up-range]"),
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
  const { date, cutoff, cutoffLabel } = getFormElements(form);
  if (!date || !cutoff) {
    return "";
  }

  const instant = localMidnightISO(date.value);
  cutoff.value = instant;
  if (cutoffLabel) {
    cutoffLabel.textContent = instant
      ? `Cutoff: ${date.value} at midnight local time.`
      : "Choose a valid date for the cutoff.";
  }

  return instant;
};

const previewForm = (form) => {
  const instant = syncCutoff(form);
  if (!instant) {
    form.dataset.previewCutoff = "";
    showFormError(form, "Choose a valid date before previewing or applying Catch up.");
    const { preview, submit } = getFormElements(form);
    if (preview) {
      preview.textContent = "Choose a valid date to preview matching unread items.";
    }
    if (submit) {
      submit.disabled = true;
    }
    return;
  }

  void requestPreview(form, instant);
};

const initializeForm = (form) => {
  if (form.dataset.initialized === "true") {
    previewForm(form);
    return;
  }

  const { range, date } = getFormElements(form);
  if (!range || !date) {
    return;
  }

  range.value = "7";
  date.value = addCalendarDays(7);
  date.disabled = true;
  form.dataset.initialized = "true";
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
        form.querySelector("[data-catch-up-range]")?.focus({ preventScroll: true });
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
  });

  document.addEventListener("change", (event) => {
    const form = closestCatchUpForm(event.target);
    if (!form) {
      return;
    }

    const { range, date } = getFormElements(form);
    if (event.target === range && range && date) {
      const customDate = range.value === "custom";
      date.disabled = !customDate;
      if (!customDate) {
        date.value = addCalendarDays(Number(range.value));
      }
      if (customDate) {
        date.focus({ preventScroll: true });
      }
    }
    previewForm(form);
  });

  document.addEventListener("input", (event) => {
    if (event.target.matches("[data-catch-up-date]")) {
      const form = closestCatchUpForm(event.target);
      if (form) {
        previewForm(form);
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
