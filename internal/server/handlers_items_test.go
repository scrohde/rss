//nolint:testpackage // Handler integration tests intentionally exercise unexported helpers.
package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"

	"rss/internal/store"
	"rss/internal/view"
)

func TestFeedItemsUpdatesFeedListSelection(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	fixtureIDs := setupFeedSelectionFixtures(t, app)
	otherFeedID := fixtureIDs.otherFeedID
	selectedFeedID := fixtureIDs.selectedFeedID

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet,
		feedItemsPath(selectedFeedID),
		http.NoBody,
	)
	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	assertResponseCode(t, rec, msgFeedItemsStatus)

	body := rec.Body.String()
	assertContains(
		t,
		body,
		"Selected Item",
		"expected selected feed items in response",
	)
	assertFeedListOOBUpdate(t, body)

	selectedButton := activeFeedButton(selectedFeedID)
	assertContains(
		t,
		body,
		selectedButton,
		"expected selected feed to be active in feed list",
	)

	otherButton := activeFeedButton(otherFeedID)
	assertNotContains(
		t,
		body,
		otherButton,
		"expected non-selected feed not to be active",
	)
}

type feedContinuationFixture struct {
	currentFeedID int64
	skippedFeedID int64
	nextFeedID    int64
	currentItemID int64
	skippedItemID int64
}

func setupFeedContinuationFixture(t *testing.T, app *App) feedContinuationFixture {
	t.Helper()

	currentFeedID := mustUpsertFeed(t, app, "http://example.com/continue-current", "Current Feed")
	skippedFeedID := mustUpsertFeed(t, app, "http://example.com/continue-skipped", "Caught Up Feed")
	nextFeedID := mustUpsertFeed(t, app, "http://example.com/continue-next", "Next Feed")

	mustUpsertSingleStory(
		t,
		app,
		currentFeedID,
		"Current Story",
		"http://example.com/current-story",
		"current-story",
		time.Now().UTC().Add(-time.Hour),
	)
	mustUpsertSingleStory(
		t,
		app,
		skippedFeedID,
		"Caught Up Story",
		"http://example.com/caught-up-story",
		"caught-up-story",
		time.Now().UTC().Add(-2*time.Hour),
	)
	mustUpsertSingleStory(
		t,
		app,
		nextFeedID,
		"Next Story",
		"http://example.com/next-story",
		"next-story",
		time.Now().UTC().Add(-3*time.Hour),
	)
	mustMarkFeedItemRead(t, app, skippedFeedID, "caught-up-story")

	currentItems := mustListItems(t, app, currentFeedID)
	skippedItems := mustListItems(t, app, skippedFeedID)

	return feedContinuationFixture{
		currentFeedID: currentFeedID,
		skippedFeedID: skippedFeedID,
		nextFeedID:    nextFeedID,
		currentItemID: currentItems[0].ID,
		skippedItemID: skippedItems[0].ID,
	}
}

func TestFeedItemsRenderNextUnreadContinuationWithoutPrefetch(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	fixture := setupFeedContinuationFixture(t, app)

	err := store.UpdateFeedOrder(
		context.Background(),
		app.db,
		[]int64{fixture.skippedFeedID, fixture.currentFeedID, fixture.nextFeedID},
	)
	requireNoErr(t, err, "save continuation feed order: %v")

	rec := getRequest(app, fmt.Sprintf("/feeds/%d/items", fixture.currentFeedID))
	assertResponseCode(t, rec, msgFeedItemsStatus)

	body := rec.Body.String()
	assertContains(t, body, `data-feed-continuation`, "expected continuation action")
	assertContains(t, body, "Continue to Next Feed", "expected next unread feed label")
	assertContains(
		t,
		body,
		fmt.Sprintf(`hx-get="/feeds/%d/items/continue"`, fixture.currentFeedID),
		"expected dynamic continuation path",
	)
	assertNotContains(t, body, "Next Story", "expected next feed items not to be prefetched")
}

func TestContinueFeedRevalidatesCaughtUpCandidate(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	fixture := setupFeedContinuationFixture(t, app)

	err := store.ToggleRead(context.Background(), app.db, fixture.skippedItemID)
	requireNoErr(t, err, "make skipped feed unread: %v")

	initial := getRequest(app, fmt.Sprintf("/feeds/%d/items", fixture.currentFeedID))
	assertResponseCode(t, initial, msgFeedItemsStatus)
	assertContains(t, initial.Body.String(), "Continue to Caught Up Feed", "expected initial candidate")

	err = store.MarkAllRead(context.Background(), app.db, fixture.skippedFeedID)
	requireNoErr(t, err, "catch up initial continuation candidate: %v")

	rec := getRequest(app, fmt.Sprintf("/feeds/%d/items/continue", fixture.currentFeedID))
	assertResponseCode(t, rec, "continue feed status")

	body := rec.Body.String()
	assertContains(t, body, "Next Story", "expected revalidated next feed items")
	assertContains(t, body, activeFeedButton(fixture.nextFeedID), "expected next feed selected")
	assertFeedListOOBUpdate(t, body)
	assertContentPanelOOBUpdate(t, body)
}

