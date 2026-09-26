//nolint:testpackage // Handler tests intentionally exercise unexported request helpers.
package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

var errForcedUndoTokenGeneration = errors.New("forced token generation failure")

//nolint:funlen,revive // Keeps the preview-to-apply concurrency contract in one test.
func TestCatchUpPreviewAndApplyExposeRecalculatedResult(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "http://example.com/catch-up", "Catch up")
	otherFeedID := mustUpsertFeed(t, app, "http://example.com/catch-up-other", "Other")
	cutoff := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	old := cutoff.Add(-time.Hour)
	boundary := cutoff

	mustUpsertItems(t, app, feedID, []*gofeed.Item{
		newGofeedItem("Old unread", "https://example.com/old", "old", "", &old),
		newGofeedItem("Boundary", "https://example.com/boundary", "boundary", "", &boundary),
		newGofeedItem("Already read", "https://example.com/already-read", "already-read", "", &old),
	})
	mustUpsertSingleStory(
		t,
		app,
		otherFeedID,
		"Other feed",
		"https://example.com/other",
		"other",
		old,
	)
	mustMarkFeedItemRead(t, app, feedID, "already-read")

	previewPath := fmt.Sprintf(
		"/feeds/%d/items/catch-up/preview?cutoff=%s",
		feedID,
		url.QueryEscape(cutoff.Format(time.RFC3339Nano)),
	)
	preview := getHTMXRequest(app, previewPath)
	assertResponseCode(t, preview, "catch-up preview status")
	assertContains(t, preview.Body.String(), `data-catch-up-preview`, "expected preview fragment marker")
	assertContains(t, preview.Body.String(), `data-count="1"`, "expected only the eligible unread item in preview")
	assertContains(
		t,
		preview.Body.String(),
		`data-cutoff="2026-07-15T12:00:00Z"`,
		"expected preview to return the normalized cutoff instant",
	)
	assertUnreadStateByGUID(t, app, feedID, "old", false)

	newArrival := cutoff.Add(-2 * time.Hour)
	mustUpsertSingleStory(
		t,
		app,
		feedID,
		"Arrived after preview",
		"https://example.com/arrived",
		"arrived",
		newArrival,
	)

	form := url.Values{catchUpCutoffField: {cutoff.Format(time.RFC3339Nano)}}
	response := postHTMXFormRequest(app, fmt.Sprintf("/feeds/%d/items/catch-up", feedID), form)
	assertResponseCode(t, response, "catch-up apply status")
	if got := response.Header().Get("X-Catch-Up-Affected-Count"); got != "2" {
		t.Fatalf("expected actual affected count header 2, got %q", got)
	}
	var events map[string]catchUpAppliedEvent
	decodeErr := json.Unmarshal([]byte(response.Header().Get("Hx-Trigger")), &events)
	if decodeErr != nil {
		t.Fatalf("decode Catch up event: %v", decodeErr)
	}
	event, ok := events[catchUpResultEvent]
	if !ok || event.FeedID != feedID || event.Cutoff != "2026-07-15T12:00:00Z" || event.AffectedCount != 2 {
		t.Fatalf("expected exact Catch up event detail, got %#v", events)
	}
	if event.UndoToken == "" {
		t.Fatal("expected Catch up event to expose the feed-scoped undo token")
	}
	assertContains(t, response.Body.String(), `data-mark-all-read-undo-button`, "expected shared feed undo control")

	assertUnreadStateByGUID(t, app, feedID, "old", true)
	assertUnreadStateByGUID(t, app, feedID, "arrived", true)
	assertUnreadStateByGUID(t, app, feedID, "boundary", false)
	assertUnreadStateByGUID(t, app, feedID, "already-read", true)
	assertUnreadStateByGUID(t, app, otherFeedID, "other", false)
}

