//go:build smoke

//nolint:testpackage // Smoke tests intentionally exercise unexported test helpers and wiring.
package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func runActions(t *testing.T, ctx context.Context, actions ...chromedp.Action) {
	t.Helper()

	if err := chromedp.Run(ctx, actions...); err != nil {
		t.Fatalf("chromedp run: %v", err)
	}
}

func clickElement(t *testing.T, ctx context.Context, selector, label string) {
	t.Helper()

	clickElementWithDetail(t, ctx, selector, label, 0)
}

func clickPointerElement(t *testing.T, ctx context.Context, selector, label string) {
	t.Helper()

	clickElementWithDetail(t, ctx, selector, label, 1)
}

func clickElementWithDetail(t *testing.T, ctx context.Context, selector, label string, detail int) {
	t.Helper()

	expression := fmt.Sprintf(
		`(() => {
			const el = document.querySelector(%q);
			if (!el || typeof el.click !== "function") {
				return false;
			}
			if (document.querySelector(".htmx-request, .htmx-swapping, .htmx-settling")) {
				return false;
			}
			const htmxSelector = [
				"[hx-get]", "[hx-post]", "[hx-put]", "[hx-delete]", "[hx-patch]",
				"[data-hx-get]", "[data-hx-post]", "[data-hx-put]", "[data-hx-delete]", "[data-hx-patch]",
			].join(",");
			if (el.matches(htmxSelector)) {
				const data = el["htmx-internal-data"];
				if (!data || !Array.isArray(data.listenerInfos) || data.listenerInfos.length === 0) {
					return false;
				}
			}
			if (%d > 0) {
				el.dispatchEvent(new MouseEvent("click", {
					bubbles: true,
					cancelable: true,
					composed: true,
					detail: %d,
					view: window,
				}));
			} else {
				el.click();
			}
			return true;
		})()`,
		selector,
		detail,
		detail,
	)

	waitForJS(t, ctx, expression, label+" on settled HTMX element")
	waitForJS(t, ctx, htmxSettledExpression(), label+" HTMX settle")
}

func navigateToMobileStream(t *testing.T, ctx context.Context, serverURL string) {
	t.Helper()

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(390, 568),
		chromedp.Navigate(serverURL+pathMobileStream),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready after mobile stream navigation")
	waitForJS(t, ctx, responsiveMobileLayoutExpression(0), "mobile stream navigation layout")
	waitForJS(t, ctx, htmxSettledExpression(), "mobile stream navigation HTMX settle")
}

func openMobileReaderAndStashOrigin(
	t *testing.T,
	ctx context.Context,
	itemID, nextItemID int64,
	cardIndex int,
	label string,
) {
	t.Helper()

	selector := fmt.Sprintf("#mobile-card-%d .mobile-card-open", itemID)
	clickElement(t, ctx, selector, "open "+label)
	waitForJS(t, ctx, mobileReaderNavigationStateExpression(itemID), label+" navigation state")
	waitForJS(
		t,
		ctx,
		mobileReaderOriginStashedExpression(itemID, nextItemID, cardIndex),
		label+" stashed origin",
	)
}

func dispatchSyntheticTouch(
	t *testing.T,
	ctx context.Context,
	selector, eventName string,
	clientX, clientY, touchCount int,
) bool {
	t.Helper()

	return dispatchSyntheticTouchWithCancelable(
		t,
		ctx,
		selector,
		eventName,
		clientX,
		clientY,
		touchCount,
		true,
	)
}

func dispatchSyntheticTouchWithCancelable(
	t *testing.T,
	ctx context.Context,
	selector, eventName string,
	clientX, clientY, touchCount int,
	cancelable bool,
) bool {
	t.Helper()

	expression := fmt.Sprintf(
		`(() => {
			const target = document.querySelector(%q);
			if (!target) {
				return { found: false, prevented: false };
			}
			const makeTouch = (identifier, offset) => ({
				identifier,
				target,
				clientX: %d + offset,
				clientY: %d + offset,
				pageX: %d + offset,
				pageY: %d + offset,
				screenX: %d + offset,
				screenY: %d + offset,
			});
			const primaryTouch = makeTouch(37, 0);
			const touches = Array.from(
				{ length: %d },
				(_value, index) => index === 0 ? primaryTouch : makeTouch(37 + index, index * 8),
			);
			const event = new Event(%q, {
				bubbles: true,
				cancelable: %t,
				composed: true,
			});
			Object.defineProperties(event, {
				touches: { value: touches },
				targetTouches: { value: touches },
				changedTouches: { value: [primaryTouch] },
			});
			target.dispatchEvent(event);
			return { found: true, prevented: event.defaultPrevented };
		})()`,
		selector,
		clientX,
		clientY,
		clientX,
		clientY,
		clientX,
		clientY,
		touchCount,
		eventName,
		cancelable,
	)

	var result struct {
		Found     bool `json:"found"`
		Prevented bool `json:"prevented"`
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &result)); err != nil {
		t.Fatalf("dispatch synthetic %s on %s: %v", eventName, selector, err)
	}
	if !result.Found {
		t.Fatalf("dispatch synthetic %s: selector %s was not found", eventName, selector)
	}
	return result.Prevented
}

