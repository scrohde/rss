package server

import (
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
	feedpkg "rss/internal/feed"
	"rss/internal/store"
	"rss/internal/testutil"
	"rss/internal/view"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	db := testutil.OpenTestDB(t)
	tmpl := templateMust()
	return New(db, tmpl)
}

func templateMust() *template.Template {
	tmpl := template.Must(template.ParseGlob(filepath.Join("..", "..", "templates", "*.html")))
	return template.Must(tmpl.ParseGlob(filepath.Join("..", "..", "templates", "partials", "*.html")))
}

func TestSubscribeAndList(t *testing.T) {
	items := []testutil.RSSItem{
		{
			Title:       "Alpha",
			Link:        "http://example.com/alpha",
			GUID:        "alpha",
			PubDate:     time.Now().UTC().Format(time.RFC1123Z),
			Description: "<p>Alpha summary</p>",
		},
		{
			Title:       "Beta",
			Link:        "http://example.com/beta",
			GUID:        "beta",
			PubDate:     time.Now().Add(-time.Hour).UTC().Format(time.RFC1123Z),
			Description: "<p>Beta summary</p>",
		},
	}
	_, feedURL := testutil.NewFeedServer(t, testutil.RSSXML("Test Feed", items))

	app := newTestApp(t)

	form := url.Values{}
	form.Set("url", feedURL)
	req := httptest.NewRequest(http.MethodPost, "/feeds", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	app.route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "Subscribed to ") {
		t.Fatalf("expected subscribe success message to be omitted")
	}

	feeds, err := store.ListFeeds(app.db)
	if err != nil {
		t.Fatalf("store.ListFeeds: %v", err)
	}
	if len(feeds) != 1 {
		t.Fatalf("expected 1 feed, got %d", len(feeds))
	}
	if feeds[0].Title != "Test Feed" {
		t.Fatalf("expected feed title, got %q", feeds[0].Title)
	}

	itemsInDB, err := store.ListItems(app.db, feeds[0].ID)
	if err != nil {
		t.Fatalf("store.ListItems: %v", err)
	}
	if len(itemsInDB) != 2 {
		t.Fatalf("expected 2 items, got %d", len(itemsInDB))
	}
}

func TestListFeedsUnreadCount(t *testing.T) {
	app := newTestApp(t)

	feedID, err := store.UpsertFeed(app.db, "http://example.com/rss", "Unread Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	if _, err := store.UpsertItems(app.db, feedID, []*gofeed.Item{{
		Title:           "Unread A",
		Link:            "http://example.com/a",
		GUID:            "a",
		Description:     "<p>Summary</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Unread B",
		Link:            "http://example.com/b",
		GUID:            "b",
		Description:     "<p>Summary</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-2 * time.Hour)),
	}}); err != nil {
		t.Fatalf("store.UpsertItems: %v", err)
	}

	feeds, err := store.ListFeeds(app.db)
	if err != nil {
		t.Fatalf("store.ListFeeds: %v", err)
	}
	if len(feeds) != 1 {
		t.Fatalf("expected 1 feed, got %d", len(feeds))
	}
	if feeds[0].ItemCount != 2 {
		t.Fatalf("expected 2 items, got %d", feeds[0].ItemCount)
	}
	if feeds[0].UnreadCount != 2 {
		t.Fatalf("expected 2 unread items, got %d", feeds[0].UnreadCount)
	}

	items, err := store.ListItems(app.db, feedID)
	if err != nil {
		t.Fatalf("store.ListItems: %v", err)
	}
	if err := store.ToggleRead(app.db, items[0].ID); err != nil {
		t.Fatalf("store.ToggleRead: %v", err)
	}

	feeds, err = store.ListFeeds(app.db)
	if err != nil {
		t.Fatalf("store.ListFeeds again: %v", err)
	}
	if feeds[0].UnreadCount != 1 {
		t.Fatalf("expected 1 unread item, got %d", feeds[0].UnreadCount)
	}
}

