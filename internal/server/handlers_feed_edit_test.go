//nolint:testpackage // Handler integration tests intentionally exercise unexported helpers.
package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"

	"rss/internal/store"
)

func TestEnterFeedEditMode(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Edit Mode Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Unread",
		Link:            "http://example.com/unread",
		GUID:            "unread",
		Description:     "<p>Unread summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})

	zeroFeedID := mustUpsertFeed(t, app, "http://example.com/zero", "Zero Feed")
	if zeroFeedID == expectedNoItems {
		t.Fatal("expected zero feed id to be set")
	}

	form := url.Values{}
	setSelectedFeedID(form, feedID)
	rec := postFormRequest(app, pathFeedEditMode, form)
	assertResponseCode(t, rec, "edit mode status")

	body := rec.Body.String()
	assertEnterFeedEditModeBody(t, body, feedID)
	assertEditModeCookieSet(t, rec.Header().Get(headerSetCookie))

	itemsPath := feedItemsPath(feedID)
	itemsRec := getRequest(app, itemsPath, editModeCookie())
	assertResponseCode(t, itemsRec, msgFeedItemsStatus)
	assertContains(
		t,
		itemsRec.Body.String(),
		classFeedListEdit,
		"expected edit mode to persist while cookie is set",
	)
}

func TestCancelFeedEditModeEndpoint(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(
		t,
		app,
		exampleRSSURL,
		"Cancel Edit Mode Feed",
	)

	form := url.Values{}
	setSelectedFeedID(form, feedID)
	rec := postFormRequest(
		app,
		pathEditModeCancel,
		form,
		editModeCookie(),
	)
	assertResponseCode(t, rec, "cancel edit mode status")

	body := rec.Body.String()
	assertNotContains(
		t,
		body,
		classFeedListEdit,
		"expected edit mode class to be cleared",
	)
	assertNotContains(
		t,
		body,
		`class="feed-title-revert"`,
		"expected no revert controls outside edit mode",
	)
	assertContains(
		t,
		body,
		`class="edit-feeds-button"`,
		"expected pencil edit control after cancel",
	)
	assertContains(
		t,
		body,
		`aria-label="Edit feeds"`,
		"expected edit control to keep its accessible name",
	)
	assertContains(
		t,
		body,
		`<span class="edit-feeds-label" aria-hidden="true">Edit</span>`,
		"expected visible edit control label after cancel",
	)
	assertNotContains(
		t,
		body,
		`class="feed-drag-handle"`,
		"expected drag handles to be hidden outside edit mode",
	)

	assertEditModeCookieCleared(t, rec.Header().Get(headerSetCookie))
}

func TestFeedEditModeCancelDiscardsPendingRenames(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Cancel Feed")

	form := url.Values{}
	setSelectedFeedID(form, feedID)
	form.Set(fmt.Sprintf("feed_title_%d", feedID), "Changed But Canceled")
	form.Set(fmt.Sprintf("feed_delete_%d", feedID), valueEnabled)
	rec := postFormRequest(
		app,
		pathEditModeCancel,
		form,
		editModeCookie(),
	)
	assertResponseCode(t, rec, "cancel status")

	body := rec.Body.String()
	assertNotContains(
		t,
		body,
		classFeedListEdit,
		"expected edit mode to be cleared on cancel",
	)
	assertEditModeCookieCleared(t, rec.Header().Get(headerSetCookie))

	feeds, err := store.ListFeeds(context.Background(), app.db)
	requireNoErr(t, err, errStoreListFeeds)

	if len(feeds) != expectedSingleFeed {
		t.Fatalf(
			"expected feed to remain after cancel, got %d feeds",
			len(feeds),
		)
	}

	if feeds[firstFeedIndex].Title != "Cancel Feed" {
		t.Fatalf(
			"expected pending rename to be discarded, got %q",
			feeds[firstFeedIndex].Title,
		)
	}
}

