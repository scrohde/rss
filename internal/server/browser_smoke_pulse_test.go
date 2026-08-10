//go:build smoke

//nolint:testpackage // Smoke tests intentionally exercise unexported test helpers and wiring.
package server

import (
	"testing"

	"github.com/chromedp/chromedp"
)

func TestBrowserSmokePulseIndicatorFlows(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	seedSmokePulseStatuses(t, app, fixture)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout")
	waitForJS(t, ctx, desktopPulseIndicatorsExpression(fixture), "desktop pulse indicators")

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(320, 568),
		chromedp.Navigate(server.URL),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready after mobile resize")
	waitForJS(t, ctx, mobileLayoutExpression(), "narrow mobile layout")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "mobile stream loaded")

	selectMobileFeedFilter(t, ctx, fixture.secondaryFeedID)
	waitForJS(t, ctx, mobileFlatStreamLayoutExpression(fixture), "narrow mobile flat stream layout")
}