func TestContinueFeedDoesNotWrapAfterFinalFeed(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	fixture := setupFeedContinuationFixture(t, app)

	initial := getRequest(app, fmt.Sprintf("/feeds/%d/items", fixture.nextFeedID))
	assertResponseCode(t, initial, msgFeedItemsStatus)

	initialBody := initial.Body.String()
	assertContains(t, initialBody, `data-feed-continuation-end`, "expected end-of-order state")
	assertContains(t, initialBody, "No later feeds have unread items.", "expected useful end copy")
	assertNotContains(t, initialBody, `class="feed-continuation-button"`, "expected no continuation action")

	rec := getRequest(app, fmt.Sprintf("/feeds/%d/items/continue", fixture.nextFeedID))
	assertResponseCode(t, rec, "final feed continuation status")

	body := rec.Body.String()
	assertContains(t, body, "Next Story", "expected final feed to remain selected")
	assertNotContains(t, body, "Current Story", "expected continuation not to wrap to an earlier feed")
	assertContains(t, body, activeFeedButton(fixture.nextFeedID), "expected final feed to stay selected")
}

func TestToggleFinalUnreadItemRefreshesContinuation(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	fixture := setupFeedContinuationFixture(t, app)

	form := url.Values{}
	form.Set("view", "compact")
	form.Set(selectedItemIDParam, fmt.Sprintf("item-%d", fixture.currentItemID))
	req := newURLEncodedRequest(fmt.Sprintf("/items/%d/toggle", fixture.currentItemID), form)
	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)
	assertResponseCode(t, rec, "toggle final unread status")

	body := rec.Body.String()
	assertContains(t, body, `id="feed-continuation"`, "expected continuation refresh")
	assertContains(t, body, `hx-swap-oob="outerHTML"`, "expected continuation OOB swap")
	assertContains(t, body, "Continue to Next Feed", "expected continuation after final unread")
	assertContains(
		t,
		body,
		fmt.Sprintf(`id="selected-feed-id" name="selected_feed_id" value="%d"`, fixture.currentFeedID),
		"expected current feed to remain selected",
	)
}

func TestMarkAllReadAndSweepKeepContinuationAvailable(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	fixture := setupFeedContinuationFixture(t, app)

	markRec := postRequest(app, fmt.Sprintf("/feeds/%d/items/read", fixture.currentFeedID))
	assertResponseCode(t, markRec, "mark final unread status")

	markBody := markRec.Body.String()
	assertContains(t, markBody, "Continue to Next Feed", "expected continuation after mark-all")
	assertContains(t, markBody, `data-mark-all-read-undo-button`, "expected undo after mark-all")

	sweepRec := postRequest(app, fmt.Sprintf("/feeds/%d/items/sweep", fixture.currentFeedID))
	assertResponseCode(t, sweepRec, "sweep final read item status")

	sweepBody := sweepRec.Body.String()
	assertContains(t, sweepBody, "Continue to Next Feed", "expected continuation after sweep")
	assertUndoTokenCleared(t, sweepBody, "expected sweep to clear mark-all undo")
}

func TestRenameFeedOverridesSourceTitle(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(),
		app.db,
		exampleRSSURL,
		sourceTitle)
	if err != nil {
		t.Fatalf(errStoreUpsertFeed, err)
	}

	err = store.UpdateFeedTitle(context.Background(), app.db, feedID, customTitle)
	if err != nil {
		t.Fatalf("store.UpdateFeedTitle: %v", err)
	}

	feeds, err := store.ListFeeds(context.Background(), app.db)
	if err != nil {
		t.Fatalf(errStoreListFeeds, err)
	}

	if feeds[firstFeedIndex].Title != customTitle {
		t.Fatalf(
			"expected custom title, got %q",
			feeds[firstFeedIndex].Title,
		)
	}

	_, err = store.UpsertFeed(context.Background(),
		app.db,
		exampleRSSURL,
		"Updated Source")
	if err != nil {
		t.Fatalf("store.UpsertFeed update: %v", err)
	}

	feeds, err = store.ListFeeds(context.Background(), app.db)
	if err != nil {
		t.Fatalf("store.ListFeeds again: %v", err)
	}

	if feeds[firstFeedIndex].Title != customTitle {
		t.Fatalf(
			"expected custom title after refresh, got %q",
			feeds[firstFeedIndex].Title,
		)
	}
}

