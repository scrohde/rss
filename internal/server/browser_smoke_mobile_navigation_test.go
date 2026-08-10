//go:build smoke

//nolint:testpackage // Smoke tests intentionally exercise unexported test helpers and wiring.
package server

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"rss/internal/store"
)

//nolint:funlen,revive // One history journey covers cached back, forward, repeat back, and cache-miss recovery.
func TestBrowserSmokeMobileReaderHistoryNavigation(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedMobileReaderNavigationSmokeFixture(t, app)

	var streamRequests atomic.Int64
	var historyRestoreRequests atomic.Int64
	routes := app.Routes()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == pathMobileStream && r.Header.Get("Hx-Request") == "true" {
			if r.Header.Get("Hx-History-Restore-Request") == "true" {
				historyRestoreRequests.Add(1)
			} else {
				streamRequests.Add(1)
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
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for mobile reader history")
	waitForJS(t, ctx, responsiveMobileLayoutExpression(0), "mobile stream for reader history")
	waitForJS(t, ctx, htmxSettledExpression(), "initial mobile stream history settle")
	if got := streamRequests.Load(); got != 1 {
		t.Fatalf("expected one mobile bootstrap stream request, got %d", got)
	}

	const targetIndex = 8
	targetItemID := fixture.itemIDs[targetIndex]
	targetSelector := fmt.Sprintf("#mobile-card-%d", targetItemID)
	scrollCardToViewportOffset(t, ctx, targetSelector, 164)
	waitForJS(t, ctx, mobileCardAtViewportOffsetExpression(targetItemID, 164), "history target card offset")

	clickElement(t, ctx, targetSelector+" .mobile-card-open", "open reader with saved stream origin")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "history reader loaded")
	waitForJS(
		t,
		ctx,
		mobileReaderOriginStoredExpression(
			targetItemID,
			targetIndex,
			fixture.itemIDs[targetIndex-1],
			fixture.itemIDs[targetIndex+1],
		),
		"reader origin stored in history and session storage",
	)
	waitForJS(t, ctx, windowScrollYExpression(0), "reader starts at document top")

	runActions(t, ctx, chromedp.Reload(), chromedp.WaitVisible(`[data-mobile-reader="true"]`, chromedp.ByQuery))
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready after reader reload")
	waitForJS(t, ctx, mobileReaderNavigationStateExpression(targetItemID), "reader reload recovers session origin")

	clickElement(t, ctx, ".mobile-reader-back", "history-backed visible reader back")
	waitForJS(t, ctx, mobileReaderOriginRestoredExpression(targetItemID), "visible back restores stream anchor")
	if got := streamRequests.Load(); got != 1 {
		t.Fatalf("history-backed visible back issued a stream request: got %d total", got)
	}
	if got := historyRestoreRequests.Load(); got != 0 {
		t.Fatalf("cached visible back unexpectedly missed HTMX history: got %d restore requests", got)
	}

	runActions(t, ctx, chromedp.Evaluate(`window.history.forward()`, nil))
	waitForJS(t, ctx, mobileReaderNavigationStateExpression(targetItemID), "forward restores reader navigation state")
	clickElement(t, ctx, ".mobile-reader-back", "visible back after forward history")
	waitForJS(t, ctx, mobileReaderOriginRestoredExpression(targetItemID), "repeat back restores stream anchor")
	if got := streamRequests.Load(); got != 1 {
		t.Fatalf("repeat history-backed visible back issued a stream request: got %d total", got)
	}

	clickElement(t, ctx, targetSelector+" .mobile-card-open", "reopen reader before cache-miss back")
	waitForJS(t, ctx, mobileReaderNavigationStateExpression(targetItemID), "reopened reader navigation state")
	runActions(t, ctx, chromedp.Evaluate(`window.localStorage.removeItem("htmx-history-cache")`, nil))
	clickElement(t, ctx, ".mobile-reader-back", "history cache-miss visible reader back")
	waitForJS(t, ctx, mobileReaderOriginRestoredExpression(targetItemID), "cache miss restores stream anchor")
	if got := historyRestoreRequests.Load(); got != 1 {
		t.Fatalf("expected one HTMX history cache-miss request, got %d", got)
	}
	if got := streamRequests.Load(); got != 1 {
		t.Fatalf("cache-miss history back used the normal stream request path: got %d total", got)
	}
}

//nolint:funlen // One browser journey verifies failed-reader stability and direct-reader fallback semantics.
func TestBrowserSmokeMobileReaderHistoryFallbacks(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedMobileReaderNavigationSmokeFixture(t, app)
	failedItemID := fixture.itemIDs[9]
	failedReaderPath := fmt.Sprintf("/mobile/items/%d/reader", failedItemID)

	var streamRequests atomic.Int64
	failedReaderRequests := make(chan struct{}, 1)
	routes := app.Routes()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == failedReaderPath {
			failedReaderRequests <- struct{}{}
			http.Error(w, "forced reader failure", http.StatusInternalServerError)

			return
		}
		if r.URL.Path == pathMobileStream && r.Header.Get("Hx-Request") == "true" &&
			r.Header.Get("Hx-History-Restore-Request") != "true" {
			streamRequests.Add(1)
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
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for mobile reader fallbacks")
	waitForJS(t, ctx, responsiveMobileLayoutExpression(0), "mobile stream for reader fallbacks")
	waitForJS(t, ctx, htmxSettledExpression(), "initial fallback stream settle")

	failedCardSelector := fmt.Sprintf("#mobile-card-%d", failedItemID)
	scrollCardToViewportOffset(t, ctx, failedCardSelector, 176)
	runActions(t, ctx, chromedp.Evaluate(`window.__failedReaderScrollY = window.scrollY`, nil))
	clickElement(t, ctx, failedCardSelector+" .mobile-card-open", "open forced failed reader")
	select {
	case <-failedReaderRequests:
	case <-time.After(smokeWaitTimeout):
		t.Fatal("forced failed reader request did not arrive")
	}
	waitForJS(t, ctx, htmxSettledExpression(), "failed reader request settles")
	waitForJS(t, ctx, failedReaderPreservesStreamExpression(), "failed reader preserves stream position")

	directItemID := fixture.itemIDs[2]
	directReaderPath := fmt.Sprintf("/mobile/items/%d/reader", directItemID)
	runActions(t, ctx, chromedp.Navigate(server.URL+directReaderPath))
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready on direct mobile reader")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "direct mobile reader loaded")
	waitForJS(t, ctx, directReaderHasNoOriginExpression(), "direct reader has no valid navigation origin")

	requestsBeforeBack := streamRequests.Load()
	clickElement(t, ctx, ".mobile-reader-back", "direct reader server-provided back")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "direct reader back returns to stream")
	waitForJS(t, ctx, pathnameExpression(pathMobileStream), "direct reader back stream URL")
	if got := streamRequests.Load(); got != requestsBeforeBack+1 {
		t.Fatalf("expected direct reader back to issue one normal stream request, got %d after %d", got, requestsBeforeBack)
	}
}

//nolint:funlen,revive // One browser journey covers the ordered continuation fallbacks and failure retention.
func TestBrowserSmokeMobileReaderMarkReadContinuation(t *testing.T) {
	app := newSmokeApp(t)
	base := time.Date(2026, time.January, 5, 12, 0, 0, 0, time.UTC)
	refillFeedID := mustUpsertFeed(
		t,
		app,
		"https://example.com/mobile-reader-continuation-refill.xml",
		"Continuation Refill",
	)
	refillItems := seedMobileAggregateItems(
		t,
		app,
		refillFeedID,
		"Continuation Refill",
		mobileAggregateItemPageLimit+1,
		base,
	)

	var failedMarkItemID atomic.Int64
	routes := app.Routes()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failedItemID := failedMarkItemID.Load()
		if failedItemID > 0 &&
			r.Method == http.MethodPost &&
			r.URL.Path == fmt.Sprintf("/mobile/items/%d/read", failedItemID) {
			http.Error(w, "forced reader mark-read failure", http.StatusServiceUnavailable)
			return
		}
		routes.ServeHTTP(w, r)
	})
	server := newSmokeServer(t, handler)
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	navigateToMobileStream(t, ctx, server.URL)

	refillRemovedID := refillItems[mobileAggregateItemPageLimit-1].ID
	refillTargetID := refillItems[mobileAggregateItemPageLimit].ID
	waitForJS(
		t,
		ctx,
		elementAbsentExpression(fmt.Sprintf("#mobile-card-%d", refillTargetID)),
		"refill continuation target initially outside bounded page",
	)
	scrollCardToViewportOffset(t, ctx, fmt.Sprintf("#mobile-card-%d", refillRemovedID), 176)
	openMobileReaderAndStashOrigin(
		t,
		ctx,
		refillRemovedID,
		0,
		mobileAggregateItemPageLimit-1,
		"page-refill reader",
	)
	clickElement(t, ctx, ".mobile-reader-mark-read", "mark page-refill reader item read")
	waitForJS(
		t,
		ctx,
		mobileReaderMarkReadContinuationExpression(refillRemovedID, refillTargetID),
		"newly refilled card occupies removed card position",
	)
	waitForJS(t, ctx, requestURIExpression(pathMobileStream), "page-refill continuation stream URL")

	crossSourceFeedID := mustUpsertFeed(
		t,
		app,
		"https://example.com/mobile-reader-continuation-source.xml",
		"Continuation Source",
	)
	crossTargetFeedID := mustUpsertFeed(
		t,
		app,
		"https://example.com/mobile-reader-continuation-target.xml",
		"Continuation Target",
	)
	mustUpsertSingleStory(
		t,
		app,
		crossSourceFeedID,
		"Cross-feed Source",
		"https://example.com/cross-feed-source",
		"cross-feed-source",
		base.Add(time.Hour),
	)
	mustUpsertSingleStory(
		t,
		app,
		crossTargetFeedID,
		"Cross-feed Target",
		"https://example.com/cross-feed-target",
		"cross-feed-target",
		base.Add(2*time.Hour),
	)
	crossSourceItemID := mustListItems(t, app, crossSourceFeedID)[0].ID
	crossTargetItemID := mustListItems(t, app, crossTargetFeedID)[0].ID
	err := store.UpdateFeedOrder(
		context.Background(),
		app.db,
		[]int64{crossSourceFeedID, crossTargetFeedID, refillFeedID},
	)
	requireNoErr(t, err, "store.UpdateFeedOrder cross-feed continuation: %v")

	navigateToMobileStream(t, ctx, server.URL)
	scrollCardToViewportOffset(t, ctx, fmt.Sprintf("#mobile-card-%d", crossSourceItemID), 176)
	openMobileReaderAndStashOrigin(
		t,
		ctx,
		crossSourceItemID,
		crossTargetItemID,
		0,
		"cross-feed reader",
	)
	clickElement(t, ctx, ".mobile-reader-mark-read", "mark final item in first feed section read")
	waitForJS(
		t,
		ctx,
		mobileReaderMarkReadContinuationExpression(crossSourceItemID, crossTargetItemID),
		"next feed continues at removed section position",
	)
	waitForJS(
		t,
		ctx,
		elementAbsentExpression(fmt.Sprintf("#mobile-feed-section-%d", crossSourceFeedID)),
		"caught-up source section removed",
	)

	previousFeedID := mustUpsertFeed(
		t,
		app,
		"https://example.com/mobile-reader-continuation-previous.xml",
		"Continuation Previous",
	)
	previousItems := seedMobileAggregateItems(
		t,
		app,
		previousFeedID,
		"Continuation Previous",
		mobileAggregateItemPageLimit,
		base.Add(3*time.Hour),
	)
	navigateToMobileStream(t, ctx, server.URL)
	selectMobileFeedFilter(t, ctx, previousFeedID)
	previousStreamPath := fmt.Sprintf("%s?selected_feed_id=%d", pathMobileStream, previousFeedID)
	waitForJS(t, ctx, requestURIExpression(previousStreamPath), "previous-fallback selected stream")

	previousRemovedID := previousItems[len(previousItems)-1].ID
	previousTargetID := previousItems[len(previousItems)-2].ID
	scrollCardToViewportOffset(t, ctx, fmt.Sprintf("#mobile-card-%d", previousRemovedID), 176)
	openMobileReaderAndStashOrigin(
		t,
		ctx,
		previousRemovedID,
		0,
		len(previousItems)-1,
		"previous-fallback reader",
	)
	clickElement(t, ctx, ".mobile-reader-mark-read", "mark final selected-feed reader item read")
	waitForJS(
		t,
		ctx,
		mobileReaderMarkReadContinuationExpression(previousRemovedID, previousTargetID),
		"previous card fallback clamps near document bottom",
	)
	waitForJS(t, ctx, requestURIExpression(previousStreamPath), "previous fallback preserves selected feed URL")

	emptyFeedID := mustUpsertFeed(
		t,
		app,
		"https://example.com/mobile-reader-continuation-empty.xml",
		"Continuation Empty",
	)
	mustUpsertSingleStory(
		t,
		app,
		emptyFeedID,
		"Empty-stream Source",
		"https://example.com/empty-stream-source",
		"empty-stream-source",
		base.Add(4*time.Hour),
	)
	emptyItemID := mustListItems(t, app, emptyFeedID)[0].ID
	navigateToMobileStream(t, ctx, server.URL)
	selectMobileFeedFilter(t, ctx, emptyFeedID)
	emptyStreamPath := fmt.Sprintf("%s?selected_feed_id=%d", pathMobileStream, emptyFeedID)
	openMobileReaderAndStashOrigin(t, ctx, emptyItemID, 0, 0, "empty-stream reader")
	clickElement(t, ctx, ".mobile-reader-mark-read", "mark only selected-feed reader item read")
	waitForJS(
		t,
		ctx,
		mobileReaderEmptyMarkReadContinuationExpression(emptyItemID),
		"empty stream continuation resets to top",
	)
	waitForJS(t, ctx, requestURIExpression(emptyStreamPath), "empty continuation preserves selected feed URL")

	failureFeedID := mustUpsertFeed(
		t,
		app,
		"https://example.com/mobile-reader-continuation-failure.xml",
		"Continuation Failure",
	)
	mustUpsertSingleStory(
		t,
		app,
		failureFeedID,
		"Failure Source",
		"https://example.com/failure-source",
		"failure-source",
		base.Add(5*time.Hour),
	)
	failureItemID := mustListItems(t, app, failureFeedID)[0].ID
	failedMarkItemID.Store(failureItemID)
	navigateToMobileStream(t, ctx, server.URL)
	selectMobileFeedFilter(t, ctx, failureFeedID)
	failureStreamPath := fmt.Sprintf("%s?selected_feed_id=%d", pathMobileStream, failureFeedID)
	openMobileReaderAndStashOrigin(t, ctx, failureItemID, 0, 0, "failed mark-read reader")
	failureReaderPath := fmt.Sprintf(
		"/mobile/items/%d/reader?selected_feed_id=%d",
		failureItemID,
		failureFeedID,
	)
	clickElement(t, ctx, ".mobile-reader-mark-read", "force reader mark-read failure")
	waitForJS(
		t,
		ctx,
		mobileReaderMarkReadFailurePreservesOriginExpression(failureItemID),
		"failed mark-read retains reader origin",
	)
	waitForJS(t, ctx, requestURIExpression(failureReaderPath), "failed mark-read keeps reader URL")

	failureItem, err := store.GetItem(context.Background(), app.db, failureItemID)
	requireNoErr(t, err, "store.GetItem failed reader mark-read item: %v")
	if failureItem.IsRead {
		t.Fatal("failed reader mark-read unexpectedly persisted read state")
	}

	clickElement(t, ctx, ".mobile-reader-back", "back after failed reader mark-read")
	waitForJS(
		t,
		ctx,
		elementPresentExpression(`[data-mobile-stream="true"]`),
		"failed mark-read back restores stream",
	)
	waitForJS(t, ctx, requestURIExpression(failureStreamPath), "failed mark-read back stream URL")
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", failureItemID)),
		"failed mark-read back restores unread card",
	)
	waitForJS(
		t,
		ctx,
		mobileReaderOriginRestoredAtTopExpression(failureItemID),
		"failed mark-read origin still restores stream position",
	)
}