func TestRenameFeedOverridesSourceTitle(t *testing.T) {
	app := newTestApp(t)

	feedID, err := store.UpsertFeed(app.db, "http://example.com/rss", "Source Title")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	if err := store.UpdateFeedTitle(app.db, feedID, "Custom Title"); err != nil {
		t.Fatalf("store.UpdateFeedTitle: %v", err)
	}

	feeds, err := store.ListFeeds(app.db)
	if err != nil {
		t.Fatalf("store.ListFeeds: %v", err)
	}
	if feeds[0].Title != "Custom Title" {
		t.Fatalf("expected custom title, got %q", feeds[0].Title)
	}

	if _, err := store.UpsertFeed(app.db, "http://example.com/rss", "Updated Source"); err != nil {
		t.Fatalf("store.UpsertFeed update: %v", err)
	}

	feeds, err = store.ListFeeds(app.db)
	if err != nil {
		t.Fatalf("store.ListFeeds again: %v", err)
	}
	if feeds[0].Title != "Custom Title" {
		t.Fatalf("expected custom title after refresh, got %q", feeds[0].Title)
	}
}

func TestToggleReadUpdatesFeedList(t *testing.T) {
	app := newTestApp(t)

	feedID, err := store.UpsertFeed(app.db, "http://example.com/rss", "Toggle Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	if _, err := store.UpsertItems(app.db, feedID, []*gofeed.Item{{
		Title:           "One",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>Summary</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Two",
		Link:            "http://example.com/2",
		GUID:            "2",
		Description:     "<p>Summary</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-2 * time.Hour)),
	}}); err != nil {
		t.Fatalf("store.UpsertItems: %v", err)
	}

	items, err := store.ListItems(app.db, feedID)
	if err != nil {
		t.Fatalf("store.ListItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	form := url.Values{}
	form.Set("view", "compact")
	form.Set("selected_item_id", fmt.Sprintf("item-%d", items[0].ID))
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/items/%d/toggle", items[0].ID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	app.route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("toggle read status: %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `id="feed-list"`) {
		t.Fatalf("expected feed list OOB update")
	}
	if !strings.Contains(body, `hx-swap-oob="innerHTML"`) {
		t.Fatalf("expected OOB innerHTML swap for feed list")
	}
	if strings.Contains(body, `feed-count">2`) {
		t.Fatalf("expected unread count to decrease")
	}
	if !strings.Contains(body, `feed-count">1`) {
		t.Fatalf("expected unread count to be 1")
	}
	if !strings.Contains(body, "is-active") {
		t.Fatalf("expected toggled item to stay active")
	}
}

func TestToggleReadExpandedView(t *testing.T) {
	app := newTestApp(t)

	feedID, err := store.UpsertFeed(app.db, "http://example.com/rss", "Toggle Expanded Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	if _, err := store.UpsertItems(app.db, feedID, []*gofeed.Item{{
		Title:           "Expanded",
		Link:            "http://example.com/expanded",
		GUID:            "expanded",
		Description:     "<p>Expanded summary</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-time.Hour)),
	}}); err != nil {
		t.Fatalf("store.UpsertItems: %v", err)
	}

	items, err := store.ListItems(app.db, feedID)
	if err != nil {
		t.Fatalf("store.ListItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	form := url.Values{}
	form.Set("view", "expanded")
	form.Set("selected_item_id", fmt.Sprintf("%d", items[0].ID))
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/items/%d/toggle", items[0].ID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	app.route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("toggle read status: %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "item-card expanded") {
		t.Fatalf("expected expanded item response")
	}
	if !strings.Contains(body, "is-active") {
		t.Fatalf("expected expanded toggled item to stay active")
	}
}

func TestItemExpandedKeepsActiveClass(t *testing.T) {
	app := newTestApp(t)

	feedID, err := store.UpsertFeed(app.db, "http://example.com/rss", "Expanded Active Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	if _, err := store.UpsertItems(app.db, feedID, []*gofeed.Item{{
		Title:           "Expanded",
		Link:            "http://example.com/expanded",
		GUID:            "expanded-active",
		Description:     "<p>Expanded summary</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-time.Hour)),
	}}); err != nil {
		t.Fatalf("store.UpsertItems: %v", err)
	}

	items, err := store.ListItems(app.db, feedID)
	if err != nil {
		t.Fatalf("store.ListItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/items/%d?selected_item_id=item-%d", items[0].ID, items[0].ID), nil)
	rec := httptest.NewRecorder()
	app.route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expanded status: %d", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "is-active") {
		t.Fatalf("expected expanded item to include active class")
	}
}