func TestToggleReadUpdatesFeedList(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Toggle Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "One",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Two",
		Link:            "http://example.com/2",
		GUID:            "2",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})

	items := mustListItems(t, app, feedID)

	assertItemCount(t, items, expectedTwoItems)

	form := url.Values{}
	form.Set("view", "compact")
	form.Set(
		selectedItemIDParam,
		fmt.Sprintf("item-%d", items[firstItemIndex].ID),
	)
	req := newURLEncodedRequest(
		fmt.Sprintf("/items/%d/toggle", items[firstItemIndex].ID),
		form,
	)

	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("toggle read status: %d", rec.Code)
	}

	body := rec.Body.String()
	assertToggleReadFeedListBody(t, body)
}

func TestToggleReadExpandedView(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Toggle Expanded Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Expanded",
		Link:            "http://example.com/expanded",
		GUID:            "expanded",
		Description:     "<p>Expanded summary</p>",
		Content:         "<p>Expanded content</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	items := mustListItems(t, app, feedID)

	assertItemCount(t, items, expectedSingleItem)

	form := url.Values{}
	form.Set("view", "expanded")
	form.Set(
		selectedItemIDParam,
		strconv.FormatInt(items[firstItemIndex].ID, decimalBase),
	)
	req := newURLEncodedRequest(
		fmt.Sprintf("/items/%d/toggle", items[firstItemIndex].ID),
		form,
	)

	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("toggle read status: %d", rec.Code)
	}

	body := rec.Body.String()
	assertContains(
		t,
		body,
		"item-entry is-expanded",
		"expected expanded item response",
	)
	assertContains(
		t,
		body,
		classIsActive,
		"expected expanded toggled item to stay active",
	)
	assertContains(
		t,
		body,
		"content-panel is-open",
		"expected expanded toggle response to open content panel",
	)
	assertContentPanelOOBUpdate(t, body)
}

func TestToggleReadExpandedSummaryOnlyKeepsContentPanelOpen(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Toggle Expanded Summary Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Expanded Summary",
		Link:            "http://example.com/expanded-summary",
		GUID:            "expanded-summary",
		Description:     "<p>Expanded summary-only <strong>article</strong></p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	items := mustListItems(t, app, feedID)

	assertItemCount(t, items, expectedSingleItem)

	form := url.Values{}
	form.Set("view", "expanded")
	form.Set(
		selectedItemIDParam,
		strconv.FormatInt(items[firstItemIndex].ID, decimalBase),
	)
	req := newURLEncodedRequest(
		fmt.Sprintf("/items/%d/toggle", items[firstItemIndex].ID),
		form,
	)

	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("toggle read status: %d", rec.Code)
	}

	body := rec.Body.String()
	assertContains(
		t,
		body,
		"content-panel is-open",
		"expected expanded summary-only toggle response to keep content panel open",
	)
	assertContains(
		t,
		body,
		"Expanded summary-only <strong>article</strong>",
		"expected expanded summary-only toggle response to render summary HTML in the panel",
	)
	assertNotContains(
		t,
		body,
		"content-panel-empty",
		"expected expanded summary-only toggle response to avoid empty panel fallback",
	)
	assertContains(
		t,
		body,
		classIsActive,
		"expected expanded summary-only toggle response to keep the item active",
	)
}

func TestItemExpandedKeepsActiveClass(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Expanded Active Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Expanded",
		Link:            "http://example.com/expanded",
		GUID:            "expanded-active",
		Description:     "<p>Expanded summary</p>",
		Content:         "<p>Expanded content</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	items := mustListItems(t, app, feedID)

	assertItemCount(t, items, expectedSingleItem)

	itemPath := fmt.Sprintf(
		"/items/%d?selected_item_id=item-%d",
		items[firstItemIndex].ID,
		items[firstItemIndex].ID,
	)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, itemPath, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expanded status: %d", rec.Code)
	}

	body := rec.Body.String()
	assertExpandedItemBody(t, body, items[firstItemIndex].ID)
	assertContentPanelOOBUpdate(t, body)
}

