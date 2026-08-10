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
	"github.com/mmcdole/gofeed"
)

func TestBrowserSmokeReaderFlowsMenuInteractions(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(1024, 360),
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#topbar-shortcuts-button", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for general menu interactions")
	waitForJS(t, ctx, desktopLayoutExpression(), "compact desktop menu layout")
	runFeedSelectionFlow(t, ctx, fixture)

	runGeneralMenuKeyboardFlow(t, ctx)
	runGeneralMenuOutsideClickFlow(t, ctx)
}

func TestBrowserSmokeReaderFlowsItemActionTypography(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	runActions(
		t,
		ctx,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
		chromedp.WaitVisible("#main-content", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout")
	runFeedSelectionFlow(t, ctx, fixture)

	readableRow := fmt.Sprintf("#item-%d", fixture.secondaryFirstItemID)
	waitForJS(
		t,
		ctx,
		itemActionHierarchyExpression(readableRow+" .item-read-in-app", readableRow+" .item-source-link"),
		"primary reader action typography hierarchy",
	)
}

func TestBrowserSmokeInactiveFeedContentBoundary(t *testing.T) {
	app := newSmokeApp(t)
	feedID := mustUpsertFeed(t, app, "https://example.com/malicious.xml", "Inactive Boundary Feed")
	published := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title: "Inactive Boundary Item",
		Link:  "https://example.com/malicious-item",
		GUID:  "inactive-boundary-item",
		Content: `<p>Inactive boundary smoke content</p>` +
			`<div hx-post="/smoke-unauthorized" hx-trigger="load" hx-swap="none"></div>` +
			`<div data-hx-post="/smoke-unauthorized" data-hx-trigger="every 1ms"></div>`,
		PublishedParsed: &published,
	}})
	items := mustListItems(t, app, feedID)
	assertItemCount(t, items, 1)
	itemID := items[0].ID

	var unauthorizedRequests atomic.Int64
	routes := app.Routes()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/smoke-unauthorized" {
			unauthorizedRequests.Add(1)
			w.WriteHeader(http.StatusNoContent)

			return
		}

		routes.ServeHTTP(w, r)
	})
	server := newSmokeServer(t, handler)
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	runActions(
		t,
		ctx,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
		chromedp.WaitVisible("#main-content", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout")

	feedSelector := fmt.Sprintf(`.feed-link[data-feed-id="%d"]`, feedID)
	clickElement(t, ctx, feedSelector, "select inactive-boundary feed")
	rowSelector := fmt.Sprintf("#item-%d", itemID)
	waitForJS(t, ctx, elementPresentExpression(rowSelector), "inactive-boundary item row")
	clickElement(t, ctx, rowSelector, "open inactive-boundary item")
	waitForJS(t, ctx, contentPanelItemExpression(itemID), "inactive-boundary reader")
	waitForJS(t, ctx, inactiveReaderBoundaryExpression(), "inactive reader boundary")
	runActions(t, ctx, chromedp.Sleep(500*time.Millisecond))

	if got := unauthorizedRequests.Load(); got != 0 {
		t.Fatalf("malicious reader content issued %d unauthorized request(s)", got)
	}
}

func TestBrowserSmokeHiddenSelectionFallback(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
		chromedp.WaitVisible("#main-content", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout")

	runFeedSelectionFlow(t, ctx, fixture)
	runHiddenSelectionFallbackFlow(t, ctx, fixture)
}