func TestToggleReadAndCleanup(t *testing.T) {
	app := newTestApp(t)

	feedID, err := store.UpsertFeed(app.db, "http://example.com/rss", "Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	_, err = store.UpsertItems(app.db, feedID, []*gofeed.Item{{
		Title:           "Item",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>Summary</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-time.Hour)),
		UpdatedParsed:   testutil.TimePtr(time.Now().Add(-time.Hour)),
	}})
	if err != nil {
		t.Fatalf("store.UpsertItems: %v", err)
	}

	items, err := store.ListItems(app.db, feedID)
	if err != nil {
		t.Fatalf("store.ListItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	itemID := items[0].ID
	if err := store.ToggleRead(app.db, itemID); err != nil {
		t.Fatalf("store.ToggleRead: %v", err)
	}

	var readAt sql.NullTime
	if err := app.db.QueryRow("SELECT read_at FROM items WHERE id = ?", itemID).Scan(&readAt); err != nil {
		t.Fatalf("read_at query: %v", err)
	}
	if !readAt.Valid {
		t.Fatalf("expected read_at to be set")
	}

	if err := store.ToggleRead(app.db, itemID); err != nil {
		t.Fatalf("store.ToggleRead again: %v", err)
	}

	if err := app.db.QueryRow("SELECT read_at FROM items WHERE id = ?", itemID).Scan(&readAt); err != nil {
		t.Fatalf("read_at query: %v", err)
	}
	if readAt.Valid {
		t.Fatalf("expected read_at to be cleared")
	}

	// Mark item as read in the past to trigger cleanup.
	past := time.Now().UTC().Add(-3 * time.Hour)
	if _, err := app.db.Exec("UPDATE items SET read_at = ? WHERE id = ?", past, itemID); err != nil {
		t.Fatalf("set read_at: %v", err)
	}
	if err := store.CleanupReadItems(app.db); err != nil {
		t.Fatalf("store.CleanupReadItems: %v", err)
	}

	items, err = store.ListItems(app.db, feedID)
	if err != nil {
		t.Fatalf("store.ListItems after cleanup: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected item to be deleted, got %d", len(items))
	}
	if !existsInTombstones(t, app.db, feedID, "1") {
		t.Fatalf("expected tombstone to be recorded")
	}

	if _, err := store.UpsertItems(app.db, feedID, []*gofeed.Item{{
		Title:           "Item",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>Summary</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-time.Hour)),
		UpdatedParsed:   testutil.TimePtr(time.Now().Add(-time.Hour)),
	}}); err != nil {
		t.Fatalf("store.UpsertItems after cleanup: %v", err)
	}
	items, err = store.ListItems(app.db, feedID)
	if err != nil {
		t.Fatalf("store.ListItems after reinserting: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected item to stay deleted, got %d", len(items))
	}
}

