import { getDisplayedFeedID, mobileLayoutQuery } from "./dom.js";

const mobileStreamPath = "/mobile/stream";
const desktopReaderPath = "/";

let layoutMedia = null;
let pendingTransition = null;
let lastDesktopFeedID = "";
let lastMobileFeedID = "";
let lastMobileStreamPath = mobileStreamPath;
const contentRequests = new Map();
const focusedPaginationRequests = new WeakSet();

const hasMainContentTarget = () => Boolean(document.getElementById("main-content"));
const hasMobileContent = () =>
  Boolean(
    document.querySelector("#main-content [data-mobile-stream='true'], #main-content [data-mobile-reader='true']"),
  );
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

const currentMobileStreamPath = () => {
  if (document.querySelector("[data-mobile-stream='true']") && window.location.pathname === mobileStreamPath) {
    return `${window.location.pathname}${window.location.search}`;
  }

  const readerBack = document.querySelector("[data-mobile-reader='true'] .mobile-reader-back");
  const backPath = readerBack ? readerBack.getAttribute("hx-get") : "";
  return backPath && backPath.startsWith(mobileStreamPath) ? backPath : "";
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
  const streamPath = currentMobileStreamPath();
  if (streamPath) {
    lastMobileStreamPath = streamPath;
  }
};

const syncMobileFilterHistoryState = (event) => {
  const selector = event.target;
  if (!selector || selector.id !== "mobile-stream-feed-filter") {
    return;
  }

  for (const option of selector.options) {
    option.defaultSelected = option.selected;
  }

  rememberMobileFeed();
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

const isContentTarget = (target) => {
  const mainContent = document.getElementById("main-content");
  if (!mainContent) {
    return false;
  }

  return (
    target === "#main-content" ||
    target === mainContent ||
    Boolean(target && typeof target === "object" && mainContent.contains(target))
  );
};

const contentRequestSource = (event) => {
  if (!event || !event.detail || !isContentTarget(event.detail.target)) {
    return null;
  }
  return event.detail.elt || event.target || null;
};

const trackContentRequest = (event) => {
  const source = contentRequestSource(event);
  const request = event && event.detail ? event.detail.xhr : null;
  if (!source || !request) {
    return;
  }
  if (pendingTransition && source !== pendingTransition.mainContent) {
    event.preventDefault();
    return;
  }
  contentRequests.set(request, source);
};

const forgetContentRequest = (event) => {
  const request = event && event.detail ? event.detail.xhr : null;
  if (request) {
    contentRequests.delete(request);
  }
};

const abortRequest = (source) => {
  if (typeof htmx !== "undefined" && typeof htmx.trigger === "function") {
    htmx.trigger(source, "htmx:abort");
    return;
  }
  source.dispatchEvent(new Event("htmx:abort", { bubbles: true }));
};

const abortContentRequests = () => {
  for (const source of new Set(contentRequests.values())) {
    abortRequest(source);
  }
  contentRequests.clear();
};

const focusMobilePaginationResult = (event) => {
  const detail = event ? event.detail : null;
  const request = detail ? detail.xhr : null;
  if (!request || focusedPaginationRequests.has(request)) {
    return;
  }

  const requestPath = detail.pathInfo ? detail.pathInfo.requestPath : "";
  const parsedPath = new URL(requestPath, window.location.origin).pathname;
  const feedMatch = parsedPath.match(/^\/mobile\/feeds\/(\d+)\/items$/);
  const isSectionPage = parsedPath === "/mobile/stream/sections";
  if (!feedMatch && !isSectionPage) {
    return;
  }

  focusedPaginationRequests.add(request);

  let focusTarget = null;
  if (isSectionPage) {
    const sections = document.getElementById("mobile-stream-sections");
    focusTarget = sections ? sections.querySelector("[data-mobile-feed-section]") || sections : null;
  } else {
    focusTarget = document.getElementById(`mobile-feed-section-${feedMatch[1]}`);
  }

  if (focusTarget) {
    focusTarget.focus({ preventScroll: true });
  }
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

  abortContentRequests();
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
  for (const [request, source] of contentRequests) {
    if (source === pendingTransition.mainContent) {
      contentRequests.delete(request);
    }
  }
  abortRequest(pendingTransition.mainContent);
};

const loadMobileStream = () => {
  rememberDesktopFeed();
  startTransition("mobile", lastMobileStreamPath);
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
    document.body.addEventListener("change", syncMobileFilterHistoryState);
    document.body.addEventListener("htmx:beforeRequest", trackContentRequest);
    document.body.addEventListener("htmx:afterRequest", forgetContentRequest);
    document.body.addEventListener("htmx:afterSettle", focusMobilePaginationResult);
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
