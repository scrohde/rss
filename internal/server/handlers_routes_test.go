//nolint:testpackage // Handler integration tests intentionally exercise unexported helpers.
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"

	"rss/internal/store"
)

func TestRoutesMethodMismatchReturns405(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/feeds", http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}

	allow := rec.Header().Get("Allow")
	if !strings.Contains(allow, http.MethodPost) {
		t.Fatalf("expected Allow header to include POST, got %q", allow)
	}
}

func TestRoutesInvalidFeedIDReturns404(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, "/feeds/not-a-number/items", http.NoBody,
	)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRoutesInvalidItemIDReturns404(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost, "/items/not-a-number/toggle", http.NoBody,
	)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func setupFeedListCollapseFixtures(t *testing.T, app *App) {
	t.Helper()

	_ = mustUpsertFeed(t, app, "http://example.com/a-empty", "Aardvark Empty")
	alphaID := mustUpsertFeed(t, app, "http://example.com/b-alpha", "Alpha Active")
	betaID := mustUpsertFeed(t, app, "http://example.com/c-beta", "Beta Active")
	readOnlyID := mustUpsertFeed(t, app, "http://example.com/d-readonly", "Delta Read")

	mustUpsertItems(t, app, alphaID, []*gofeed.Item{{
		Title:           "Alpha item",
		Link:            "http://example.com/alpha-item",
		GUID:            "alpha-item",
		Description:     "<p>Alpha item</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	mustUpsertItems(t, app, betaID, []*gofeed.Item{{
		Title:           "Beta item",
		Link:            "http://example.com/beta-item",
		GUID:            "beta-item",
		Description:     "<p>Beta item</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})
	mustUpsertItems(t, app, readOnlyID, []*gofeed.Item{{
		Title:           "Delta item",
		Link:            "http://example.com/delta-item",
		GUID:            "delta-item",
		Description:     "<p>Delta item</p>",
		PublishedParsed: new(time.Now().Add(-threeUnits * time.Hour)),
	}})

	err := store.MarkAllRead(context.Background(), app.db, readOnlyID)
	requireNoErr(t, err, "store.MarkAllRead read only: %v")
}

func assertCollapsedZeroUnreadFeedList(t *testing.T, body string) {
	t.Helper()

	assertContains(t, body, `class="feed-more-button"`, "expected more button when zero-unread feeds exist")
	assertContains(
		t,
		body,
		`aria-label="Toggle feeds with no unread items"`,
		"expected accessible label for zero-unread toggle",
	)
	assertContains(t, body, `aria-expanded="false"`, "expected collapsed more button by default")
	assertContains(t, body, `aria-controls="feed-zero-list"`, "expected button to control zero-unread list")
	assertContains(t, body, `class="feed-zero-list"`, "expected collapsed zero-unread feed section")
	assertContains(t, body, `id="feed-zero-list"`, "expected zero-unread list id for aria-controls linkage")
	assertContains(t, body, `class="feed-zero-list" hidden`, "expected zero-unread list to start hidden")
	assertNotContains(t, body, "feed-more-label-collapsed", "expected legacy More label markup to be removed")
	assertNotContains(t, body, "feed-more-label-expanded", "expected legacy Less label markup to be removed")
	assertNotContains(t, body, "<summary", "expected native summary element to be removed")
	assertNotContains(t, body, "<details", "expected native details element to be removed")

	alphaIdx := strings.Index(body, "Alpha Active")
	betaIdx := strings.Index(body, "Beta Active")
	moreIdx := strings.Index(body, `class="feed-more-button"`)
	emptyIdx := strings.Index(body, "Aardvark Empty")
	readOnlyIdx := strings.Index(body, "Delta Read")

	if alphaIdx == -1 || betaIdx == -1 || moreIdx == -1 || emptyIdx == -1 || readOnlyIdx == -1 {
		t.Fatal("expected alpha, beta, more button, empty feed, and read-only feed in output")
	}

	if alphaIdx > betaIdx {
		t.Fatal("expected unread feeds to remain alphabetical")
	}

	if betaIdx > moreIdx || moreIdx > emptyIdx || moreIdx > readOnlyIdx {
		t.Fatal("expected zero-unread feeds below unread feeds behind the more section")
	}
}

func TestFeedListCollapsesZeroUnreadFeeds(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	setupFeedListCollapseFixtures(t, app)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, pathIndex, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(errIndexStatusFmt, rec.Code)
	}

	assertCollapsedZeroUnreadFeedList(t, rec.Body.String())
}

func TestFeedListHidesMoreButtonWithoutZeroUnreadFeeds(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	alphaID, err := store.UpsertFeed(context.Background(), app.db, "http://example.com/a-alpha", "Alpha Active")
	if err != nil {
		t.Fatalf("store.UpsertFeed alpha: %v", err)
	}

	betaID, err := store.UpsertFeed(context.Background(), app.db, "http://example.com/b-beta", "Beta Active")
	if err != nil {
		t.Fatalf("store.UpsertFeed beta: %v", err)
	}

	_, upsertErr := store.UpsertItems(context.Background(), app.db, alphaID, []*gofeed.Item{{
		Title:           "Alpha item",
		Link:            "http://example.com/alpha-item",
		GUID:            "alpha-item",
		Description:     "<p>Alpha item</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	if upsertErr != nil {
		t.Fatalf("store.UpsertItems alpha: %v", upsertErr)
	}

	_, upsertErr = store.UpsertItems(context.Background(), app.db, betaID, []*gofeed.Item{{
		Title:           "Beta item",
		Link:            "http://example.com/beta-item",
		GUID:            "beta-item",
		Description:     "<p>Beta item</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})
	if upsertErr != nil {
		t.Fatalf("store.UpsertItems beta: %v", upsertErr)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, pathIndex, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(errIndexStatusFmt, rec.Code)
	}

	body := rec.Body.String()
	if strings.Contains(body, `class="feed-more-button"`) {
		t.Fatal("expected more button to be hidden when all feeds have unread items")
	}
}

func newSelectedItemIDRequest(raw string) *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, pathIndex, http.NoBody)

	q := req.URL.Query()
	q.Set(selectedItemIDParam, raw)
	req.URL.RawQuery = q.Encode()

	return req
}

func TestParseSelectedItemID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{name: "empty", raw: "", want: 0},
		{name: "plain id", raw: selectedItemIDRaw, want: selectedItemIDPlain},
		{name: "prefixed id", raw: selectedItemIDPrefix, want: selectedItemIDPlain},
		{name: "invalid", raw: "item-abc", want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := newSelectedItemIDRequest(tc.raw)
			if got := parseSelectedItemID(req); got != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, got)
			}
		})
	}
}