func TestMarkAllRead(t *testing.T) {
	app := newTestApp(t)

	feedID, err := store.UpsertFeed(app.db, "http://example.com/rss", "Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	_, err = store.UpsertItems(app.db, feedID, []*gofeed.Item{{
		Title:           "Item A",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>Summary</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Item B",
		Link:            "http://example.com/2",
		GUID:            "2",
		Description:     "<p>Summary</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-2 * time.Hour)),
	}})
	if err != nil {
		t.Fatalf("store.UpsertItems: %v", err)
	}

	items, err := store.ListItems(app.db, feedID)
	if err != nil {
		t.Fatalf("store.ListItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	past := time.Now().UTC().Add(-30 * time.Minute)
	if _, err := app.db.Exec("UPDATE items SET read_at = ? WHERE id = ?", past, items[0].ID); err != nil {
		t.Fatalf("set read_at: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/feeds/%d/items/read", feedID), nil)
	rec := httptest.NewRecorder()
	app.route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark all read status: %d", rec.Code)
	}

	rows, err := app.db.Query("SELECT read_at FROM items WHERE feed_id = ?", feedID)
	if err != nil {
		t.Fatalf("read_at query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var readAt sql.NullTime
		if err := rows.Scan(&readAt); err != nil {
			t.Fatalf("read_at scan: %v", err)
		}
		if !readAt.Valid {
			t.Fatalf("expected read_at to be set for all items")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read_at rows: %v", err)
	}
}

func TestSweepReadItems(t *testing.T) {
	app := newTestApp(t)

	feedID, err := store.UpsertFeed(app.db, "http://example.com/rss", "Sweep Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}
	otherFeedID, err := store.UpsertFeed(app.db, "http://example.com/other", "Other Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed other: %v", err)
	}

	if _, err := store.UpsertItems(app.db, feedID, []*gofeed.Item{{
		Title:           "Keep me",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>Summary</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Sweep me A",
		Link:            "http://example.com/2",
		GUID:            "2",
		Description:     "<p>Summary</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-2 * time.Hour)),
	}, {
		Title:           "Sweep me B",
		Link:            "http://example.com/3",
		GUID:            "3",
		Description:     "<p>Summary</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-3 * time.Hour)),
	}}); err != nil {
		t.Fatalf("store.UpsertItems: %v", err)
	}
	if _, err := store.UpsertItems(app.db, otherFeedID, []*gofeed.Item{{
		Title:           "Other Feed Item",
		Link:            "http://example.com/4",
		GUID:            "4",
		Description:     "<p>Summary</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-time.Hour)),
	}}); err != nil {
		t.Fatalf("store.UpsertItems other: %v", err)
	}

	now := time.Now().UTC()
	if _, err := app.db.Exec("UPDATE items SET read_at = ? WHERE feed_id = ? AND guid IN (?, ?)", now, feedID, "2", "3"); err != nil {
		t.Fatalf("set read_at feed: %v", err)
	}
	if _, err := app.db.Exec("UPDATE items SET read_at = ? WHERE feed_id = ? AND guid = ?", now, otherFeedID, "4"); err != nil {
		t.Fatalf("set read_at other feed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/feeds/%d/items/sweep", feedID), nil)
	rec := httptest.NewRecorder()
	app.route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sweep read status: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), fmt.Sprintf(`hx-post="/feeds/%d/items/sweep"`, feedID)) {
		t.Fatalf("expected sweep action to remain in response")
	}

	if !existsByGUID(t, app.db, feedID, "1") {
		t.Fatalf("expected unread item to remain")
	}
	if existsByGUID(t, app.db, feedID, "2") || existsByGUID(t, app.db, feedID, "3") {
		t.Fatalf("expected read items to be deleted from selected feed")
	}
	if !existsInTombstones(t, app.db, feedID, "2") || !existsInTombstones(t, app.db, feedID, "3") {
		t.Fatalf("expected deleted read items to be tombstoned")
	}

	if !existsByGUID(t, app.db, otherFeedID, "4") {
		t.Fatalf("expected other feed to be unchanged")
	}
}

func TestManualFeedRefresh(t *testing.T) {
	base := time.Now().UTC().Add(-2 * time.Hour)
	feedXML := testutil.RSSXML("Manual Refresh Feed", []testutil.RSSItem{
		{
			Title:       "First",
			Link:        "http://example.com/1",
			GUID:        "1",
			PubDate:     base.Format(time.RFC1123Z),
			Description: "<p>First summary</p>",
		},
	})
	fs, feedURL := testutil.NewFeedServer(t, feedXML)
	app := newTestApp(t)

	feedID, err := store.UpsertFeed(app.db, feedURL, "Manual Refresh Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	if _, err := feedpkg.Refresh(app.db, feedID); err != nil {
		t.Fatalf("feedpkg.Refresh initial: %v", err)
	}

	fs.SetFeedXML(testutil.RSSXML("Manual Refresh Feed", []testutil.RSSItem{
		{
			Title:       "Second",
			Link:        "http://example.com/2",
			GUID:        "2",
			PubDate:     base.Add(time.Minute).Format(time.RFC1123Z),
			Description: "<p>Second summary</p>",
		},
		{
			Title:       "First",
			Link:        "http://example.com/1",
			GUID:        "1",
			PubDate:     base.Format(time.RFC1123Z),
			Description: "<p>First summary</p>",
		},
	}))

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/feeds/%d/refresh", feedID), nil)
	rec := httptest.NewRecorder()
	app.route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("manual refresh status: %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Second") {
		t.Fatalf("expected refreshed item in response")
	}
	if !strings.Contains(body, fmt.Sprintf(`hx-post="/feeds/%d/refresh"`, feedID)) {
		t.Fatalf("expected manual refresh button in response")
	}
	if !strings.Contains(body, `id="feed-list"`) {
		t.Fatalf("expected feed list OOB update")
	}
	if !strings.Contains(body, `hx-swap-oob="innerHTML"`) {
		t.Fatalf("expected OOB innerHTML swap for feed list")
	}

	items, err := store.ListItems(app.db, feedID)
	if err != nil {
		t.Fatalf("store.ListItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items after manual refresh, got %d", len(items))
	}
}

