//go:build smoke

//nolint:testpackage // Smoke tests intentionally exercise unexported test helpers and wiring.
package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

//nolint:funlen // One browser journey checks the complete desktop and mobile Catch up flow.
func TestBrowserSmokeCatchUpDesktopAndMobile(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	runActions(t, ctx, chromedp.Navigate("about:blank"))
	runActions(t, ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return emulation.SetTimezoneOverride("America/Los_Angeles").Do(ctx)
	}))

	feedSelector := fmt.Sprintf(`#feed-list .feed-link[data-feed-id="%d"]`, fixture.secondaryFeedID)
	runActions(t, ctx, chromedp.Navigate(server.URL), chromedp.WaitVisible("#feed-list", chromedp.ByQuery))
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for Catch up")
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout for Catch up")
	clickElement(t, ctx, feedSelector, "open feed with Catch up items")
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf(`#item-list[data-feed-id="%d"]`, fixture.secondaryFeedID)),
		"selected desktop Catch up feed",
	)

	openButton := "[data-catch-up-open]"
	clickElement(t, ctx, openButton, "open desktop Catch up dialog")
	waitForJS(t, ctx, `(() => {
		const dialog = document.querySelector("[data-catch-up-dialog]");
		const opener = document.querySelector("[data-catch-up-open]");
		return Boolean(dialog && dialog.open && dialog.contains(document.activeElement) &&
			opener.getAttribute("aria-haspopup") === "dialog" &&
			dialog.getAttribute("aria-labelledby") && dialog.getAttribute("aria-describedby"));
	})()`, "desktop dialog opens with accessible focus")
	waitForJS(t, ctx, defaultCatchUpDateExpression(7), "seven-day local-calendar preset")
	waitForJS(
		t,
		ctx,
		elementPresentExpression(`[data-catch-up-preview][data-count="4"]`),
		"default cutoff preview count",
	)

	setCatchUpPreset(t, ctx, "30")
	waitForJS(t, ctx, defaultCatchUpDateExpression(30), "thirty-day local-calendar preset")
	waitForJS(
		t,
		ctx,
		elementPresentExpression(`[data-catch-up-preview][data-count="4"]`),
		"thirty-day preview count",
	)
	setCatchUpPreset(t, ctx, "custom")
	setCatchUpDate(t, ctx, "2026-03-08")
	waitForJS(
		t,
		ctx,
		cutoffValueExpression("2026-03-08T08:00:00.000Z"),
		"local midnight before daylight-saving transition",
	)
	waitForJS(t, ctx, previewMatchesCutoffExpression(), "custom-date preview uses the exact cutoff")
	setCatchUpDate(t, ctx, "2026-03-09")
	waitForJS(
		t,
		ctx,
		cutoffValueExpression("2026-03-09T07:00:00.000Z"),
		"local midnight after daylight-saving transition",
	)
	waitForJS(t, ctx, previewMatchesCutoffExpression(), "post-transition preview uses the exact cutoff")

	clickElement(t, ctx, ".catch-up-dialog-actions [data-catch-up-close]", "close desktop Catch up dialog")
	waitForJS(t, ctx, `!document.querySelector("[data-catch-up-dialog]").open`, "desktop dialog closes")
	waitForJS(
		t,
		ctx,
		`document.activeElement.matches("[data-catch-up-open]")`,
		"focus returns to desktop Catch up opener",
	)
	clickElement(t, ctx, openButton, "reopen desktop Catch up dialog")
	waitForJS(t, ctx, previewMatchesCutoffExpression(), "desktop preview ready before apply")
	clickElement(t, ctx, "[data-catch-up-submit]", "apply desktop Catch up")
	waitForJS(
		t,
		ctx,
		`document.querySelector("[data-catch-up-notice]")?.textContent.includes("Marked 4 unread items") &&
			!document.querySelector("[data-mark-all-read-undo-button]")?.hidden`,
		"desktop Catch up reports the actual count and keeps Undo visible",
	)
	waitForJS(
		t,
		ctx,
		fmt.Sprintf(`(() => {
			const link = document.querySelector('#feed-list .feed-link[data-feed-id="%d"]');
			return document.querySelector("#item-list")?.dataset.feedId === "%d" &&
				link?.classList.contains("active");
		})()`, fixture.secondaryFeedID, fixture.secondaryFeedID),
		"zero-unread desktop feed remains selected with its Undo notice",
	)
	clickElement(t, ctx, "[data-mark-all-read-undo-button]", "undo desktop Catch up")
	waitForJS(
		t,
		ctx,
		`document.querySelector("[data-catch-up-notice]")?.textContent.includes("Undo complete") &&
			document.querySelectorAll("#item-list .item-entry").length === 4`,
		"desktop Undo restores the affected items",
	)

	clickElement(t, ctx, openButton, "open desktop Catch up for a zero-result preview")
	setCatchUpPreset(t, ctx, "custom")
	setCatchUpDate(t, ctx, "2025-12-30")
	waitForJS(
		t,
		ctx,
		elementPresentExpression(`[data-catch-up-preview][data-count="0"]`),
		"zero-result cutoff preview",
	)
	clickElement(t, ctx, "[data-catch-up-submit]", "apply zero-result desktop Catch up")
	waitForJS(
		t,
		ctx,
		`document.querySelector("[data-catch-up-notice]")?.textContent.includes("No unread items matched") &&
			(document.querySelector("[data-mark-all-read-undo-button]")?.hidden ?? true)`,
		"zero-result Catch up reports a clear no-op state",
	)

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL+pathMobileStream),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready on mobile Catch up stream")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "mobile stream for Catch up")
	selectMobileFeedFilter(t, ctx, fixture.secondaryFeedID)
	waitForJS(t, ctx, htmxSettledExpression(), "mobile feed selection settled")
	waitForJS(t, ctx, `Boolean(document.querySelector("[data-mobile-feed-actions]"))`, "mobile per-feed actions")

	clickElement(t, ctx, openButton, "open mobile Catch up dialog")
	waitForJS(t, ctx, defaultCatchUpDateExpression(7), "mobile seven-day default preset")
	waitForJS(
		t,
		ctx,
		elementPresentExpression(`[data-catch-up-preview][data-count="4"]`),
		"mobile cutoff preview count",
	)
	setCatchUpPreset(t, ctx, "custom")
	setCatchUpDate(t, ctx, "2026-03-09")
	waitForJS(t, ctx, cutoffValueExpression("2026-03-09T07:00:00.000Z"), "mobile local cutoff across DST")
	waitForJS(t, ctx, previewMatchesCutoffExpression(), "mobile preview uses the exact cutoff")
	clickElement(t, ctx, "[data-catch-up-submit]", "apply mobile Catch up")
	waitForJS(
		t,
		ctx,
		fmt.Sprintf(
			`document.querySelector("[data-mobile-feed-actions]")?.dataset.feedId === "%d"`,
			fixture.secondaryFeedID,
		),
		"mobile zero-unread feed stays selected",
	)
	waitForJS(
		t,
		ctx,
		elementPresentExpression("[data-mobile-feed-actions] [data-mark-all-read-undo-button]"),
		"mobile Catch up exposes Undo",
	)
	waitForJS(
		t,
		ctx,
		`document.querySelector("[data-catch-up-notice]")?.textContent.includes("Marked 4 unread items")`,
		"mobile Catch up reports the actual count",
	)
	clickElement(t, ctx, "[data-mobile-feed-actions] [data-mark-all-read-undo-button]", "undo mobile Catch up")
	waitForJS(
		t,
		ctx,
		`document.querySelector("[data-catch-up-notice]")?.textContent.includes("Undo complete") &&
			document.querySelectorAll("[data-mobile-stream] .mobile-card").length === 4`,
		"mobile Undo restores affected articles",
	)

	clickElement(t, ctx, openButton, "reopen mobile Catch up before navigation")
	waitForJS(
		t,
		ctx,
		elementPresentExpression(`[data-catch-up-preview][data-count="4"]`),
		"mobile preview ready before second apply",
	)
	clickElement(t, ctx, "[data-catch-up-submit]", "apply mobile Catch up before navigation")
	waitForJS(
		t,
		ctx,
		`Boolean(document.querySelector("[data-mobile-feed-actions] [data-mark-all-read-undo-button]"))`,
		"mobile Undo available before navigation",
	)
	selectMobileFeedFilter(t, ctx, fixture.primaryFeedID)
	waitForJS(
		t,
		ctx,
		fmt.Sprintf(`new URLSearchParams(window.location.search).get("selected_feed_id") === "%d"`, fixture.primaryFeedID),
		"mobile navigation to another feed",
	)
	selectMobileFeedFilter(t, ctx, fixture.secondaryFeedID)
	waitForJS(
		t,
		ctx,
		fmt.Sprintf(`new URLSearchParams(window.location.search).get("selected_feed_id") === "%d"`, fixture.secondaryFeedID),
		"return to mobile Catch up feed",
	)
	waitForJS(
		t,
		ctx,
		fmt.Sprintf(`document.querySelector("[data-mobile-feed-actions]")?.dataset.feedId === "%d" &&
			!document.querySelector("[data-mobile-feed-actions] [data-mark-all-read-undo-button]")`,
			fixture.secondaryFeedID,
		),
		"mobile feed navigation invalidates Catch up Undo",
	)
}

