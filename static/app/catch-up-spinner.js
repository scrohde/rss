const validDays = (input) => {
  const days = Number(input.value);
  return input.validity.valid && Number.isSafeInteger(days) && days >= 1 ? days : null;
};

export const syncCatchUpSpinner = (form) => {
  const input = form.querySelector("[data-catch-up-days]");
  const days = validDays(input);
  form.querySelector("[data-catch-up-previous]").textContent = days > 1 ? days - 1 : "";
  form.querySelector("[data-catch-up-next]").textContent = days === null ? "" : days + 1;
  form.querySelector("[data-catch-up-days-unit]").textContent = days === 1 ? "day" : "days";
  form.querySelector('[data-catch-up-step="-1"]').disabled = days === 1;
  input.dataset.digits = input.value.length > 3 ? "long" : "short";
  form.querySelectorAll("[data-catch-up-preset]").forEach((button) => {
    button.setAttribute("aria-pressed", String(Number(button.dataset.catchUpPreset) === days));
  });
};

export const bindCatchUpSpinner = (onChange) => {
  let drag = null;
  let wheelDelta = 0;
  let lastWheel = null;
  let lastWheelTime = 0;
  let animation = null;

  const setDays = (form, value) => {
    const input = form.querySelector("[data-catch-up-days]");
    const previous = validDays(input);
    input.value = String(Math.max(1, value));
    syncCatchUpSpinner(form);
    if (Number(input.value) === previous) return;
    if (!window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      animation?.cancel();
      animation = form.querySelector("[data-catch-up-wheel-numbers]").animate(
        [{ transform: `translateY(${value > previous ? 6 : -6}px)`, opacity: 0.6 },
          { transform: "translateY(0)", opacity: 1 }],
        { duration: 140, easing: "ease-out" }
      );
    }
    onChange(form);
  };

  const stepDays = (form, delta) => {
    const input = form.querySelector("[data-catch-up-days]");
    setDays(form, (validDays(input) || 7) + delta);
  };

  document.addEventListener("click", (event) => {
    const button = event.target.closest("[data-catch-up-step], [data-catch-up-preset]");
    if (!button) return;
    const form = button.closest("[data-catch-up-form]");
    if (button.hasAttribute("data-catch-up-preset")) {
      setDays(form, Number(button.dataset.catchUpPreset));
    } else {
      stepDays(form, Number(button.dataset.catchUpStep) * (event.shiftKey ? 10 : 1));
    }
  });

  document.addEventListener("keydown", (event) => {
    if (!event.target.matches("[data-catch-up-days]")) return;
    const steps = { ArrowUp: 1, ArrowDown: -1, PageUp: 10, PageDown: -10 };
    if (!Object.hasOwn(steps, event.key)) return;
    event.preventDefault();
    stepDays(event.target.closest("[data-catch-up-form]"), steps[event.key] * (event.shiftKey ? 10 : 1));
  });

  document.addEventListener("wheel", (event) => {
    const wheel = event.target.closest("[data-catch-up-wheel]");
    if (!wheel || event.ctrlKey || !event.deltaY) return;
    event.preventDefault();
    if (wheel !== lastWheel || event.timeStamp - lastWheelTime > 200) wheelDelta = 0;
    lastWheel = wheel;
    lastWheelTime = event.timeStamp;
    const scale = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? 100 : 1;
    wheelDelta += event.deltaY * scale;
    const steps = Math.trunc(wheelDelta / 24);
    if (steps) {
      wheelDelta -= steps * 24;
      stepDays(wheel.closest("[data-catch-up-form]"), steps * (event.shiftKey ? 10 : 1));
    }
  }, { passive: false });

  document.addEventListener("pointerdown", (event) => {
    const wheel = event.target.closest("[data-catch-up-wheel]");
    if (!wheel || event.button !== 0) return;
    const form = wheel.closest("[data-catch-up-form]");
    drag = { wheel, form, id: event.pointerId, startY: event.clientY,
      days: validDays(form.querySelector("[data-catch-up-days]")) || 7, captured: false };
  });

  document.addEventListener("pointermove", (event) => {
    if (!drag || drag.id !== event.pointerId) return;
    const steps = Math.trunc((drag.startY - event.clientY) / 12);
    if (!steps && !drag.captured) return;
    if (!drag.captured) {
      drag.wheel.setPointerCapture(event.pointerId);
      drag.captured = true;
    }
    event.preventDefault();
    setDays(drag.form, drag.days + steps);
  });

  const stopDrag = () => { drag = null; };
  document.addEventListener("pointerup", stopDrag);
  document.addEventListener("pointercancel", stopDrag);
  document.addEventListener("lostpointercapture", stopDrag);
  document.addEventListener("close", stopDrag, true);
};
