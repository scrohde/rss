//go:build smoke

//nolint:testpackage // Smoke tests intentionally exercise unexported test helpers and wiring.
package server

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/mmcdole/gofeed"
)

const (
	smokeBrowserPathEnv = "PULSE_SMOKE_BROWSER_BIN"
	smokeWaitTimeout    = 10 * time.Second
)

type smokeFixture struct {
	primaryFeedID         int64
	archiveFeedID         int64
	secondaryFeedID       int64
	tertiaryFeedID        int64
	quaternaryFeedID      int64
	primaryFirstItemID    int64
	secondaryFirstItemID  int64
	secondarySecondItemID int64
}

func TestBrowserSmokeReaderFlows(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := httptest.NewServer(app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
		chromedp.WaitVisible("#main-content", chromedp.ByQuery),
	)
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout")

	runFeedSelectionFlow(t, ctx, fixture)
	runMoreTogglePersistenceFlow(t, ctx, fixture)
	runFeedToItemsOutlineEntryFlow(t, ctx, fixture)
	runContentToItemsOutlineEntryFlow(t, ctx, fixture)
	runExpandCollapseFlow(t, ctx, fixture)
	runToggleReadFlow(t, ctx, fixture)
	runKeyboardFlow(t, ctx, fixture)
	runFeedBoundaryKeyboardFlow(t, ctx, fixture)
}

func TestBrowserSmokeHiddenSelectionFallback(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := httptest.NewServer(app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
		chromedp.WaitVisible("#main-content", chromedp.ByQuery),
	)
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout")

	runFeedSelectionFlow(t, ctx, fixture)
	runHiddenSelectionFallbackFlow(t, ctx, fixture)
}

func TestBrowserSmokeMobileReaderFlows(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := httptest.NewServer(app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL),
	)
	waitForJS(t, ctx, mobileLayoutExpression(), "mobile layout")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "mobile stream loaded")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "mobile stream URL")
	waitForJS(
		t,
		ctx,
		textPresentExpression("Secondary One"),
		"secondary item present in mobile stream before reading",
	)

	requestHTMX(
		t,
		ctx,
		"GET",
		fmt.Sprintf("/mobile/items/%d/reader", fixture.secondaryFirstItemID),
		"#main-content",
		fmt.Sprintf("item-%d", fixture.secondaryFirstItemID),
	)
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "mobile reader loaded")
	waitForJS(
		t,
		ctx,
		pathnameExpression(fmt.Sprintf("/mobile/items/%d/reader", fixture.secondaryFirstItemID)),
		"mobile reader URL",
	)
	waitForJS(t, ctx, textPresentExpression("Secondary One"), "reader title present")

	requestHTMX(
		t,
		ctx,
		"POST",
		fmt.Sprintf("/mobile/items/%d/read", fixture.secondaryFirstItemID),
		"#main-content",
		fmt.Sprintf("item-%d", fixture.secondaryFirstItemID),
	)
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "stream returns after mark read")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "stream URL after mark read")
	waitForJS(
		t,
		ctx,
		textAbsentExpression("Secondary One"),
		"marked item removed from mobile unread stream",
	)

	requestHTMX(
		t,
		ctx,
		"GET",
		fmt.Sprintf("/mobile/items/%d/reader", fixture.secondarySecondItemID),
		"#main-content",
		fmt.Sprintf("item-%d", fixture.secondarySecondItemID),
	)
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "reader can open another item")
	waitForJS(
		t,
		ctx,
		pathnameExpression(fmt.Sprintf("/mobile/items/%d/reader", fixture.secondarySecondItemID)),
		"second reader URL",
	)
	runActions(t, ctx, chromedp.Evaluate(`history.back()`, nil))
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "back returns to stream")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "stream URL after history back")
}

func TestBrowserSmokeMobileFilteredFeedFlows(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := httptest.NewServer(app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL),
	)
	waitForJS(t, ctx, mobileLayoutExpression(), "mobile layout")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "mobile stream loaded")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "mobile stream URL")

	runMobileFilteredHistoryFlow(t, ctx, fixture)
	runMobileFilteredEmptyStateFlow(t, ctx, fixture)
}

func newSmokeApp(t *testing.T) *App {
	t.Helper()

	app := newTestApp(t)
	staticRoot := filepath.Join(pathParentDir, pathParentDir, "static")
	app.SetStaticFS(os.DirFS(staticRoot))

	return app
}