func TestCatchUpMobileSurfaceUsesMobileStreamAndUndoResponse(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "http://example.com/catch-up-mobile", "Mobile catch up")
	cutoff := time.Now().UTC()
	mustUpsertSingleStory(
		t,
		app,
		feedID,
		"Mobile story",
		"https://example.com/mobile-story",
		"mobile-story",
		cutoff.Add(-time.Hour),
	)

	form := url.Values{
		catchUpCutoffField:  {cutoff.Format(time.RFC3339Nano)},
		catchUpSurfaceField: {catchUpMobileSurface},
		formSelectedFeedID:  {strconv.FormatInt(feedID, 10)},
	}
	apply := postHTMXFormRequest(app, fmt.Sprintf("/feeds/%d/items/catch-up", feedID), form)
	assertResponseCode(t, apply, "mobile catch-up apply")
	assertContains(t, apply.Body.String(), `data-mobile-stream="true"`, "expected mobile stream response")
	assertNotContains(t, apply.Body.String(), `class="items"`, "expected desktop item-list response to be skipped")
	assertUnreadStateByGUID(t, app, feedID, "mobile-story", true)

	var events map[string]catchUpAppliedEvent
	decodeErr := json.Unmarshal([]byte(apply.Header().Get("Hx-Trigger")), &events)
	if decodeErr != nil {
		t.Fatalf("decode mobile Catch up event: %v", decodeErr)
	}
	event, ok := events[catchUpResultEvent]
	if !ok || event.UndoToken == "" {
		t.Fatalf("expected mobile Catch up event with an undo token, got %#v", events)
	}

	undoForm := url.Values{
		"undo_token":        {event.UndoToken},
		catchUpSurfaceField: {catchUpMobileSurface},
		formSelectedFeedID:  {strconv.FormatInt(feedID, 10)},
	}
	undo := postHTMXFormRequest(app, fmt.Sprintf("/feeds/%d/items/read/undo", feedID), undoForm)
	assertResponseCode(t, undo, "mobile catch-up undo")
	assertContains(t, undo.Body.String(), `data-mobile-stream="true"`, "expected mobile stream after undo")
	assertContains(t, undo.Body.String(), "Mobile story", "expected undo to restore the story in the mobile stream")
	assertUnreadStateByGUID(t, app, feedID, "mobile-story", false)
}

func TestCatchUpFullPageApplyRedirectsWithResult(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "http://example.com/catch-up-full-page", "Full page")
	cutoff := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	mustUpsertSingleStory(t, app, feedID, "Old", "https://example.com/old-full", "old-full", cutoff.Add(-time.Hour))

	form := url.Values{catchUpCutoffField: {cutoff.Format(time.RFC3339Nano)}}
	response := postFormRequest(app, fmt.Sprintf("/feeds/%d/items/catch-up", feedID), form)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("expected full-page catch-up to redirect, got %d", response.Code)
	}

	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	if location.Path != "/" {
		t.Fatalf("expected redirect to app root, got %q", location.Path)
	}
	query := location.Query()
	if got := query.Get("catch_up_feed_id"); got != strconv.FormatInt(feedID, 10) {
		t.Fatalf("expected selected feed %d in redirect, got %q", feedID, got)
	}
	if got := query.Get(catchUpPreviewCountField); got != "1" {
		t.Fatalf("expected actual catch-up count in redirect, got %q", got)
	}
	if got := query.Get("catch_up_cutoff"); got != cutoff.Format(time.RFC3339Nano) {
		t.Fatalf("expected cutoff instant in redirect, got %q", got)
	}
	assertUnreadStateByGUID(t, app, feedID, "old-full", true)

	page := getRequest(app, location.RequestURI())
	assertResponseCode(t, page, "full-page catch-up destination")
	assertContains(t, page.Body.String(), "Old", "expected full-page destination to render the selected feed")
	assertContains(
		t,
		page.Body.String(),
		`data-mark-all-read-undo-button`,
		"expected the full-page destination to retain Catch up undo",
	)
}

