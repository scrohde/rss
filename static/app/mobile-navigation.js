import { mobileLayoutQuery } from "./dom.js";

const readerOriginStorageKey = "pulse.mobileReaderOrigin.v1";
const readerNavigationStateKey = "pulseMobileReaderNavigationID";
const readerOriginVersion = 1;
const maximumPathLength = 2048;
const maximumNavigationIDLength = 128;
const maximumCardIndex = 1000;
const maximumScrollCoordinate = 10_000_000;
const streamRestoreTimeout = 10_000;

let activeReaderOrigin = null;
let currentReaderNavigationID = "";
let pendingPushedReaderOrigin = null;
let pendingStreamRestore = null;
let navigationSequence = 0;
let streamRestoreSequence = 0;

const pendingReaderOrigins = new WeakMap();

const currentPath = () => `${window.location.pathname}${window.location.search}`;

const normalizedPath = (value) => {
  if (typeof value !== "string" || value.length === 0 || value.length > maximumPathLength) {
    return "";
  }

  try {
    const parsed = new URL(value, window.location.origin);
    if (parsed.origin !== window.location.origin) {
      return "";
    }
    return `${parsed.pathname}${parsed.search}`;
  } catch (_error) {
    return "";
  }
};

const normalizedItemID = (value, allowEmpty = false) => {
  const normalized = String(value || "").trim();
  if (allowEmpty && normalized === "") {
    return "";
  }
  return /^\d{1,20}$/.test(normalized) && normalized !== "0" ? normalized : "";
};

const boundedNumber = (value, minimum, maximum) => {
  const number = Number(value);
  if (!Number.isFinite(number)) {
    return null;
  }
  return Math.min(maximum, Math.max(minimum, number));
};

const sanitizeReaderOrigin = (value) => {
  if (!value || typeof value !== "object" || value.version !== readerOriginVersion) {
    return null;
  }

  const navigationID =
    typeof value.navigationID === "string" && value.navigationID.length <= maximumNavigationIDLength
      ? value.navigationID
      : "";
  const streamURL = normalizedPath(value.streamURL);
  const readerRequestPath = normalizedPath(value.readerRequestPath);
  const itemID = normalizedItemID(value.itemID);
  const previousItemID = normalizedItemID(value.previousItemID, true);
  const nextItemID = normalizedItemID(value.nextItemID, true);
  const cardIndex = boundedNumber(value.cardIndex, 0, maximumCardIndex);
  const scrollY = boundedNumber(value.scrollY, 0, maximumScrollCoordinate);
  const cardViewportOffset = boundedNumber(
    value.cardViewportOffset,
    -maximumScrollCoordinate,
    maximumScrollCoordinate,
  );

  if (
    !navigationID ||
    !streamURL.startsWith("/mobile/stream") ||
    !/^\/mobile\/items\/\d+\/reader(?:\?|$)/.test(readerRequestPath) ||
    !itemID ||
    !Number.isInteger(cardIndex) ||
    scrollY === null ||
    cardViewportOffset === null
  ) {
    return null;
  }

  return {
    version: readerOriginVersion,
    navigationID,
    streamURL,
    readerRequestPath,
    itemID,
    cardIndex,
    previousItemID,
    nextItemID,
    scrollY,
    cardViewportOffset,
  };
};

const readStoredReaderOrigin = () => {
  try {
    const raw = window.sessionStorage.getItem(readerOriginStorageKey);
    return raw ? sanitizeReaderOrigin(JSON.parse(raw)) : null;
  } catch (_error) {
    return null;
  }
};

const storeReaderOrigin = (record) => {
  try {
    window.sessionStorage.setItem(readerOriginStorageKey, JSON.stringify(record));
  } catch (_error) {
    // History navigation still works from the in-memory record when storage is unavailable.
  }
};

const navigationIDFromState = (state = window.history.state) => {
  if (!state || state.htmx !== true) {
    return "";
  }
  const navigationID = state[readerNavigationStateKey];
  if (
    typeof navigationID !== "string" ||
    navigationID.length === 0 ||
    navigationID.length > maximumNavigationIDLength
  ) {
    return "";
  }
  return navigationID;
};

const newNavigationID = () => {
  if (window.crypto && typeof window.crypto.randomUUID === "function") {
    return window.crypto.randomUUID();
  }

  navigationSequence += 1;
  const randomPart = Math.random().toString(36).slice(2, 12);
  return `${Date.now().toString(36)}-${navigationSequence.toString(36)}-${randomPart}`;
};

const readerRequestPath = (source, detail) => {
  const pathInfo = detail && detail.pathInfo ? detail.pathInfo : null;
  const finalRequestPath = normalizedPath(pathInfo ? pathInfo.finalRequestPath : "");
  if (finalRequestPath) {
    return finalRequestPath;
  }

  const attributePath = source.getAttribute("hx-get") || source.getAttribute("data-hx-get") || "";
  if (attributePath) {
    return normalizedPath(attributePath);
  }

  return normalizedPath(pathInfo ? pathInfo.requestPath : "");
};