func TestItemExpandedSummaryOnlyRendersSummaryFallbackInContentPanel(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Expanded Summary Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Expanded Summary Item",
		Link:            "http://example.com/expanded-summary-item",
		GUID:            "expanded-summary-item",
		Description:     "<p>Expanded summary-only <strong>article</strong></p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	items := mustListItems(t, app, feedID)

	assertItemCount(t, items, expectedSingleItem)

	itemPath := fmt.Sprintf(
		"/items/%d?selected_item_id=item-%d",
		items[firstItemIndex].ID,
		items[firstItemIndex].ID,
	)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, itemPath, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expanded status: %d", rec.Code)
	}

	body := rec.Body.String()
	assertExpandedItemBody(t, body, items[firstItemIndex].ID)
	assertContentPanelOOBUpdate(t, body)
	articleHTML := itemArticleHTML(t, body, items[firstItemIndex].ID)
	assertContains(
		t,
		articleHTML,
		"<p>Expanded summary-only article</p>",
		"expected expanded row to keep the compact preview text",
	)
	assertNotContains(
		t,
		articleHTML,
		"<strong>article</strong>",
		"expected expanded row to avoid rendering the full summary markup",
	)
	assertContains(
		t,
		body,
		"Expanded summary-only <strong>article</strong>",
		"expected expanded summary-only response to render summary HTML in content panel",
	)
	assertNotContains(
		t,
		body,
		"content-panel-empty",
		"expected expanded summary-only response to avoid empty panel fallback",
	)
}

func TestReaderViewsRenderOnlyInactiveFeedContent(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Inactive Content Feed")
	malicious := `<p>Reader safe content <strong>stays formatted</strong></p>` +
		`<div hx-post="/attack-from-feed" data-hx-trigger="load"></div>` +
		`<form action="/attack-from-feed"><button>attack control</button></form>` +
		`<style>@import "/attack-from-feed"</style>` +
		`<svg><a href="/attack-from-feed">foreign attack</a></svg>` +
		`<math><mtext>math attack</mtext></math>` +
		`<iframe src="/attack-from-feed"></iframe>`
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Inactive Content Item",
		Link:            "http://example.com/inactive-content",
		GUID:            "inactive-content",
		Content:         malicious,
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	items := mustListItems(t, app, feedID)
	assertItemCount(t, items, expectedSingleItem)
	itemID := items[firstItemIndex].ID

	desktopPath := fmt.Sprintf("/items/%d?selected_item_id=item-%d", itemID, itemID)
	desktop := getHTMXRequest(app, desktopPath)
	assertResponseCode(t, desktop, "inactive desktop reader status")
	assertInactiveReaderResponse(t, desktop.Body.String(), "desktop")

	mobile := getHTMXRequest(app, fmt.Sprintf("/mobile/items/%d/reader", itemID))
	assertResponseCode(t, mobile, "inactive mobile reader status")
	assertInactiveReaderResponse(t, mobile.Body.String(), "mobile")
}

func assertInactiveReaderResponse(t *testing.T, body, viewName string) {
	t.Helper()

	assertContains(
		t,
		body,
		`data-reader-content="true" hx-disable`,
		"expected "+viewName+" reader htmx boundary",
	)
	assertContains(
		t,
		body,
		`<p>Reader safe content <strong>stays formatted</strong></p>`,
		"expected "+viewName+" reader safe formatting",
	)
	assertNotContains(t, body, "/attack-from-feed", "expected "+viewName+" active URLs removed")
	assertNotContains(t, body, "attack control", "expected "+viewName+" form controls removed")
	assertNotContains(t, body, "foreign attack", "expected "+viewName+" SVG content removed")
	assertNotContains(t, body, "math attack", "expected "+viewName+" MathML content removed")
}

func TestItemExpandedRendersPanelActionButtons(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Expanded Actions Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Expanded Actions Item",
		Link:            "http://example.com/expanded-actions",
		GUID:            "expanded-actions",
		Description:     "<p>Expanded actions summary</p>",
		Content:         "<p>Expanded actions content</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	items := mustListItems(t, app, feedID)

	assertItemCount(t, items, expectedSingleItem)

	itemID := items[firstItemIndex].ID
	itemPath := fmt.Sprintf("/items/%d?selected_item_id=item-%d", itemID, itemID)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, itemPath, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expanded status: %d", rec.Code)
	}

	body := rec.Body.String()
	assertExpandedPanelActions(t, body, itemID)
}

func TestItemExpandedCompactsCollapsedItemViaOOB(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Expanded Collapse Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Expanded Item",
		Link:            "http://example.com/expanded-oob",
		GUID:            "expanded-oob",
		Description:     "<p>Expanded summary</p>",
		Content:         "<p>Expanded content</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Collapsed Item",
		Link:            "http://example.com/collapsed-oob",
		GUID:            "collapsed-oob",
		Description:     "<p>Collapsed summary</p>",
		Content:         "<p>Collapsed content</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})
	items := mustListItems(t, app, feedID)

	assertItemCount(t, items, expectedTwoItems)

	targetID := items[firstItemIndex].ID
	collapseID := items[firstItemIndex+1].ID
	itemPath := fmt.Sprintf(
		"/items/%d?selected_item_id=item-%d&%s=%d",
		targetID,
		targetID,
		collapseItemIDParam,
		collapseID,
	)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, itemPath, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expanded status: %d", rec.Code)
	}

	body := rec.Body.String()
	assertExpandedItemBody(t, body, targetID)
	assertContentPanelOOBUpdate(t, body)
	assertContains(
		t,
		body,
		fmt.Sprintf(`id="item-%d"`, collapseID),
		"expected collapsed row OOB update in expanded response",
	)
	assertContains(
		t,
		body,
		"item-entry item-entry-compact",
		"expected collapsed row to render compact markup",
	)
	assertItemArticleNotActive(t, body, collapseID)
}