func seedSmokeFixture(t *testing.T, app *App) smokeFixture {
	t.Helper()

	base := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	primaryFeedID := mustUpsertFeed(t, app, "https://example.com/feed-primary.xml", "Primary Feed")
	secondaryFeedID := mustUpsertFeed(t, app, "https://example.com/feed-secondary.xml", "Secondary Feed")
	tertiaryFeedID := mustUpsertFeed(t, app, "https://example.com/feed-tertiary.xml", "Tertiary Feed")
	quaternaryFeedID := mustUpsertFeed(t, app, "https://example.com/feed-quaternary.xml", "Quaternary Feed")
	archiveFeedID := mustUpsertFeed(t, app, "https://example.com/feed-archive.xml", "Archive Feed")

	mustUpsertItems(t, app, primaryFeedID, []*gofeed.Item{
		newSmokeItem("Primary One", "https://example.com/p1", "primary-1", base.Add(-3*time.Hour)),
	})
	mustUpsertItems(t, app, secondaryFeedID, []*gofeed.Item{
		newSmokeItem("Secondary One", "https://example.com/s1", "secondary-1", base),
		newSmokeItem("Secondary Two", "https://example.com/s2", "secondary-2", base.Add(-time.Hour)),
	})
	mustUpsertItems(t, app, tertiaryFeedID, []*gofeed.Item{
		newSmokeItem("Tertiary One", "https://example.com/t1", "tertiary-1", base.Add(-2*time.Hour)),
	})
	mustUpsertItems(t, app, quaternaryFeedID, []*gofeed.Item{
		newSmokeItem("Quaternary One", "https://example.com/q1", "quaternary-1", base.Add(-4*time.Hour)),
	})

	primaryItems := mustListItems(t, app, primaryFeedID)
	assertItemCount(t, primaryItems, 1)

	secondaryItems := mustListItems(t, app, secondaryFeedID)
	assertItemCount(t, secondaryItems, 2)

	return smokeFixture{
		primaryFeedID:         primaryFeedID,
		archiveFeedID:         archiveFeedID,
		secondaryFeedID:       secondaryFeedID,
		tertiaryFeedID:        tertiaryFeedID,
		quaternaryFeedID:      quaternaryFeedID,
		primaryFirstItemID:    primaryItems[0].ID,
		secondaryFirstItemID:  secondaryItems[0].ID,
		secondarySecondItemID: secondaryItems[1].ID,
	}
}

func newSmokeItem(title, link, guid string, publishedAt time.Time) *gofeed.Item {
	return &gofeed.Item{
		Title:           title,
		Link:            link,
		GUID:            guid,
		Description:     fmt.Sprintf("<p>%s summary</p>", title),
		Content:         fmt.Sprintf("<p>%s content</p>", title),
		PublishedParsed: timePtr(publishedAt),
	}
}

func timePtr(value time.Time) *time.Time {
	ptr := new(time.Time)
	*ptr = value

	return ptr
}

func newSmokeBrowserContext(t *testing.T) context.Context {
	t.Helper()

	browserPath, ok := resolveSmokeBrowserPath()
	if !ok {
		t.Skipf(
			"smoke browser not found; set %s to an installed Chrome/Chromium binary",
			smokeBrowserPathEnv,
		)
	}

	allocatorOptions := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browserPath),
		chromedp.Headless,
		chromedp.DisableGPU,
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("window-size", "1365,1024"),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)

	allocatorCtx, allocatorCancel := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)
	t.Cleanup(allocatorCancel)

	browserCtx, browserCancel := chromedp.NewContext(allocatorCtx)
	t.Cleanup(browserCancel)

	timeoutCtx, timeoutCancel := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(timeoutCancel)

	return timeoutCtx
}

func resolveSmokeBrowserPath() (string, bool) {
	if browserPath := os.Getenv(smokeBrowserPathEnv); browserPath != "" {
		if isExecutablePath(browserPath) {
			return browserPath, true
		}
	}

	for _, candidate := range smokeBrowserCandidates() {
		if candidate == "" {
			continue
		}
		if filepath.IsAbs(candidate) {
			if isExecutablePath(candidate) {
				return candidate, true
			}

			continue
		}

		binaryPath, err := exec.LookPath(candidate)
		if err == nil {
			return binaryPath, true
		}
	}

	return "", false
}

func smokeBrowserCandidates() []string {
	candidates := []string{
		"google-chrome",
		"chromium",
		"chromium-browser",
		"chrome",
		"microsoft-edge",
	}

	if runtime.GOOS == "darwin" {
		candidates = append(candidates,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		)
	}

	return candidates
}

func isExecutablePath(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}

	return info.Mode()&0o111 != 0
}

func runFeedSelectionFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	feedSelector := fmt.Sprintf(`#feed-list .feed-link[data-feed-id="%d"]`, fixture.secondaryFeedID)
	itemListSelector := fmt.Sprintf(`#main-content #item-list[data-feed-id="%d"]`, fixture.secondaryFeedID)
	firstItemSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)

	runActions(
		t,
		ctx,
		chromedp.WaitVisible(feedSelector, chromedp.ByQuery),
		chromedp.Click(feedSelector, chromedp.ByQuery),
	)
	runActions(
		t,
		ctx,
		chromedp.WaitVisible(itemListSelector, chromedp.ByQuery),
		chromedp.WaitVisible(firstItemSelector, chromedp.ByQuery),
	)
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

	runActions(t, ctx, chromedp.Click(lastUnreadFeedSelector, chromedp.ByQuery))
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
	secondaryFirstRowTarget := fmt.Sprintf("#item-%d", fixture.secondaryFirstItemID)
	secondarySecondRowTarget := fmt.Sprintf("#item-%d", fixture.secondarySecondItemID)
	secondaryFirstSelectedID := fmt.Sprintf("item-%d", fixture.secondaryFirstItemID)
	secondarySecondSelectedID := fmt.Sprintf("item-%d", fixture.secondarySecondItemID)
	tertiaryMarkAllSelector := fmt.Sprintf(
		`.item-actions button[hx-post="/feeds/%d/items/read"]`,
		fixture.tertiaryFeedID,
	)

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

	runActions(t, ctx, chromedp.Click(tertiaryMarkAllSelector, chromedp.ByQuery))
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
	toggleSelector := fmt.Sprintf(`#item-%d button[hx-post*="/toggle"]`, fixture.secondaryFirstItemID)

	runActions(t, ctx, chromedp.Click(toggleSelector, chromedp.ByQuery))
	waitForJS(t, ctx, hasClassExpression(rowSelector, "is-read"), "row marked read")

	runActions(t, ctx, chromedp.Click(toggleSelector, chromedp.ByQuery))
	waitForJS(t, ctx, missingClassExpression(rowSelector, "is-read"), "row marked unread")
}

func runKeyboardFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	firstRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)
	secondRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondarySecondItemID)
	firstRowToggleSelector := fmt.Sprintf(`#item-%d button[hx-post*="/toggle"]`, fixture.secondaryFirstItemID)

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

	runActions(t, ctx, chromedp.Click(firstRowToggleSelector, chromedp.ByQuery))
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

func runActions(t *testing.T, ctx context.Context, actions ...chromedp.Action) {
	t.Helper()

	if err := chromedp.Run(ctx, actions...); err != nil {
		t.Fatalf("chromedp run: %v", err)
	}
}

func clickElement(t *testing.T, ctx context.Context, selector, label string) {
	t.Helper()

	expression := fmt.Sprintf(
		`(() => {
			const el = document.querySelector(%q);
			if (!el || typeof el.click !== "function") {
				return false;
			}
			el.click();
			return true;
		})()`,
		selector,
	)

	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &ok)); err != nil {
		t.Fatalf("click element %s: %v", label, err)
	}
	if !ok {
		t.Fatalf("click element %s: selector not clickable (%s)", label, selector)
	}
}

func selectMobileFeedFilter(t *testing.T, ctx context.Context, feedID int64) {
	t.Helper()

	expression := fmt.Sprintf(
		`(() => {
			const form = document.querySelector(".mobile-stream-filter");
			const select = document.querySelector("#mobile-stream-feed-filter");
			if (!form || !select) {
				return false;
			}
			select.value = %q;
			if (typeof form.requestSubmit === "function") {
				form.requestSubmit();
			} else {
				const submit = form.querySelector('button[type="submit"]');
				if (!submit || typeof submit.click !== "function") {
					return false;
				}
				submit.click();
			}
			return select.value === %q;
		})()`,
		fmt.Sprintf("%d", feedID),
		fmt.Sprintf("%d", feedID),
	)

	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &ok)); err != nil {
		t.Fatalf("select mobile feed %d: %v", feedID, err)
	}
	if !ok {
		t.Fatalf("select mobile feed %d: mobile filter not available", feedID)
	}
}

func requestHTMX(t *testing.T, ctx context.Context, method, path, target, selectedItemID string) {
	t.Helper()

	expression := fmt.Sprintf(
		`(() => {
			if (!window.htmx || typeof window.htmx.ajax !== "function") {
				return false;
			}
			window.htmx.ajax(%q, %q, {
				target: %q,
				swap: "outerHTML",
				values: { selected_item_id: %q },
			});
			return true;
		})()`,
		method,
		path,
		target,
		selectedItemID,
	)

	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &ok)); err != nil {
		t.Fatalf("request htmx %s %s: %v", method, path, err)
	}

	if !ok {
		t.Fatalf("request htmx %s %s: htmx not available", method, path)
	}
}

func pressKey(t *testing.T, ctx context.Context, key string) {
	t.Helper()

	expression := fmt.Sprintf(
		`(() => {
			const target = document.activeElement || document.body;
			target.dispatchEvent(new KeyboardEvent("keydown", {key: %q, bubbles: true, cancelable: true}));
			target.dispatchEvent(new KeyboardEvent("keyup", {key: %q, bubbles: true, cancelable: true}));
			return true;
		})()`,
		key,
		key,
	)

	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &ok)); err != nil {
		t.Fatalf("press key %q: %v", key, err)
	}
}

