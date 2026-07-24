export const createPanelResizer = ({
  state,
  storageKey,
  cssProperty,
  minimum,
  maximum,
  fallbackWidth,
  getResizer,
  measureWidth,
  bodyClass,
  canStart = () => true,
  widthFromDelta,
  onStored = () => {},
}) => {
  const clampWidth = (width) =>
    Math.min(maximum, Math.max(minimum, Math.round(width)));

  const currentWidth = () => {
    const computed = getComputedStyle(document.documentElement);
    const parsed = parseFloat(computed.getPropertyValue(cssProperty));
    if (Number.isFinite(parsed)) {
      return parsed;
    }

    const measured = measureWidth();
    return Number.isFinite(measured) ? measured : fallbackWidth;
  };

  const setWidth = (width, persist) => {
    const clamped = clampWidth(width);
    document.documentElement.style.setProperty(cssProperty, `${clamped}px`);
    if (!persist) {
      return clamped;
    }

    try {
      window.localStorage.setItem(storageKey, String(clamped));
      onStored();
    } catch (_error) {
      // Ignore localStorage failures.
    }

    return clamped;
  };

  const readStoredWidth = () => {
    try {
      const raw = window.localStorage.getItem(storageKey);
      if (!raw) {
        return null;
      }
      const parsed = parseInt(raw, 10);
      return Number.isFinite(parsed) ? clampWidth(parsed) : null;
    } catch (_error) {
      return null;
    }
  };

  const stop = (persist) => {
    if (!state.active) {
      return;
    }
    state.active = false;
    state.pointerId = null;
    document.body.classList.remove(bodyClass);
    if (persist) {
      setWidth(currentWidth(), true);
    }
  };

  const bind = () => {
    const resizer = getResizer();
    if (!resizer || resizer.dataset.bound === "true") {
      return;
    }
    resizer.dataset.bound = "true";

    resizer.addEventListener("pointerdown", (event) => {
      if (event.button !== 0 || !canStart(event)) {
        return;
      }

      state.active = true;
      state.pointerId = event.pointerId;
      state.startX = event.clientX;
      state.startWidth = currentWidth();
      document.body.classList.add(bodyClass);
      if (typeof resizer.setPointerCapture === "function") {
        resizer.setPointerCapture(event.pointerId);
      }
      event.preventDefault();
    });

    resizer.addEventListener("pointermove", (event) => {
      if (!state.active || state.pointerId !== event.pointerId) {
        return;
      }

      const delta = event.clientX - state.startX;
      setWidth(widthFromDelta(state.startWidth, delta), false);
    });

    resizer.addEventListener("pointerup", (event) => {
      if (state.pointerId === event.pointerId) {
        stop(true);
      }
    });

    resizer.addEventListener("pointercancel", (event) => {
      if (state.pointerId === event.pointerId) {
        stop(false);
      }
    });
  };

  return {
    bind,
    clampWidth,
    currentWidth,
    readStoredWidth,
    setWidth,
  };
};
