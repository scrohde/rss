//go:build smoke

//nolint:testpackage // Smoke tests intentionally exercise unexported test helpers and wiring.
package server

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestBrowserSmokeReaderFlowsBreakpointTransitions(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)

	var mobileBootstrapRequests atomic.Int64
	var desktopRestoreRequests atomic.Int64
	var mobileSelectorRequests atomic.Int64
	firstMobileStarted := make(chan struct{}, 1)
	firstMobileCanceled := make(chan struct{}, 1)
	releaseFirstMobile := make(chan struct{})
	firstSelectorStarted := make(chan struct{}, 1)
	firstSelectorCanceled := make(chan struct{}, 1)
	releaseFirstSelector := make(chan struct{})
	t.Cleanup(func() {
		close(releaseFirstMobile)
		close(releaseFirstSelector)
	})

	routes := app.Routes()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isHTMX := r.Header.Get("Hx-Request") == "true"
		isMobileBootstrap := r.URL.Path == pathMobileStream && isHTMX &&
			r.Header.Get("Hx-Trigger") != "mobile-stream-feed-filter"
		if isMobileBootstrap {
			requestNumber := mobileBootstrapRequests.Add(1)
			if requestNumber == 1 {
				firstMobileStarted <- struct{}{}
				select {
				case <-releaseFirstMobile:
				case <-r.Context().Done():
					firstMobileCanceled <- struct{}{}

					return
				}
			}
		}
		isMobileSelector := r.URL.Path == pathMobileStream && isHTMX &&
			r.Header.Get("Hx-Trigger") == "mobile-stream-feed-filter"
		if isMobileSelector {
			requestNumber := mobileSelectorRequests.Add(1)
			if requestNumber == 1 {
				firstSelectorStarted <- struct{}{}
				select {
				case <-releaseFirstSelector:
				case <-r.Context().Done():
					firstSelectorCanceled <- struct{}{}

					return
				}
			}
		}
		if r.URL.Path == pathIndex && isHTMX {
			desktopRestoreRequests.Add(1)
		}

		routes.ServeHTTP(w, r)
	})
	server := newSmokeServer(t, handler)
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(1365, 1024),
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for responsive transitions")
	waitForJS(t, ctx, desktopLayoutExpression(), "initial desktop responsive layout")
	runFeedSelectionFlow(t, ctx, fixture)
	runActions(
		t,
		ctx,
		chromedp.Evaluate(`(() => {
			window.__responsiveDocumentMarker = "responsive-reader-transition";
			window.__responsiveErrors = [];
			window.__responsiveHistoryRestores = 0;
			document.body.addEventListener("htmx:historyRestore", () => {
				window.__responsiveHistoryRestores += 1;
			});
			window.addEventListener("error", (event) => {
				window.__responsiveErrors.push(event.message || "window error");
			});
			window.addEventListener("unhandledrejection", (event) => {
				const reason = event.reason;
				window.__responsiveErrors.push(reason && reason.message ? reason.message : String(reason));
			});
			return true;
		})()`, nil),
	)

	runActions(t, ctx, chromedp.EmulateViewport(390, 844))
	select {
	case <-firstMobileStarted:
	case <-time.After(smokeWaitTimeout):
		t.Fatal("mobile bootstrap request did not start after crossing the breakpoint")
	}

	runActions(t, ctx, chromedp.EmulateViewport(1365, 1024))
	select {
	case <-firstMobileCanceled:
	case <-time.After(smokeWaitTimeout):
		t.Fatal("stale mobile bootstrap request was not canceled after returning to desktop")
	}
	waitForJS(
		t,
		ctx,
		feedSelectionSettledExpression(fixture.secondaryFeedID, "Secondary Feed"),
		"desktop reader remains usable after canceling stale mobile transition",
	)
	if got := desktopRestoreRequests.Load(); got != 0 {
		t.Fatalf("expected no redundant desktop restore request, got %d", got)
	}

	runActions(t, ctx, chromedp.EmulateViewport(390, 844))
	waitForJS(t, ctx, responsiveMobileLayoutExpression(0), "live desktop-to-mobile transition")
	waitForJS(t, ctx, htmxSettledExpression(), "desktop-to-mobile HTMX settle")
	if got := mobileBootstrapRequests.Load(); got != 2 {
		t.Fatalf("expected one canceled and one completed mobile bootstrap request, got %d", got)
	}

	runActions(t, ctx, chromedp.EmulateViewport(420, 844), chromedp.Sleep(150*time.Millisecond))
	if got := mobileBootstrapRequests.Load(); got != 2 {
		t.Fatalf("same-layout resize issued a duplicate mobile bootstrap request: got %d", got)
	}

	waitForJS(
		t,
		ctx,
		htmxElementReadyExpression("#mobile-stream-feed-filter"),
		"responsive mobile feed selector ready",
	)
	selectMobileFeedFilter(t, ctx, fixture.primaryFeedID)
	select {
	case <-firstSelectorStarted:
	case <-time.After(smokeWaitTimeout):
		t.Fatal("mobile selector request did not start")
	}

	runActions(t, ctx, chromedp.EmulateViewport(1365, 1024))
	select {
	case <-firstSelectorCanceled:
	case <-time.After(smokeWaitTimeout):
		t.Fatal("in-flight mobile selector request was not canceled at the desktop breakpoint")
	}
	waitForJS(
		t,
		ctx,
		responsiveDesktopLayoutExpression(fixture.primaryFeedID),
		"live mobile-to-desktop transition",
	)
	waitForJS(t, ctx, pathnameExpression(pathIndex), "desktop restore URL")
	waitForJS(t, ctx, htmxSettledExpression(), "mobile-to-desktop HTMX settle")
	if got := desktopRestoreRequests.Load(); got != 1 {
		t.Fatalf("expected one desktop restore request, got %d", got)
	}
	if got := mobileSelectorRequests.Load(); got != 1 {
		t.Fatalf("expected exactly one mobile selector request, got %d", got)
	}

	primaryReaderButton := fmt.Sprintf("#item-%d .item-read-in-app", fixture.primaryFirstItemID)
	clickElement(t, ctx, primaryReaderButton, "open desktop item after responsive restore")
	waitForJS(
		t,
		ctx,
		contentPanelItemExpression(fixture.primaryFirstItemID),
		"desktop content panel after responsive restore",
	)

	runActions(t, ctx, chromedp.EmulateViewport(390, 844))
	waitForJS(
		t,
		ctx,
		responsiveMobileLayoutExpression(fixture.primaryFeedID),
		"second live desktop-to-mobile transition",
	)
	waitForJS(t, ctx, htmxSettledExpression(), "second desktop-to-mobile HTMX settle")
	if got := mobileBootstrapRequests.Load(); got != 3 {
		t.Fatalf("expected one bootstrap request per completed mobile crossing, got %d", got)
	}

	mobileReaderButton := fmt.Sprintf(
		`.mobile-card-open[hx-get^="/mobile/items/%d/reader"]`,
		fixture.primaryFirstItemID,
	)
	clickElement(t, ctx, mobileReaderButton, "open mobile reader before history restore")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "mobile reader before history restore")

	runActions(t, ctx, chromedp.EmulateViewport(1365, 1024))
	waitForJS(
		t,
		ctx,
		responsiveDesktopLayoutExpression(fixture.primaryFeedID),
		"desktop restore before cached history transition",
	)
	waitForJS(t, ctx, htmxSettledExpression(), "desktop restore before history settle")
	if got := desktopRestoreRequests.Load(); got != 2 {
		t.Fatalf("expected second desktop restore request, got %d", got)
	}

	runActions(t, ctx, chromedp.Evaluate(`history.back()`, nil))
	waitForJS(
		t,
		ctx,
		`(() => window.__responsiveHistoryRestores >= 1)()`,
		"cached mobile history restoration observed",
	)
	waitForJS(
		t,
		ctx,
		responsiveDesktopLayoutExpression(fixture.primaryFeedID),
		"cached mobile history reconciles to desktop",
	)
	waitForJS(t, ctx, htmxSettledExpression(), "cached history desktop reconciliation settle")
	if got := desktopRestoreRequests.Load(); got != 3 {
		t.Fatalf("expected cached history to trigger one desktop restore request, got %d", got)
	}
	waitForJS(
		t,
		ctx,
		`(() => window.__responsiveDocumentMarker === "responsive-reader-transition" &&
			Array.isArray(window.__responsiveErrors) && window.__responsiveErrors.length === 0)()`,
		"responsive transitions preserve the document without JavaScript errors",
	)
}
