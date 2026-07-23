import { mobileLayoutQuery } from "./dom.js";

const interactiveStartSelector = [
  "a",
  "button",
  "input",
  "select",
  "textarea",
  "option",
  "label",
  "form",
  "[contenteditable]:not([contenteditable='false'])",
  "[role='button']",
  "[role='link']",
].join(",");

const gestureDecisionDistance = 10;
const verticalDominanceRatio = 1.2;
const activationDistance = 84;
const maximumVisualDistance = 92;
const refreshingVisualDistance = 52;

const indicatorLabels = Object.freeze({
  idle: "Pull to refresh",
  pulling: "Keep pulling",
  ready: "Release to refresh",
  refreshing: "Refreshing",
});

let layoutMedia = null;
let gesture = null;
let refreshRequest = null;
let refreshStream = null;
let activationPending = false;

const mobileStream = () =>
  document.querySelector("#main-content [data-mobile-stream='true']");

const pullIndicator = (stream) =>
  stream ? stream.querySelector("[data-mobile-pull-refresh]") : null;

const pullSurface = (stream) =>
  stream ? stream.querySelector("#mobile-stream-content") : null;

const announce = (stream, message) => {
  const indicator = pullIndicator(stream);
  const liveRegion = indicator ? indicator.querySelector("[data-mobile-pull-announcement]") : null;
  if (liveRegion) {
    liveRegion.textContent = message;
  }
};

const setIndicatorState = (stream, state, announcement = "") => {
  const indicator = pullIndicator(stream);
  if (!indicator) {
    return;
  }

  const changed = indicator.dataset.state !== state;
  indicator.dataset.state = state;
  const label = indicator.querySelector("[data-mobile-pull-label]");
  if (label) {
    label.textContent = indicatorLabels[state];
  }
  if (announcement && changed) {
    announce(stream, announcement);
  }
};

const setVisualDistance = (stream, distance) => {
  const surface = pullSurface(stream);
  if (surface) {
    surface.style.setProperty("--mobile-pull-distance", `${Math.max(0, distance)}px`);
  }
};

const resetStreamVisuals = (stream, announcement = "") => {
  if (!stream) {
    return;
  }

  delete stream.dataset.mobilePullTracking;
  setVisualDistance(stream, 0);
  setIndicatorState(stream, "idle");
  if (announcement) {
    announce(stream, announcement);
  }
};

const cancelGesture = (announcement = "") => {
  const stream = gesture ? gesture.stream : null;
  gesture = null;
  resetStreamVisuals(stream, announcement);
};

const refreshIsPending = () => {
  const brandButton = document.getElementById("topbar-brand-button");
  return Boolean(
    activationPending ||
      refreshRequest ||
      (brandButton && brandButton.classList.contains("htmx-request")),
  );
};

const documentIsAtTop = () => {
  const root = document.scrollingElement;
  return window.scrollY <= 0 && (!root || root.scrollTop <= 0);
};

const touchWithIdentifier = (touches, identifier) => {
  for (const touch of Array.from(touches || [])) {
    if (touch.identifier === identifier) {
      return touch;
    }
  }
  return null;
};

const resistedDistance = (distance) => {
  const initial = Math.min(distance, activationDistance) * 0.62;
  const beyondThreshold = Math.max(0, distance - activationDistance) * 0.18;
  return Math.min(maximumVisualDistance, initial + beyondThreshold);
};

const onTouchStart = (event) => {
  if (
    gesture ||
    refreshIsPending() ||
    !layoutMedia ||
    !layoutMedia.matches ||
    !documentIsAtTop() ||
    event.touches.length !== 1 ||
    (event.target.closest && event.target.closest(interactiveStartSelector))
  ) {
    return;
  }

  const stream = event.target.closest ? event.target.closest("[data-mobile-stream='true']") : null;
  const indicator = pullIndicator(stream);
  const surface = pullSurface(stream);
  if (!stream || stream !== mobileStream() || !indicator || !surface) {
    return;
  }

  const touch = event.touches[0];
  gesture = {
    stream,
    identifier: touch.identifier,
    startX: touch.clientX,
    startY: touch.clientY,
    claimed: false,
    armed: false,
  };
};

const onTouchMove = (event) => {
  if (!gesture) {
    return;
  }

  if (event.touches.length !== 1) {
    if (gesture.claimed && event.cancelable) {
      event.preventDefault();
    }
    cancelGesture(gesture.claimed ? "Refresh canceled." : "");
    return;
  }

  const touch = touchWithIdentifier(event.touches, gesture.identifier);
  if (!touch) {
    cancelGesture(gesture.claimed ? "Refresh canceled." : "");
    return;
  }

  const deltaX = touch.clientX - gesture.startX;
  const deltaY = touch.clientY - gesture.startY;
  const horizontalDistance = Math.abs(deltaX);
  const verticalDistance = Math.abs(deltaY);

  if (!gesture.claimed) {
    if (!documentIsAtTop()) {
      cancelGesture();
      return;
    }
    if (horizontalDistance < gestureDecisionDistance && verticalDistance < gestureDecisionDistance) {
      return;
    }
    const predominantlyDownward =
      deltaY > gestureDecisionDistance &&
      deltaY >= horizontalDistance * verticalDominanceRatio;
    if (!predominantlyDownward) {
      cancelGesture();
      return;
    }

    gesture.claimed = true;
    gesture.stream.dataset.mobilePullTracking = "true";
    setIndicatorState(gesture.stream, "pulling", "Pull down to refresh.");
  }

  if (event.cancelable) {
    event.preventDefault();
  }

  const pullDistance = Math.max(0, deltaY);
  gesture.armed = pullDistance >= activationDistance;
  setVisualDistance(gesture.stream, resistedDistance(pullDistance));
  if (gesture.armed) {
    setIndicatorState(gesture.stream, "ready", "Release to refresh.");
  } else {
    setIndicatorState(gesture.stream, "pulling", "Keep pulling to refresh.");
  }
};

