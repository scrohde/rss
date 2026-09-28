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

func TestBrowserSmokeTodayEmptySetupNavigation(t *testing.T) {
	app := newSmokeApp(t)
	mustUpsertFeed(t, app, "https://example.com/setup.xml", "Setup feed")
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)
	ctx := newSmokeBrowserContext(t)
	runActions(t, ctx, chromedp.EmulateViewport(1365, 900), chromedp.Navigate(server.URL))
	waitForJS(t, ctx, htmxReadyExpression(), "sidebar ready")
	clickElement(t, ctx, ".feed-link[data-feed-id='-1']", "open Today before setup")
	waitForJS(t, ctx, desktopTodayLayoutExpression(), "Today setup has visible reading width")
	waitForJS(t, ctx, textPresentExpression("No recent stories"), "Today works without setup")
}

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

	clickElement(t, ctx, "[data-feed-more-toggle]", "show caught-up feeds in sidebar")
	clickElement(t, ctx, fmt.Sprintf(`.feed-link[data-feed-id="%d"]`, zeroFeedID), "select a feed from Today")
	waitForJS(t, ctx, elementAbsentExpression(`[data-today-view="true"]`), "feed selection exits Today")
	waitForJS(t, ctx, textPresentExpression("Caught Up Smoke Feed"), "selected feed opens")
	clickElement(t, ctx, ".feed-link[data-feed-id='-1']", "return to Today")
	waitForJS(t, ctx, desktopTodayLayoutExpression(), "sidebar persists after HTMX navigation")
	waitForJS(t, ctx, elementPresentExpression(".feed-link[data-feed-id='-1'].active"),
		"Today uses the shared selected feed state")

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
		const today = document.querySelector(".today-view");
		const bounds = today?.getBoundingClientRect();
		return !!app && !!feedPanel && document.querySelector(".today-view") &&
			getComputedStyle(feedPanel).display !== "none" &&
			getComputedStyle(app).display === "grid" && bounds.width > 500 &&
			bounds.left >= 0 && bounds.right <= innerWidth;
	})()`
}

func todayAccessibilityExpression() string {
	return `(() => {
        const today = document.querySelector(".feed-link[data-feed-id='-1']");
        const title = document.querySelector(".feed-link:not([data-feed-id='-1']) .feed-title");
        return !document.querySelector("#today-choose-feeds") && !document.querySelector("#today-all-feeds") &&
            !!today && !!title && !!document.querySelector(".items > .item-list > .item-entry") &&
            Math.abs(title.getBoundingClientRect().left - today.querySelector('.feed-title').getBoundingClientRect().left) < 2;
	})()`
}

func todayMobileAccessibilityExpression() string {
	return `(() => {
		const choose = document.querySelector("#today-choose-feeds");
		const link = document.querySelector("#mobile-today-all-feeds");
		const read = document.querySelector(".today-mobile-card .mobile-card-mark-read");
		return !choose && !!link && link.textContent.trim() === "All feeds" &&
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

//nolint:funlen // Exercises keyboard selection, reader focus, and read-state swaps as one journey.
func TestBrowserSmokeTodayKeyboardShortcuts(t *testing.T) {
	app := newSmokeApp(t)
	feedID := mustUpsertFeed(t, app, "https://example.com/keys.xml", "Keyboard feed")
	now := time.Now().UTC()
	mustUpsertItems(t, app, feedID, []*gofeed.Item{
		newSmokeItem("First story", "https://example.com/first", "first", now.Add(-time.Hour)),
		newSmokeItem("Second story", "https://example.com/second", "second", now.Add(-2*time.Hour)),
	})
	items := mustListItems(t, app, feedID)
	requireNoErr(t, store.SetTodayFeedIDs(context.Background(), app.db, []int64{feedID}), "select feeds: %v")
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)
	ctx := newSmokeBrowserContext(t)
	runActions(t, ctx, chromedp.EmulateViewport(1365, 900), chromedp.Navigate(server.URL+"/today"))
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "Today list receives keyboard focus")
	first := fmt.Sprintf("#item-%d", items[0].ID)
	second := fmt.Sprintf("#item-%d", items[1].ID)
	waitForJS(t, ctx, hasClassExpression(first, "is-active"), "first story is active")
	runActions(t, ctx, chromedp.KeyEvent("j"))
	waitForJS(t, ctx, hasClassExpression(second, "is-active"), "j selects next story")
	runActions(t, ctx, chromedp.KeyEvent("k"))
	waitForJS(t, ctx, hasClassExpression(first, "is-active"), "k selects previous story")
	runActions(t, ctx, chromedp.KeyEvent(kb.ArrowDown))
	waitForJS(t, ctx, hasClassExpression(second, "is-active"), "down selects next story")
	runActions(t, ctx, chromedp.KeyEvent(kb.ArrowUp), chromedp.KeyEvent("l"))
	waitForJS(t, ctx, activeElementMatchesExpression("#content-panel"), "l opens the reader")
	waitForJS(t, ctx, htmxSettledExpression(), "reader expansion settles")
	runActions(t, ctx, chromedp.KeyEvent("h"))
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "h returns to Today")
	waitForJS(t, ctx, htmxSettledExpression(), "reader collapse settles before read shortcut")
	runActions(t, ctx, chromedp.KeyEvent(kb.ArrowLeft))
	waitForJS(t, ctx, activeElementMatchesExpression(".feed-link[data-feed-id='-1']"), "left selects Today in sidebar")
	runActions(t, ctx, chromedp.KeyEvent(kb.ArrowDown))
	waitForJS(t, ctx, elementAbsentExpression(".today-view"), "down opens first feed")
	waitForJS(t, ctx, activeElementMatchesExpression(".feed-link.active"), "first feed receives focus")
	var feedStyle string
	runActions(t, ctx, chromedp.Evaluate(selectedFeedStyleExpression(), &feedStyle))
	runActions(t, ctx, chromedp.KeyEvent(kb.ArrowUp))
	waitForJS(t, ctx, elementPresentExpression(".today-view"), "up returns to Today")
	waitForJS(t, ctx, activeElementMatchesExpression(".feed-link[data-feed-id='-1']"), "Today receives sidebar focus")
	var todayStyle string
	runActions(t, ctx, chromedp.Evaluate(selectedFeedStyleExpression(), &todayStyle))
	if todayStyle != feedStyle {
		t.Fatalf("selected Today style %s differs from feed style %s", todayStyle, feedStyle)
	}
	runActions(t, ctx, chromedp.KeyEvent(kb.ArrowUp), chromedp.KeyEvent(kb.ArrowRight))
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "right returns to Today items")
	waitForJS(t, ctx, `(() => {
		const list = document.querySelector('#item-list');
		const first = list?.querySelector('.item-entry');
		return first?.classList.contains('is-active') && getComputedStyle(list).outlineStyle === 'none' &&
			getComputedStyle(first).boxShadow !== 'none';
	})()`, "Right highlights the first story, without an outline around the list")
	runActions(t, ctx, chromedp.KeyEvent("r"))
	waitForJS(t, ctx, hasClassExpression(first, "is-read"), "r marks the active story read")
	waitForJS(t, ctx, hasClassExpression(second, "is-active"), "read action advances within the batch")
	waitForJS(t, ctx, fmt.Sprintf(`document.querySelector(%q)?.getAttribute("hx-get").includes("today=1")`, first),
		"read-state swap preserves Today context")
	runActions(t, ctx, chromedp.KeyEvent("k"), chromedp.KeyEvent("r"))
	waitForJS(t, ctx, missingClassExpression(first, "is-read"), "r can restore the read story to unread")
	runActions(t, ctx, chromedp.KeyEvent(kb.Enter))
	waitForJS(t, ctx, activeElementMatchesExpression("#content-panel"), "Enter opens the active Today story")
}