const captureReaderOrigin = (source, detail) => {
  if (!window.matchMedia(mobileLayoutQuery).matches || !source.matches(".mobile-card-open")) {
    return null;
  }

  const stream = source.closest("[data-mobile-stream='true']");
  const card = source.closest("[data-mobile-item-id]");
  const requestPath = readerRequestPath(source, detail);
  if (!stream || !card || !requestPath) {
    return null;
  }

  const cards = Array.from(stream.querySelectorAll("[data-mobile-item-id]"));
  const cardIndex = cards.indexOf(card);
  const itemID = normalizedItemID(card.dataset.mobileItemId);
  if (cardIndex < 0 || !itemID) {
    return null;
  }

  const previousCard = cardIndex > 0 ? cards[cardIndex - 1] : null;
  const nextCard = cardIndex + 1 < cards.length ? cards[cardIndex + 1] : null;
  const record = {
    version: readerOriginVersion,
    navigationID: newNavigationID(),
    streamURL: currentPath(),
    readerRequestPath: requestPath,
    itemID,
    cardIndex,
    previousItemID: previousCard ? normalizedItemID(previousCard.dataset.mobileItemId) : "",
    nextItemID: nextCard ? normalizedItemID(nextCard.dataset.mobileItemId) : "",
    scrollY: Math.max(0, window.scrollY),
    cardViewportOffset: card.getBoundingClientRect().top,
  };

  return sanitizeReaderOrigin(record);
};

const onBeforeRequest = (event) => {
  const detail = event && event.detail ? event.detail : null;
  const source = detail ? detail.elt : null;
  const request = detail ? detail.xhr : null;
  if (!source || !source.matches || !request) {
    return;
  }

  const record = captureReaderOrigin(source, detail);
  if (record) {
    pendingReaderOrigins.set(request, record);
    pendingPushedReaderOrigin = record;
  }
};

const activatePushedReaderOrigin = (event) => {
  const detail = event && event.detail ? event.detail : null;
  const pendingRecord = pendingPushedReaderOrigin;
  if (!pendingRecord) {
    currentReaderNavigationID = navigationIDFromState();
    return;
  }

  const pushedPath = normalizedPath(detail.path);
  const record = sanitizeReaderOrigin({
    ...pendingRecord,
    readerRequestPath: pushedPath || pendingRecord.readerRequestPath,
  });
  const state = window.history.state;
  if (
    !record ||
    pushedPath !== pendingRecord.readerRequestPath ||
    !state ||
    state.htmx !== true ||
    currentPath() !== record.readerRequestPath
  ) {
    return;
  }

  window.history.replaceState(
    {
      ...state,
      [readerNavigationStateKey]: record.navigationID,
    },
    "",
    window.location.href,
  );
  activeReaderOrigin = record;
  currentReaderNavigationID = record.navigationID;
  pendingPushedReaderOrigin = null;
  storeReaderOrigin(record);
};

const resetNavigationStateFromHistory = () => {
  currentReaderNavigationID = navigationIDFromState();
  pendingStreamRestore = null;
  streamRestoreSequence += 1;
};

const scrollReaderToTop = (event) => {
  const detail = event && event.detail ? event.detail : null;
  const request = detail ? detail.xhr : null;
  const record = request ? pendingReaderOrigins.get(request) : null;
  if (
    !record ||
    !document.querySelector("[data-mobile-reader='true']") ||
    navigationIDFromState() !== record.navigationID
  ) {
    return;
  }
  window.scrollTo(0, 0);
  pendingReaderOrigins.delete(request);
};

const forgetPendingReaderOrigin = (event) => {
  const detail = event && event.detail ? event.detail : null;
  const request = detail ? detail.xhr : null;
  const record = request ? pendingReaderOrigins.get(request) : null;
  const activeRequestIsSettling =
    detail &&
    detail.successful &&
    record &&
    activeReaderOrigin &&
    activeReaderOrigin.navigationID === record.navigationID &&
    document.querySelector("[data-mobile-reader='true']");
  if (request && !activeRequestIsSettling) {
    pendingReaderOrigins.delete(request);
  }
  if (
    record &&
    pendingPushedReaderOrigin &&
    pendingPushedReaderOrigin.navigationID === record.navigationID
  ) {
    pendingPushedReaderOrigin = null;
  }
};

const readerOriginForCurrentHistory = () => {
  const record = activeReaderOrigin || readStoredReaderOrigin();
  if (
    !record ||
    navigationIDFromState() !== record.navigationID ||
    currentPath() !== record.readerRequestPath ||
    !document.querySelector("[data-mobile-reader='true']")
  ) {
    return null;
  }

  activeReaderOrigin = record;
  return record;
};

