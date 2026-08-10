//go:build smoke

//nolint:testpackage // Smoke tests intentionally exercise unexported test helpers and wiring.
package server

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

//nolint:funlen,revive // One gesture journey covers rejection, cancellation, in-flight locking, and both endpoints.
func TestBrowserSmokeMobilePullRefreshFlows(t *testing.T) {
	app := newSmokeApp(t)
	feedID := mustUpsertFeed(t, app, "not a valid feed url", "Pull Refresh Feed")
	mustUpsertSingleStory(
		t,
		app,
		feedID,
		"Pull Refresh Story",
		"https://example.com/pull-refresh-story",
		"pull-refresh-story",
		time.Now().UTC().Add(-time.Hour),
	)

	var refreshRequests atomic.Int64
	var forceRefreshFailure atomic.Bool
	refreshStarted := make(chan string, 8)
	releaseRefresh := make(chan struct{}, 8)
	t.Cleanup(func() {
		close(releaseRefresh)
	})

	selectedRefreshPath := fmt.Sprintf(pathMobileFeedRefresh, feedID)
	routes := app.Routes()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && (r.URL.Path == pathMobilePulse || r.URL.Path == selectedRefreshPath) {
			refreshRequests.Add(1)
			refreshStarted <- r.URL.RequestURI()
			<-releaseRefresh
			if r.Context().Err() != nil {
				return
			}
			if forceRefreshFailure.Swap(false) {
				http.Error(w, "forced pull-refresh failure", http.StatusServiceUnavailable)
				return
			}
		}
		routes.ServeHTTP(w, r)
	})
	server := newSmokeServer(t, handler)
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(390, 568),
		chromedp.Navigate(server.URL),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for mobile pull refresh")
	waitForJS(t, ctx, responsiveMobileLayoutExpression(0), "short mobile stream for pull refresh")
	waitForJS(t, ctx, mobilePullRefreshIdleExpression(), "idle pull-refresh indicator")
	waitForJS(t, ctx, mobilePullRootOverscrollBlockedExpression(), "native vertical overscroll disabled")

	streamSelector := `[data-mobile-stream="true"]`
	waitForJS(
		t,
		ctx,
		`(() => {
			const stream = document.querySelector("[data-mobile-stream='true']");
			if (!stream) return false;
			stream.style.minHeight = String(innerHeight + 400) + "px";
			window.scrollTo(0, 160);
			return window.scrollY >= 100;
		})()`,
		"scrollable pull-refresh fixture away from document top",
	)
	dispatchSyntheticTouch(t, ctx, streamSelector, "touchstart", 180, 100, 1)
	assertSyntheticTouchPrevented(
		t,
		ctx,
		streamSelector,
		"touchmove",
		181,
		230,
		1,
		false,
		"vertical pull away from document top",
	)
	dispatchSyntheticTouch(t, ctx, streamSelector, "touchend", 181, 230, 0)
	waitForJS(
		t,
		ctx,
		`(() => {
			const stream = document.querySelector("[data-mobile-stream='true']");
			if (!stream) return false;
			stream.style.removeProperty("min-height");
			window.scrollTo(0, 0);
			return window.scrollY === 0;
		})()`,
		"restore pull-refresh fixture to document top",
	)
	waitForJS(t, ctx, mobilePullRefreshIdleExpression(), "away-from-top pull leaves indicator idle")

	assertSyntheticTouchPrevented(
		t,
		ctx,
		".mobile-card-open",
		"touchstart",
		180,
		100,
		1,
		false,
		"interactive touch start",
	)
	assertSyntheticTouchPrevented(
		t,
		ctx,
		".mobile-card-open",
		"touchmove",
		182,
		220,
		1,
		false,
		"interactive vertical move",
	)
	dispatchSyntheticTouch(t, ctx, ".mobile-card-open", "touchend", 182, 220, 0)

	dispatchSyntheticTouch(t, ctx, streamSelector, "touchstart", 16, 120, 1)
	assertSyntheticTouchPrevented(
		t,
		ctx,
		streamSelector,
		"touchmove",
		116,
		132,
		1,
		false,
		"horizontal edge gesture",
	)
	dispatchSyntheticTouch(t, ctx, streamSelector, "touchend", 116, 132, 0)
	waitForJS(t, ctx, mobilePullRefreshIdleExpression(), "horizontal gesture leaves pull refresh idle")

	dispatchSyntheticTouch(t, ctx, streamSelector, "touchstart", 180, 100, 1)
	assertSyntheticTouchPrevented(
		t,
		ctx,
		streamSelector,
		"touchmove",
		240,
		160,
		1,
		false,
		"diagonal gesture",
	)
	dispatchSyntheticTouch(t, ctx, streamSelector, "touchend", 240, 160, 0)
	waitForJS(t, ctx, mobilePullRefreshIdleExpression(), "diagonal gesture leaves pull refresh idle")

	dispatchSyntheticTouch(t, ctx, streamSelector, "touchstart", 180, 100, 2)
	assertSyntheticTouchPrevented(
		t,
		ctx,
		streamSelector,
		"touchmove",
		182,
		220,
		2,
		false,
		"multitouch gesture",
	)
	dispatchSyntheticTouch(t, ctx, streamSelector, "touchcancel", 182, 220, 0)
	waitForJS(t, ctx, mobilePullRefreshIdleExpression(), "multitouch leaves pull refresh idle")

	dispatchSyntheticTouch(t, ctx, streamSelector, "touchstart", 180, 100, 1)
	assertSyntheticTouchPrevented(
		t,
		ctx,
		streamSelector,
		"touchmove",
		214,
		190,
		1,
		true,
		"fast first vertical move",
	)
	waitForJS(
		t,
		ctx,
		mobilePullRefreshDistanceAtLeastExpression("ready", 64),
		"fast first move claimed with useful travel",
	)
	assertSyntheticTouchPrevented(
		t,
		ctx,
		streamSelector,
		"touchcancel",
		214,
		190,
		0,
		true,
		"fast first move cancellation",
	)
	waitForJS(t, ctx, mobilePullRefreshIdleExpression(), "fast first move cancellation cleans up")

	dispatchSyntheticTouch(t, ctx, streamSelector, "touchstart", 180, 100, 1)
	if dispatchSyntheticTouchWithCancelable(t, ctx, streamSelector, "touchmove", 181, 230, 1, false) {
		t.Fatal("non-cancelable vertical move unexpectedly prevented its default")
	}
	dispatchSyntheticTouch(t, ctx, streamSelector, "touchend", 181, 230, 0)
	waitForJS(t, ctx, mobilePullRefreshIdleExpression(), "non-cancelable move abandons custom refresh")

	dispatchSyntheticTouch(t, ctx, streamSelector, "touchstart", 180, 100, 1)
	assertSyntheticTouchPrevented(
		t,
		ctx,
		streamSelector,
		"touchmove",
		182,
		132,
		1,
		true,
		"claimed sub-threshold pull",
	)
	waitForJS(t, ctx, mobilePullRefreshStateExpression("pulling"), "sub-threshold pulling state")
	waitForJS(
		t,
		ctx,
		mobilePullRefreshDistanceAtLeastExpression("pulling", 24),
		"sub-threshold pull has useful travel",
	)
	assertSyntheticTouchPrevented(
		t,
		ctx,
		streamSelector,
		"touchend",
		182,
		132,
		0,
		true,
		"sub-threshold release",
	)
	waitForJS(t, ctx, mobilePullRefreshIdleExpression(), "sub-threshold pull springs idle")

	dispatchSyntheticTouch(t, ctx, streamSelector, "touchstart", 180, 100, 1)
	assertSyntheticTouchPrevented(
		t,
		ctx,
		streamSelector,
		"touchmove",
		181,
		150,
		1,
		true,
		"claimed pull before cancellation",
	)
	assertSyntheticTouchPrevented(
		t,
		ctx,
		streamSelector,
		"touchcancel",
		181,
		150,
		0,
		true,
		"claimed touch cancellation",
	)
	waitForJS(t, ctx, mobilePullRefreshIdleExpression(), "touch cancellation springs idle")
	if got := refreshRequests.Load(); got != 0 {
		t.Fatalf("non-activating gestures issued %d refresh request(s)", got)
	}

	performSyntheticPullToRefresh(t, ctx, streamSelector)
	if got := awaitRefreshPath(t, refreshStarted); got != pathMobilePulse {
		t.Fatalf("expected all-feeds pull endpoint %q, got %q", pathMobilePulse, got)
	}
	waitForJS(t, ctx, mobilePullRefreshingExpression(), "all-feeds pull refreshing state")

	dispatchSyntheticTouch(t, ctx, streamSelector, "touchstart", 180, 100, 1)
	assertSyntheticTouchPrevented(
		t,
		ctx,
		streamSelector,
		"touchmove",
		181,
		230,
		1,
		false,
		"repeated pull while refresh is pending",
	)
	dispatchSyntheticTouch(t, ctx, streamSelector, "touchend", 181, 230, 0)
	if got := refreshRequests.Load(); got != 1 {
		t.Fatalf("repeated in-flight pull changed request count to %d", got)
	}

	releaseRefresh <- struct{}{}
	waitForJS(t, ctx, mobilePullRefreshCompleteExpression(), "all-feeds pull completion reset")
	waitForJS(t, ctx, htmxSettledExpression(), "all-feeds pull HTMX settle")

	selectMobileFeedFilter(t, ctx, feedID)
	filteredStreamPath := fmt.Sprintf("%s?selected_feed_id=%d", pathMobileStream, feedID)
	waitForJS(t, ctx, requestURIExpression(filteredStreamPath), "filtered stream before selected pull")
	waitForJS(t, ctx, mobilePullRefreshIdleExpression(), "filtered pull-refresh indicator idle")

	performSyntheticPullToRefresh(t, ctx, streamSelector)
	expectedSelectedPath := fmt.Sprintf(pathMobileFeedRefresh+"?selected_feed_id=%d", feedID, feedID)
	if got := awaitRefreshPath(t, refreshStarted); got != expectedSelectedPath {
		t.Fatalf("expected selected-feed pull endpoint %q, got %q", expectedSelectedPath, got)
	}
	waitForJS(t, ctx, mobilePullRefreshingExpression(), "selected-feed pull refreshing state")
	releaseRefresh <- struct{}{}
	waitForJS(t, ctx, mobilePullRefreshCompleteExpression(), "selected-feed pull completion reset")
	waitForJS(t, ctx, requestURIExpression(filteredStreamPath), "selected-feed pull preserves stream URL")
	if got := refreshRequests.Load(); got != 2 {
		t.Fatalf("expected exactly two completed pull refresh requests, got %d", got)
	}

	performSyntheticPullToRefresh(t, ctx, streamSelector)
	if got := awaitRefreshPath(t, refreshStarted); got != expectedSelectedPath {
		t.Fatalf("expected navigation-aborted pull endpoint %q, got %q", expectedSelectedPath, got)
	}
	waitForJS(t, ctx, mobilePullRefreshingExpression(), "navigation-aborted pull refreshing state")
	selectMobileFeedFilter(t, ctx, 0)
	waitForJS(t, ctx, requestURIExpression(pathMobileStream), "filter change replaces pending refresh stream")
	waitForJS(t, ctx, mobilePullRefreshCanceledExpression(), "filter change cancels pending pull refresh")
	releaseRefresh <- struct{}{}
	waitForJS(t, ctx, htmxSettledExpression(), "navigation-aborted pull HTMX settle")
	if got := refreshRequests.Load(); got != 3 {
		t.Fatalf("expected filter change to leave three pull requests, got %d", got)
	}

	forceRefreshFailure.Store(true)
	performSyntheticPullToRefresh(t, ctx, streamSelector)
	if got := awaitRefreshPath(t, refreshStarted); got != pathMobilePulse {
		t.Fatalf("expected forced-failure pull endpoint %q, got %q", pathMobilePulse, got)
	}
	waitForJS(t, ctx, mobilePullRefreshingExpression(), "forced-failure pull refreshing state")
	releaseRefresh <- struct{}{}
	waitForJS(t, ctx, mobilePullRefreshFailedExpression(), "failed pull resets state and lock")
	waitForJS(t, ctx, htmxSettledExpression(), "failed pull HTMX settle")

	performSyntheticPullToRefresh(t, ctx, streamSelector)
	if got := awaitRefreshPath(t, refreshStarted); got != pathMobilePulse {
		t.Fatalf("expected post-failure retry endpoint %q, got %q", pathMobilePulse, got)
	}
	releaseRefresh <- struct{}{}
	waitForJS(t, ctx, mobilePullRefreshCompleteExpression(), "post-failure pull retry completes")
	if got := refreshRequests.Load(); got != 5 {
		t.Fatalf("expected failure and retry to leave five pull requests, got %d", got)
	}

	runActions(
		t,
		ctx,
		emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{
			{Name: "prefers-reduced-motion", Value: "reduce"},
		}),
	)
	waitForJS(t, ctx, mobilePullReducedMotionExpression("idle"), "reduced-motion idle feedback")
	performSyntheticPullToRefresh(t, ctx, streamSelector)
	if got := awaitRefreshPath(t, refreshStarted); got != pathMobilePulse {
		t.Fatalf("expected reduced-motion pull endpoint %q, got %q", pathMobilePulse, got)
	}
	waitForJS(t, ctx, mobilePullRefreshingExpression(), "reduced-motion pull refreshing state")
	waitForJS(t, ctx, mobilePullReducedMotionExpression("refreshing"), "reduced-motion refreshing feedback")
	releaseRefresh <- struct{}{}
	waitForJS(t, ctx, mobilePullRefreshCompleteExpression(), "reduced-motion pull completion reset")
	if got := refreshRequests.Load(); got != 6 {
		t.Fatalf("expected reduced-motion pull to leave six pull requests, got %d", got)
	}

	readerSelector := fmt.Sprintf("#mobile-card-%d .mobile-card-open", mustListItems(t, app, feedID)[0].ID)
	clickElement(t, ctx, readerSelector, "open mobile reader before pull-disabled check")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "reader loaded for pull-disabled check")
	waitForJS(t, ctx, elementAbsentExpression(`[data-mobile-pull-refresh]`), "pull indicator absent in reader")
	dispatchSyntheticTouch(t, ctx, ".mobile-reader-article", "touchstart", 180, 100, 1)
	assertSyntheticTouchPrevented(
		t,
		ctx,
		".mobile-reader-article",
		"touchmove",
		181,
		230,
		1,
		false,
		"reader vertical touch",
	)
	dispatchSyntheticTouch(t, ctx, ".mobile-reader-article", "touchend", 181, 230, 0)
	if got := refreshRequests.Load(); got != 6 {
		t.Fatalf("reader touch changed refresh request count to %d", got)
	}
}
