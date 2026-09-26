//go:build smoke

//nolint:testpackage // Smoke tests exercise the rendered Today flow and its browser history.
package server

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/mmcdole/gofeed"

	"rss/internal/store"
)

//nolint:funlen,revive // One browser journey covers Today setup, feed exits, responsive navigation, and history.
func TestBrowserSmokeTodayResponsiveNavigationAndAccessibility(t *testing.T) {
	app := newSmokeApp(t)
	feedID := mustUpsertFeed(t, app, "https://example.com/today-smoke.xml", "Today Smoke Feed")
	zeroFeedID := mustUpsertFeed(t, app, "https://example.com/today-smoke-zero.xml", "Caught Up Smoke Feed")
	itemTime := time.Now().UTC().Add(-time.Hour)
	mustUpsertItems(t, app, feedID, []*gofeed.Item{
		newSmokeItem("Today Smoke Story", "https://example.com/today-smoke-story", "today-smoke", itemTime),
	})
	items := mustListItems(t, app, feedID)
	if len(items) != 1 {
		t.Fatalf("expected one Today smoke story, got %d", len(items))
	}
	err := store.SetTodayFeedIDs(context.Background(), app.db, []int64{feedID})
	if err != nil {
		t.Fatalf("store.SetTodayFeedIDs: %v", err)
	}

	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)
	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(1365, 900),
		chromedp.Navigate(server.URL),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "HTMX ready on the default Today landing")
	waitForJS(t, ctx, requestURIExpression("/today"), "root navigation lands on Today after setup")
	waitForJS(t, ctx, elementPresentExpression(`[data-today-view="true"]`), "desktop Today page rendered")
	waitForJS(t, ctx, desktopTodayLayoutExpression(), "Today uses the available desktop reading width")
	waitForJS(t, ctx, textPresentExpression("Today Smoke Story"), "recent story appears with its source")
	waitForJS(t, ctx, todayAccessibilityExpression(), "setup and feed controls expose accessible names")

	runActions(t, ctx, chromedp.Focus("#today-choose-feeds > summary", chromedp.ByQuery), chromedp.KeyEvent(kb.Enter))
	waitForJS(t, ctx, detailsOpenExpression("#today-choose-feeds"), "keyboard opens Choose feeds")
	runActions(t, ctx, chromedp.Focus("#today-all-feeds > summary", chromedp.ByQuery), chromedp.KeyEvent(kb.Enter))
	waitForJS(t, ctx, detailsOpenExpression("#today-all-feeds"), "keyboard opens All feeds")
	waitForJS(t, ctx, textPresentExpression("Caught Up Smoke Feed"), "All feeds includes a zero-unread subscription")

	feedPath := fmt.Sprintf("/feeds/%d/items?from_today=1", zeroFeedID)
	clickElement(t, ctx, fmt.Sprintf(`.today-feed-link[href="%s"]`, feedPath), "select a feed from Today")
	waitForJS(t, ctx, requestURIExpression(feedPath), "feed choice is pushed as a normal reader URL")
	waitForJS(t, ctx, textPresentExpression("Caught Up Smoke Feed"), "normal reader opens the selected feed")
	waitForJS(t, ctx, elementAbsentExpression(`[data-today-view="true"]`), "normal feed selection exits Today mode")

	runActions(t, ctx, chromedp.EmulateViewport(390, 844), chromedp.Navigate(server.URL+"/today?layout=mobile"))
	waitForJS(t, ctx, htmxReadyExpression(), "HTMX ready on mobile Today reload")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"][data-today-view="true"]`),
		"mobile Today reload keeps Today mode")
	var mobilePathState struct {
		Path  string `json:"path"`
		Batch string `json:"batch"`
	}
	runActions(t, ctx, chromedp.Evaluate(`(() => ({
		path: location.pathname + location.search,
		batch: document.querySelector("[data-today-view='true']")?.dataset.todayBatchIds || "",
	}))()`, &mobilePathState))
	if want := todayPath([]int64{items[0].ID}, true); mobilePathState.Path != want {
		t.Fatalf("mobile Today URL does not match batch: got %q (batch %q), want %q",
			mobilePathState.Path, mobilePathState.Batch, want)
	}
	waitForJS(t, ctx, todayMobileAccessibilityExpression(), "mobile controls expose accessible labels")

	runActions(t, ctx, chromedp.EmulateViewport(1365, 900))
	waitForJS(t, ctx, elementPresentExpression(`[data-today-view="true"]`), "mobile-to-desktop change keeps Today")
	waitForJS(t, ctx, desktopTodayLayoutExpression(), "responsive desktop Today layout settles")
	runActions(t, ctx, chromedp.EmulateViewport(390, 844))
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"][data-today-view="true"]`),
		"desktop-to-mobile change keeps Today")
	waitForJS(t, ctx, requestURIExpression(todayPath([]int64{items[0].ID}, true)),
		"responsive return keeps the mobile Today batch URL")

	clickElement(t, ctx, ".today-mobile-card .mobile-card-open", "open a Today story")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"][data-today-reader="true"]`),
		"mobile story reader keeps Today context")
	waitForJS(t, ctx, todayReaderOriginExpression(items[0].ID), "reader history records its Today stream origin")
	clickElement(t, ctx, ".mobile-reader-back", "return to the Today batch")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"][data-today-view="true"]`),
		"reader back restores the Today batch")
	waitForJS(t, ctx, requestURIExpression(todayPath([]int64{items[0].ID}, true)),
		"reader history returns to the stable Today URL")

	clickElement(t, ctx, "#mobile-today-all-feeds", "show all subscriptions from Today")
	waitForJS(t, ctx, elementPresentExpression(".mobile-all-feed-list"), "mobile All feeds directory rendered")
	waitForJS(t, ctx, textPresentExpression("Caught Up Smoke Feed"), "mobile All feeds includes zero-unread subscription")
	clickElement(t, ctx, fmt.Sprintf("#mobile-all-feed-selection-%d", zeroFeedID), "select mobile feed")
	waitForJS(t, ctx, requestURIExpression(fmt.Sprintf("/mobile/stream?selected_feed_id=%d&view=all", zeroFeedID)),
		"mobile feed choice is pushed into normal reading")
	waitForJS(t, ctx, elementAbsentExpression(".mobile-all-feed-list"), "selected mobile feed exits the directory")
	runActions(t, ctx, chromedp.Evaluate(`window.history.back()`, nil))
	waitForJS(t, ctx, elementPresentExpression(".mobile-all-feed-list"), "browser back restores the mobile All feeds directory")
	waitForJS(t, ctx, requestURIExpression("/mobile/stream?view=all"), "browser back restores the all-feed URL")
}

