//go:build smoke

//nolint:testpackage // Smoke tests intentionally exercise unexported test helpers and wiring.
package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/mmcdole/gofeed"

	"rss/internal/store"
)

func TestBrowserSmokeMobileReaderFlows(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, mobileLayoutExpression(), "mobile layout")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "mobile stream loaded")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "mobile stream URL")
	runMobileSummaryOnlyPreviewFlow(t, ctx, fixture)
	waitForJS(
		t,
		ctx,
		textPresentExpression("Secondary One"),
		"secondary item present in mobile stream before reading",
	)

	aggregateState := mobileAggregateState{}
	firstReaderSelector := fmt.Sprintf("#mobile-card-%d .mobile-card-open", fixture.secondaryFirstItemID)
	clickElement(t, ctx, firstReaderSelector, "open first mobile aggregate reader")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "mobile reader loaded")
	waitForJS(
		t,
		ctx,
		requestURIExpression(mobileReaderItemPath(fixture.secondaryFirstItemID, 0, aggregateState)),
		"mobile reader URL",
	)
	waitForJS(t, ctx, textPresentExpression("Secondary One"), "reader title present")

	clickElement(t, ctx, ".mobile-reader-mark-read", "mark first aggregate reader item read")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "stream returns after mark read")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "stream URL after mark read")
	waitForJS(
		t,
		ctx,
		textAbsentExpression("Secondary One"),
		"marked item removed from mobile unread stream",
	)

	secondReaderSelector := fmt.Sprintf("#mobile-card-%d .mobile-card-open", fixture.secondarySecondItemID)
	clickElement(t, ctx, secondReaderSelector, "open second mobile aggregate reader")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "reader can open another item")
	waitForJS(
		t,
		ctx,
		requestURIExpression(mobileReaderItemPath(fixture.secondarySecondItemID, 0, aggregateState)),
		"second reader URL",
	)
	runActions(t, ctx, chromedp.Evaluate(`history.back()`, nil))
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "back returns to stream")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "stream URL after history back")
}