func TestFeedEditModeSaveAppliesRenamesAndExits(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Old Title")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Unread",
		Link:            "http://example.com/unread",
		GUID:            "unread",
		Description:     "<p>Unread summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})

	form := url.Values{}
	form.Set(fmt.Sprintf("feed_title_%d", feedID), newFeedTitle)
	setSelectedFeedID(form, feedID)
	rec := postFormRequest(app, pathEditModeSave, form, editModeCookie())
	assertResponseCode(t, rec, "save status")

	body := rec.Body.String()
	assertContains(t, body, newFeedTitle, "expected renamed title in response")
	assertNotContains(
		t,
		body,
		classFeedListEdit,
		"expected edit mode to be cleared on save",
	)
	assertEditModeCookieCleared(t, rec.Header().Get(headerSetCookie))

	feeds, err := store.ListFeeds(context.Background(), app.db)
	requireNoErr(t, err, errStoreListFeeds)

	if feeds[firstFeedIndex].Title != newFeedTitle {
		t.Fatalf(
			"expected rename to persist on save, got %q",
			feeds[firstFeedIndex].Title,
		)
	}
}

func TestFeedEditModeSaveDeletesMarkedFeeds(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	deleteFeedID := mustUpsertFeed(
		t,
		app,
		"http://example.com/delete",
		"Delete Me",
	)
	keepFeedID := mustUpsertFeed(
		t,
		app,
		"http://example.com/keep",
		"Keep Me",
	)
	mustUpsertItems(t, app, keepFeedID, []*gofeed.Item{{
		Title:           "Keep Item",
		Link:            "http://example.com/keep-item",
		GUID:            "keep-item",
		Description:     "<p>Keep summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})

	form := url.Values{}
	setSelectedFeedID(form, deleteFeedID)
	form.Set(fmt.Sprintf("feed_delete_%d", deleteFeedID), valueEnabled)
	rec := postFormRequest(app, pathEditModeSave, form, editModeCookie())
	assertResponseCode(t, rec, "save status")

	body := rec.Body.String()
	assertFeedEditModeSaveDeleteBody(t, body)
	assertEditModeCookieCleared(t, rec.Header().Get(headerSetCookie))

	feeds, err := store.ListFeeds(context.Background(), app.db)
	requireNoErr(t, err, errStoreListFeeds)

	if len(feeds) != expectedSingleFeed {
		t.Fatalf("expected one feed after save delete, got %d", len(feeds))
	}

	if feeds[firstFeedIndex].ID != keepFeedID {
		t.Fatalf(
			"expected remaining feed %d, got %d",
			keepFeedID,
			feeds[firstFeedIndex].ID,
		)
	}
}

func TestFeedEditModeSavePersistsFeedOrder(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	fixture := seedFeedOrderFixtures(t, app)

	assertFeedEditModeOrderRequest(
		t,
		app,
		pathEditModeSave,
		fixture.firstID,
		[]int64{fixture.thirdID, fixture.firstID, fixture.secondID},
		[]int64{fixture.thirdID, fixture.firstID, fixture.secondID},
		"save",
	)
}

func TestFeedEditModeCancelIgnoresPendingFeedOrder(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	fixture := seedFeedOrderFixtures(t, app)

	assertFeedEditModeOrderRequest(
		t,
		app,
		pathEditModeCancel,
		fixture.firstID,
		[]int64{fixture.thirdID, fixture.firstID, fixture.secondID},
		[]int64{fixture.firstID, fixture.secondID, fixture.thirdID},
		"cancel",
	)
}

func seedFeedOrderFixtures(t *testing.T, app *App) feedOrderFixtureIDs {
	t.Helper()

	firstID := mustUpsertFeed(t, app, "http://example.com/first", "First")
	secondID := mustUpsertFeed(t, app, "http://example.com/second", "Second")
	thirdID := mustUpsertFeed(t, app, "http://example.com/third", "Third")

	return feedOrderFixtureIDs{
		firstID:  firstID,
		secondID: secondID,
		thirdID:  thirdID,
	}
}

func newEditModeOrderRequest(
	t *testing.T,
	path string,
	selectedID int64,
	orderedFeedIDs ...int64,
) *http.Request {
	t.Helper()

	form := url.Values{}
	setSelectedFeedID(form, selectedID)

	for _, feedID := range orderedFeedIDs {
		form.Add("feed_order", strconv.FormatInt(feedID, decimalBase))
	}

	req := newURLEncodedRequest(path, form)
	req.AddCookie(editModeCookie())

	return req
}

func assertFeedEditModeOrderRequest(
	t *testing.T,
	app *App,
	path string,
	selectedID int64,
	pendingOrder []int64,
	expectedOrder []int64,
	action string,
) {
	t.Helper()

	req := newEditModeOrderRequest(t, path, selectedID, pendingOrder...)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	assertResponseCode(t, rec, action+" status")

	feeds, err := store.ListFeeds(context.Background(), app.db)
	requireNoErr(t, err, errStoreListFeeds)

	if len(feeds) != len(expectedOrder) {
		t.Fatalf("expected %d feeds, got %d", len(expectedOrder), len(feeds))
	}

	for idx, feedID := range expectedOrder {
		if feeds[idx].ID == feedID {
			continue
		}

		gotOrder := []int64{feeds[0].ID, feeds[1].ID, feeds[2].ID}
		t.Fatalf("unexpected feed order after %s: got %v", action, gotOrder)
	}
}

func assertFeedEditModeRevertUI(t *testing.T, body string, feedID int64) {
	t.Helper()

	target := fmt.Sprintf(`data-feed-title-input="feed-title-%d"`, feedID)
	assertContains(t, body, target, "expected revert control target")
	assertContains(
		t,
		body,
		fmt.Sprintf(`data-original-title=%q`, sourceTitle),
		"expected canonical source title in revert control",
	)
	assertContains(
		t,
		body,
		`title="Revert to original feed title"`,
		"expected revert control title text",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`aria-label="Revert feed name to original title: %s"`, sourceTitle),
		"expected revert control aria label to include canonical title",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`value=%q`, customTitle),
		"expected editable value to remain the current custom title",
	)
}

func TestFeedEditModeShowsRevertToCanonicalTitle(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, sourceTitle)
	err := store.UpdateFeedTitle(context.Background(), app.db, feedID, customTitle)
	requireNoErr(t, err, "store.UpdateFeedTitle: %v")

	form := url.Values{}
	setSelectedFeedID(form, feedID)
	rec := postFormRequest(app, pathFeedEditMode, form)
	assertResponseCode(t, rec, "edit mode status")

	body := rec.Body.String()
	assertFeedEditModeRevertUI(t, body, feedID)
}

func TestFeedEditModeSaveCanonicalTitleClearsCustomOverride(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, sourceTitle)
	err := store.UpdateFeedTitle(context.Background(), app.db, feedID, customTitle)
	requireNoErr(t, err, "store.UpdateFeedTitle: %v")

	form := url.Values{}
	form.Set(fmt.Sprintf("feed_title_%d", feedID), sourceTitle)
	setSelectedFeedID(form, feedID)
	rec := postFormRequest(app, pathEditModeSave, form, editModeCookie())
	assertResponseCode(t, rec, "save status")
	assertFirstFeedTitle(
		t,
		app.db,
		sourceTitle,
		"expected canonical title after save, got %q",
	)

	_, err = store.UpsertFeed(context.Background(),
		app.db,
		exampleRSSURL,
		"Updated Source Title")

	requireNoErr(t, err, "store.UpsertFeed update: %v")
	assertFirstFeedTitle(
		t,
		app.db,
		"Updated Source Title",
		"expected custom title override to be cleared, got %q",
	)
}