const activateRefresh = (stream) => {
  const brandButton = document.getElementById("topbar-brand-button");
  if (!brandButton || !brandButton.isConnected || refreshIsPending()) {
    resetStreamVisuals(stream, "Refresh unavailable.");
    return;
  }

  activationPending = true;
  delete stream.dataset.mobilePullTracking;
  setVisualDistance(stream, refreshingVisualDistance);
  setIndicatorState(stream, "refreshing", "Refreshing feeds.");
  brandButton.click();

  window.queueMicrotask(() => {
    if (activationPending) {
      activationPending = false;
      resetStreamVisuals(stream, "Refresh unavailable.");
    }
  });
};

const abandonRefresh = (announcement = "") => {
  const request = refreshRequest;
  const oldStream = refreshStream;
  refreshRequest = null;
  refreshStream = null;
  activationPending = false;

  resetStreamVisuals(oldStream);
  resetStreamVisuals(mobileStream(), announcement);

  if (request && request.readyState !== XMLHttpRequest.DONE) {
    request.abort();
  }
};

const finishGesture = (event, canceled = false) => {
  if (!gesture) {
    return;
  }

  const endedTouch = touchWithIdentifier(event.changedTouches, gesture.identifier);
  if (!endedTouch && !canceled) {
    return;
  }
  if (gesture.claimed && event.cancelable) {
    event.preventDefault();
  }

  const stream = gesture.stream;
  const activate = !canceled && gesture.claimed && gesture.armed && !refreshIsPending();
  const wasClaimed = gesture.claimed;
  gesture = null;

  if (activate) {
    activateRefresh(stream);
    return;
  }
  resetStreamVisuals(stream, wasClaimed ? "Refresh canceled." : "");
};

const onTouchEnd = (event) => finishGesture(event);
const onTouchCancel = (event) => finishGesture(event, true);

const onBeforeRequest = (event) => {
  const detail = event && event.detail ? event.detail : null;
  const source = detail ? detail.elt : null;
  if (!source || source.id !== "topbar-brand-button") {
    if (gesture) {
      cancelGesture(gesture.claimed ? "Refresh canceled." : "");
    }
    return;
  }

  refreshRequest = detail.xhr || null;
  refreshStream = mobileStream();
  activationPending = false;
  if (refreshStream) {
    delete refreshStream.dataset.mobilePullTracking;
    setVisualDistance(refreshStream, refreshingVisualDistance);
    const pendingLabel = source.querySelector(".brand-subtitle-pending");
    const announcement = pendingLabel ? pendingLabel.textContent.trim() : "Refreshing feeds.";
    setIndicatorState(refreshStream, "refreshing", announcement);
  }
};

const onAfterRequest = (event) => {
  const detail = event && event.detail ? event.detail : null;
  if (!detail || !refreshRequest || detail.xhr !== refreshRequest) {
    return;
  }

  const successful = detail.successful === true;
  const oldStream = refreshStream;
  refreshRequest = null;
  refreshStream = null;
  activationPending = false;
  resetStreamVisuals(oldStream);
  resetStreamVisuals(
    mobileStream(),
    successful ? "Refresh complete." : "Refresh failed.",
  );
};

const onAfterSwap = () => {
  if (gesture && (!gesture.stream.isConnected || gesture.stream !== mobileStream())) {
    cancelGesture();
  }

  const currentStream = mobileStream();
  const refreshNavigatedAway =
    refreshRequest &&
    refreshRequest.readyState !== XMLHttpRequest.DONE &&
    (!currentStream || !refreshStream || !refreshStream.isConnected || refreshStream !== currentStream);
  if (refreshNavigatedAway) {
    abandonRefresh(currentStream ? "Refresh canceled." : "");
  } else if (!currentStream) {
    resetStreamVisuals(refreshStream);
  }
};

const onLayoutChange = () => {
  if (gesture) {
    cancelGesture();
  }
  if (!layoutMedia.matches) {
    resetStreamVisuals(refreshStream);
  }
};

const onScroll = () => {
  if (gesture && !documentIsAtTop()) {
    cancelGesture(gesture.claimed ? "Refresh canceled." : "");
  }
};

export const bindMobilePullRefresh = () => {
  if (document.body.dataset.mobilePullRefreshBound === "true") {
    return;
  }
  document.body.dataset.mobilePullRefreshBound = "true";

  layoutMedia = window.matchMedia(mobileLayoutQuery);
  if (typeof layoutMedia.addEventListener === "function") {
    layoutMedia.addEventListener("change", onLayoutChange);
  } else if (typeof layoutMedia.addListener === "function") {
    layoutMedia.addListener(onLayoutChange);
  }

  document.addEventListener("touchstart", onTouchStart, { passive: true });
  document.addEventListener("touchmove", onTouchMove, { passive: false });
  document.addEventListener("touchend", onTouchEnd, { passive: false });
  document.addEventListener("touchcancel", onTouchCancel, { passive: false });
  document.body.addEventListener("htmx:beforeRequest", onBeforeRequest);
  document.body.addEventListener("htmx:afterRequest", onAfterRequest);
  document.body.addEventListener("htmx:afterSwap", onAfterSwap);
  window.addEventListener("scroll", onScroll, { passive: true });
};