func selectedFeedStyleExpression() string {
	return `(() => {
		const style = getComputedStyle(document.querySelector('.feed-link.active'));
		return JSON.stringify(['backgroundColor', 'borderColor', 'borderRadius', 'fontWeight', 'padding', 'color']
			.map(property => style[property]));
	})()`
}

func TestBrowserSmokeTodayFromPopulatedFeed(t *testing.T) {
	for _, recent := range []bool{false, true} {
		t.Run(fmt.Sprintf("recent=%t", recent), func(t *testing.T) {
			smokeTodayFromPopulatedFeed(t, recent)
		})
	}
}

//nolint:funlen,revive // Covers empty and populated Today navigation, focus, and stale reader cleanup.
func smokeTodayFromPopulatedFeed(t *testing.T, recent bool) {
	t.Helper()
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	if recent {
		mustUpsertItems(t, app, fixture.primaryFeedID, []*gofeed.Item{
			newSmokeItem("Recent story", "https://example.com/recent", "recent", time.Now().UTC().Add(-time.Hour)),
		})
	}

	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)
	ctx := newSmokeBrowserContext(t)

	runActions(t, ctx, chromedp.EmulateViewport(1365, 900), chromedp.Navigate(server.URL+"/today"))
	waitForJS(t, ctx, htmxReadyExpression(), "Today ready")
	clickElement(t, ctx, fmt.Sprintf(`.feed-link[data-feed-id="%d"]`, fixture.primaryFeedID), "open populated feed")
	waitForJS(t, ctx, elementAbsentExpression(".today-view"), "feed opens")
	clickElement(t, ctx, ".feed-link[data-feed-id='-1']", "return to Today from populated feed")
	waitForJS(t, ctx, desktopTodayLayoutExpression(), "Today opens from populated feed")
	clickElement(t, ctx, fmt.Sprintf(`.feed-link[data-feed-id="%d"]`, fixture.primaryFeedID), "reopen populated feed")
	waitForJS(t, ctx, elementAbsentExpression(".today-view"), "feed reopens")
	clickElement(t, ctx, ".item-entry .item-read-in-app", "open article reader")
	waitForJS(t, ctx, elementPresentExpression("#content-panel.is-open"), "reader opens")
	clickElement(t, ctx, ".feed-link[data-feed-id='-1']", "return to Today from reader")
	waitForJS(t, ctx, elementPresentExpression(".today-view"), "Today opens from reader")
	waitForJS(t, ctx, elementAbsentExpression("#content-panel.is-open"), "Today closes stale feed reader")
}
