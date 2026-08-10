//go:build smoke

//nolint:testpackage // Smoke tests intentionally exercise unexported test helpers and wiring.
package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/mmcdole/gofeed"
)

func TestBrowserSmokeReaderFlows(t *testing.T) {
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
	runItemActionHierarchyFlow(t, ctx, fixture)
	runSummaryOnlyDesktopReaderFlow(t, ctx, fixture)
	runMoreTogglePersistenceFlow(t, ctx, fixture)
	runFeedToItemsOutlineEntryFlow(t, ctx, fixture)
	runContentToItemsOutlineEntryFlow(t, ctx, fixture)
	runExpandCollapseFlow(t, ctx, fixture)
	runToggleReadFlow(t, ctx, fixture)
	runKeyboardFlow(t, ctx, fixture)
	runContentPanelMarkReadButtonFlow(t, ctx, fixture)
	runFeedBoundaryKeyboardFlow(t, ctx, fixture)
}

func TestBrowserSmokeReaderFlowsContinuation(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	primaryFeedSelector := fmt.Sprintf(
		`#feed-list .feed-link[data-feed-id="%d"]`,
		fixture.primaryFeedID,
	)
	primaryListSelector := fmt.Sprintf(
		`#main-content #item-list[data-feed-id="%d"]`,
		fixture.primaryFeedID,
	)
	primaryRowSelector := fmt.Sprintf(`#item-%d`, fixture.primaryFirstItemID)
	primaryToggleSelector := fmt.Sprintf(`#item-%d .item-read-toggle`, fixture.primaryFirstItemID)
	secondaryRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)
	continuationSelector := `#feed-continuation [data-feed-continuation]`

	runActions(
		t,
		ctx,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
		chromedp.WaitVisible("#main-content", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for continuation flow")
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout for continuation flow")

	clickElement(t, ctx, primaryFeedSelector, "open primary feed before continuation flow")
	runActions(
		t,
		ctx,
		chromedp.WaitVisible(primaryListSelector, chromedp.ByQuery),
		chromedp.WaitVisible(primaryRowSelector, chromedp.ByQuery),
		chromedp.WaitVisible(continuationSelector, chromedp.ByQuery),
	)
	waitForJS(t, ctx, textPresentExpression("Continue to Secondary Feed"), "next feed continuation label")
	waitForJS(t, ctx, elementAbsentExpression(secondaryRowSelector), "next feed items are not prefetched")

	clickPointerElement(t, ctx, primaryToggleSelector, "mark primary final unread item read")
	waitForJS(t, ctx, hasClassExpression(primaryRowSelector, "is-read"), "primary final item marked read")
	waitForJS(t, ctx, elementPresentExpression(primaryListSelector), "current feed remains after final read")
	waitForJS(t, ctx, elementVisibleExpression(continuationSelector), "continuation remains after final read")

	clickElement(t, ctx, continuationSelector, "continue to next unread feed")
	waitForJS(
		t,
		ctx,
		feedSelectionSettledExpression(fixture.secondaryFeedID, "Secondary Feed"),
		"secondary feed selected by continuation",
	)
	waitForJS(t, ctx, elementPresentExpression(secondaryRowSelector), "secondary feed items loaded after continuation")
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "continuation clears content panel")
}

func TestBrowserSmokeReaderFlowsPanelResize(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	runActions(
		t,
		ctx,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-panel-resizer", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for panel resize")
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout for panel resize")
	assertPanelResize(
		t,
		ctx,
		"#feed-panel-resizer",
		"--feed-panel-width",
		"pulse.feedPanelWidth",
		40,
	)

	rowSelector := fmt.Sprintf("#item-%d", fixture.primaryFirstItemID)
	requestHTMX(
		t,
		ctx,
		"GET",
		fmt.Sprintf("/items/%d", fixture.primaryFirstItemID),
		rowSelector,
		rowSelector[1:],
	)
	waitForJS(t, ctx, hasClassExpression("#content-panel", "is-open"), "content panel open for resize")
	assertPanelResize(
		t,
		ctx,
		"#content-panel-resizer",
		"--content-panel-width",
		"pulse.contentPanelWidth",
		-40,
	)
}