func TestFullPageSelectedFeedIgnoresUnknownFeedID(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "http://example.com/known-feed", "Known feed")
	mustUpsertSingleStory(
		t,
		app,
		feedID,
		"Known story",
		"https://example.com/known-story",
		"known-story",
		time.Now().UTC(),
	)

	page := getRequest(app, "/?catch_up_feed_id=999999&catch_up_count=1&catch_up_cutoff=2026-07-15T12:00:00Z")
	assertResponseCode(t, page, "unknown full-page selected feed")
	assertContains(t, page.Body.String(), "Pick a feed to start reading.", "expected safe empty state for unknown feed")
	assertNotContains(t, page.Body.String(), "Known story", "expected unknown feed ID not to select another feed")
}

func TestCatchUpMobileFullPageApplyRedirectsToMobileStream(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "http://example.com/catch-up-mobile-full", "Mobile full page")
	cutoff := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	mustUpsertSingleStory(
		t,
		app,
		feedID,
		"Old mobile story",
		"https://example.com/old-mobile-full",
		"old-mobile-full",
		cutoff.Add(-time.Hour),
	)

	form := url.Values{
		catchUpCutoffField:  {cutoff.Format(time.RFC3339Nano)},
		catchUpSurfaceField: {catchUpMobileSurface},
		formSelectedFeedID:  {strconv.FormatInt(feedID, 10)},
	}
	response := postFormRequest(app, fmt.Sprintf("/feeds/%d/items/catch-up", feedID), form)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("expected mobile full-page catch-up to redirect, got %d", response.Code)
	}

	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse mobile redirect location: %v", err)
	}
	if location.Path != "/mobile/stream" {
		t.Fatalf("expected mobile redirect path, got %q", location.Path)
	}
	query := location.Query()
	if got := query.Get(formSelectedFeedID); got != strconv.FormatInt(feedID, 10) {
		t.Fatalf("expected selected feed %d in mobile redirect, got %q", feedID, got)
	}
	if got := query.Get(catchUpPreviewCountField); got != "1" {
		t.Fatalf("expected actual catch-up count in mobile redirect, got %q", got)
	}
	if got := query.Get("catch_up_cutoff"); got != cutoff.Format(time.RFC3339Nano) {
		t.Fatalf("expected cutoff instant in mobile redirect, got %q", got)
	}
}

func TestCatchUpInvalidCutoffDoesNotMutateOrGenerateUndo(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "http://example.com/catch-up-invalid", "Invalid cutoff")
	cutoff := time.Now().UTC()
	mustUpsertSingleStory(
		t,
		app,
		feedID,
		"Unread",
		"https://example.com/unread-invalid",
		"unread",
		cutoff.Add(-time.Hour),
	)

	generationCalls := 0
	app.undoTokenGenerator = func() (string, error) {
		generationCalls++

		return "test-token", nil
	}

	for _, invalidCutoff := range []string{"", "2026-07-15", "not-a-time"} {
		form := url.Values{catchUpCutoffField: {invalidCutoff}}
		response := postFormRequest(app, fmt.Sprintf("/feeds/%d/items/catch-up", feedID), form)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("expected invalid cutoff %q to return 400, got %d", invalidCutoff, response.Code)
		}
		assertUnreadStateByGUID(t, app, feedID, "unread", false)
	}

	previewPath := fmt.Sprintf("/feeds/%d/items/catch-up/preview?cutoff=2026-07-15", feedID)
	preview := getRequest(app, previewPath)
	if preview.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid preview cutoff to return 400, got %d", preview.Code)
	}
	if generationCalls != 0 {
		t.Fatalf("expected no undo token generation for invalid cutoffs, got %d calls", generationCalls)
	}
}

func TestCatchUpTokenGenerationFailurePrecedesMutation(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "http://example.com/catch-up-token-failure", "Token failure")
	cutoff := time.Now().UTC()
	mustUpsertSingleStory(
		t,
		app,
		feedID,
		"Unread",
		"https://example.com/unread-token-failure",
		"unread",
		cutoff.Add(-time.Hour),
	)
	app.undoTokenGenerator = func() (string, error) {
		return "", errForcedUndoTokenGeneration
	}

	form := url.Values{catchUpCutoffField: {cutoff.Format(time.RFC3339Nano)}}
	response := postHTMXFormRequest(app, fmt.Sprintf("/feeds/%d/items/catch-up", feedID), form)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected token generation failure to return 500, got %d", response.Code)
	}
	assertUnreadStateByGUID(t, app, feedID, "unread", false)
}

