(() => {
  const skipKey = "pulse-rss:skip-delete-warning";

  const isEditing = () => document.body.dataset.feedEdit === "true";

  const syncEditButtons = () => {
    const editing = isEditing();
    document.querySelectorAll("[data-edit-feeds]").forEach((button) => {
      button.setAttribute("aria-pressed", editing ? "true" : "false");
      button.setAttribute("aria-label", editing ? "Done editing feeds" : "Edit feeds");
      button.setAttribute("title", editing ? "Done editing feeds" : "Edit feeds");
    });
    document.querySelectorAll("[data-feed-remove]").forEach((button) => {
      button.hidden = !editing;
    });
  };

  const setEditing = (enabled) => {
    document.body.dataset.feedEdit = enabled ? "true" : "false";
    syncEditButtons();
  };

  document.addEventListener("click", (event) => {
    const button = event.target.closest("[data-edit-feeds]");
    if (!button) {
      return;
    }
    event.preventDefault();
    setEditing(!isEditing());
  });

  const shouldSkipWarning = () => window.localStorage.getItem(skipKey) === "true";

  document.addEventListener("htmx:confirm", (event) => {
    const trigger = event.target;
    if (!trigger || !trigger.matches("[data-feed-remove]")) {
      return;
    }

    event.preventDefault();

    if (shouldSkipWarning()) {
      event.detail.issueRequest(true);
      return;
    }

    const dialog = document.getElementById("delete-feed-dialog");
    const message = document.getElementById("delete-feed-message");
    const checkbox = document.getElementById("delete-feed-skip");
    const title = trigger.getAttribute("data-feed-title");

    if (!dialog || typeof dialog.showModal !== "function" || !message || !checkbox) {
      const fallback = title
        ? `Remove "${title}" and all its items?`
        : "Remove this feed and all its items?";
      if (window.confirm(fallback)) {
        event.detail.issueRequest(true);
      }
      return;
    }

    message.textContent = title
      ? `This removes "${title}" and all saved items.`
      : "This removes the feed and all saved items.";
    checkbox.checked = false;

    const handleClose = () => {
      dialog.removeEventListener("close", handleClose);
      if (dialog.returnValue === "confirm") {
        if (checkbox.checked) {
          window.localStorage.setItem(skipKey, "true");
        }
        event.detail.issueRequest(true);
      }
    };

    dialog.addEventListener("close", handleClose);
    dialog.showModal();
  });

  document.addEventListener("htmx:load", syncEditButtons);

  document.body.dataset.feedEdit = document.body.dataset.feedEdit === "true" ? "true" : "false";

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", syncEditButtons);
  } else {
    syncEditButtons();
  }
})();
