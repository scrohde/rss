package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

func TestUpsertFeedCustomTitlePreserved(t *testing.T) {
	db := openTestDB(t)

	feedID, err := UpsertFeed(db, "http://example.com/rss", "Source Title")
	if err != nil {
		t.Fatalf("UpsertFeed: %v", err)
	}
	if err := UpdateFeedTitle(db, feedID, "Custom Title"); err != nil {
		t.Fatalf("UpdateFeedTitle: %v", err)
	}
	if _, err := UpsertFeed(db, "http://example.com/rss", "Updated Source"); err != nil {
		t.Fatalf("UpsertFeed update: %v", err)
	}

	feeds, err := ListFeeds(db)
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != 1 {
		t.Fatalf("expected 1 feed, got %d", len(feeds))
	}
	if feeds[0].Title != "Custom Title" {
		t.Fatalf("expected custom title after refresh, got %q", feeds[0].Title)
	}
}

func TestUpdateFeedOrderPersistsListOrder(t *testing.T) {
	db := openTestDB(t)

	firstID, err := UpsertFeed(db, "http://example.com/first", "First")
	if err != nil {
		t.Fatalf("UpsertFeed first: %v", err)
	}
	secondID, err := UpsertFeed(db, "http://example.com/second", "Second")
	if err != nil {
		t.Fatalf("UpsertFeed second: %v", err)
	}
	thirdID, err := UpsertFeed(db, "http://example.com/third", "Third")
	if err != nil {
		t.Fatalf("UpsertFeed third: %v", err)
	}

	if err := UpdateFeedOrder(db, []int64{thirdID, firstID, secondID}); err != nil {
		t.Fatalf("UpdateFeedOrder: %v", err)
	}

	feeds, err := ListFeeds(db)
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != 3 {
		t.Fatalf("expected 3 feeds, got %d", len(feeds))
	}
	if feeds[0].ID != thirdID || feeds[1].ID != firstID || feeds[2].ID != secondID {
		t.Fatalf("unexpected feed order: got [%d %d %d]", feeds[0].ID, feeds[1].ID, feeds[2].ID)
	}
}

func TestInitAddsFeedSortOrderToExistingSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`
CREATE TABLE feeds (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	url TEXT NOT NULL UNIQUE,
	title TEXT NOT NULL,
	custom_title TEXT,
	created_at DATETIME NOT NULL,
	etag TEXT,
	last_modified TEXT,
	last_refreshed_at DATETIME,
	last_error TEXT,
	unchanged_count INTEGER NOT NULL DEFAULT 0,
	next_refresh_at DATETIME
)
`); err != nil {
		t.Fatalf("create legacy feeds table: %v", err)
	}

	now := time.Now().UTC()
	if _, err := db.Exec(
		`INSERT INTO feeds (url, title, created_at) VALUES (?, ?, ?), (?, ?, ?)`,
		"http://example.com/bravo", "Bravo", now,
		"http://example.com/alpha", "Alpha", now.Add(time.Second),
	); err != nil {
		t.Fatalf("insert legacy feeds: %v", err)
	}

	if err := Init(db); err != nil {
		t.Fatalf("Init: %v", err)
	}

	var hasSortOrder int
	if err := db.QueryRow(`
SELECT COUNT(*)
FROM pragma_table_info('feeds')
WHERE name = 'sort_order'
`).Scan(&hasSortOrder); err != nil {
		t.Fatalf("check sort_order column: %v", err)
	}
	if hasSortOrder != 1 {
		t.Fatalf("expected sort_order column to be added")
	}

	feeds, err := ListFeeds(db)
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != 2 {
		t.Fatalf("expected 2 feeds, got %d", len(feeds))
	}
	if feeds[0].Title != "Alpha" || feeds[1].Title != "Bravo" {
		t.Fatalf("expected legacy feeds to be initialized in title order, got %q then %q", feeds[0].Title, feeds[1].Title)
	}
}

func TestItemLimitAndTombstones(t *testing.T) {
	db := openTestDB(t)
	feedID, err := UpsertFeed(db, "http://example.com/rss", "Feed")
	if err != nil {
		t.Fatalf("UpsertFeed: %v", err)
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

	if _, err := UpsertItems(db, feedID, items); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}
	if err := EnforceItemLimit(db, feedID); err != nil {
		t.Fatalf("EnforceItemLimit: %v", err)
	}

	itemsInDB, err := ListItems(db, feedID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(itemsInDB) != 200 {
		t.Fatalf("expected 200 items, got %d", len(itemsInDB))
	}

	for i := 0; i < 10; i++ {
		guid := fmt.Sprintf("guid-%03d", i)
		if existsByGUID(t, db, feedID, guid) {
			t.Fatalf("expected %s to be deleted", guid)
		}
		if !existsInTombstones(t, db, feedID, guid) {
			t.Fatalf("expected %s to be tombstoned", guid)
		}
	}
}

func TestSweepReadItems(t *testing.T) {
	db := openTestDB(t)

	feedID, err := UpsertFeed(db, "http://example.com/rss", "Sweep Feed")
	if err != nil {
		t.Fatalf("UpsertFeed: %v", err)
	}

	if _, err := UpsertItems(db, feedID, []*gofeed.Item{{
		Title:           "Keep me",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>Summary</p>",
		PublishedParsed: timePtr(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Sweep me A",
		Link:            "http://example.com/2",
		GUID:            "2",
		Description:     "<p>Summary</p>",
		PublishedParsed: timePtr(time.Now().Add(-2 * time.Hour)),
	}}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	now := time.Now().UTC()
	if _, err := db.Exec("UPDATE items SET read_at = ? WHERE feed_id = ? AND guid = ?", now, feedID, "2"); err != nil {
		t.Fatalf("set read_at: %v", err)
	}

	deleted, err := SweepReadItems(db, feedID)
	if err != nil {
		t.Fatalf("SweepReadItems: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted item, got %d", deleted)
	}
	if existsByGUID(t, db, feedID, "2") {
		t.Fatalf("expected read item to be deleted")
	}
	if !existsInTombstones(t, db, feedID, "2") {
		t.Fatalf("expected deleted item to be tombstoned")
	}
}

func TestCleanupReadItems(t *testing.T) {
	db := openTestDB(t)

	feedID, err := UpsertFeed(db, "http://example.com/rss", "Cleanup Feed")
	if err != nil {
		t.Fatalf("UpsertFeed: %v", err)
	}

	if _, err := UpsertItems(db, feedID, []*gofeed.Item{{
		Title:           "Old Read",
		Link:            "http://example.com/old",
		GUID:            "old",
		Description:     "<p>Summary</p>",
		PublishedParsed: timePtr(time.Now().Add(-2 * time.Hour)),
	}}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	past := time.Now().UTC().Add(-31 * time.Minute)
	if _, err := db.Exec("UPDATE items SET read_at = ? WHERE feed_id = ? AND guid = ?", past, feedID, "old"); err != nil {
		t.Fatalf("set read_at: %v", err)
	}

	if err := CleanupReadItems(db); err != nil {
		t.Fatalf("CleanupReadItems: %v", err)
	}
	if existsByGUID(t, db, feedID, "old") {
		t.Fatalf("expected old read item to be deleted")
	}
	if !existsInTombstones(t, db, feedID, "old") {
		t.Fatalf("expected old read item to be tombstoned")
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

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := Init(db); err != nil {
		_ = db.Close()
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func timePtr(tw time.Time) *time.Time {
	return &tw
}
