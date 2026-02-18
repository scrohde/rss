//nolint:testpackage // Store tests exercise package-internal helpers directly.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"

	"rss/internal/view"
)

func TestUpsertFeedCustomTitlePreserved(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	feedID, err := UpsertFeed(context.Background(), db, "http://example.com/rss", "Source Title")
	if err != nil {
		t.Fatalf("UpsertFeed: %v", err)
	}

	updateErr := UpdateFeedTitle(context.Background(), db, feedID, "Custom Title")
	if updateErr != nil {
		t.Fatalf("UpdateFeedTitle: %v", updateErr)
	}

	_, err = UpsertFeed(context.Background(), db, "http://example.com/rss", "Updated Source")
	if err != nil {
		t.Fatalf("UpsertFeed update: %v", err)
	}

	feeds, err := ListFeeds(context.Background(), db)
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
	t.Parallel()

	db := openTestDB(t)

	firstID := mustUpsertFeed(t, db, "http://example.com/first", "First")
	secondID := mustUpsertFeed(t, db, "http://example.com/second", "Second")
	thirdID := mustUpsertFeed(t, db, "http://example.com/third", "Third")

	err := UpdateFeedOrder(context.Background(), db, []int64{thirdID, firstID, secondID})
	if err != nil {
		t.Fatalf("UpdateFeedOrder: %v", err)
	}

	feeds := mustListFeeds(t, db)

	if len(feeds) != 3 {
		t.Fatalf("expected 3 feeds, got %d", len(feeds))
	}

	assertFeedOrderIDs(t, feeds, thirdID, firstID, secondID)
}

func TestInitAddsFeedSortOrderToExistingSchema(t *testing.T) {
	t.Parallel()

	db := openLegacySchemaDB(t)
	mustInsertLegacyFeeds(t, db)

	initErr := Init(db)
	if initErr != nil {
		t.Fatalf("Init: %v", initErr)
	}

	assertHasSortOrderColumn(t, db)

	feeds := mustListFeeds(t, db)

	if len(feeds) != 2 {
		t.Fatalf("expected 2 feeds, got %d", len(feeds))
	}

	if feeds[0].Title != "Alpha" || feeds[1].Title != "Bravo" {
		t.Fatalf(
			"expected legacy feeds to be initialized in title order, got %q then %q",
			feeds[0].Title,
			feeds[1].Title,
		)
	}
}

func TestItemLimitAndTombstones(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	feedID := mustUpsertFeed(t, db, "http://example.com/rss", "Feed")

	_, upsertErr := UpsertItems(context.Background(), db, feedID, sequentialItems(210))
	if upsertErr != nil {
		t.Fatalf("UpsertItems: %v", upsertErr)
	}

	enforceErr := EnforceItemLimit(context.Background(), db, feedID)
	if enforceErr != nil {
		t.Fatalf("EnforceItemLimit: %v", enforceErr)
	}

	itemsInDB, err := ListItems(context.Background(), db, feedID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}

	if len(itemsInDB) != 200 {
		t.Fatalf("expected 200 items, got %d", len(itemsInDB))
	}

	assertGUIDRangeDeletedAndTombstoned(t, db, feedID, 0, 10)
}