//nolint:funlen,revive // One browser journey verifies lazy paging, history, reader back, and local read updates.
func TestBrowserSmokeMobileAggregateFlows(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedMobileAggregateSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, mobileLayoutExpression(), "mobile layout")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "mobile aggregate loaded")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "mobile aggregate URL")
	waitForJS(
		t,
		ctx,
		mobileAggregateSectionOrderExpression(fixture.highFeedID, fixture.laterFeedID),
		"mobile aggregate saved feed order",
	)
	waitForJS(t, ctx, mobileAggregateCompactSectionsExpression(), "mobile aggregate compact feed sections")
	waitForJS(
		t,
		ctx,
		elementAbsentExpression(fmt.Sprintf("#mobile-feed-section-%d", fixture.quietFeedID)),
		"caught-up mobile aggregate feed skipped",
	)
	waitForJS(
		t,
		ctx,
		elementAbsentExpression(fmt.Sprintf("#mobile-card-%d", fixture.highOldestItemID)),
		"first feed overflow item initially lazy",
	)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.laterItemID)),
		"later feed reachable before loading first feed backlog",
	)
	waitForJS(
		t,
		ctx,
		mobileAggregateBoundedExpression(mobileAggregateFeedPageLimit, mobileAggregateItemPageLimit),
		"initial aggregate DOM bounds",
	)

	olderButton := fmt.Sprintf("#mobile-feed-section-%d [data-mobile-feed-next]", fixture.highFeedID)
	clickElement(t, ctx, olderButton, "load older page from high-volume feed")
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.highOldestItemID)),
		"older high-volume item loaded",
	)
	waitForJS(
		t,
		ctx,
		elementAbsentExpression(fmt.Sprintf("#mobile-card-%d", fixture.highNewestItemID)),
		"newest high-volume page replaced",
	)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.laterItemID)),
		"later feed remains mounted after first-feed continuation",
	)
	waitForJS(
		t,
		ctx,
		mobileAggregateBoundedExpression(mobileAggregateFeedPageLimit, mobileAggregateItemPageLimit),
		"continued aggregate DOM bounds",
	)
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression(fmt.Sprintf("#mobile-feed-section-%d", fixture.highFeedID)),
		"continued feed section receives focus",
	)

	readerSelector := fmt.Sprintf("#mobile-card-%d .mobile-card-open", fixture.highOldestItemID)
	readerPath := mobileReaderItemPath(fixture.highOldestItemID, 0, fixture.olderState)

	clickElement(t, ctx, readerSelector, "open older aggregate item reader")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "aggregate reader loaded")
	waitForJS(t, ctx, requestURIExpression(readerPath), "aggregate reader URL preserves page state")

	runActions(t, ctx, chromedp.Evaluate(`history.back()`, nil))
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "history restores aggregate stream")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "history restores aggregate stream URL")
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.highOldestItemID)),
		"history restores older first-feed page",
	)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.laterItemID)),
		"history restores later feed section",
	)

	clickElement(t, ctx, readerSelector, "reopen older aggregate item reader")
	waitForJS(t, ctx, requestURIExpression(readerPath), "reopened aggregate reader URL")
	clickElement(t, ctx, ".mobile-reader-back", "aggregate reader back button")

	streamPath := mobileStreamStatePath(0, fixture.olderState)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(`[data-mobile-stream="true"]`),
		"aggregate reader back returns to stream",
	)
	waitForJS(t, ctx, requestURIExpression(streamPath), "aggregate reader back preserves page state")
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.highOldestItemID)),
		"reader back preserves older first-feed page",
	)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.laterItemID)),
		"reader back preserves later feed reachability",
	)

	markReadSelector := fmt.Sprintf("#mobile-card-%d .mobile-card-mark-read", fixture.highOldestItemID)
	clickElement(t, ctx, markReadSelector, "mark older aggregate item read")
	waitForJS(
		t,
		ctx,
		elementAbsentExpression(fmt.Sprintf("#mobile-card-%d", fixture.highOldestItemID)),
		"marked aggregate item removed",
	)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-feed-section-%d", fixture.highFeedID)),
		"owning section remains on its current page",
	)
	waitForJS(
		t,
		ctx,
		textPresentExpression("No unread items remain on this page."),
		"empty aggregate item page state",
	)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.laterItemID)),
		"section-local mark read leaves later feed mounted",
	)
	waitForJS(
		t,
		ctx,
		elementAbsentExpression(fmt.Sprintf("#mobile-card-%d", fixture.highNewestItemID)),
		"section-local mark read does not reset to newest page",
	)
	waitForJS(t, ctx, mobileFilterValueExpression(0), "aggregate filter remains All feeds")
	waitForJS(t, ctx, requestURIExpression(streamPath), "aggregate mark read retains stream URL")
	waitForJS(
		t,
		ctx,
		mobileAggregateBoundedExpression(mobileAggregateFeedPageLimit, mobileAggregateItemPageLimit),
		"aggregate DOM stays bounded after mark read",
	)
}

func TestBrowserSmokeMobileTopBarFlows(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, mobileLayoutExpression(), "mobile layout")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "mobile stream loaded")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "mobile stream URL")

	runMobileReaderTopBarFlow(t, ctx, fixture)
}

func TestBrowserSmokeMobileFilteredFeedFlows(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, mobileLayoutExpression(), "mobile layout")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "mobile stream loaded")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "mobile stream URL")

	runMobileFilteredHistoryFlow(t, ctx, fixture)
	runMobileFilteredEmptyStateFlow(t, ctx, fixture)
}