func TestItemCompactClosesContentPanel(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Compact Panel Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Compact Panel Item",
		Link:            "http://example.com/compact-panel",
		GUID:            "compact-panel",
		Description:     "<p>Compact panel summary</p>",
		Content:         "<p>Compact panel content</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	items := mustListItems(t, app, feedID)
	assertItemCount(t, items, expectedSingleItem)

	itemPath := fmt.Sprintf(
		"/items/%d/compact?selected_item_id=item-%d",
		items[firstItemIndex].ID,
		items[firstItemIndex].ID,
	)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, itemPath, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("compact status: %d", rec.Code)
	}

	body := rec.Body.String()
	assertContentPanelOOBUpdate(t, body)
	assertContains(
		t,
		body,
		classIsActive,
		"expected compact response to keep selected item active",
	)
	assertNotContains(
		t,
		body,
		"content-panel is-open",
		"expected compact response to close content panel",
	)
}

func TestItemCompactExpandRequestIncludesSelectedItemID(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(),
		app.db,
		exampleRSSURL,
		"Compact Selected Feed")
	if err != nil {
		t.Fatalf(errStoreUpsertFeed, err)
	}

	_, upsertErr := store.UpsertItems(context.Background(), app.db, feedID, []*gofeed.Item{{
		Title:           "Compact Item",
		Link:            "http://example.com/compact",
		GUID:            "compact-selected",
		Description:     "<p>Compact summary</p>",
		Content:         "<p>Compact content</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	if upsertErr != nil {
		t.Fatalf(errStoreUpsertItems, upsertErr)
	}

	itemsPath := feedItemsPath(feedID)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, itemsPath, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	assertResponseCode(t, rec, msgFeedItemsStatus)

	body := rec.Body.String()
	assertContains(
		t,
		body,
		`hx-vals='{"selected_item_id":"item-`,
		"expected compact item expand request to include selected_item_id",
	)
}

func TestFeedItemsRenderCompactPreviewForSummaryOnlyItems(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Compact Preview Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title: "Preview Item",
		Link:  "http://example.com/preview-item",
		GUID:  "preview-item",
		Description: `<div><h2>Big heading</h2><p>Alpha <strong>beta</strong> ` +
			`<a href="/story">gamma</a>.</p><img src="/hero.jpg" alt="hero image"></div>`,
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	items := mustListItems(t, app, feedID)

	assertItemCount(t, items, expectedSingleItem)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, feedItemsPath(feedID), http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	assertResponseCode(t, rec, msgFeedItemsStatus)

	body := rec.Body.String()
	assertContains(
		t,
		body,
		"<p>Big heading Alpha beta gamma.</p>",
		"expected compact row to render a plain-text preview paragraph",
	)
	assertNotContains(
		t,
		body,
		"<h2>Big heading</h2>",
		"expected compact row to avoid rendering full summary heading markup",
	)
	assertNotContains(
		t,
		body,
		`<img src="/hero.jpg" alt="hero image">`,
		"expected compact row to avoid rendering summary media markup",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`hx-get="/items/%d"`, items[firstItemIndex].ID),
		"expected summary-only compact row to stay expandable",
	)
	assertContains(
		t,
		body,
		`data-has-content="true"`,
		"expected summary-only compact row to remain readable in app",
	)
}

func TestFeedItemsRenderNoPreviewCompactRowsWithPublishedMeta(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "No Preview Feed")
	published := time.Date(2026, time.April, 16, 12, 30, 0, 0, time.UTC)
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "A long content-only story title that should keep its scan rhythm without a summary preview",
		Link:            "http://example.com/no-preview-item",
		GUID:            "no-preview-item",
		Content:         "<p>Full content body without a summary.</p>",
		PublishedParsed: &published,
	}})
	items := mustListItems(t, app, feedID)

	assertItemCount(t, items, expectedSingleItem)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, feedItemsPath(feedID), http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	assertResponseCode(t, rec, msgFeedItemsStatus)

	body := rec.Body.String()
	assertContains(
		t,
		body,
		`item-entry-no-preview`,
		"expected content-only compact row to opt into the no-preview styling variant",
	)
	assertContains(
		t,
		body,
		`<div class="item-meta item-meta-compact">`,
		"expected content-only compact row to render a compact metadata subline",
	)
	assertContains(
		t,
		body,
		view.FormatTime(published),
		"expected content-only compact row to show the published timestamp in the metadata subline",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`hx-get="/items/%d"`, items[firstItemIndex].ID),
		"expected content-only compact row to remain expandable",
	)
	assertNotContains(
		t,
		body,
		`<div class="item-summary">`,
		"expected content-only compact row to omit the summary block when no compact preview exists",
	)
}