func TestCatchUpUndoRetainsTokenAfterRestoreFailure(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "http://example.com/catch-up-undo-retry", "Undo retry")
	cutoff := time.Now().UTC()
	old := cutoff.Add(-time.Hour)
	mustUpsertItems(t, app, feedID, []*gofeed.Item{
		newGofeedItem("Restorable", "https://example.com/restorable", "restorable", "", &old),
		newGofeedItem("Blocked", "https://example.com/blocked", "blocked", "", &old),
	})

	apply := postHTMXFormRequest(
		app,
		fmt.Sprintf("/feeds/%d/items/catch-up", feedID),
		url.Values{catchUpCutoffField: {cutoff.Format(time.RFC3339Nano)}},
	)
	assertResponseCode(t, apply, "catch-up apply before undo retry")
	token := extractUndoToken(t, apply.Body.String())

	_, err := app.db.ExecContext(context.Background(), `
CREATE TRIGGER fail_catch_up_undo BEFORE UPDATE OF read_at ON items
WHEN OLD.guid = 'blocked' AND NEW.read_at IS NULL
BEGIN
	SELECT RAISE(ABORT, 'forced catch-up undo failure');
END;
	`)
	requireNoErr(t, err, "create catch-up undo failure trigger: %v")

	undoForm := url.Values{"undo_token": {token}}
	failedUndo := postFormRequest(app, fmt.Sprintf("/feeds/%d/items/read/undo", feedID), undoForm)
	if failedUndo.Code != http.StatusInternalServerError {
		t.Fatalf("expected restoration failure to return 500, got %d", failedUndo.Code)
	}
	assertUnreadStateByGUID(t, app, feedID, "restorable", true)
	assertUnreadStateByGUID(t, app, feedID, "blocked", true)

	_, err = app.db.ExecContext(context.Background(), "DROP TRIGGER fail_catch_up_undo")
	requireNoErr(t, err, "drop catch-up undo failure trigger: %v")

	retriedUndo := postFormRequest(app, fmt.Sprintf("/feeds/%d/items/read/undo", feedID), undoForm)
	assertResponseCode(t, retriedUndo, "retry catch-up undo")
	assertUnreadStateByGUID(t, app, feedID, "restorable", false)
	assertUnreadStateByGUID(t, app, feedID, "blocked", false)
	assertUndoTokenCleared(t, retriedUndo.Body.String(), "expected successful retry to consume the undo token")
}