//nolint:funlen // One browser journey verifies the mobile scroll owner across stream, reader, and breakpoint swaps.
func TestBrowserSmokeMobileDocumentScrolling(t *testing.T) {
	app := newSmokeApp(t)
	feedID := mustUpsertFeed(t, app, "https://example.com/mobile-document-scroll.xml", "Document Scroll")
	base := time.Date(2026, time.January, 3, 12, 0, 0, 0, time.UTC)
	feedItems := make([]*gofeed.Item, 0, mobileAggregateItemPageLimit)
	for index := range mobileAggregateItemPageLimit {
		item := newSmokeItem(
			fmt.Sprintf("Document Scroll %02d", index+1),
			fmt.Sprintf("https://example.com/mobile-document-scroll/%d", index+1),
			fmt.Sprintf("mobile-document-scroll-%d", index+1),
			base.Add(-time.Duration(index)*time.Minute),
		)
		if index == 0 {
			item.Content = strings.Repeat(
				"<p>Long mobile reader content keeps the document scroll surface active.</p>",
				40,
			)
		}
		feedItems = append(feedItems, item)
	}
	mustUpsertItems(t, app, feedID, feedItems)
	items := mustListItems(t, app, feedID)
	assertItemCount(t, items, mobileAggregateItemPageLimit)
	firstItemID := items[0].ID

	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(1365, 1024),
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for document scroll flow")
	waitForJS(t, ctx, desktopLayoutExpression(), "initial desktop layout for document scroll flow")

	runActions(t, ctx, chromedp.EmulateViewport(390, 568))
	waitForJS(t, ctx, responsiveMobileLayoutExpression(0), "live desktop-to-mobile scroll transition")
	waitForJS(t, ctx, htmxSettledExpression(), "desktop-to-mobile scroll transition settle")
	waitForJS(
		t,
		ctx,
		mobileDocumentScrollSurfaceExpression(firstItemID),
		"mobile stream uses the document scroll surface",
	)
	runActions(t, ctx, chromedp.Evaluate(`window.scrollTo(0, document.scrollingElement.scrollHeight)`, nil))
	waitForJS(t, ctx, mobileDocumentScrolledExpression(), "mobile stream document scroll")

	firstReaderSelector := fmt.Sprintf("#mobile-card-%d .mobile-card-open", firstItemID)
	clickElement(t, ctx, firstReaderSelector, "open long mobile reader")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "long mobile reader loaded")
	waitForJS(t, ctx, mobileDocumentScrollSurfaceExpression(0), "mobile reader uses the document scroll surface")
	runActions(t, ctx, chromedp.Evaluate(`window.scrollTo(0, document.scrollingElement.scrollHeight)`, nil))
	waitForJS(t, ctx, mobileDocumentScrolledExpression(), "mobile reader document scroll")

	runActions(t, ctx, chromedp.EmulateViewport(1365, 1024))
	waitForJS(t, ctx, responsiveDesktopLayoutExpression(feedID), "live mobile-to-desktop scroll transition")
	waitForJS(t, ctx, htmxSettledExpression(), "mobile-to-desktop scroll transition settle")
	waitForJS(t, ctx, desktopPanelScrollSurfaceExpression(), "desktop panel scroll surfaces remain intact")
}

func TestBrowserSmokeMobileReaderLargeTextDoesNotOverflow(t *testing.T) {
	app := newSmokeApp(t)
	feedID := mustUpsertFeed(
		t,
		app,
		"https://example.com/mobile-large-text.xml",
		"MobileLargeTextFeedNameWithoutBreaks",
	)
	title := strings.Repeat("LargeReaderTitle", 12)
	item := newSmokeItem(
		title,
		"https://example.com/mobile-large-text",
		"mobile-large-text",
		time.Date(2026, time.January, 4, 12, 0, 0, 0, time.UTC),
	)
	item.Content = fmt.Sprintf("<p>%s</p>", strings.Repeat("ReaderBodyWord", 24))
	mustUpsertItems(t, app, feedID, []*gofeed.Item{item})
	itemID := mustListItems(t, app, feedID)[0].ID

	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)
	ctx := newSmokeBrowserContext(t)
	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for large mobile reader text")
	waitForJS(t, ctx, responsiveMobileLayoutExpression(0), "mobile stream for large reader text")
	clickElement(
		t,
		ctx,
		fmt.Sprintf("#mobile-card-%d .mobile-card-open", itemID),
		"open large-text mobile reader",
	)
	waitForJS(t, ctx, mobileReaderLargeTextFitsExpression(), "large mobile reader text stays within viewport")
}