func assertSyntheticTouchPrevented(
	t *testing.T,
	ctx context.Context,
	selector, eventName string,
	clientX, clientY, touchCount int,
	wantPrevented bool,
	label string,
) {
	t.Helper()

	if got := dispatchSyntheticTouch(t, ctx, selector, eventName, clientX, clientY, touchCount); got != wantPrevented {
		t.Fatalf("%s: synthetic %s defaultPrevented = %t, want %t", label, eventName, got, wantPrevented)
	}
}

func performSyntheticPullToRefresh(t *testing.T, ctx context.Context, streamSelector string) {
	t.Helper()

	dispatchSyntheticTouch(t, ctx, streamSelector, "touchstart", 180, 100, 1)
	assertSyntheticTouchPrevented(
		t,
		ctx,
		streamSelector,
		"touchmove",
		181,
		230,
		1,
		true,
		"armed pull",
	)
	waitForJS(t, ctx, mobilePullRefreshStateExpression("ready"), "armed pull-refresh state")
	assertSyntheticTouchPrevented(
		t,
		ctx,
		streamSelector,
		"touchend",
		181,
		230,
		0,
		true,
		"armed pull release",
	)
}

func awaitRefreshPath(t *testing.T, refreshStarted <-chan string) string {
	t.Helper()

	select {
	case path := <-refreshStarted:
		return path
	case <-time.After(smokeWaitTimeout):
		t.Fatal("timed out waiting for pull-refresh request")
		return ""
	}
}

func scrollCardToViewportOffset(t *testing.T, ctx context.Context, selector string, offset int) {
	t.Helper()

	expression := fmt.Sprintf(
		`(() => {
			const card = document.querySelector(%q);
			if (!card) return false;
			window.scrollTo(0, window.scrollY + card.getBoundingClientRect().top - %d);
			return true;
		})()`,
		selector,
		offset,
	)
	waitForJS(t, ctx, expression, "scroll mobile card to viewport offset")
}

func selectMobileFeedFilter(t *testing.T, ctx context.Context, feedID int64) {
	t.Helper()

	expression := fmt.Sprintf(
		`(() => {
			const select = document.querySelector("#mobile-stream-feed-filter");
			if (!select) {
				return false;
			}
			select.value = %q;
			select.dispatchEvent(new Event("change", { bubbles: true }));
			return select.value === %q;
		})()`,
		fmt.Sprintf("%d", feedID),
		fmt.Sprintf("%d", feedID),
	)

	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &ok)); err != nil {
		t.Fatalf("select mobile feed %d: %v", feedID, err)
	}
	if !ok {
		t.Fatalf("select mobile feed %d: mobile filter not available", feedID)
	}
}

func requestHTMX(t *testing.T, ctx context.Context, method, path, target, selectedItemID string) {
	t.Helper()

	expression := fmt.Sprintf(
		`(() => {
			if (document.querySelector(".htmx-request, .htmx-swapping, .htmx-settling")) {
				return false;
			}
			if (!window.htmx || typeof window.htmx.ajax !== "function") {
				return false;
			}
			window.htmx.ajax(%q, %q, {
				source: document.getElementById("main-content"),
				target: %q,
				swap: "outerHTML",
				values: { selected_item_id: %q },
			});
			return true;
		})()`,
		method,
		path,
		target,
		selectedItemID,
	)

	waitForJS(t, ctx, expression, fmt.Sprintf("start htmx %s %s from settled state", method, path))
	waitForJS(t, ctx, htmxSettledExpression(), fmt.Sprintf("htmx %s %s settle", method, path))
}

func pressKey(t *testing.T, ctx context.Context, key string) {
	t.Helper()

	expression := fmt.Sprintf(
		`(() => {
			if (document.querySelector(".htmx-request, .htmx-swapping, .htmx-settling")) {
				return false;
			}
			const target = document.activeElement || document.body;
			target.dispatchEvent(new KeyboardEvent("keydown", {key: %q, bubbles: true, cancelable: true}));
			target.dispatchEvent(new KeyboardEvent("keyup", {key: %q, bubbles: true, cancelable: true}));
			return true;
		})()`,
		key,
		key,
	)

	waitForJS(t, ctx, expression, fmt.Sprintf("press key %q after HTMX settle", key))
}

func waitForJS(t *testing.T, ctx context.Context, expression, label string) {
	t.Helper()

	deadline := time.Now().Add(smokeWaitTimeout)
	for time.Now().Before(deadline) {
		var matches bool
		err := chromedp.Run(ctx, chromedp.Evaluate(expression, &matches))
		if err == nil && matches {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", label)
}