func desktopTodayLayoutExpression() string {
	return `(() => {
		const app = document.querySelector(".app");
		const feedPanel = document.querySelector(".feed-panel");
		return !!app && !!feedPanel && document.querySelector(".today-view") &&
			getComputedStyle(feedPanel).display === "none" &&
			getComputedStyle(app).display === "grid";
	})()`
}

func todayAccessibilityExpression() string {
	return `(() => {
		const choose = document.querySelector("#today-choose-feeds > summary");
		const fieldset = document.querySelector("#today-choose-feeds fieldset");
		const allFeeds = document.querySelector("#today-all-feeds > summary");
		const source = document.querySelector(".today-item-source");
		return !!choose && choose.textContent.trim() === "Choose feeds" &&
			!!fieldset && !!fieldset.querySelector("legend") && !!allFeeds &&
			allFeeds.textContent.trim() === "All feeds" && !!source && source.textContent.trim().length > 0;
	})()`
}

func todayMobileAccessibilityExpression() string {
	return `(() => {
		const choose = document.querySelector("#today-choose-feeds > summary");
		const link = document.querySelector("#mobile-today-all-feeds");
		const read = document.querySelector(".today-mobile-card .mobile-card-mark-read");
		return !!choose && !!link && link.textContent.trim() === "All feeds" &&
			!!read && read.getAttribute("aria-label") === "Mark Today Smoke Story read" &&
			!!document.querySelector(".today-mobile-header h1");
	})()`
}

func todayReaderOriginExpression(itemID int64) string {
	return fmt.Sprintf(`(() => {
		let origin;
		try { origin = JSON.parse(sessionStorage.getItem("pulse.mobileReaderOrigin.v1")); }
		catch (_error) { return false; }
		return !!origin && origin.streamURL === %q && origin.itemID === %q &&
			origin.readerRequestPath === location.pathname + location.search &&
			!!history.state && history.state.pulseMobileReaderNavigationID === origin.navigationID;
	})()`, todayPath([]int64{itemID}, true), strconv.FormatInt(itemID, 10))
}