func TestCatchUpUndoRejectsWrongFeedAndStaleToken(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "http://example.com/catch-up-undo", "Undo")
	otherFeedID := mustUpsertFeed(t, app, "http://example.com/catch-up-undo-other", "Other")
	cutoff := time.Now().UTC()
	old := cutoff.Add(-time.Hour)
	later := cutoff.Add(-10 * time.Minute)
	mustUpsertItems(t, app, feedID, []*gofeed.Item{
		newGofeedItem("Catch up item", "https://example.com/catch-up-item", "catch-up-item", "", &old),
		newGofeedItem("Later bulk item", "https://example.com/later-bulk", "later-bulk", "", &later),
	})
	mustUpsertSingleStory(t, app, otherFeedID, "Other", "https://example.com/other-undo", "other", old)

	apply := postHTMXFormRequest(
		app,
		fmt.Sprintf("/feeds/%d/items/catch-up", feedID),
		url.Values{catchUpCutoffField: {cutoff.Add(-30 * time.Minute).Format(time.RFC3339Nano)}},
	)
	assertResponseCode(t, apply, "catch-up apply before token checks")
	oldToken := extractUndoToken(t, apply.Body.String())

	wrongFeedUndo := postFormRequest(
		app,
		fmt.Sprintf("/feeds/%d/items/read/undo", otherFeedID),
		url.Values{"undo_token": {oldToken}},
	)
	assertResponseCode(t, wrongFeedUndo, "wrong-feed catch-up undo")
	assertUnreadStateByGUID(t, app, feedID, "catch-up-item", true)
	assertUnreadStateByGUID(t, app, otherFeedID, "other", false)

	markAll := postRequest(app, fmt.Sprintf("/feeds/%d/items/read", feedID))
	assertResponseCode(t, markAll, "later mark-all-read action")
	newToken := extractUndoToken(t, markAll.Body.String())
	if oldToken == newToken {
		t.Fatal("expected later bulk action to replace the prior undo token")
	}

	staleUndo := postFormRequest(
		app,
		fmt.Sprintf("/feeds/%d/items/read/undo", feedID),
		url.Values{"undo_token": {oldToken}},
	)
	assertResponseCode(t, staleUndo, "stale catch-up undo")
	assertUnreadStateByGUID(t, app, feedID, "catch-up-item", true)
	assertUnreadStateByGUID(t, app, feedID, "later-bulk", true)

	currentUndo := postFormRequest(
		app,
		fmt.Sprintf("/feeds/%d/items/read/undo", feedID),
		url.Values{"undo_token": {newToken}},
	)
	assertResponseCode(t, currentUndo, "current mark-all-read undo")
	assertUnreadStateByGUID(t, app, feedID, "catch-up-item", true)
	assertUnreadStateByGUID(t, app, feedID, "later-bulk", false)
}

//nolint:funlen,revive // Groups the three independent undo invalidation contracts.
func TestCatchUpUndoInvalidatesOnFeedDepartureSweepAndRestart(t *testing.T) {
	t.Parallel()

	t.Run("feed departure", func(t *testing.T) {
		t.Parallel()

		app := newTestApp(t)
		feedID := mustUpsertFeed(t, app, "http://example.com/catch-up-departure", "Departure")
		otherFeedID := mustUpsertFeed(t, app, "http://example.com/catch-up-departure-other", "Other")
		cutoff := time.Now().UTC()
		mustUpsertSingleStory(
			t,
			app,
			feedID,
			"Leave me read",
			"https://example.com/leave-read",
			"leave-read",
			cutoff.Add(-time.Hour),
		)

		apply := postHTMXFormRequest(
			app,
			fmt.Sprintf("/feeds/%d/items/catch-up", feedID),
			url.Values{catchUpCutoffField: {cutoff.Format(time.RFC3339Nano)}},
		)
		assertResponseCode(t, apply, "catch-up apply before departure")
		token := extractUndoToken(t, apply.Body.String())

		depart := getRequest(app, fmt.Sprintf("/feeds/%d/items", otherFeedID))
		assertResponseCode(t, depart, "depart from catch-up feed")
		returned := getRequest(app, fmt.Sprintf("/feeds/%d/items", feedID))
		assertResponseCode(t, returned, "return to catch-up feed")
		assertUndoTokenCleared(t, returned.Body.String(), "expected feed departure to invalidate catch-up undo")

		staleUndo := postFormRequest(
			app,
			fmt.Sprintf("/feeds/%d/items/read/undo", feedID),
			url.Values{"undo_token": {token}},
		)
		assertResponseCode(t, staleUndo, "stale undo after feed departure")
		assertUnreadStateByGUID(t, app, feedID, "leave-read", true)
	})

	t.Run("sweep", func(t *testing.T) {
		t.Parallel()

		app := newTestApp(t)
		feedID := mustUpsertFeed(t, app, "http://example.com/catch-up-sweep", "Sweep")
		cutoff := time.Now().UTC()
		mustUpsertSingleStory(
			t,
			app,
			feedID,
			"Remove me",
			"https://example.com/remove-me",
			"remove-me",
			cutoff.Add(-time.Hour),
		)

		apply := postHTMXFormRequest(
			app,
			fmt.Sprintf("/feeds/%d/items/catch-up", feedID),
			url.Values{catchUpCutoffField: {cutoff.Format(time.RFC3339Nano)}},
		)
		assertResponseCode(t, apply, "catch-up apply before sweep")
		token := extractUndoToken(t, apply.Body.String())

		sweep := postRequest(app, fmt.Sprintf("/feeds/%d/items/sweep", feedID))
		assertResponseCode(t, sweep, "sweep catch-up read item")
		assertUndoTokenCleared(t, sweep.Body.String(), "expected sweep to invalidate catch-up undo")

		staleUndo := postFormRequest(
			app,
			fmt.Sprintf("/feeds/%d/items/read/undo", feedID),
			url.Values{"undo_token": {token}},
		)
		assertResponseCode(t, staleUndo, "stale undo after sweep")
		if got := len(mustListItems(t, app, feedID)); got != 0 {
			t.Fatalf("expected swept item to stay deleted, found %d items", got)
		}
	})

	t.Run("restart", func(t *testing.T) {
		t.Parallel()

		app := newTestApp(t)
		feedID := mustUpsertFeed(t, app, "http://example.com/catch-up-restart", "Restart")
		cutoff := time.Now().UTC()
		mustUpsertSingleStory(
			t,
			app,
			feedID,
			"Read before restart",
			"https://example.com/restart-story",
			"restart-story",
			cutoff.Add(-time.Hour),
		)

		apply := postHTMXFormRequest(
			app,
			fmt.Sprintf("/feeds/%d/items/catch-up", feedID),
			url.Values{catchUpCutoffField: {cutoff.Format(time.RFC3339Nano)}},
		)
		assertResponseCode(t, apply, "catch-up apply before restart")
		token := extractUndoToken(t, apply.Body.String())

		restarted := New(app.db, app.tmpl)
		undo := postFormRequest(
			restarted,
			fmt.Sprintf("/feeds/%d/items/read/undo", feedID),
			url.Values{"undo_token": {token}},
		)
		assertResponseCode(t, undo, "undo after app restart")
		assertUnreadStateByGUID(t, app, feedID, "restart-story", true)
	})
}