func TestDeleteFeedRemovesData(t *testing.T) {
	app := newTestApp(t)

	feedID, err := store.UpsertFeed(app.db, "http://example.com/rss", "Delete Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	if _, err := store.UpsertItems(app.db, feedID, []*gofeed.Item{{
		Title:           "Item A",
		Link:            "http://example.com/a",
		GUID:            "a",
		Description:     "<p>Summary</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-time.Hour)),
	}}); err != nil {
		t.Fatalf("store.UpsertItems: %v", err)
	}

	if _, err := app.db.Exec(
		"INSERT INTO tombstones (feed_id, guid, deleted_at) VALUES (?, ?, ?)",
		feedID,
		"gone",
		time.Now().UTC(),
	); err != nil {
		t.Fatalf("insert tombstone: %v", err)
	}

	form := url.Values{}
	form.Set("selected_feed_id", fmt.Sprintf("%d", feedID))
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/feeds/%d/delete", feedID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	app.route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete feed status: %d", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "Pick a feed to start reading.") {
		t.Fatalf("expected empty state after deleting last feed")
	}

	var count int
	if err := app.db.QueryRow("SELECT COUNT(*) FROM feeds WHERE id = ?", feedID).Scan(&count); err != nil {
		t.Fatalf("feed count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected feed to be deleted, got %d", count)
	}
	if err := app.db.QueryRow("SELECT COUNT(*) FROM items WHERE feed_id = ?", feedID).Scan(&count); err != nil {
		t.Fatalf("items count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected items to be deleted, got %d", count)
	}
	if err := app.db.QueryRow("SELECT COUNT(*) FROM tombstones WHERE feed_id = ?", feedID).Scan(&count); err != nil {
		t.Fatalf("tombstones count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected tombstones to be deleted, got %d", count)
	}
}