func setCatchUpPreset(t *testing.T, ctx context.Context, preset string) {
	t.Helper()

	expression := fmt.Sprintf(`(() => {
		const range = document.querySelector("[data-catch-up-range]");
		if (!range) return false;
		range.value = %q;
		range.dispatchEvent(new Event("change", {bubbles: true}));
		return true;
	})()`, preset)
	waitForJS(t, ctx, expression, "select Catch up preset")
}

func setCatchUpDate(t *testing.T, ctx context.Context, date string) {
	t.Helper()

	expression := fmt.Sprintf(`(() => {
		const input = document.querySelector("[data-catch-up-date]");
		if (!input) return false;
		input.value = %q;
		input.dispatchEvent(new Event("input", {bubbles: true}));
		input.dispatchEvent(new Event("change", {bubbles: true}));
		return true;
	})()`, date)
	waitForJS(t, ctx, expression, "choose Catch up custom date")
}

func defaultCatchUpDateExpression(days int) string {
	return fmt.Sprintf(`(() => {
		const expected = new Date();
		expected.setHours(12, 0, 0, 0);
		expected.setDate(expected.getDate() - %d);
		const pad = (value) => String(value).padStart(2, "0");
		const expectedValue = String(expected.getFullYear()) + "-" + pad(expected.getMonth() + 1) +
			"-" + pad(expected.getDate());
		const input = document.querySelector("[data-catch-up-date]");
		const range = document.querySelector("[data-catch-up-range]");
		return Boolean(input && input.value === expectedValue && input.disabled && range?.value === "%d");
	})()`, days, days)
}

func cutoffValueExpression(value string) string {
	return fmt.Sprintf(`document.querySelector("[data-catch-up-cutoff]")?.value === %q`, value)
}

func previewMatchesCutoffExpression() string {
	return `(() => {
		const preview = document.querySelector("[data-catch-up-preview]");
		const cutoff = document.querySelector("[data-catch-up-cutoff]");
		const submit = document.querySelector("[data-catch-up-submit]");
		return Boolean(preview && cutoff && Date.parse(preview.dataset.cutoff) === Date.parse(cutoff.value) &&
			submit && !submit.disabled);
	})()`
}
