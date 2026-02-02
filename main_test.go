package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

type feedServer struct {
	mu      sync.RWMutex
	feedXML string
}

func newFeedServer(t *testing.T, feedXML string) (*feedServer, *httptest.Server) {
	t.Helper()
	fs := &feedServer{feedXML: feedXML}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.RLock()
		defer fs.mu.RUnlock()
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(fs.feedXML))
	}))
	return fs, server
}

func (f *feedServer) setFeedXML(xml string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feedXML = xml
}

func rssXML(title string, items []rssItem) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString("<rss version=\"2.0\"><channel>")
	b.WriteString(fmt.Sprintf("<title>%s</title>", title))
	b.WriteString("<link>http://example.com</link>")
	b.WriteString("<description>Test feed</description>")
	for _, item := range items {
		b.WriteString("<item>")
		b.WriteString(fmt.Sprintf("<title>%s</title>", item.Title))
		b.WriteString(fmt.Sprintf("<link>%s</link>", item.Link))
		b.WriteString(fmt.Sprintf("<guid>%s</guid>", item.GUID))
		b.WriteString(fmt.Sprintf("<pubDate>%s</pubDate>", item.PubDate))
		b.WriteString(fmt.Sprintf("<description><![CDATA[%s]]></description>", item.Description))
		b.WriteString("</item>")
	}
	b.WriteString("</channel></rss>")
	return b.String()
}

type rssItem struct {
	Title       string
	Link        string
	GUID        string
	PubDate     string
	Description string
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	if err := initDB(db); err != nil {
		_ = db.Close()
		t.Fatalf("initDB: %v", err)
	}
	tmpl := templateMust()
	app := &App{db: db, tmpl: tmpl}
	t.Cleanup(func() { _ = db.Close() })
	return app
}

func templateMust() *template.Template {
	tmpl := template.Must(template.ParseGlob("templates/*.html"))
	return template.Must(tmpl.ParseGlob("templates/partials/*.html"))
}

