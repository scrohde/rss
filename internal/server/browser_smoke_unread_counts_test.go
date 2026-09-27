//go:build smoke

//nolint:testpackage // Smoke tests intentionally exercise package fixtures and app wiring.
package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

func TestBrowserSmokeUnreadCountPreferenceAndDisclosure(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	zeroFeedID := mustUpsertFeed(t, app, "https://example.com/zero-unread.xml", "Zero Unread Feed")
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	feedSelector := fmt.Sprintf(`#feed-list .feed-link[data-feed-id="%d"]`, fixture.secondaryFeedID)
	zeroFeedSelector := fmt.Sprintf(`#feed-list .feed-link[data-feed-id="%d"]`, zeroFeedID)

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(1365, 1024),
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
		chromedp.WaitVisible("#main-content", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for unread-count controls")
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop sidebar for unread-count controls")
	waitForJS(
		t,
		ctx,
		feedUnreadBadgeModeExpression(feedSelector, false),
		"quiet feed row with exact count hidden",
	)
	waitForJS(t, ctx, zeroFeedHasNoCountExpression(zeroFeedSelector), "zero-unread feed has no badge")

	movePointerOverElement(t, ctx, feedSelector)
	waitForJS(t, ctx, exactUnreadCountVisibleExpression(feedSelector), "hover reveals exact unread count")
	runActions(t, ctx, chromedp.Evaluate(`document.activeElement.blur()`, nil))
	movePointerTo(t, ctx, 0, 0)

	runActions(t, ctx, chromedp.Focus("#topbar-shortcuts-button", chromedp.ByQuery))
	for range 20 {
		if activeElementMatches(t, ctx, feedSelector) {
			break
		}
		runActions(t, ctx, chromedp.KeyEvent(kb.Tab))
	}
	waitForJS(t, ctx, activeElementMatchesExpression(feedSelector), "keyboard focus reaches unread feed")
	waitForJS(t, ctx, focusVisibleExpression(feedSelector), "keyboard focus indicator on unread feed")
	waitForJS(t, ctx, exactUnreadCountVisibleExpression(feedSelector), "keyboard focus reveals exact unread count")
	waitForJS(t, ctx, elementAbsentExpression(".feed-details"), "feed rows have no info buttons")

	clickElement(t, ctx, "#topbar-shortcuts-button", "open reading preferences menu")
	runActions(t, ctx, chromedp.Focus("#show-unread-counts", chromedp.ByQuery))
	clickElement(t, ctx, "#show-unread-counts", "enable exact unread counts")
	waitForJS(t, ctx, htmxSettledExpression(), "reading preference update settles")
	waitForJS(
		t,
		ctx,
		feedUnreadBadgeModeExpression(feedSelector, true),
		"preference swap shows exact unread counts",
	)
	waitForJS(t, ctx, activeElementMatchesExpression("#show-unread-counts"), "preference toggle keeps focus")
	waitForJS(t, ctx, textPresentExpression("Unread counts are on."), "accessible preference status updates")

	runActions(t, ctx, chromedp.Reload())
	waitForJS(t, ctx, feedUnreadBadgeModeExpression(feedSelector, true), "exact-count preference survives reload")
	waitForJS(t, ctx, checkedExpression("#show-unread-counts"), "menu reflects persisted exact-count preference")

	runActions(t, ctx, chromedp.EmulateViewport(390, 844))
	runActions(t, ctx, chromedp.Navigate(server.URL+pathMobileStream))
	runActions(t, ctx, chromedp.WaitVisible("#mobile-stream-feed-filter", chromedp.ByQuery))
	waitForJS(t, ctx, mobileOptionUnreadCountExpression(fixture.secondaryFeedID, "4 unread"),
		"touch-friendly mobile feed option shows the exact count")
	clickElement(t, ctx, ".mobile-card-open", "open mobile article after preference update")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "mobile reader remains usable")
}

func movePointerOverElement(t *testing.T, ctx context.Context, selector string) {
	t.Helper()

	var point struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	runActions(t, ctx, chromedp.Evaluate(fmt.Sprintf(`(() => {
		const rect = document.querySelector(%q).getBoundingClientRect();
		return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
	})()`, selector), &point))
	movePointerTo(t, ctx, point.X, point.Y)
}

func movePointerTo(t *testing.T, ctx context.Context, x, y float64) {
	t.Helper()

	runActions(t, ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return input.DispatchMouseEvent(input.MouseMoved, x, y).Do(ctx)
	}))
}

func activeElementMatches(t *testing.T, ctx context.Context, selector string) bool {
	t.Helper()

	var matches bool
	err := chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(
		`(() => document.activeElement && document.activeElement.matches(%q))()`,
		selector,
	), &matches))
	if err != nil {
		t.Fatalf("check active element: %v", err)
	}

	return matches
}

func feedUnreadBadgeModeExpression(selector string, exact bool) string {
	return fmt.Sprintf(`(() => {
		const feed = document.querySelector(%q);
		const count = feed && feed.querySelector(".feed-count-exact");
		return !!count && !feed.querySelector(".feed-count-new") &&
			feed.dataset.showExactUnreadCounts === %q &&
			(getComputedStyle(count).visibility === "visible") === %t;
	})()`, selector, fmt.Sprintf("%t", exact), exact)
}

func exactUnreadCountVisibleExpression(selector string) string {
	return fmt.Sprintf(`(() => {
		const count = document.querySelector(%q + " .feed-count-exact");
		return !!count && getComputedStyle(count).visibility === "visible";
	})()`, selector)
}

func zeroFeedHasNoCountExpression(selector string) string {
	return fmt.Sprintf(`(() => {
		const feed = document.querySelector(%q);
		return !!feed && !feed.querySelector(".feed-count");
	})()`, selector)
}

func focusVisibleExpression(selector string) string {
	return fmt.Sprintf(`(() => {
		const feed = document.querySelector(%q);
		return !!feed && feed.matches(":focus-visible");
	})()`, selector)
}

func detailsOpenExpression(selector string) string {
	return fmt.Sprintf(`(() => {
		const details = document.querySelector(%q);
		return !!details && details.open;
	})()`, selector)
}

func checkedExpression(selector string) string {
	return fmt.Sprintf(`(() => {
		const checkbox = document.querySelector(%q);
		return !!checkbox && checkbox.checked;
	})()`, selector)
}

func mobileOptionUnreadCountExpression(feedID int64, count string) string {
	return fmt.Sprintf(`(() => {
		const option = document.querySelector(
			'#mobile-stream-feed-filter option[value="%d"]'
		);
		return !!option && option.textContent.includes(%q);
	})()`, feedID, count)
}