func TestItemLimit(t *testing.T) {
	app := newTestApp(t)
	feedID, err := store.UpsertFeed(app.db, "http://example.com/rss", "Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	base := time.Now().UTC().Add(-210 * time.Minute)
	items := make([]*gofeed.Item, 0, 210)
	for i := 0; i < 210; i++ {
		published := base.Add(time.Duration(i) * time.Minute)
		items = append(items, &gofeed.Item{
			Title:           fmt.Sprintf("Item %03d", i),
			Link:            fmt.Sprintf("http://example.com/%d", i),
			GUID:            fmt.Sprintf("guid-%03d", i),
			Description:     "<p>Summary</p>",
			PublishedParsed: &published,
		})
	}

	if _, err := store.UpsertItems(app.db, feedID, items); err != nil {
		t.Fatalf("store.UpsertItems: %v", err)
	}
	if err := store.EnforceItemLimit(app.db, feedID); err != nil {
		t.Fatalf("store.EnforceItemLimit: %v", err)
	}

	itemsInDB, err := store.ListItems(app.db, feedID)
	if err != nil {
		t.Fatalf("store.ListItems: %v", err)
	}
	if len(itemsInDB) != 200 {
		t.Fatalf("expected %d items, got %d", 200, len(itemsInDB))
	}

	// Oldest 10 items should have been removed (guid-000 through guid-009).
	for i := 0; i < 10; i++ {
		guid := fmt.Sprintf("guid-%03d", i)
		if existsByGUID(t, app.db, feedID, guid) {
			t.Fatalf("expected %s to be deleted", guid)
		}
	}
	if !existsByGUID(t, app.db, feedID, "guid-010") {
		t.Fatalf("expected guid-010 to remain")
	}
}

func TestPollingAndNewItemsBanner(t *testing.T) {
	base := time.Now().UTC().Add(-2 * time.Hour)
	app := newTestApp(t)

	feedID, err := store.UpsertFeed(app.db, "http://example.com/rss", "Poll Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}
	if _, err := store.UpsertItems(app.db, feedID, []*gofeed.Item{{
		Title:           "First",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>First summary</p>",
		PublishedParsed: testutil.TimePtr(base),
	}, {
		Title:           "Second",
		Link:            "http://example.com/2",
		GUID:            "2",
		Description:     "<p>Second summary</p>",
		PublishedParsed: testutil.TimePtr(base.Add(time.Minute)),
	}}); err != nil {
		t.Fatalf("store.UpsertItems: %v", err)
	}

	list, err := store.LoadItemList(app.db, feedID)
	if err != nil {
		t.Fatalf("store.LoadItemList: %v", err)
	}

	pollReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/feeds/%d/items/poll?after_id=%d", feedID, list.NewestID), nil)
	pollRec := httptest.NewRecorder()
	app.route(pollRec, pollReq)
	if pollRec.Code != http.StatusOK {
		t.Fatalf("poll status: %d", pollRec.Code)
	}
	if !strings.Contains(pollRec.Body.String(), "New items (0)") {
		t.Fatalf("expected banner to show zero new items")
	}
	if !strings.Contains(pollRec.Body.String(), `id="feed-list"`) {
		t.Fatalf("expected feed list OOB update")
	}
	if !strings.Contains(pollRec.Body.String(), `hx-swap-oob="innerHTML"`) {
		t.Fatalf("expected OOB innerHTML swap for feed list")
	}
	if !strings.Contains(pollRec.Body.String(), `id="item-last-refresh"`) {
		t.Fatalf("expected last refresh OOB update")
	}
	if !strings.Contains(pollRec.Body.String(), `feed-count">2`) {
		t.Fatalf("expected unread count to be 2")
	}

	if _, err := store.UpsertItems(app.db, feedID, []*gofeed.Item{{
		Title:           "Third",
		Link:            "http://example.com/3",
		GUID:            "3",
		Description:     "<p>Third summary</p>",
		PublishedParsed: testutil.TimePtr(base.Add(2 * time.Minute)),
	}}); err != nil {
		t.Fatalf("store.UpsertItems new: %v", err)
	}

	pollReq = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/feeds/%d/items/poll?after_id=%d", feedID, list.NewestID), nil)
	pollRec = httptest.NewRecorder()
	app.route(pollRec, pollReq)
	if pollRec.Code != http.StatusOK {
		t.Fatalf("poll status: %d", pollRec.Code)
	}
	if !strings.Contains(pollRec.Body.String(), "New items (1)") {
		t.Fatalf("expected banner to show new items")
	}
	if !strings.Contains(pollRec.Body.String(), `feed-count">3`) {
		t.Fatalf("expected unread count to be 3")
	}

	newReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/feeds/%d/items/new?after_id=%d", feedID, list.NewestID), nil)
	newRec := httptest.NewRecorder()
	app.route(newRec, newReq)
	if newRec.Code != http.StatusOK {
		t.Fatalf("new items status: %d", newRec.Code)
	}
	body := newRec.Body.String()
	if !strings.Contains(body, "Third") {
		t.Fatalf("expected new item in response")
	}
	if !strings.Contains(body, "hx-swap-oob") {
		t.Fatalf("expected OOB cursor update")
	}
}

func TestDeleteFeedConfirmEndpoint(t *testing.T) {
	app := newTestApp(t)

	feedID, err := store.UpsertFeed(app.db, "http://example.com/rss", "Delete Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/feeds/%d/delete/confirm", feedID), nil)
	rec := httptest.NewRecorder()
	app.route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm status: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Don't warn again") {
		t.Fatalf("expected skip checkbox label")
	}
	if !strings.Contains(body, fmt.Sprintf("feed-remove-confirm-%d", feedID)) {
		t.Fatalf("expected confirm container id")
	}
	if !strings.Contains(body, fmt.Sprintf("hx-post=\"/feeds/%d/delete\"", feedID)) {
		t.Fatalf("expected delete action in confirm")
	}

	cancelReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/feeds/%d/delete/confirm?cancel=1", feedID), nil)
	cancelRec := httptest.NewRecorder()
	app.route(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel status: %d", cancelRec.Code)
	}
	cancelBody := cancelRec.Body.String()
	if strings.Contains(cancelBody, "skip_delete_warning") {
		t.Fatalf("expected cancel response to omit confirm inputs")
	}
	if !strings.Contains(cancelBody, fmt.Sprintf("feed-remove-confirm-%d", feedID)) {
		t.Fatalf("expected cancel placeholder id")
	}
}

