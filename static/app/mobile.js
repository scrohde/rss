import { getDisplayedFeedID, mobileLayoutQuery } from "./dom.js";

const mobileStreamPath = "/mobile/stream";
const desktopReaderPath = "/";

let layoutMedia = null;
let pendingTransition = null;
let lastDesktopFeedID = "";
let lastMobileFeedID = "";
const mainContentRequestSources = new Set();

const hasMainContentTarget = () => Boolean(document.getElementById("main-content"));
const hasMobileContent = () =>
  Boolean(document.querySelector("#main-content [data-mobile-stream='true'], #main-content [data-mobile-reader='true']"));
const hasCompleteMobileLayout = () =>
  hasMobileContent() && Boolean(document.getElementById("mobile-stream-feed-filter"));

const normalizeFeedID = (value) => {
  const normalized = String(value || "").trim();
  if (!/^\d+$/.test(normalized) || normalized === "0") {
    return "";
  }
  return normalized;
};

const currentMobileFeedID = () => {
  const selector = document.getElementById("mobile-stream-feed-filter");
  return selector ? normalizeFeedID(selector.value) : "";
};

const firstFeedID = () => {
  const feed = document.querySelector("#feed-list .feed-link[data-feed-id]");
  return feed ? normalizeFeedID(feed.dataset.feedId) : "";
};

const rememberDesktopFeed = () => {
  const feedID = normalizeFeedID(getDisplayedFeedID());
  if (feedID) {
    lastDesktopFeedID = feedID;
  }
};

const rememberMobileFeed = () => {
  lastMobileFeedID = currentMobileFeedID();
};

const pathWithSelectedFeed = (path, feedID) => {
  if (!feedID) {
    return path;
  }
  return `${path}?selected_feed_id=${encodeURIComponent(feedID)}`;
};

const desiredLayout = () => {
  if (!layoutMedia) {
    return "desktop";
  }
  return layoutMedia.matches ? "mobile" : "desktop";
};

const isMainContentTarget = (target) =>
  target === "#main-content" || Boolean(target && target.id === "main-content");

const mainContentRequestSource = (event) => {
  if (!event || !event.detail || !isMainContentTarget(event.detail.target)) {
    return null;
  }
  return event.detail.elt || event.target || null;
};

const trackMainContentRequest = (event) => {
  const source = mainContentRequestSource(event);
  if (!source) {
    return;
  }
  if (pendingTransition && source !== pendingTransition.mainContent) {
    event.preventDefault();
    return;
  }
  mainContentRequestSources.add(source);
};

const forgetMainContentRequest = (event) => {
  const source = mainContentRequestSource(event);
  if (source) {
    mainContentRequestSources.delete(source);
  }
};

const abortRequest = (source) => {
  if (typeof htmx !== "undefined" && typeof htmx.trigger === "function") {
    htmx.trigger(source, "htmx:abort");
    return;
  }
  source.dispatchEvent(new Event("htmx:abort", { bubbles: true }));
};

const abortMainContentRequests = () => {
  for (const source of mainContentRequestSources) {
    abortRequest(source);
  }
  mainContentRequestSources.clear();
};

const finishTransition = (transition, successful) => {
  if (pendingTransition !== transition) {
    return;
  }

  pendingTransition = null;
  if (successful && transition.layout === "mobile") {
    rememberMobileFeed();
  } else if (successful) {
    rememberDesktopFeed();
  }
  if (transition.abortRequested || desiredLayout() !== transition.layout) {
    syncResponsiveLayout();
  }
};

const startTransition = (layout, path) => {
  const mainContent = document.getElementById("main-content");
  if (!mainContent || typeof htmx === "undefined" || typeof htmx.ajax !== "function") {
    return;
  }

  abortMainContentRequests();
  const transition = {
    abortRequested: false,
    layout,
    mainContent,
  };
  pendingTransition = transition;

  let request;
  try {
    request = htmx.ajax("GET", path, {
      source: mainContent,
      target: "#main-content",
      swap: "innerHTML",
    });
  } catch (_error) {
    pendingTransition = null;
    return;
  }

  if (!request || typeof request.then !== "function") {
    pendingTransition = null;
    return;
  }

  request.then(
    () => finishTransition(transition, true),
    () => finishTransition(transition, false),
  );
};

const abortPendingTransition = () => {
  if (!pendingTransition || pendingTransition.abortRequested) {
    return;
  }

  pendingTransition.abortRequested = true;
  mainContentRequestSources.delete(pendingTransition.mainContent);
  abortRequest(pendingTransition.mainContent);
};

const loadMobileStream = () => {
  rememberDesktopFeed();
  startTransition("mobile", pathWithSelectedFeed(mobileStreamPath, lastMobileFeedID));
};

const loadDesktopReader = () => {
  rememberMobileFeed();
  const selectedFeedID = currentMobileFeedID() || lastDesktopFeedID || firstFeedID();
  startTransition("desktop", pathWithSelectedFeed(desktopReaderPath, selectedFeedID));
};

const syncResponsiveLayout = () => {
  if (!hasMainContentTarget()) {
    return;
  }

  const layout = desiredLayout();
  if (pendingTransition) {
    if (pendingTransition.layout !== layout) {
      abortPendingTransition();
    }
    return;
  }

  if (layout === "mobile") {
    if (hasCompleteMobileLayout()) {
      rememberMobileFeed();
      return;
    }
    loadMobileStream();
    return;
  }

  if (!hasMobileContent()) {
    rememberDesktopFeed();
    return;
  }
  loadDesktopReader();
};

export const bindMobileBootstrap = () => {
  if (document.body.dataset.mobileBootstrapBound === "true") {
    return;
  }
  document.body.dataset.mobileBootstrapBound = "true";

  const onReady = () => {
    layoutMedia = window.matchMedia(mobileLayoutQuery);
    if (typeof layoutMedia.addEventListener === "function") {
      layoutMedia.addEventListener("change", syncResponsiveLayout);
    } else if (typeof layoutMedia.addListener === "function") {
      layoutMedia.addListener(syncResponsiveLayout);
    }
    document.body.addEventListener("htmx:beforeRequest", trackMainContentRequest);
    document.body.addEventListener("htmx:afterRequest", forgetMainContentRequest);
    document.body.addEventListener("htmx:afterSwap", syncResponsiveLayout);
    document.body.addEventListener("htmx:historyRestore", syncResponsiveLayout);
    syncResponsiveLayout();
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", onReady, { once: true });
    return;
  }

  onReady();
};
