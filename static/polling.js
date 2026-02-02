(() => {
  const pollers = new Map();

  const triggerPoll = (el) => {
    if (!document.body.contains(el)) {
      stopPoller(el);
      return;
    }
    if (document.visibilityState !== "visible") {
      return;
    }
    if (window.htmx) {
      htmx.trigger(el, "refresh");
    }
  };

  const schedulePoller = (el) => {
    const state = pollers.get(el) || {};
    if (state.timeoutId) {
      clearTimeout(state.timeoutId);
    }
    if (state.intervalId) {
      clearInterval(state.intervalId);
    }

    const now = new Date();
    const msToNext = 60000 - (now.getSeconds() * 1000 + now.getMilliseconds());
    const delay = msToNext >= 0 && msToNext <= 60000 ? msToNext : 0;

    state.timeoutId = setTimeout(() => {
      triggerPoll(el);
      state.intervalId = setInterval(() => triggerPoll(el), 60000);
    }, delay);

    pollers.set(el, state);
  };

  const stopPoller = (el) => {
    const state = pollers.get(el);
    if (!state) {
      return;
    }
    if (state.timeoutId) {
      clearTimeout(state.timeoutId);
    }
    if (state.intervalId) {
      clearInterval(state.intervalId);
    }
    pollers.delete(el);
  };

  const initPollers = (root) => {
    const scope = root || document;
    const nodes = scope.querySelectorAll("[data-poll-aligned='true']");
    nodes.forEach((el) => {
      if (pollers.has(el)) {
        return;
      }
      schedulePoller(el);
    });
  };

  document.addEventListener("htmx:load", (event) => {
    initPollers(event.target);
  });

  document.addEventListener("DOMContentLoaded", () => {
    initPollers(document);
  });

  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState !== "visible") {
      return;
    }
    pollers.forEach((_, el) => {
      schedulePoller(el);
    });
  });
})();
