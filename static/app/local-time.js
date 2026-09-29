export function localizePublicationTimes() {
  const formatter = new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
    timeZoneName: "short",
  });
  document.querySelectorAll("time[data-local-time], [data-local-time-title]").forEach((element) => {
    if (element.closest("[data-reader-content]")) return;
    const titleOnly = element.hasAttribute("data-local-time-title");
    const timestamp = element.getAttribute(titleOnly ? "data-local-time-title" : "datetime");
    if (!timestamp) return;
    const date = new Date(timestamp);
    if (Number.isNaN(date.getTime())) return;
    const label = formatter.format(date);
    if (titleOnly) element.title = label;
    else element.textContent = label;
  });
}

export function bindLocalPublicationTimes() {
  localizePublicationTimes();
  document.body.addEventListener("htmx:afterSettle", localizePublicationTimes);
  document.body.addEventListener("htmx:historyRestore", localizePublicationTimes);
  window.addEventListener("pageshow", localizePublicationTimes);
}