func TestCatchUpPostRequiresCSRFWhenAuthIsEnabled(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)
	session := issueAuthSession(t, app)
	feedID := mustUpsertFeed(t, app, "http://example.com/catch-up-csrf", "CSRF")
	cutoff := time.Now().UTC()
	mustUpsertSingleStory(t, app, feedID, "Unread", "https://example.com/csrf", "csrf", cutoff.Add(-time.Hour))

	form := url.Values{catchUpCutoffField: {cutoff.Format(time.RFC3339Nano)}}
	request := newURLEncodedRequest(fmt.Sprintf("/feeds/%d/items/catch-up", feedID), form)
	request.AddCookie(session.cookie)
	response := httptest.NewRecorder()
	app.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected missing CSRF token to return 403, got %d", response.Code)
	}
	assertUnreadStateByGUID(t, app, feedID, "csrf", false)
}

func postHTMXFormRequest(app *App, target string, form url.Values) *httptest.ResponseRecorder {
	request := newURLEncodedRequest(target, form)
	request.Header.Set("Hx-Request", "true")
	response := httptest.NewRecorder()
	app.Routes().ServeHTTP(response, request)

	return response
}

//nolint:revive // Tests share one read-state assertion for positive and negative cases.
func assertUnreadStateByGUID(t *testing.T, app *App, feedID int64, guid string, wantRead bool) {
	t.Helper()

	var readAt sql.NullTime
	err := app.db.QueryRowContext(context.Background(), `
SELECT read_at FROM items WHERE feed_id = ? AND guid = ?
	`, feedID, guid).Scan(&readAt)
	requireNoErr(t, err, fmt.Sprintf("load read state for %q: %%v", guid))
	if readAt.Valid != wantRead {
		t.Fatalf("expected guid %q read state %t, got %t", guid, wantRead, readAt.Valid)
	}
}
