//nolint:testpackage // Handler integration tests intentionally exercise unexported helpers.
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"

	"rss/internal/store"
	"rss/internal/testutil"
)

func TestSubscribeAndList(t *testing.T) {
	t.Parallel()

	items := subscribeFeedItems(time.Now())
	_, feedURL := testutil.NewFeedServer(t, testutil.RSSXML("Test Feed", items))

	app := newTestApp(t)

	form := url.Values{}
	form.Set("url", feedURL)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost,
		"/feeds",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set(headerContentType, formURLEncoded)

	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if strings.Contains(rec.Body.String(), "Subscribed to ") {
		t.Fatal("expected subscribe success message to be omitted")
	}

	feeds, err := store.ListFeeds(context.Background(), app.db)
	if err != nil {
		t.Fatalf(errStoreListFeeds, err)
	}

	if len(feeds) != expectedSingleFeed {
		t.Fatalf("expected 1 feed, got %d", len(feeds))
	}

	if feeds[firstFeedIndex].Title != "Test Feed" {
		t.Fatalf("expected feed title, got %q", feeds[firstFeedIndex].Title)
	}

	itemsInDB, err := store.ListItems(context.Background(), app.db, feeds[firstFeedIndex].ID)
	if err != nil {
		t.Fatalf(errStoreListItems, err)
	}

	if len(itemsInDB) != expectedTwoItems {
		t.Fatalf("expected 2 items, got %d", len(itemsInDB))
	}
}

func TestSubscribeBlockedByRobotsPolicy(t *testing.T) {
	t.Parallel()

	items := subscribeFeedItems(time.Now())
	feedServer, feedURL := testutil.NewFeedServer(t, testutil.RSSXML("Test Feed", items))
	feedServer.SetRobotsTxt("User-agent: *\nDisallow: /\n")

	app := newTestApp(t)

	form := url.Values{}
	form.Set("url", feedURL)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost,
		"/feeds",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set(headerContentType, formURLEncoded)

	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	assertContains(
		t,
		rec.Body.String(),
		"Polling blocked by robots.txt",
		"expected robots block message in subscribe response",
	)

	feeds, err := store.ListFeeds(context.Background(), app.db)
	if err != nil {
		t.Fatalf(errStoreListFeeds, err)
	}

	if len(feeds) != 0 {
		t.Fatalf("expected 0 feeds when subscription is blocked, got %d", len(feeds))
	}
}

func TestListFeedsUnreadCount(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(),
		app.db,
		exampleRSSURL,
		"Unread Feed")
	if err != nil {
		t.Fatalf(errStoreUpsertFeed, err)
	}

	_, upsertErr := store.UpsertItems(context.Background(), app.db, feedID, []*gofeed.Item{{
		Title:           "Unread A",
		Link:            "http://example.com/a",
		GUID:            "a",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Unread B",
		Link:            "http://example.com/b",
		GUID:            "b",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})
	if upsertErr != nil {
		t.Fatalf(errStoreUpsertItems, upsertErr)
	}

	assertSingleFeedCounts(
		t,
		app.db,
		expectedTwoItems,
		expectedTwoUnread,
	)

	items, err := store.ListItems(context.Background(), app.db, feedID)
	if err != nil {
		t.Fatalf(errStoreListItems, err)
	}

	toggleErr := store.ToggleRead(context.Background(), app.db, items[firstFeedIndex].ID)
	if toggleErr != nil {
		t.Fatalf("store.ToggleRead: %v", toggleErr)
	}

	assertSingleFeedCounts(
		t,
		app.db,
		expectedTwoItems,
		expectedOneUnread,
	)
}