func TestBrowserSmokeReaderFlowsDockedItemsPanel(t *testing.T) {
	app := newSmokeApp(t)
	feedID := mustUpsertFeed(
		t,
		app,
		"https://example.com/docked-items-panel.xml",
		"Docked Items Panel",
	)
	base := time.Date(2026, time.January, 6, 12, 0, 0, 0, time.UTC)
	feedItems := make([]*gofeed.Item, 0, 18)
	for index := range 18 {
		item := newSmokeItem(
			fmt.Sprintf("Docked Item %02d", index+1),
			fmt.Sprintf("https://example.com/docked-items-panel/%d", index+1),
			fmt.Sprintf("docked-items-panel-%d", index+1),
			base.Add(-time.Duration(index)*time.Minute),
		)
		if index == 0 {
			item.Content = strings.Repeat(
				"<p>Long reader content keeps the docked reader independently scrollable.</p>",
				48,
			)
		}
		feedItems = append(feedItems, item)
	}
	mustUpsertItems(t, app, feedID, feedItems)
	items := mustListItems(t, app, feedID)
	assertItemCount(t, items, len(feedItems))

	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(1600, 700),
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for docked items panel")
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout for docked items panel")

	feedSelector := fmt.Sprintf(`#feed-list .feed-link[data-feed-id="%d"]`, feedID)
	clickElement(t, ctx, feedSelector, "open docked items panel feed")
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf(`#main-content #item-list[data-feed-id="%d"]`, feedID)),
		"docked items panel feed list",
	)

	firstRowSelector := fmt.Sprintf("#item-%d", items[0].ID)
	firstReaderButton := firstRowSelector + " .item-read-in-app"
	runActions(t, ctx, chromedp.Click(firstReaderButton, chromedp.ByQuery))
	waitForJS(t, ctx, contentPanelItemExpression(items[0].ID), "first docked reader item")
	waitForJS(t, ctx, dockedPanelsScrollableExpression(items[0].ID), "independent docked scroll surfaces")

	var wheelPoint struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	runActions(
		t,
		ctx,
		chromedp.Evaluate(`(() => {
			const main = document.querySelector(".main-panel");
			const rect = main.getBoundingClientRect();
			return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
		})()`, &wheelPoint),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if err := input.DispatchMouseEvent(input.MouseMoved, wheelPoint.X, wheelPoint.Y).Do(ctx); err != nil {
				return err
			}

			return input.DispatchMouseEvent(input.MouseWheel, wheelPoint.X, wheelPoint.Y).
				WithDeltaY(520).
				Do(ctx)
		}),
	)
	waitForJS(t, ctx, dockedItemsWheelScrollExpression(items[0].ID), "wheel-scroll docked items panel")

	targetRowSelector := fmt.Sprintf("#item-%d", items[6].ID)
	targetReaderButton := targetRowSelector + " .item-read-in-app"
	runActions(t, ctx, chromedp.Click(targetReaderButton, chromedp.ByQuery))
	waitForJS(t, ctx, contentPanelItemExpression(items[6].ID), "docked item control updates reader")
	waitForJS(t, ctx, hasClassExpression(targetRowSelector, "is-expanded"), "new docked item expanded")
	waitForJS(t, ctx, missingClassExpression(firstRowSelector, "is-expanded"), "previous docked item collapsed")

	runActions(t, ctx, chromedp.Click("#content-panel [data-content-panel-full-toggle]", chromedp.ByQuery))
	waitForJS(t, ctx, hasClassExpression(".app", "is-content-panel-floating"), "floating reader mode")

	blockedRowSelector := fmt.Sprintf("#item-%d", items[7].ID)
	blockedToggleSelector := blockedRowSelector + " .item-read-toggle"
	waitForJS(
		t,
		ctx,
		floatingBackdropCoversControlExpression(blockedToggleSelector),
		"floating backdrop covers items control",
	)
	runActions(t, ctx, chromedp.Click(blockedToggleSelector, chromedp.ByQuery))
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "floating outside click closes reader")
	waitForJS(t, ctx, itemReadStateExpression(blockedRowSelector, "unread"), "floating backdrop blocks item control")
}
