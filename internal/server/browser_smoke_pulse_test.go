//go:build smoke

//nolint:testpackage // Smoke tests intentionally exercise unexported test helpers and wiring.
package server

import (
	"fmt"
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
	movePointerOverElement(t, ctx, fmt.Sprintf(`.feed-link[data-feed-id="%d"]`, fixture.secondaryFeedID))
	waitForJS(t, ctx, desktopPulseIndicatorsExpression(fixture), "aligned dots with hover count visible")
	runActions(t, ctx, chromedp.Focus(
		fmt.Sprintf(`.feed-link[data-feed-id="%d"]`, fixture.tertiaryFeedID), chromedp.ByQuery,
	))
	waitForJS(t, ctx, desktopPulseIndicatorsExpression(fixture), "aligned dots with focused count visible")
	clickElement(t, ctx, "#topbar-shortcuts-button", "open reading preferences for pulse layout")
	clickElement(t, ctx, "#show-unread-counts", "show exact counts for pulse layout")
	waitForJS(t, ctx, htmxSettledExpression(), "exact-count preference settles for pulse layout")
	waitForJS(t, ctx, desktopPulseIndicatorsExpression(fixture), "aligned dots with exact counts enabled")

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

	emptyServer := newSmokeServer(t, newSmokeApp(t).Routes())
	t.Cleanup(emptyServer.Close)
	runActions(t, ctx, chromedp.EmulateViewport(1063, 804), chromedp.Navigate(emptyServer.URL))
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for pulse feedback")
	clickElement(t, ctx, "#topbar-brand-button", "pulse without stale feeds")
	waitForJS(t, ctx, `(() => {
		const message = document.querySelector('#pulse-message');
		const button = document.querySelector('#topbar-brand-button');
		if (!message || !button || message.textContent.trim() !== 'No feeds to pulse.') return false;
		const messageRect = message.getBoundingClientRect();
		const buttonRect = button.getBoundingClientRect();
		return message.getAttribute('role') === 'status' && messageRect.width > 0 &&
			messageRect.left >= buttonRect.right && messageRect.left - buttonRect.right <= 20 &&
			messageRect.top >= buttonRect.top && messageRect.bottom <= buttonRect.bottom &&
			document.querySelector('#subscribe-message').textContent.trim() === '';
	})()`, "pulse feedback beside its button")
}
