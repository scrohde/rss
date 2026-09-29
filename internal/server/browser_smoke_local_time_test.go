//go:build smoke

//nolint:testpackage // Smoke tests exercise internal app wiring.
package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

func TestBrowserSmokeReaderFlowsLocalTime(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	_, err := app.db.ExecContext(context.Background(),
		`UPDATE items SET published_at = ? WHERE id = ?`,
		"2026-09-29T02:15:00Z", fixture.secondaryFirstItemID)
	requireNoErr(t, err, "set publication timestamp: %v")
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)
	ctx := newSmokeBrowserContext(t)
	runActions(t, ctx, emulation.SetTimezoneOverride("America/Los_Angeles"),
		chromedp.Navigate(server.URL))
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	runFeedSelectionFlow(t, ctx, fixture)
	selector := fmt.Sprintf("#item-%d", fixture.secondaryFirstItemID)
	waitForJS(t, ctx, fmt.Sprintf(`(() => {
		const badge = document.querySelector('%s [data-local-time-title]');
		return badge && badge.title.includes('Sep 28, 2026') && badge.title.includes('7:15 PM');
	})()`, selector), "local publication tooltip after feed swap")
	requestHTMX(t, ctx, "GET", fmt.Sprintf("/items/%d", fixture.secondaryFirstItemID),
		selector, fmt.Sprintf("item-%d", fixture.secondaryFirstItemID))
	waitForJS(t, ctx, `(() => {
		const date = document.querySelector('#content-panel time[data-local-time]');
		return date && date.textContent.includes('Sep 28, 2026') && date.textContent.includes('7:15 PM');
	})()`, "local publication date in out-of-band reader panel")
}