func TestDeleteFeedSkipCookie(t *testing.T) {
	app := newTestApp(t)

	feedID, err := store.UpsertFeed(app.db, "http://example.com/rss", "Skip Cookie Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	app.route(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, fmt.Sprintf("hx-get=\"/feeds/%d/delete/confirm\"", feedID)) {
		t.Fatalf("expected confirm flow when cookie is not set")
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: skipDeleteWarningCookie, Value: "1"})
	rec = httptest.NewRecorder()
	app.route(rec, req)
	body = rec.Body.String()
	if !strings.Contains(body, fmt.Sprintf("hx-post=\"/feeds/%d/delete\"", feedID)) {
		t.Fatalf("expected direct delete when cookie is set")
	}
	if strings.Contains(body, fmt.Sprintf("hx-get=\"/feeds/%d/delete/confirm\"", feedID)) {
		t.Fatalf("expected confirm flow to be skipped when cookie is set")
	}
}

func TestFeedListCollapsesZeroItemFeeds(t *testing.T) {
	app := newTestApp(t)

	if _, err := store.UpsertFeed(app.db, "http://example.com/a-empty", "Aardvark Empty"); err != nil {
		t.Fatalf("store.UpsertFeed empty: %v", err)
	}
	alphaID, err := store.UpsertFeed(app.db, "http://example.com/b-alpha", "Alpha Active")
	if err != nil {
		t.Fatalf("store.UpsertFeed alpha: %v", err)
	}
	betaID, err := store.UpsertFeed(app.db, "http://example.com/c-beta", "Beta Active")
	if err != nil {
		t.Fatalf("store.UpsertFeed beta: %v", err)
	}

	if _, err := store.UpsertItems(app.db, alphaID, []*gofeed.Item{{
		Title:           "Alpha item",
		Link:            "http://example.com/alpha-item",
		GUID:            "alpha-item",
		Description:     "<p>Alpha item</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-time.Hour)),
	}}); err != nil {
		t.Fatalf("store.UpsertItems alpha: %v", err)
	}

	if _, err := store.UpsertItems(app.db, betaID, []*gofeed.Item{{
		Title:           "Beta item",
		Link:            "http://example.com/beta-item",
		GUID:            "beta-item",
		Description:     "<p>Beta item</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-2 * time.Hour)),
	}}); err != nil {
		t.Fatalf("store.UpsertItems beta: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	app.route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("index status: %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `class="feed-more-button"`) {
		t.Fatalf("expected more button when zero-item feeds exist")
	}
	if !strings.Contains(body, `class="feed-zero-list"`) {
		t.Fatalf("expected collapsed zero-item feed section")
	}

	alphaIdx := strings.Index(body, "Alpha Active")
	betaIdx := strings.Index(body, "Beta Active")
	moreIdx := strings.Index(body, `class="feed-more-button"`)
	emptyIdx := strings.Index(body, "Aardvark Empty")
	if alphaIdx == -1 || betaIdx == -1 || moreIdx == -1 || emptyIdx == -1 {
		t.Fatalf("expected alpha, beta, more button, and empty feed in output")
	}
	if alphaIdx > betaIdx {
		t.Fatalf("expected non-zero feeds to remain alphabetical")
	}
	if betaIdx > moreIdx || moreIdx > emptyIdx {
		t.Fatalf("expected zero-item feeds below non-zero feeds behind the more section")
	}
}

func TestFeedListHidesMoreButtonWithoutZeroItemFeeds(t *testing.T) {
	app := newTestApp(t)

	alphaID, err := store.UpsertFeed(app.db, "http://example.com/a-alpha", "Alpha Active")
	if err != nil {
		t.Fatalf("store.UpsertFeed alpha: %v", err)
	}
	betaID, err := store.UpsertFeed(app.db, "http://example.com/b-beta", "Beta Active")
	if err != nil {
		t.Fatalf("store.UpsertFeed beta: %v", err)
	}

	if _, err := store.UpsertItems(app.db, alphaID, []*gofeed.Item{{
		Title:           "Alpha item",
		Link:            "http://example.com/alpha-item",
		GUID:            "alpha-item",
		Description:     "<p>Alpha item</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-time.Hour)),
	}}); err != nil {
		t.Fatalf("store.UpsertItems alpha: %v", err)
	}

	if _, err := store.UpsertItems(app.db, betaID, []*gofeed.Item{{
		Title:           "Beta item",
		Link:            "http://example.com/beta-item",
		GUID:            "beta-item",
		Description:     "<p>Beta item</p>",
		PublishedParsed: testutil.TimePtr(time.Now().Add(-2 * time.Hour)),
	}}); err != nil {
		t.Fatalf("store.UpsertItems beta: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	app.route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("index status: %d", rec.Code)
	}

	body := rec.Body.String()
	if strings.Contains(body, `class="feed-more-button"`) {
		t.Fatalf("expected more button to be hidden when all feeds have items")
	}
}

func TestParseSelectedItemID(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{name: "empty", raw: "", want: 0},
		{name: "plain id", raw: "42", want: 42},
		{name: "prefixed id", raw: "item-42", want: 42},
		{name: "invalid", raw: "item-abc", want: 0},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			q := req.URL.Query()
			if tc.raw != "" {
				q.Set("selected_item_id", tc.raw)
			}
			req.URL.RawQuery = q.Encode()
			if got := parseSelectedItemID(req); got != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

func TestBuildFeedViewLastRefreshDisplay(t *testing.T) {
	feed := view.BuildFeedView(1, "Feed", "https://example.com", 0, 0, sql.NullTime{}, sql.NullString{})
	if feed.LastRefreshDisplay != "Never" {
		t.Fatalf("expected Never, got %q", feed.LastRefreshDisplay)
	}

	cases := []struct {
		name     string
		age      time.Duration
		wantUnit string
	}{
		{name: "seconds", age: 3 * time.Second, wantUnit: "s"},
		{name: "minutes", age: 3 * time.Minute, wantUnit: "m"},
		{name: "hours", age: 3 * time.Hour, wantUnit: "h"},
		{name: "days", age: 72 * time.Hour, wantUnit: "d"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			checked := sql.NullTime{Time: time.Now().Add(-tc.age), Valid: true}
			got := view.BuildFeedView(1, "Feed", "https://example.com", 0, 0, checked, sql.NullString{}).LastRefreshDisplay
			if !strings.HasSuffix(got, tc.wantUnit) {
				t.Fatalf("expected unit %q in %q", tc.wantUnit, got)
			}
		})
	}
}

func existsByGUID(t *testing.T, db *sql.DB, feedID int64, guid string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`
SELECT COUNT(*)
FROM items
WHERE feed_id = ? AND guid = ?
`, feedID, guid).Scan(&count); err != nil {
		t.Fatalf("existsByGUID: %v", err)
	}
	return count > 0
}

func existsInTombstones(t *testing.T, db *sql.DB, feedID int64, guid string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`
SELECT COUNT(*)
FROM tombstones
WHERE feed_id = ? AND guid = ?
`, feedID, guid).Scan(&count); err != nil {
		t.Fatalf("existsInTombstones: %v", err)
	}
	return count > 0
}