func TestSweepReadItems(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	feedID, err := UpsertFeed(context.Background(), db, "http://example.com/rss", "Sweep Feed")
	if err != nil {
		t.Fatalf("UpsertFeed: %v", err)
	}

	_, upsertErr := UpsertItems(context.Background(), db, feedID, []*gofeed.Item{{
		Title:           "Keep me",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Sweep me A",
		Link:            "http://example.com/2",
		GUID:            "2",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})
	if upsertErr != nil {
		t.Fatalf("UpsertItems: %v", upsertErr)
	}

	now := time.Now().UTC()

	_, err = db.ExecContext(
		context.Background(),
		"UPDATE items SET read_at = ? WHERE feed_id = ? AND guid = ?",
		now,
		feedID,
		"2",
	)
	if err != nil {
		t.Fatalf("set read_at: %v", err)
	}

	deleted, err := SweepReadItems(context.Background(), db, feedID)
	if err != nil {
		t.Fatalf("SweepReadItems: %v", err)
	}

	if deleted != 1 {
		t.Fatalf("expected 1 deleted item, got %d", deleted)
	}

	if existsByGUID(t, db, feedID, "2") {
		t.Fatal("expected read item to be deleted")
	}

	if !existsInTombstones(t, db, feedID, "2") {
		t.Fatal("expected deleted item to be tombstoned")
	}
}

func TestCleanupReadItems(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	feedID, err := UpsertFeed(context.Background(), db, "http://example.com/rss", "Cleanup Feed")
	if err != nil {
		t.Fatalf("UpsertFeed: %v", err)
	}

	_, upsertErr := UpsertItems(context.Background(), db, feedID, []*gofeed.Item{{
		Title:           "Old Read",
		Link:            "http://example.com/old",
		GUID:            "old",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})
	if upsertErr != nil {
		t.Fatalf("UpsertItems: %v", upsertErr)
	}

	past := time.Now().UTC().Add(-31 * time.Minute)

	_, err = db.ExecContext(
		context.Background(),
		"UPDATE items SET read_at = ? WHERE feed_id = ? AND guid = ?",
		past,
		feedID,
		"old",
	)
	if err != nil {
		t.Fatalf("set read_at: %v", err)
	}

	cleanupErr := CleanupReadItems(db)
	if cleanupErr != nil {
		t.Fatalf("CleanupReadItems: %v", cleanupErr)
	}

	if existsByGUID(t, db, feedID, "old") {
		t.Fatal("expected old read item to be deleted")
	}

	if !existsInTombstones(t, db, feedID, "old") {
		t.Fatal("expected old read item to be tombstoned")
	}
}

func existsByGUID(t *testing.T, db *sql.DB, feedID int64, guid string) bool {
	t.Helper()

	var count int

	err := db.QueryRowContext(context.Background(), `
SELECT COUNT(*)
FROM items
WHERE feed_id = ? AND guid = ?
`, feedID, guid).Scan(&count)
	if err != nil {
		t.Fatalf("existsByGUID: %v", err)
	}

	return count > 0
}

func existsInTombstones(t *testing.T, db *sql.DB, feedID int64, guid string) bool {
	t.Helper()

	var count int

	err := db.QueryRowContext(context.Background(), `
SELECT COUNT(*)
FROM tombstones
WHERE feed_id = ? AND guid = ?
`, feedID, guid).Scan(&count)
	if err != nil {
		t.Fatalf("existsInTombstones: %v", err)
	}

	return count > 0
}

func mustUpsertFeed(t *testing.T, db *sql.DB, feedURL, title string) int64 {
	t.Helper()

	feedID, err := UpsertFeed(context.Background(), db, feedURL, title)
	if err != nil {
		t.Fatalf("UpsertFeed %q: %v", feedURL, err)
	}

	return feedID
}

func mustListFeeds(t *testing.T, db *sql.DB) []view.FeedView {
	t.Helper()

	feeds, err := ListFeeds(context.Background(), db)
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}

	return feeds
}

func assertFeedOrderIDs(t *testing.T, feeds []view.FeedView, expected ...int64) {
	t.Helper()

	if len(feeds) < len(expected) {
		t.Fatalf("expected at least %d feeds, got %d", len(expected), len(feeds))
	}

	for idx, id := range expected {
		if feeds[idx].ID != id {
			t.Fatalf("unexpected feed order at %d: got %d, want %d", idx, feeds[idx].ID, id)
		}
	}
}

func openLegacySchemaDB(t *testing.T) *sql.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "legacy.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() {
		closeErr := db.Close()
		if closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}
	})

	_, execErr := db.ExecContext(context.Background(), `
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
`)
	if execErr != nil {
		t.Fatalf("create legacy feeds table: %v", execErr)
	}

	return db
}

func mustInsertLegacyFeeds(t *testing.T, db *sql.DB) {
	t.Helper()

	now := time.Now().UTC()

	_, insertErr := db.ExecContext(context.Background(),
		`INSERT INTO feeds (url, title, created_at) VALUES (?, ?, ?), (?, ?, ?)`,
		"http://example.com/bravo", "Bravo", now,
		"http://example.com/alpha", "Alpha", now.Add(time.Second),
	)
	if insertErr != nil {
		t.Fatalf("insert legacy feeds: %v", insertErr)
	}
}

func assertHasSortOrderColumn(t *testing.T, db *sql.DB) {
	t.Helper()

	var hasSortOrder int

	queryErr := db.QueryRowContext(context.Background(), `
SELECT COUNT(*)
FROM pragma_table_info('feeds')
WHERE name = 'sort_order'
`).Scan(&hasSortOrder)
	if queryErr != nil {
		t.Fatalf("check sort_order column: %v", queryErr)
	}

	if hasSortOrder != 1 {
		t.Fatal("expected sort_order column to be added")
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	initErr := Init(db)
	if initErr != nil {
		closeErr := db.Close()
		if closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}

		t.Fatalf("Init: %v", initErr)
	}

	t.Cleanup(func() {
		closeErr := db.Close()
		if closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}
	})

	return db
}

func sequentialItems(count int) []*gofeed.Item {
	base := time.Now().UTC().Add(-time.Duration(count) * time.Minute)
	items := make([]*gofeed.Item, 0, count)

	for i := range count {
		published := base.Add(time.Duration(i) * time.Minute)
		items = append(items, newGofeedItem(
			fmt.Sprintf("Item %03d", i),
			fmt.Sprintf("http://example.com/%d", i),
			fmt.Sprintf("guid-%03d", i),
			"<p>Summary</p>",
			&published,
		))
	}

	return items
}

func assertGUIDRangeDeletedAndTombstoned(t *testing.T, db *sql.DB, feedID int64, start, end int) {
	t.Helper()

	for i := start; i < end; i++ {
		guid := fmt.Sprintf("guid-%03d", i)
		if existsByGUID(t, db, feedID, guid) {
			t.Fatalf("expected %s to be deleted", guid)
		}

		if !existsInTombstones(t, db, feedID, guid) {
			t.Fatalf("expected %s to be tombstoned", guid)
		}
	}
}

func newGofeedItem(title, link, guid, description string, published *time.Time) *gofeed.Item {
	item := new(gofeed.Item)
	item.Title = title
	item.Link = link
	item.GUID = guid
	item.Description = description
	item.PublishedParsed = published

	return item
}