func TestFeedItemsRenderSemanticReadingActions(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Affordance Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{
		{
			Title:           "Affordance Item",
			Link:            "http://example.com/affordance-item",
			GUID:            "affordance-item",
			Description:     "<p>Summary</p>",
			PublishedParsed: new(time.Now().Add(-time.Hour)),
		},
		{
			Title:           "Source Only Item",
			Link:            "http://example.com/source-only-item",
			GUID:            "source-only-item",
			PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
		},
	})
	items := mustListItems(t, app, feedID)
	assertItemCount(t, items, 2)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, feedItemsPath(feedID), http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	assertResponseCode(t, rec, msgFeedItemsStatus)

	body := rec.Body.String()
	readableItem := itemArticleHTML(t, body, items[0].ID)
	sourceOnlyItem := itemArticleHTML(t, body, items[1].ID)
	assertReadableItemActions(t, readableItem)
	assertContains(
		t,
		sourceOnlyItem,
		`class="item-title item-source-primary"`,
		"expected an item without reader content to keep its title as the primary source link",
	)
	assertNotContains(
		t,
		sourceOnlyItem,
		`item-read-in-app`,
		"expected an item without reader content to omit the in-app reading control",
	)
	assertContains(
		t,
		body,
		`<h3>Choose an item to read</h3>`,
		"expected empty reader panel heading",
	)
	assertContains(
		t,
		body,
		`<p>Select "Read in app" on any story with reader content to open it here.</p>`,
		"expected empty reader panel to point at the in-app reader action",
	)
}

func assertReadableItemActions(t *testing.T, body string) {
	t.Helper()

	expectations := []struct {
		value   string
		message string
	}{
		{`class="item-title item-read-in-app"`, "expected item title to be an in-app reading control"},
		{`aria-label="Read Affordance Item in Pulse"`, "expected a useful in-app accessible name"},
		{
			`<span class="item-inline-open-hint" aria-hidden="true">Read in app</span>`,
			"expected a visible primary reading label",
		},
		{`class="item-source-link"`, "expected a distinct source link"},
		{`title="Open original article in a new tab"`, "expected source link external behavior text"},
		{`target="_blank"`, "expected source link to open separately"},
		{`rel="noopener"`, "expected source link new-tab safety"},
	}

	for _, expectation := range expectations {
		assertContains(t, body, expectation.value, expectation.message)
	}
}

func TestItemReadToggleRendersBothStates(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Read Toggle Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Read Toggle Item",
		Link:            "http://example.com/read-toggle-item",
		GUID:            "read-toggle-item",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	items := mustListItems(t, app, feedID)
	assertItemCount(t, items, expectedSingleItem)
	itemID := items[firstItemIndex].ID

	unreadRec := getRequest(app, feedItemsPath(feedID))
	assertResponseCode(t, unreadRec, msgFeedItemsStatus)
	unreadItem := itemArticleHTML(t, unreadRec.Body.String(), itemID)
	assertItemReadToggleState(t, unreadItem, "unread", "Mark read")

	form := url.Values{
		"view":              {"compact"},
		selectedItemIDParam: {fmt.Sprintf("item-%d", itemID)},
	}
	readRec := postFormRequest(app, fmt.Sprintf("/items/%d/toggle", itemID), form)
	assertResponseCode(t, readRec, "toggle read status")
	readItem := itemArticleHTML(t, readRec.Body.String(), itemID)
	assertItemReadToggleState(t, readItem, "read", "Mark unread")
}

func assertItemReadToggleState(t *testing.T, body, state, action string) {
	t.Helper()

	assertContains(t, body, fmt.Sprintf(`class="item-read-toggle is-%s"`, state), "expected quiet toggle class")
	assertContains(t, body, fmt.Sprintf("aria-label=%q", action), "expected read toggle accessible name")
	assertContains(t, body, fmt.Sprintf("title=%q", action), "expected read toggle tooltip")
	assertContains(t, body, fmt.Sprintf("data-read-state=%q", state), "expected explicit read state")
	assertContains(t, body, `class="item-read-toggle-icon"`, "expected non-color state icon")
	assertNotContains(t, body, `class="chip"`, "expected item toggle to avoid the prominent chip treatment")

	if state == "read" {
		assertContains(t, body, `class="item-read-toggle-check"`, "expected read state checkmark")
	} else {
		assertNotContains(t, body, `class="item-read-toggle-check"`, "expected unread state open ring")
	}
}