//nolint:funlen,revive // One selector journey covers failed, aborted, successful, and history-restored scroll.
func TestBrowserSmokeMobileFeedSelectionScrollReset(t *testing.T) {
	app := newSmokeApp(t)
	firstFeedID := mustUpsertFeed(t, app, "https://example.com/mobile-selection-first.xml", "Selection First")
	secondFeedID := mustUpsertFeed(t, app, "https://example.com/mobile-selection-second.xml", "Selection Second")
	base := time.Date(2026, time.January, 6, 12, 0, 0, 0, time.UTC)

	seedSelectionFeed := func(feedID int64, title, slug string) []int64 {
		t.Helper()

		items := make([]*gofeed.Item, 0, mobileAggregateItemPageLimit)
		for index := range mobileAggregateItemPageLimit {
			itemTitle := fmt.Sprintf("%s %02d", title, index+1)
			items = append(
				items,
				newSmokeItem(
					itemTitle,
					fmt.Sprintf("https://example.com/%s/%d", slug, index+1),
					fmt.Sprintf("%s-%d", slug, index+1),
					base.Add(-time.Duration(index)*time.Minute),
				),
			)
		}
		mustUpsertItems(t, app, feedID, items)
		storedItems := mustListItems(t, app, feedID)
		assertItemCount(t, storedItems, mobileAggregateItemPageLimit)
		itemIDs := make([]int64, 0, len(storedItems))
		for _, item := range storedItems {
			itemIDs = append(itemIDs, item.ID)
		}
		return itemIDs
	}

	firstItems := seedSelectionFeed(firstFeedID, "Selection First", "mobile-selection-first")
	seedSelectionFeed(secondFeedID, "Selection Second", "mobile-selection-second")
	err := store.UpdateFeedOrder(context.Background(), app.db, []int64{firstFeedID, secondFeedID})
	requireNoErr(t, err, "store.UpdateFeedOrder mobile selection fixture: %v")

	var selectorRequests atomic.Int64
	var failFirstSelection atomic.Bool
	var blockSecondSelection atomic.Bool
	failFirstSelection.Store(true)
	blockSecondSelection.Store(true)
	failedSelectionSeen := make(chan struct{}, 1)
	blockedSelectionStarted := make(chan struct{}, 1)
	blockedSelectionCanceled := make(chan struct{}, 1)
	releaseBlockedSelection := make(chan struct{})
	t.Cleanup(func() {
		close(releaseBlockedSelection)
	})

	routes := app.Routes()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isSelectorRequest := r.URL.Path == pathMobileStream &&
			r.Header.Get("Hx-Request") == "true" &&
			r.Header.Get("Hx-Trigger") == "mobile-stream-feed-filter"
		if isSelectorRequest {
			selectorRequests.Add(1)
			selectedFeedID := r.URL.Query().Get("selected_feed_id")
			if selectedFeedID == strconv.FormatInt(firstFeedID, 10) && failFirstSelection.Swap(false) {
				failedSelectionSeen <- struct{}{}
				http.Error(w, "forced mobile selector failure", http.StatusServiceUnavailable)
				return
			}
			if selectedFeedID == strconv.FormatInt(secondFeedID, 10) && blockSecondSelection.Swap(false) {
				blockedSelectionStarted <- struct{}{}
				select {
				case <-releaseBlockedSelection:
				case <-r.Context().Done():
					blockedSelectionCanceled <- struct{}{}
					return
				}
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
		chromedp.Navigate(server.URL+pathMobileStream),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for mobile selector scroll reset")
	waitForJS(t, ctx, responsiveMobileLayoutExpression(0), "long aggregate selector fixture")
	waitForJS(t, ctx, mobileDocumentScrollSurfaceExpression(firstItems[0]), "selector document scroll surface")
	waitForJS(
		t,
		ctx,
		`(() => {
			window.scrollTo(0, 640);
			window.__mobileSelectionScrollY = window.scrollY;
			return window.__mobileSelectionScrollY >= 300;
		})()`,
		"remember aggregate scroll before rejected selections",
	)

	selectMobileFeedFilter(t, ctx, firstFeedID)
	select {
	case <-failedSelectionSeen:
	case <-time.After(smokeWaitTimeout):
		t.Fatal("failed selector request did not reach the server")
	}
	waitForJS(t, ctx, htmxSettledExpression(), "failed selector request settles")
	waitForJS(t, ctx, mobileSelectionFailurePreservesScrollExpression(), "failed selection preserves stream scroll")

	selectMobileFeedFilter(t, ctx, secondFeedID)
	select {
	case <-blockedSelectionStarted:
	case <-time.After(smokeWaitTimeout):
		t.Fatal("blocked selector request did not start")
	}
	runActions(
		t,
		ctx,
		chromedp.Evaluate(`htmx.trigger(
			document.getElementById("mobile-stream-feed-filter"),
			"htmx:abort",
		)`, nil),
	)
	select {
	case <-blockedSelectionCanceled:
	case <-time.After(smokeWaitTimeout):
		t.Fatal("blocked selector request was not canceled")
	}
	waitForJS(t, ctx, htmxSettledExpression(), "aborted selector request settles")
	waitForJS(t, ctx, mobileSelectionFailurePreservesScrollExpression(), "aborted selection preserves stream scroll")

	firstStreamPath := fmt.Sprintf("%s?selected_feed_id=%d", pathMobileStream, firstFeedID)
	selectMobileFeedFilter(t, ctx, firstFeedID)
	waitForJS(t, ctx, requestURIExpression(firstStreamPath), "all-to-selected stream URL")
	waitForJS(t, ctx, htmxSettledExpression(), "all-to-selected stream settles")
	waitForJS(t, ctx, mobileStreamAtTrueTopExpression(firstFeedID), "all-to-selected stream starts at top")

	historyItem := firstItems[mobileAggregateItemPageLimit/2]
	historySelector := fmt.Sprintf("#mobile-card-%d .mobile-card-open", historyItem)
	scrollCardToViewportOffset(t, ctx, fmt.Sprintf("#mobile-card-%d", historyItem), 164)
	waitForJS(t, ctx, mobileCardAtViewportOffsetExpression(historyItem, 164), "selected history card offset")
	clickElement(t, ctx, historySelector, "open reader from selected stream")
	waitForJS(t, ctx, mobileReaderNavigationStateExpression(historyItem), "selected reader history state")
	runActions(t, ctx, chromedp.Evaluate(`history.back()`, nil))
	waitForJS(t, ctx, mobileReaderOriginRestoredExpression(historyItem), "history restores selected stream offset")

	secondStreamPath := fmt.Sprintf("%s?selected_feed_id=%d", pathMobileStream, secondFeedID)
	selectMobileFeedFilter(t, ctx, secondFeedID)
	waitForJS(t, ctx, requestURIExpression(secondStreamPath), "selected-to-selected stream URL")
	waitForJS(t, ctx, htmxSettledExpression(), "selected-to-selected stream settles")
	waitForJS(t, ctx, mobileStreamAtTrueTopExpression(secondFeedID), "selected-to-selected stream starts at top")

	runActions(t, ctx, chromedp.Evaluate(`window.scrollTo(0, 520)`, nil))
	waitForJS(t, ctx, mobileDocumentScrolledExpression(), "second selected stream is scrolled")
	selectMobileFeedFilter(t, ctx, 0)
	waitForJS(t, ctx, requestURIExpression(pathMobileStream), "selected-to-all stream URL")
	waitForJS(t, ctx, htmxSettledExpression(), "selected-to-all stream settles")
	waitForJS(t, ctx, mobileStreamAtTrueTopExpression(0), "selected-to-all stream starts at top")

	if got := selectorRequests.Load(); got != 5 {
		t.Fatalf("expected five selector requests without duplicates, got %d", got)
	}
}
