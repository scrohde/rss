import { isDesktopLayout } from "./dom.js";

const mobileStreamPath = "/mobile/stream";

const hasMainContentTarget = () => Boolean(document.getElementById("main-content"));
const hasMobileContent = () =>
  Boolean(document.querySelector("#main-content [data-mobile-stream='true'], #main-content [data-mobile-reader='true']"));

const loadMobileStream = () => {
  if (isDesktopLayout()) {
    return;
  }
  if (!hasMainContentTarget()) {
    return;
  }
  if (hasMobileContent()) {
    return;
  }
  if (typeof htmx === "undefined" || typeof htmx.ajax !== "function") {
    return;
  }

  htmx.ajax("GET", mobileStreamPath, {
    target: "#main-content",
    swap: "innerHTML",
  });
};

export const bindMobileBootstrap = () => {
  if (document.body.dataset.mobileBootstrapBound === "true") {
    return;
  }
  document.body.dataset.mobileBootstrapBound = "true";

  const onReady = () => {
    loadMobileStream();
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", onReady, { once: true });
    return;
  }

  onReady();
};