const onReaderBackClick = (event) => {
  const target = event.target && event.target.closest ? event.target.closest(".mobile-reader-back") : null;
  if (!target || !window.matchMedia(mobileLayoutQuery).matches) {
    return;
  }

  const record = readerOriginForCurrentHistory();
  const backPath = normalizedPath(target.getAttribute("hx-get") || target.getAttribute("data-hx-get") || "");
  if (!record || backPath !== record.streamURL) {
    return;
  }

  event.preventDefault();
  event.stopImmediatePropagation();
  pendingStreamRestore = record;
  window.history.back();
};

const clampedScrollTarget = (value) => {
  const root = document.scrollingElement;
  const maximum = root ? Math.max(0, root.scrollHeight - window.innerHeight) : 0;
  return Math.min(maximum, Math.max(0, value));
};

const restoreStreamPosition = (record) => {
  if (
    !window.matchMedia(mobileLayoutQuery).matches ||
    currentPath() !== record.streamURL ||
    !document.querySelector("[data-mobile-stream='true']")
  ) {
    return false;
  }

  const anchor = document.getElementById(`mobile-card-${record.itemID}`);
  const target = anchor
    ? window.scrollY + anchor.getBoundingClientRect().top - record.cardViewportOffset
    : record.scrollY;
  window.scrollTo(0, clampedScrollTarget(target));
  pendingStreamRestore = null;

  return true;
};

const scheduleStreamRestore = (record) => {
  pendingStreamRestore = record;
  streamRestoreSequence += 1;
  const sequence = streamRestoreSequence;
  const deadline = window.performance.now() + streamRestoreTimeout;

  const attempt = () => {
    if (sequence !== streamRestoreSequence || pendingStreamRestore !== record) {
      return;
    }
    if (!window.matchMedia(mobileLayoutQuery).matches || currentPath() !== record.streamURL) {
      pendingStreamRestore = null;
      return;
    }
    if (document.querySelector("[data-mobile-stream='true']")) {
      window.requestAnimationFrame(() => {
        if (sequence === streamRestoreSequence && pendingStreamRestore === record) {
          restoreStreamPosition(record);
        }
      });
      return;
    }
    if (window.performance.now() >= deadline) {
      pendingStreamRestore = null;
      return;
    }
    window.requestAnimationFrame(attempt);
  };

  window.requestAnimationFrame(attempt);
};

const onPopState = (event) => {
  const leavingNavigationID = currentReaderNavigationID;
  const targetNavigationID = navigationIDFromState(event.state);
  currentReaderNavigationID = targetNavigationID;

  const record = activeReaderOrigin || readStoredReaderOrigin();
  if (
    record &&
    targetNavigationID === record.navigationID &&
    currentPath() === record.readerRequestPath
  ) {
    activeReaderOrigin = record;
    window.setTimeout(() => {
      const state = window.history.state;
      if (
        state &&
        state.htmx === true &&
        currentPath() === record.readerRequestPath
      ) {
        window.history.replaceState(
          {
            ...state,
            [readerNavigationStateKey]: record.navigationID,
          },
          "",
          window.location.href,
        );
      }
    }, 0);
    pendingStreamRestore = null;
    streamRestoreSequence += 1;
    return;
  }

  if (
    record &&
    currentPath() === record.streamURL &&
    (pendingStreamRestore === record || leavingNavigationID === record.navigationID)
  ) {
    scheduleStreamRestore(record);
    return;
  }

  pendingStreamRestore = null;
  streamRestoreSequence += 1;
};

const onHistoryRestore = () => {
  if (pendingStreamRestore) {
    scheduleStreamRestore(pendingStreamRestore);
  }
};

export const bindMobileReaderNavigation = () => {
  if (document.body.dataset.mobileReaderNavigationBound === "true") {
    return;
  }
  document.body.dataset.mobileReaderNavigationBound = "true";

  activeReaderOrigin = readStoredReaderOrigin();
  currentReaderNavigationID = navigationIDFromState();

  document.addEventListener("click", onReaderBackClick, true);
  document.body.addEventListener("htmx:beforeRequest", onBeforeRequest);
  document.body.addEventListener("htmx:pushedIntoHistory", activatePushedReaderOrigin);
  document.body.addEventListener("htmx:replacedInHistory", resetNavigationStateFromHistory);
  document.body.addEventListener("htmx:afterSettle", scrollReaderToTop);
  document.body.addEventListener("htmx:afterRequest", forgetPendingReaderOrigin);
  document.body.addEventListener("htmx:historyRestore", onHistoryRestore);
  window.addEventListener("popstate", onPopState);
};