func waitForJS(t *testing.T, ctx context.Context, expression, label string) {
	t.Helper()

	deadline := time.Now().Add(smokeWaitTimeout)
	for time.Now().Before(deadline) {
		var matches bool
		err := chromedp.Run(ctx, chromedp.Evaluate(expression, &matches))
		if err == nil && matches {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", label)
}

func hasClassExpression(selector, className string) string {
	return fmt.Sprintf(
		`(() => { const el = document.querySelector(%q); return !!el && el.classList.contains(%q); })()`,
		selector,
		className,
	)
}

func pathnameExpression(path string) string {
	return fmt.Sprintf(`(() => window.location.pathname === %q)()`, path)
}

func requestURIExpression(path string) string {
	return fmt.Sprintf(`(() => (window.location.pathname + window.location.search) === %q)()`, path)
}

func mobileFilterValueExpression(feedID int64) string {
	return fmt.Sprintf(
		`(() => {
			const select = document.querySelector("#mobile-stream-feed-filter");
			return !!select && select.value === %q;
		})()`,
		fmt.Sprintf("%d", feedID),
	)
}

func elementPresentExpression(selector string) string {
	return fmt.Sprintf(
		`(() => !!document.querySelector(%q))()`,
		selector,
	)
}

func elementHiddenExpression(selector string) string {
	return fmt.Sprintf(
		`(() => {
			const el = document.querySelector(%q);
			return !!el && el.getClientRects().length === 0;
		})()`,
		selector,
	)
}

func missingClassExpression(selector, className string) string {
	return fmt.Sprintf(
		`(() => { const el = document.querySelector(%q); return !!el && !el.classList.contains(%q); })()`,
		selector,
		className,
	)
}

func activeElementMatchesExpression(selector string) string {
	return fmt.Sprintf(
		`(() => { const el = document.querySelector(%q); return !!el && document.activeElement === el; })()`,
		selector,
	)
}

func feedMoreExpandedExpression() string {
	return `(() => {
		const button = document.querySelector("#feed-list .feed-more-button");
		const zeroList = document.querySelector("#feed-list #feed-zero-list");
		if (!button || !zeroList) {
			return false;
		}
		return button.getAttribute("aria-expanded") === "true" &&
			zeroList.hidden === false &&
			window.getComputedStyle(zeroList).display !== "none";
	})()`
}

func feedMoreCollapsedExpression() string {
	return `(() => {
		const button = document.querySelector("#feed-list .feed-more-button");
		const zeroList = document.querySelector("#feed-list #feed-zero-list");
		if (!button || !zeroList) {
			return false;
		}
		return button.getAttribute("aria-expanded") === "false" &&
			zeroList.hidden === true &&
			window.getComputedStyle(zeroList).display === "none";
	})()`
}

func itemOutlineVisibleExpression(rowSelector string) string {
	return fmt.Sprintf(
		`(() => {
			const list = document.querySelector("#item-list");
			const row = document.querySelector(%q);
			if (!list || !row) {
				return false;
			}
			const boxShadow = window.getComputedStyle(row).boxShadow || "";
			return list.classList.contains("is-keyboard-nav") &&
				list.matches(":focus-within") &&
				boxShadow.includes("rgb(37, 99, 235)");
		})()`,
		rowSelector,
	)
}

func itemOutlineAbsentExpression(rowSelector string) string {
	return fmt.Sprintf(
		`(() => {
			const row = document.querySelector(%q);
			if (!row) {
				return false;
			}
			const boxShadow = window.getComputedStyle(row).boxShadow || "";
			return !boxShadow.includes("rgb(37, 99, 235)");
		})()`,
		rowSelector,
	)
}

func desktopLayoutExpression() string {
	return `(() => !window.matchMedia("(max-width: 960px)").matches)()`
}

func mobileLayoutExpression() string {
	return `(() => window.matchMedia("(max-width: 960px)").matches)()`
}

func textPresentExpression(text string) string {
	return fmt.Sprintf(
		`(() => document.body && document.body.textContent && document.body.textContent.includes(%q))()`,
		text,
	)
}

func textAbsentExpression(text string) string {
	return fmt.Sprintf(
		`(() => !document.body || !document.body.textContent || !document.body.textContent.includes(%q))()`,
		text,
	)
}

func contentPanelItemExpression(itemID int64) string {
	return fmt.Sprintf(
		`(() => {
			const panel = document.querySelector("#content-panel.is-open");
			if (!panel) {
				return false;
			}
			const article = panel.querySelector(".content-panel-article[data-item-id]");
			return !!article && article.getAttribute("data-item-id") === %q;
		})()`,
		fmt.Sprintf("%d", itemID),
	)
}