func TestItemReadToggleStyles(t *testing.T) {
	t.Parallel()

	stylesheet, err := os.ReadFile("../../static/styles.css")
	requireNoErr(t, err, "read styles.css: %v")

	css := string(stylesheet)
	for _, selector := range []string{
		".item-read-toggle {",
		".item-read-toggle:hover {",
		".item-read-toggle:focus-visible {",
		".item-read-toggle.is-read {",
	} {
		assertContains(t, css, selector, "expected read toggle CSS regression selector")
	}

	assertContains(t, css, "width: 32px;", "expected a sufficiently large read toggle target")
	assertContains(t, css, "outline: 2px solid var(--accent);", "expected theme-aware keyboard focus treatment")
	assertContains(t, css, "background: var(--icon-button-bg-hover);", "expected theme-aware hover treatment")
}

func TestToggleReadAndCleanup(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(), app.db, exampleRSSURL, itemLimitFeedTitle)
	requireNoErr(t, err, errStoreUpsertFeed)

	upsertSingleCleanupItem(t, app, feedID)

	items, err := store.ListItems(context.Background(), app.db, feedID)
	requireNoErr(t, err, errStoreListItems)

	assertItemCount(t, items, expectedSingleItem)

	itemID := items[firstItemIndex].ID
	err = store.ToggleRead(context.Background(), app.db, itemID)
	requireNoErr(t, err, "store.ToggleRead: %v")

	readAt := queryItemReadAt(t, app.db, itemID)
	if !readAt.Valid {
		t.Fatal("expected read_at to be set")
	}

	err = store.ToggleRead(context.Background(), app.db, itemID)
	requireNoErr(t, err, "store.ToggleRead again: %v")

	readAt = queryItemReadAt(t, app.db, itemID)
	if readAt.Valid {
		t.Fatal("expected read_at to be cleared")
	}

	// Mark item as read in the past to trigger cleanup.
	past := time.Now().UTC().Add(-3 * time.Hour)
	_, err = app.db.ExecContext(
		context.Background(),
		sqlUpdateItemReadAt,
		past,
		itemID,
	)
	requireNoErr(t, err, "set read_at: %v")

	err = store.CleanupReadItems(app.db)
	requireNoErr(t, err, "store.CleanupReadItems: %v")

	items, err = store.ListItems(context.Background(), app.db, feedID)
	requireNoErr(t, err, "store.ListItems after cleanup: %v")

	assertItemCount(t, items, expectedNoItems)

	if !existsInTombstones(t, app.db, feedID, "1") {
		t.Fatal(expectedTombstoneMsg)
	}

	upsertSingleCleanupItem(t, app, feedID)

	items, err = store.ListItems(context.Background(), app.db, feedID)
	requireNoErr(t, err, "store.ListItems after reinserting: %v")

	assertItemCount(t, items, expectedNoItems)
}

func TestMarkAllRead(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, itemLimitFeedTitle)

	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Item A",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Item B",
		Link:            "http://example.com/2",
		GUID:            "2",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})

	items := mustListItems(t, app, feedID)
	assertItemCount(t, items, expectedTwoItems)

	past := time.Now().UTC().Add(-30 * time.Minute)
	_, err := app.db.ExecContext(
		context.Background(),
		sqlUpdateItemReadAt,
		past,
		items[firstItemIndex].ID,
	)
	requireNoErr(t, err, "set read_at: %v")

	rec := postRequest(
		app,
		fmt.Sprintf("/feeds/%d/items/read", feedID),
	)
	assertResponseCode(t, rec, "mark all read status")
	assertContains(t, rec.Body.String(), `data-mark-all-read-undo-button`, "expected undo control after mark-all-read")
	assertNotContains(
		t,
		rec.Body.String(),
		`data-mark-all-read-button hidden`,
		"expected mark-all-read button to be replaced by undo",
	)

	assertAllItemsRead(t, app, feedID)
}