func TestSubscribeAndList(t *testing.T) {
	items := []rssItem{
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
	_, server := newFeedServer(t, rssXML("Test Feed", items))
	defer server.Close()

	app := newTestApp(t)

	form := url.Values{}
	form.Set("url", server.URL)
	req := httptest.NewRequest(http.MethodPost, "/feeds", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	app.route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	feeds, err := listFeeds(app.db)
	if err != nil {
		t.Fatalf("listFeeds: %v", err)
	}
	if len(feeds) != 1 {
		t.Fatalf("expected 1 feed, got %d", len(feeds))
	}
	if feeds[0].Title != "Test Feed" {
		t.Fatalf("expected feed title, got %q", feeds[0].Title)
	}

	itemsInDB, err := listItems(app.db, feeds[0].ID)
	if err != nil {
		t.Fatalf("listItems: %v", err)
	}
	if len(itemsInDB) != 2 {
		t.Fatalf("expected 2 items, got %d", len(itemsInDB))
	}
}

func TestToggleReadAndCleanup(t *testing.T) {
	app := newTestApp(t)

	feedID, err := upsertFeed(app.db, "http://example.com/rss", "Feed")
	if err != nil {
		t.Fatalf("upsertFeed: %v", err)
	}

	err = upsertItems(app.db, feedID, []*gofeed.Item{{
		Title:           "Item",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>Summary</p>",
		PublishedParsed: timePtr(time.Now().Add(-time.Hour)),
		UpdatedParsed:   timePtr(time.Now().Add(-time.Hour)),
	}})
	if err != nil {
		t.Fatalf("upsertItems: %v", err)
	}

	items, err := listItems(app.db, feedID)
	if err != nil {
		t.Fatalf("listItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	itemID := items[0].ID
	if err := toggleRead(app.db, itemID); err != nil {
		t.Fatalf("toggleRead: %v", err)
	}

	var readAt sql.NullTime
	if err := app.db.QueryRow("SELECT read_at FROM items WHERE id = ?", itemID).Scan(&readAt); err != nil {
		t.Fatalf("read_at query: %v", err)
	}
	if !readAt.Valid {
		t.Fatalf("expected read_at to be set")
	}

	if err := toggleRead(app.db, itemID); err != nil {
		t.Fatalf("toggleRead again: %v", err)
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
	if err := cleanupReadItems(app.db); err != nil {
		t.Fatalf("cleanupReadItems: %v", err)
	}

	items, err = listItems(app.db, feedID)
	if err != nil {
		t.Fatalf("listItems after cleanup: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected item to be deleted, got %d", len(items))
	}
	if !existsInTombstones(t, app.db, feedID, "1") {
		t.Fatalf("expected tombstone to be recorded")
	}

	if err := upsertItems(app.db, feedID, []*gofeed.Item{{
		Title:           "Item",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>Summary</p>",
		PublishedParsed: timePtr(time.Now().Add(-time.Hour)),
		UpdatedParsed:   timePtr(time.Now().Add(-time.Hour)),
	}}); err != nil {
		t.Fatalf("upsertItems after cleanup: %v", err)
	}
	items, err = listItems(app.db, feedID)
	if err != nil {
		t.Fatalf("listItems after reinserting: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected item to stay deleted, got %d", len(items))
	}
}

func TestItemLimit(t *testing.T) {
	app := newTestApp(t)
	feedID, err := upsertFeed(app.db, "http://example.com/rss", "Feed")
	if err != nil {
		t.Fatalf("upsertFeed: %v", err)
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

	if err := upsertItems(app.db, feedID, items); err != nil {
		t.Fatalf("upsertItems: %v", err)
	}
	if err := enforceItemLimit(app.db, feedID); err != nil {
		t.Fatalf("enforceItemLimit: %v", err)
	}

	itemsInDB, err := listItems(app.db, feedID)
	if err != nil {
		t.Fatalf("listItems: %v", err)
	}
	if len(itemsInDB) != maxItemsPerFeed {
		t.Fatalf("expected %d items, got %d", maxItemsPerFeed, len(itemsInDB))
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
	items := []rssItem{
		{
			Title:       "First",
			Link:        "http://example.com/1",
			GUID:        "1",
			PubDate:     base.Format(time.RFC1123Z),
			Description: "<p>First summary</p>",
		},
		{
			Title:       "Second",
			Link:        "http://example.com/2",
			GUID:        "2",
			PubDate:     base.Add(time.Minute).Format(time.RFC1123Z),
			Description: "<p>Second summary</p>",
		},
	}
	fs, server := newFeedServer(t, rssXML("Poll Feed", items))
	defer server.Close()

	app := newTestApp(t)

	feedID, err := upsertFeed(app.db, server.URL, "Poll Feed")
	if err != nil {
		t.Fatalf("upsertFeed: %v", err)
	}
	if err := upsertItems(app.db, feedID, []*gofeed.Item{{
		Title:           "First",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>First summary</p>",
		PublishedParsed: timePtr(base),
	}, {
		Title:           "Second",
		Link:            "http://example.com/2",
		GUID:            "2",
		Description:     "<p>Second summary</p>",
		PublishedParsed: timePtr(base.Add(time.Minute)),
	}}); err != nil {
		t.Fatalf("upsertItems: %v", err)
	}

	list, err := loadItemList(app.db, feedID)
	if err != nil {
		t.Fatalf("loadItemList: %v", err)
	}

	newItem := rssItem{
		Title:       "Third",
		Link:        "http://example.com/3",
		GUID:        "3",
		PubDate:     base.Add(2 * time.Minute).Format(time.RFC1123Z),
		Description: "<p>Third summary</p>",
	}
	fs.setFeedXML(rssXML("Poll Feed", append(items, newItem)))

	pollReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/feeds/%d/items/poll?after_id=%d", feedID, list.NewestID), nil)
	pollRec := httptest.NewRecorder()
	app.route(pollRec, pollReq)
	if pollRec.Code != http.StatusOK {
		t.Fatalf("poll status: %d", pollRec.Code)
	}
	if !strings.Contains(pollRec.Body.String(), "New items (1)") {
		t.Fatalf("expected banner to show new items")
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

func TestRewriteSummaryImages(t *testing.T) {
	input := `<p>Hello</p><img src="https://example.com/image.jpg" alt="x">`
	output := rewriteSummaryImages(input)
	expected := imageProxyPath + "?url=" + url.QueryEscape("https://example.com/image.jpg")
	if !strings.Contains(output, expected) {
		t.Fatalf("expected proxied image url, got %q", output)
	}
}

func TestRewriteSummaryImagesSrcset(t *testing.T) {
	input := `<img srcset="https://example.com/a.jpg 1x, https://example.com/b.jpg 2x" src="https://example.com/a.jpg">`
	output := rewriteSummaryImages(input)
	expectedA := imageProxyPath + "?url=" + url.QueryEscape("https://example.com/a.jpg")
	expectedB := imageProxyPath + "?url=" + url.QueryEscape("https://example.com/b.jpg")
	if !strings.Contains(output, expectedA) || !strings.Contains(output, expectedB) {
		t.Fatalf("expected proxied srcset urls, got %q", output)
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
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
