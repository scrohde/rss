//go:build smoke

//nolint:testpackage // Smoke tests intentionally exercise unexported test helpers and wiring.
package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

func runFeedSelectionFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	feedSelector := fmt.Sprintf(`#feed-list .feed-link[data-feed-id="%d"]`, fixture.secondaryFeedID)
	itemListSelector := fmt.Sprintf(`#main-content #item-list[data-feed-id="%d"]`, fixture.secondaryFeedID)
	firstItemSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)

	runActions(
		t,
		ctx,
		chromedp.WaitVisible(feedSelector, chromedp.ByQuery),
	)
	clickElement(t, ctx, feedSelector, "open secondary feed in selection flow")
	runActions(
		t,
		ctx,
		chromedp.WaitVisible(itemListSelector, chromedp.ByQuery),
		chromedp.WaitVisible(firstItemSelector, chromedp.ByQuery),
	)
	waitForJS(
		t,
		ctx,
		feedSelectionSettledExpression(fixture.secondaryFeedID, "Secondary Feed"),
		"secondary feed selection settled",
	)
	waitForJS(t, ctx, activeElementMatchesExpression(feedSelector), "secondary feed retains focus after swap")
}

func runGeneralMenuKeyboardFlow(t *testing.T, ctx context.Context) {
	t.Helper()

	runActions(
		t,
		ctx,
		chromedp.Focus("#topbar-shortcuts-button", chromedp.ByQuery),
		chromedp.KeyEvent(kb.Enter),
	)
	waitForJS(t, ctx, generalMenuOpenExpression(), "general menu opens from keyboard")
	waitForJS(t, ctx, generalMenuCompactExpression(), "general menu has compact scrolling")

	runActions(t, ctx, chromedp.KeyEvent(kb.Tab))
	waitForJS(t, ctx, activeElementMatchesExpression("#topbar-shortcuts-panel"), "focus enters general menu")
	waitForJS(t, ctx, focusOutlineVisibleExpression("#topbar-shortcuts-panel"), "general menu focus outline")
	runActions(
		t,
		ctx,
		chromedp.Evaluate(`(() => {
			const active = document.querySelector("#item-list .item-entry.is-active");
			const panel = document.querySelector("#topbar-shortcuts-panel");
			window.__menuReaderActiveID = active ? active.id : "";
			panel.scrollTop = 0;
			return window.__menuReaderActiveID !== "";
		})()`, nil),
		chromedp.KeyEvent(kb.ArrowDown),
	)
	waitForJS(t, ctx, generalMenuArrowScrollExpression(), "menu arrow scroll leaves reader selection unchanged")

	runActions(t, ctx, chromedp.KeyEvent(kb.End))
	waitForJS(t, ctx, generalMenuEndVisibleExpression(), "final shortcut remains reachable in compact menu")
	runActions(t, ctx, chromedp.KeyEvent(kb.Escape))
	waitForJS(t, ctx, generalMenuClosedExpression(), "Escape closes general menu")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression("#topbar-shortcuts-button"),
		"Escape restores menu trigger focus",
	)
}

func runGeneralMenuOutsideClickFlow(t *testing.T, ctx context.Context) {
	t.Helper()

	runActions(
		t,
		ctx,
		chromedp.KeyEvent(kb.Enter),
		chromedp.Evaluate(`(() => {
			const button = document.createElement("button");
			button.id = "menu-outside-target";
			button.type = "button";
			button.textContent = "Outside target";
			document.querySelector("#main-content").prepend(button);
			return true;
		})()`, nil),
	)
	waitForJS(t, ctx, generalMenuOpenExpression(), "general menu reopens")
	runActions(t, ctx, chromedp.Click("#menu-outside-target", chromedp.ByQuery))
	waitForJS(t, ctx, generalMenuClosedExpression(), "outside click closes general menu")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression("#menu-outside-target"),
		"outside click focus is not stolen by menu trigger",
	)
}

func runItemActionHierarchyFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	readableRow := fmt.Sprintf("#item-%d", fixture.secondaryFirstItemID)
	readButton := readableRow + " .item-read-in-app"
	sourceLink := readableRow + " .item-source-link"
	readToggle := readableRow + " .item-read-toggle"
	noReaderRow := fmt.Sprintf("#item-%d", fixture.secondaryNoReaderID)
	noReaderLink := noReaderRow + " .item-source-primary"

	waitForJS(t, ctx, elementPresentExpression(readButton), "semantic in-app reading button")
	waitForJS(t, ctx, elementPresentExpression(sourceLink), "separate external source link")
	waitForJS(t, ctx, elementPresentExpression(noReaderLink), "no-reader primary source link")
	waitForJS(
		t,
		ctx,
		itemReadToggleStateExpression(readToggle, "unread", "Mark read", false),
		"unread item toggle state",
	)
	for _, theme := range []string{"light", "dark", "system"} {
		waitForJS(
			t,
			ctx,
			itemReadToggleThemeExpression(readToggle, theme),
			fmt.Sprintf("read toggle styling in %s theme", theme),
		)
	}
	waitForJS(
		t,
		ctx,
		elementAbsentExpression(noReaderRow+" .item-read-in-app"),
		"no-reader item omits in-app action",
	)

	runActions(
		t,
		ctx,
		chromedp.Focus(readButton, chromedp.ByQuery),
		chromedp.KeyEvent(kb.Tab),
	)
	waitForJS(t, ctx, activeElementMatchesExpression(sourceLink), "source link follows reader button in focus order")
	waitForJS(t, ctx, focusOutlineVisibleExpression(sourceLink), "source link keyboard focus treatment")

	runActions(t, ctx, chromedp.Focus(readButton, chromedp.ByQuery))
	waitForJS(t, ctx, focusOutlineVisibleExpression(readButton), "reader button keyboard focus treatment")
	runActions(t, ctx, chromedp.KeyEvent(kb.Tab), chromedp.KeyEvent(kb.Tab))
	waitForJS(t, ctx, activeElementMatchesExpression(readToggle), "read-state action follows item open actions")

	runActions(t, ctx, chromedp.KeyEvent(kb.Enter))
	waitForJS(t, ctx, hasClassExpression(readableRow, "is-read"), "read-state action activated by keyboard")
	waitForJS(
		t,
		ctx,
		itemReadToggleStateExpression(readToggle, "read", "Mark unread", true),
		"read item toggle state",
	)
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "read-state action does not open reader")
	waitForJS(t, ctx, htmxElementReadyExpression(readToggle), "read-state action ready after keyboard swap")
	runActions(
		t,
		ctx,
		chromedp.Focus(readToggle, chromedp.ByQuery),
		chromedp.KeyEvent(kb.Enter),
	)
	waitForJS(t, ctx, missingClassExpression(readableRow, "is-read"), "read-state action restored by keyboard")
	waitForJS(
		t,
		ctx,
		itemReadToggleStateExpression(readToggle, "unread", "Mark read", false),
		"unread item toggle state restored",
	)

	runActions(
		t,
		ctx,
		chromedp.Focus(readButton, chromedp.ByQuery),
		chromedp.KeyEvent(kb.Enter),
	)
	waitForJS(t, ctx, hasClassExpression(readableRow, "is-expanded"), "reader button opens item by keyboard")
	waitForJS(t, ctx, contentPanelItemExpression(fixture.secondaryFirstItemID), "reader button opens content panel")

	requestHTMX(
		t,
		ctx,
		"GET",
		fmt.Sprintf("/items/%d/compact", fixture.secondaryFirstItemID),
		readableRow,
		fmt.Sprintf("item-%d", fixture.secondaryFirstItemID),
	)
	waitForJS(t, ctx, missingClassExpression(readableRow, "is-expanded"), "reader action test row collapsed")
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "reader action test panel closed")

	armSourceLinkCapture(t, ctx)
	runActions(
		t,
		ctx,
		chromedp.Focus(sourceLink, chromedp.ByQuery),
		chromedp.KeyEvent(kb.Enter),
	)
	waitForJS(t, ctx, sourceLinkCapturedExpression(sourceLink), "external source link keyboard activation")
	waitForJS(t, ctx, missingClassExpression(readableRow, "is-expanded"), "source link does not open reader")

	armSourceLinkCapture(t, ctx)
	runActions(
		t,
		ctx,
		chromedp.Focus(noReaderLink, chromedp.ByQuery),
		chromedp.KeyEvent(kb.Enter),
	)
	waitForJS(t, ctx, sourceLinkCapturedExpression(noReaderLink), "no-reader primary link keyboard activation")
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "no-reader link does not open reader")
}

func runMoreTogglePersistenceFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	rowSelector := fmt.Sprintf("#item-%d", fixture.secondaryFirstItemID)
	itemTarget := fmt.Sprintf("#item-%d", fixture.secondaryFirstItemID)
	selectedItemID := fmt.Sprintf("item-%d", fixture.secondaryFirstItemID)

	runActions(
		t,
		ctx,
		chromedp.WaitVisible("#feed-list .feed-more-button", chromedp.ByQuery),
	)
	waitForJS(t, ctx, feedMoreCollapsedExpression(), "more panel collapsed by default")

	runActions(t, ctx, chromedp.Click("#feed-list .feed-more-button", chromedp.ByQuery))
	waitForJS(t, ctx, feedMoreExpandedExpression(), "more panel expanded after click")

	requestHTMX(
		t,
		ctx,
		"POST",
		fmt.Sprintf("/items/%d/toggle", fixture.secondaryFirstItemID),
		itemTarget,
		selectedItemID,
	)
	waitForJS(t, ctx, hasClassExpression(rowSelector, "is-read"), "row marked read in persistence flow")
	waitForJS(t, ctx, feedMoreExpandedExpression(), "more panel stays expanded after feed-list swap")

	runActions(t, ctx, chromedp.Click("#feed-list .feed-more-button", chromedp.ByQuery))
	waitForJS(t, ctx, feedMoreCollapsedExpression(), "more panel collapsed after second click")

	requestHTMX(
		t,
		ctx,
		"POST",
		fmt.Sprintf("/items/%d/toggle", fixture.secondaryFirstItemID),
		itemTarget,
		selectedItemID,
	)
	waitForJS(t, ctx, missingClassExpression(rowSelector, "is-read"), "row marked unread in persistence flow")
	waitForJS(t, ctx, feedMoreCollapsedExpression(), "more panel stays collapsed after feed-list swap")
}

func runFeedBoundaryKeyboardFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	lastUnreadFeedSelector := fmt.Sprintf(
		`#feed-list .feed-link[data-feed-id="%d"]`,
		fixture.quaternaryFeedID,
	)
	archiveFeedSelector := fmt.Sprintf(
		`#feed-list #feed-zero-list .feed-link[data-feed-id="%d"]`,
		fixture.archiveFeedID,
	)
	archiveListSelector := fmt.Sprintf(
		`#main-content #item-list[data-feed-id="%d"]`,
		fixture.archiveFeedID,
	)

	clickElement(t, ctx, lastUnreadFeedSelector, "open last unread feed before boundary flow")
	waitForJS(t, ctx, hasClassExpression(lastUnreadFeedSelector, "active"), "last unread feed active before boundary down")
	runActions(t, ctx, chromedp.Focus(lastUnreadFeedSelector, chromedp.ByQuery))
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression(lastUnreadFeedSelector),
		"last unread feed focused before boundary down",
	)
	waitForJS(t, ctx, feedMoreCollapsedExpression(), "more panel collapsed before boundary down")

	pressKey(t, ctx, "ArrowDown")
	waitForJS(t, ctx, feedMoreExpandedExpression(), "boundary down expands more panel")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression("#feed-list .feed-more-button"),
		"boundary down focuses more button",
	)

	pressKey(t, ctx, "ArrowDown")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression(archiveFeedSelector),
		"arrow down from more focuses first zero-unread feed",
	)
	waitForJS(t, ctx, elementPresentExpression(archiveListSelector), "archive feed main content loaded")

	pressKey(t, ctx, "ArrowUp")
	waitForJS(t, ctx, feedMoreCollapsedExpression(), "boundary up collapses more panel")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression("#feed-list .feed-more-button"),
		"boundary up focuses more button",
	)

	pressKey(t, ctx, "ArrowUp")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression(lastUnreadFeedSelector),
		"arrow up from more returns focus to last unread feed",
	)
	waitForJS(t, ctx, hasClassExpression(lastUnreadFeedSelector, "active"), "last unread feed active after boundary flow")
	waitForJS(t, ctx, activeElementMatchesExpression(lastUnreadFeedSelector), "last unread feed retains focus")
}

func runHiddenSelectionFallbackFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	secondaryFeedSelector := fmt.Sprintf(`#feed-list .feed-link[data-feed-id="%d"]`, fixture.secondaryFeedID)
	tertiaryFeedSelector := fmt.Sprintf(`#feed-list .feed-link[data-feed-id="%d"]`, fixture.tertiaryFeedID)
	quaternaryFeedSelector := fmt.Sprintf(
		`#feed-list .feed-link[data-feed-id="%d"]`,
		fixture.quaternaryFeedID,
	)
	secondaryListSelector := fmt.Sprintf(`#main-content #item-list[data-feed-id="%d"]`, fixture.secondaryFeedID)
	tertiaryListSelector := fmt.Sprintf(`#main-content #item-list[data-feed-id="%d"]`, fixture.tertiaryFeedID)
	quaternaryListSelector := fmt.Sprintf(`#main-content #item-list[data-feed-id="%d"]`, fixture.quaternaryFeedID)
	secondaryFirstRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)
	secondarySecondRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondarySecondItemID)
	secondaryNoReaderRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryNoReaderID)
	secondarySummaryRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondarySummaryItemID)
	secondaryFirstRowTarget := fmt.Sprintf("#item-%d", fixture.secondaryFirstItemID)
	secondarySecondRowTarget := fmt.Sprintf("#item-%d", fixture.secondarySecondItemID)
	secondaryNoReaderRowTarget := fmt.Sprintf("#item-%d", fixture.secondaryNoReaderID)
	secondarySummaryRowTarget := fmt.Sprintf("#item-%d", fixture.secondarySummaryItemID)
	secondaryFirstSelectedID := fmt.Sprintf("item-%d", fixture.secondaryFirstItemID)
	secondarySecondSelectedID := fmt.Sprintf("item-%d", fixture.secondarySecondItemID)
	secondaryNoReaderSelectedID := fmt.Sprintf("item-%d", fixture.secondaryNoReaderID)
	secondarySummarySelectedID := fmt.Sprintf("item-%d", fixture.secondarySummaryItemID)
	tertiaryListTarget := `#main-content > section.items`

	clickElement(t, ctx, secondaryFeedSelector, "open secondary feed before hidden-selection flow")
	waitForJS(
		t,
		ctx,
		hasClassExpression(secondaryFeedSelector, "active"),
		"secondary feed active before hidden-selection flow",
	)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(secondaryListSelector),
		"secondary items loaded before hidden-selection flow",
	)
	waitForJS(t, ctx, feedMoreCollapsedExpression(), "more panel collapsed before hidden-selection flow")

	requestHTMX(
		t,
		ctx,
		"POST",
		fmt.Sprintf("/items/%d/toggle", fixture.secondaryFirstItemID),
		secondaryFirstRowTarget,
		secondaryFirstSelectedID,
	)
	waitForJS(t, ctx, hasClassExpression(secondaryFirstRowSelector, "is-read"), "secondary first row marked read")

	requestHTMX(
		t,
		ctx,
		"POST",
		fmt.Sprintf("/items/%d/toggle", fixture.secondarySecondItemID),
		secondarySecondRowTarget,
		secondarySecondSelectedID,
	)
	waitForJS(t, ctx, hasClassExpression(secondarySecondRowSelector, "is-read"), "secondary second row marked read")

	requestHTMX(
		t,
		ctx,
		"POST",
		fmt.Sprintf("/items/%d/toggle", fixture.secondaryNoReaderID),
		secondaryNoReaderRowTarget,
		secondaryNoReaderSelectedID,
	)
	waitForJS(t, ctx, hasClassExpression(secondaryNoReaderRowSelector, "is-read"), "secondary no-reader row marked read")

	requestHTMX(
		t,
		ctx,
		"POST",
		fmt.Sprintf("/items/%d/toggle", fixture.secondarySummaryItemID),
		secondarySummaryRowTarget,
		secondarySummarySelectedID,
	)
	waitForJS(t, ctx, hasClassExpression(secondarySummaryRowSelector, "is-read"), "secondary summary row marked read")

	waitForJS(
		t,
		ctx,
		elementHiddenExpression(secondaryFeedSelector),
		"secondary feed hidden after individual toggles reach zero unread",
	)

	runActions(t, ctx, chromedp.Focus("#item-list", chromedp.ByQuery))
	pressKey(t, ctx, "h")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression(tertiaryFeedSelector),
		"hidden selected feed resolves to next visible feed after individual toggles",
	)
	waitForJS(t, ctx, hasClassExpression(tertiaryFeedSelector, "active"), "tertiary feed active after toggle fallback")
	waitForJS(t, ctx, elementPresentExpression(tertiaryListSelector), "tertiary items auto-load after returning to feed panel")

	pressKey(t, ctx, "l")
	waitForJS(t, ctx, elementPresentExpression(tertiaryListSelector), "tertiary items loaded before mark-all")
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "items panel focused on tertiary feed")

	requestHTMX(
		t,
		ctx,
		"POST",
		fmt.Sprintf("/feeds/%d/items/read", fixture.tertiaryFeedID),
		tertiaryListTarget,
		"",
	)
	waitForJS(
		t,
		ctx,
		elementHiddenExpression(tertiaryFeedSelector),
		"tertiary feed hidden after mark-all-read reaches zero unread",
	)

	runActions(t, ctx, chromedp.Focus("#item-list", chromedp.ByQuery))
	pressKey(t, ctx, "h")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression(quaternaryFeedSelector),
		"hidden selected feed resolves to next visible feed after mark-all-read",
	)
	waitForJS(
		t,
		ctx,
		hasClassExpression(quaternaryFeedSelector, "active"),
		"quaternary feed active after mark-all fallback",
	)
	waitForJS(t, ctx, elementPresentExpression(quaternaryListSelector), "quaternary items auto-load after mark-all fallback")
}

func runMobileFilteredHistoryFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	selectedFeedID := fixture.secondaryFeedID
	filteredStreamURI := fmt.Sprintf("/mobile/stream?selected_feed_id=%d", selectedFeedID)
	readerURI := fmt.Sprintf("/mobile/items/%d/reader?selected_feed_id=%d", fixture.secondaryFirstItemID, selectedFeedID)
	readerSelector := fmt.Sprintf(
		`.mobile-card-open[hx-get="/mobile/items/%d/reader?selected_feed_id=%d"]`,
		fixture.secondaryFirstItemID,
		selectedFeedID,
	)

	selectMobileFeedFilter(t, ctx, selectedFeedID)
	waitForJS(t, ctx, requestURIExpression(filteredStreamURI), "filtered mobile stream URL")
	waitForJS(t, ctx, mobileFilterValueExpression(selectedFeedID), "secondary feed selected in filter")
	waitForJS(t, ctx, textPresentExpression("Secondary One"), "secondary item present in filtered stream")
	waitForJS(t, ctx, textAbsentExpression("Primary One"), "other feed item hidden from filtered stream")

	runActions(
		t,
		ctx,
		chromedp.WaitVisible(readerSelector, chromedp.ByQuery),
		chromedp.Click(readerSelector, chromedp.ByQuery),
	)
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "filtered reader loaded")
	waitForJS(t, ctx, requestURIExpression(readerURI), "filtered reader URL")
	waitForJS(t, ctx, textPresentExpression("Secondary One"), "filtered reader title present")

	runActions(t, ctx, chromedp.Evaluate(`history.back()`, nil))
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "history back returns to filtered stream")
	waitForJS(t, ctx, requestURIExpression(filteredStreamURI), "filtered stream URL after history back")
	waitForJS(t, ctx, mobileFilterValueExpression(selectedFeedID), "filter stays selected after history back")
	waitForJS(t, ctx, textPresentExpression("Secondary One"), "filtered stream item still present after history back")
	waitForJS(t, ctx, textAbsentExpression("Primary One"), "other feed item remains hidden after history back")
}

func runMobileSummaryOnlyPreviewFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	waitForJS(
		t,
		ctx,
		mobileCardCompactPreviewExpression(
			fixture.secondarySummaryItemID,
			"Summary-only heading Summary-only fallback preview.",
		),
		"summary-only mobile card compact preview",
	)
}

func runMobileFilteredEmptyStateFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	selectedFeedID := fixture.primaryFeedID
	filteredStreamURI := fmt.Sprintf("/mobile/stream?selected_feed_id=%d", selectedFeedID)
	markReadSelector := fmt.Sprintf(
		`.mobile-card-mark-read[hx-post="/mobile/items/%d/read?selected_feed_id=%d"]`,
		fixture.primaryFirstItemID,
		selectedFeedID,
	)

	selectMobileFeedFilter(t, ctx, selectedFeedID)
	waitForJS(t, ctx, requestURIExpression(filteredStreamURI), "single-item filtered stream URL")
	waitForJS(t, ctx, mobileFilterValueExpression(selectedFeedID), "single-item feed selected in filter")
	waitForJS(t, ctx, textPresentExpression("Primary One"), "single-item filtered story present before clear")

	runActions(
		t,
		ctx,
		chromedp.WaitVisible(markReadSelector, chromedp.ByQuery),
		chromedp.Click(markReadSelector, chromedp.ByQuery),
	)
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-empty="true"]`), "filtered empty state after clearing last unread")
	waitForJS(t, ctx, requestURIExpression(filteredStreamURI), "filtered stream URL retained after clearing last unread")
	waitForJS(t, ctx, mobileFilterValueExpression(selectedFeedID), "filter stays selected after clearing last unread")
	waitForJS(t, ctx, textPresentExpression("Primary Feed is caught up."), "feed-specific empty-state heading shown")
	waitForJS(
		t,
		ctx,
		textPresentExpression("There is nothing unread in Primary Feed right now."),
		"feed-specific empty-state copy shown",
	)
}

func runMobileReaderTopBarFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	selectedFeedID := fixture.secondaryFeedID
	filteredStreamURI := fmt.Sprintf("/mobile/stream?selected_feed_id=%d", selectedFeedID)
	readerURI := fmt.Sprintf("/mobile/items/%d/reader?selected_feed_id=%d", fixture.secondaryFirstItemID, selectedFeedID)
	readerSelector := fmt.Sprintf(
		`.mobile-card-open[hx-get="/mobile/items/%d/reader?selected_feed_id=%d"]`,
		fixture.secondaryFirstItemID,
		selectedFeedID,
	)
	primaryStreamURI := fmt.Sprintf("/mobile/stream?selected_feed_id=%d", fixture.primaryFeedID)

	selectMobileFeedFilter(t, ctx, selectedFeedID)
	waitForJS(t, ctx, requestURIExpression(filteredStreamURI), "reader topbar filtered stream URL")

	runActions(
		t,
		ctx,
		chromedp.WaitVisible(readerSelector, chromedp.ByQuery),
		chromedp.Click(readerSelector, chromedp.ByQuery),
	)
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "reader topbar reader loaded")
	waitForJS(t, ctx, requestURIExpression(readerURI), "reader topbar reader URL")
	waitForJS(t, ctx, mobileFilterValueExpression(selectedFeedID), "reader topbar filter visible in reader")

	clickElement(t, ctx, "#topbar-brand-button", "mobile topbar brand pulse")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "brand pulse returns to stream")
	waitForJS(t, ctx, requestURIExpression(filteredStreamURI), "brand pulse preserves filtered stream URL")
	waitForJS(t, ctx, mobileFilterValueExpression(selectedFeedID), "brand pulse preserves filtered selection")

	runActions(
		t,
		ctx,
		chromedp.WaitVisible(readerSelector, chromedp.ByQuery),
		chromedp.Click(readerSelector, chromedp.ByQuery),
	)
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "reader reopened before selector change")
	waitForJS(t, ctx, mobileFilterValueExpression(selectedFeedID), "reader reopened with selected feed")

	selectMobileFeedFilter(t, ctx, fixture.primaryFeedID)
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "reader selector change returns to stream")
	waitForJS(t, ctx, requestURIExpression(primaryStreamURI), "reader selector change updates stream URL")
	waitForJS(t, ctx, mobileFilterValueExpression(fixture.primaryFeedID), "reader selector change updates selection")
	waitForJS(t, ctx, textPresentExpression("Primary One"), "reader selector change shows primary item")
	waitForJS(t, ctx, textAbsentExpression("Secondary One"), "reader selector change hides previous feed items")
}

func runExpandCollapseFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	rowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)
	itemID := fixture.secondaryFirstItemID
	selectedItemID := fmt.Sprintf("item-%d", itemID)
	itemTarget := fmt.Sprintf("#item-%d", itemID)

	requestHTMX(
		t,
		ctx,
		"GET",
		fmt.Sprintf("/items/%d", itemID),
		itemTarget,
		selectedItemID,
	)

	waitForJS(t, ctx, hasClassExpression(rowSelector, "is-expanded"), "expanded row")
	waitForJS(t, ctx, hasClassExpression("#content-panel", "is-open"), "opened content panel")
	waitForJS(t, ctx, contentPanelItemExpression(fixture.secondaryFirstItemID), "expanded panel item")

	requestHTMX(
		t,
		ctx,
		"GET",
		fmt.Sprintf("/items/%d/compact", itemID),
		itemTarget,
		selectedItemID,
	)
	waitForJS(t, ctx, missingClassExpression(rowSelector, "is-expanded"), "collapsed row")
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "closed content panel")
}

func runSummaryOnlyDesktopReaderFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	rowSelector := fmt.Sprintf(`#item-%d`, fixture.secondarySummaryItemID)
	itemID := fixture.secondarySummaryItemID
	itemTarget := fmt.Sprintf("#item-%d", itemID)
	selectedItemID := fmt.Sprintf("item-%d", itemID)

	waitForJS(t, ctx, elementPresentExpression(rowSelector), "summary-only row present in desktop list")
	waitForJS(t, ctx, textPresentExpression("Summary-only heading Summary-only fallback preview."), "summary-only compact preview text")
	waitForJS(t, ctx, elementAbsentExpression(rowSelector+" .item-summary h2"), "summary-only compact row heading markup removed")
	waitForJS(t, ctx, elementAbsentExpression(rowSelector+" .item-summary img"), "summary-only compact row image removed")

	requestHTMX(
		t,
		ctx,
		"GET",
		fmt.Sprintf("/items/%d", itemID),
		itemTarget,
		selectedItemID,
	)
	waitForJS(t, ctx, hasClassExpression(rowSelector, "is-expanded"), "summary-only row expanded")
	waitForJS(t, ctx, hasClassExpression("#content-panel", "is-open"), "summary-only content panel opened")
	waitForJS(t, ctx, contentPanelItemExpression(itemID), "summary-only content panel item")
}

func runFeedToItemsOutlineEntryFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	firstRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)

	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-active"), "first row active before feed-to-items step")
	waitForJS(
		t,
		ctx,
		missingClassExpression("#item-list", "is-keyboard-nav"),
		"keyboard nav marker absent before feed focus",
	)

	runActions(t, ctx, chromedp.Focus("#feed-list .feed-link.active", chromedp.ByQuery))
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression("#feed-list .feed-link.active"),
		"feed panel focus before right arrow",
	)

	pressKey(t, ctx, "l")
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "items panel focus after right arrow from feed")
	waitForJS(t, ctx, itemOutlineVisibleExpression(firstRowSelector), "outline visible after right arrow from feed")
}

func runContentToItemsOutlineEntryFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	firstRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)
	firstRowTitleSelector := fmt.Sprintf(`#item-%d .item-title`, fixture.secondaryFirstItemID)

	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-active"), "first row active before content-to-items step")
	runActions(t, ctx, chromedp.Click(firstRowTitleSelector, chromedp.ByQuery))
	waitForJS(
		t,
		ctx,
		hasClassExpression(firstRowSelector, "is-expanded"),
		"title opens first row before content entry",
	)
	waitForJS(
		t,
		ctx,
		hasClassExpression("#content-panel", "is-open"),
		"title opens content panel before content entry",
	)
	waitForJS(
		t,
		ctx,
		missingClassExpression("#item-list", "is-keyboard-nav"),
		"keyboard nav marker absent before items-to-content step",
	)

	runActions(t, ctx, chromedp.Focus("#item-list", chromedp.ByQuery))
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "items panel focus before content entry")

	pressKey(t, ctx, "l")
	waitForJS(
		t,
		ctx,
		hasClassExpression(firstRowSelector, "is-expanded"),
		"expanded first row before content-to-items step",
	)
	waitForJS(t, ctx, hasClassExpression("#content-panel", "is-open"), "opened content panel before content-to-items step")
	waitForJS(t, ctx, activeElementMatchesExpression("#content-panel"), "content panel focus before left arrow")

	pressKey(t, ctx, "h")
	waitForJS(
		t,
		ctx,
		missingClassExpression(firstRowSelector, "is-expanded"),
		"collapsed first row after left arrow from content",
	)
	waitForJS(
		t,
		ctx,
		missingClassExpression("#content-panel", "is-open"),
		"closed content panel after left arrow from content",
	)
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "items panel focus after left arrow from content")
	waitForJS(t, ctx, itemOutlineVisibleExpression(firstRowSelector), "outline visible after left arrow from content")
}

func runToggleReadFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	rowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)
	toggleSelector := fmt.Sprintf(`#item-%d .item-read-toggle`, fixture.secondaryFirstItemID)

	clickPointerElement(t, ctx, toggleSelector, "mark row read with pointer")
	waitForJS(t, ctx, hasClassExpression(rowSelector, "is-read"), "row marked read")
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "mouse read toggle keeps reader closed")

	clickPointerElement(t, ctx, toggleSelector, "mark row unread with pointer")
	waitForJS(t, ctx, missingClassExpression(rowSelector, "is-read"), "row marked unread")
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "mouse unread toggle keeps reader closed")
}

func runKeyboardFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	firstRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)
	secondRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondarySecondItemID)
	firstRowToggleSelector := fmt.Sprintf(`#item-%d .item-read-toggle`, fixture.secondaryFirstItemID)

	runActions(
		t,
		ctx,
		chromedp.Focus("#item-list", chromedp.ByQuery),
	)
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "item panel focus for keyboard flow")
	pressKey(t, ctx, "k")
	pressKey(t, ctx, "k")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-active"), "first row active")

	pressKey(t, ctx, "j")
	waitForJS(t, ctx, hasClassExpression(secondRowSelector, "is-active"), "second row active")
	waitForJS(t, ctx, itemOutlineVisibleExpression(secondRowSelector), "outline visible after keyboard navigation")

	pressKey(t, ctx, "k")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-active"), "first row active after up")

	clickPointerElement(t, ctx, firstRowToggleSelector, "mark active row read with pointer")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-read"), "row marked read after pointer interaction")
	waitForJS(
		t,
		ctx,
		missingClassExpression("#item-list", "is-keyboard-nav"),
		"pointer click clears keyboard outline state",
	)
	waitForJS(t, ctx, itemOutlineAbsentExpression(firstRowSelector), "outline hidden after pointer interaction")

	runActions(t, ctx, chromedp.Focus("#item-list", chromedp.ByQuery))
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "item panel focus after pointer interaction")

	pressKey(t, ctx, "k")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-active"), "first row active after keyboard restore")
	waitForJS(t, ctx, hasClassExpression("#item-list", "is-keyboard-nav"), "keyboard outline state restored")
	waitForJS(t, ctx, itemOutlineVisibleExpression(firstRowSelector), "outline visible after keyboard restore")

	pressKey(t, ctx, "h")
	waitForJS(t, ctx, activeElementMatchesExpression("#feed-list .feed-link.active"), "feed panel focus")
	waitForJS(
		t,
		ctx,
		hasClassExpression("#item-list", "is-keyboard-nav"),
		"keyboard outline state retained while feed panel focused",
	)
	waitForJS(t, ctx, itemOutlineAbsentExpression(firstRowSelector), "outline hidden while feed panel focused")

	pressKey(t, ctx, "l")
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "item panel focus")
	waitForJS(t, ctx, itemOutlineVisibleExpression(firstRowSelector), "outline visible when returning to items panel")
	pressKey(t, ctx, "k")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-active"), "first row active before content panel open")

	pressKey(t, ctx, "l")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-expanded"), "expanded first row via keyboard")
	waitForJS(t, ctx, hasClassExpression("#content-panel", "is-open"), "opened content panel via keyboard")
	waitForJS(t, ctx, contentPanelItemExpression(fixture.secondaryFirstItemID), "content panel item via keyboard")
	waitForJS(t, ctx, activeElementMatchesExpression("#content-panel"), "content panel focus via keyboard")
	waitForJS(t, ctx, itemOutlineAbsentExpression(firstRowSelector), "outline hidden while content panel focused")

	pressKey(t, ctx, "h")
	waitForJS(t, ctx, missingClassExpression(firstRowSelector, "is-expanded"), "collapsed first row via keyboard")
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "closed content panel via keyboard")
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "item panel focus after keyboard collapse")
	pressKey(t, ctx, "j")
	waitForJS(t, ctx, hasClassExpression(secondRowSelector, "is-active"), "second row active after returning to items")
	waitForJS(t, ctx, itemOutlineVisibleExpression(secondRowSelector), "outline visible after return and keyboard nav")
}

func runContentPanelMarkReadButtonFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	firstRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)
	secondRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondarySecondItemID)
	noReaderRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryNoReaderID)
	summaryRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondarySummaryItemID)
	markReadSelector := `#content-panel button[data-content-panel-mark-read="true"]`

	clickElement(t, ctx, firstRowSelector, "open first article before panel mark-read flow")
	waitForJS(t, ctx, hasClassExpression("#content-panel", "is-open"), "content panel open before mark-read")
	waitForJS(t, ctx, contentPanelItemExpression(fixture.secondaryFirstItemID), "first article open in content panel")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-read"), "first article starts read before mark-read")
	waitForJS(
		t,
		ctx,
		feedUnreadCountExpression(fixture.secondaryFeedID, "3"),
		"secondary feed unread count before mark-read",
	)

	clickElement(t, ctx, markReadSelector, "advance from read first article in content panel")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-read"), "first article stays read after panel advance")
	waitForJS(
		t,
		ctx,
		contentPanelItemExpression(fixture.secondarySecondItemID),
		"second article opens after read first article",
	)
	waitForJS(t, ctx, hasClassExpression(secondRowSelector, "is-expanded"), "second article row expanded")
	waitForJS(
		t,
		ctx,
		feedUnreadCountExpression(fixture.secondaryFeedID, "3"),
		"secondary feed unread count unchanged after read article advance",
	)

	clickElement(t, ctx, markReadSelector, "mark second article read from content panel")
	waitForJS(t, ctx, hasClassExpression(secondRowSelector, "is-read"), "second article marked read from content panel")
	waitForJS(
		t,
		ctx,
		missingClassExpression(noReaderRowSelector, "is-expanded"),
		"no-reader row skipped by panel mark-read",
	)
	waitForJS(
		t,
		ctx,
		contentPanelItemExpression(fixture.secondarySummaryItemID),
		"summary article opens after skipping no-reader row",
	)
	waitForJS(t, ctx, hasClassExpression(summaryRowSelector, "is-expanded"), "summary row expanded after no-reader skip")
	waitForJS(
		t,
		ctx,
		feedUnreadCountExpression(fixture.secondaryFeedID, "2"),
		"secondary feed unread count after second mark-read",
	)
}