func TestMarkAllReadUndo(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, itemLimitFeedTitle)

	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Item A",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Item B",
		Link:            "http://example.com/2",
		GUID:            "2",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})

	items := mustListItems(t, app, feedID)
	assertItemCount(t, items, expectedTwoItems)
	initialReadItemID := items[firstItemIndex].ID
	initialUnreadItemID := items[expectedOneUnread].ID

	past := time.Now().UTC().Add(-30 * time.Minute)
	_, err := app.db.ExecContext(
		context.Background(),
		sqlUpdateItemReadAt,
		past,
		initialReadItemID,
	)
	requireNoErr(t, err, "set initial read_at: %v")

	rec := postRequest(app, fmt.Sprintf("/feeds/%d/items/read", feedID))
	assertResponseCode(t, rec, "mark all read status")
	assertAllItemsRead(t, app, feedID)

	form := make(url.Values)
	form.Set("undo_token", extractUndoToken(t, rec.Body.String()))

	undoRec := postFormRequest(app, fmt.Sprintf("/feeds/%d/items/read/undo", feedID), form)
	assertResponseCode(t, undoRec, "undo mark all read status")

	items = mustListItems(t, app, feedID)
	assertUndoRestoresReadStates(t, items, initialReadItemID, initialUnreadItemID)

	body := undoRec.Body.String()
	assertMarkAllReadButtonVisible(t, body, "expected mark-all-read button after undo")
	assertUndoTokenCleared(t, body, "expected active undo token to clear after undo")
}

func TestMarkAllReadUndoClearsWhenSwitchingFeeds(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Undo Source")
	otherFeedID := mustUpsertFeed(t, app, "http://example.com/other", "Other Feed")
	mustUpsertSingleStory(
		t,
		app,
		feedID,
		"Undo me",
		"http://example.com/undo-me",
		"undo-me",
		time.Now().UTC().Add(-time.Hour),
	)
	mustUpsertSingleStory(
		t,
		app,
		otherFeedID,
		"Other story",
		"http://example.com/other-story",
		"other-story",
		time.Now().UTC().Add(-2*time.Hour),
	)

	rec := postRequest(app, fmt.Sprintf("/feeds/%d/items/read", feedID))
	assertResponseCode(t, rec, "mark all read status")
	assertContains(t, rec.Body.String(), `data-mark-all-read-undo-button`, "expected undo control before feed switch")

	switchedRec := getRequest(app, fmt.Sprintf("/feeds/%d/items", otherFeedID))
	assertResponseCode(t, switchedRec, msgFeedItemsStatus)

	returnedRec := getRequest(app, fmt.Sprintf("/feeds/%d/items", feedID))
	assertResponseCode(t, returnedRec, msgFeedItemsStatus)

	body := returnedRec.Body.String()
	assertMarkAllReadButtonVisible(t, body, "expected mark-all-read button after feed switch")
	assertUndoTokenCleared(t, body, "expected active undo token to clear after feed switch")
}

func TestMarkAllReadUndoClearsWhenReadItemsSwept(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, itemLimitFeedTitle)
	mustUpsertSingleStory(
		t,
		app,
		feedID,
		"Sweep me",
		"http://example.com/sweep-me",
		"sweep-me",
		time.Now().UTC().Add(-time.Hour),
	)

	rec := postRequest(app, fmt.Sprintf("/feeds/%d/items/read", feedID))
	assertResponseCode(t, rec, "mark all read status")
	assertContains(t, rec.Body.String(), `data-mark-all-read-undo-button`, "expected undo control before sweep")

	sweepRec := postRequest(app, fmt.Sprintf("/feeds/%d/items/sweep", feedID))
	assertResponseCode(t, sweepRec, "sweep read status")

	body := sweepRec.Body.String()
	assertMarkAllReadButtonVisible(t, body, "expected mark-all-read button after sweep")
	assertUndoTokenCleared(t, body, "expected active undo token to clear after sweep")
}

func TestFeedItemsRenderMarkAllReadAction(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, itemLimitFeedTitle)
	mustUpsertSingleStory(
		t,
		app,
		feedID,
		"Guarded bulk action",
		"http://example.com/guarded",
		"guarded-bulk-action",
		time.Now().UTC().Add(-time.Hour),
	)

	rec := getRequest(app, fmt.Sprintf("/feeds/%d/items", feedID))
	assertResponseCode(t, rec, msgFeedItemsStatus)

	body := rec.Body.String()
	assertContains(t, body, `data-mark-all-read-button`, "expected mark-all-read action button")
	assertContains(t, body, "Mark all read", "expected mark-all-read button label")
	assertNotContains(t, body, `data-mark-all-read-confirm`, "expected confirmation control to be removed")
	assertNotContains(t, body, "Mark all unread items as read?", "expected confirmation copy to be removed")
}

func TestSweepReadItems(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	fixture := setupSweepReadFixture(t, app)
	feedID := fixture.feedID
	otherFeedID := fixture.otherFeedID

	now := time.Now().UTC()
	markSweepItemsRead(t, app, feedID, otherFeedID, now)

	rec := postRequest(
		app,
		fmt.Sprintf("/feeds/%d/items/sweep", feedID),
	)
	assertResponseCode(t, rec, "sweep read status")

	assertSweepReadResults(t, app, feedID, otherFeedID, rec.Body.String())
}
