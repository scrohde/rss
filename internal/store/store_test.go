//nolint:testpackage // Store tests exercise package-internal helpers directly.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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

func TestListPulseFeedIDsSkipsRecentlyRefreshed(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	firstID := mustUpsertFeed(t, db, "http://example.com/first", "First")
	secondID := mustUpsertFeed(t, db, "http://example.com/second", "Second")
	thirdID := mustUpsertFeed(t, db, "http://example.com/third", "Third")
	now := time.Now().UTC()

	err := UpdateFeedOrder(context.Background(), db, []int64{thirdID, firstID, secondID})
	if err != nil {
		t.Fatalf("UpdateFeedOrder: %v", err)
	}

	_, execErr := db.ExecContext(
		context.Background(),
		"UPDATE feeds SET last_refreshed_at = ? WHERE id = ?",
		now.Add(-2*time.Hour),
		thirdID,
	)
	if execErr != nil {
		t.Fatalf("set stale refresh timestamp: %v", execErr)
	}

	_, execErr = db.ExecContext(
		context.Background(),
		"UPDATE feeds SET last_refreshed_at = ? WHERE id = ?",
		now.Add(-30*time.Minute),
		firstID,
	)
	if execErr != nil {
		t.Fatalf("set recent refresh timestamp: %v", execErr)
	}

	ids, err := ListPulseFeedIDs(context.Background(), db, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ListPulseFeedIDs: %v", err)
	}

	if len(ids) != 2 {
		t.Fatalf("expected 2 pulse feed IDs, got %d", len(ids))
	}

	if ids[0] != thirdID || ids[1] != secondID {
		t.Fatalf("unexpected pulse feed IDs order: %+v", ids)
	}
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
	assertNoTombstoneLimitColumn(t, db)

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

	_, upsertErr := UpsertItems(context.Background(), db, feedID, sequentialItems(MaxFeedItems))
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

	assertGUIDRangeDeletedAndTombstoned(t, db, feedID, 0, MaxFeedItems-maxItemsPerFeed)
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

func TestCleanupTombstonesKeepsOldRowsBelowFixedLimit(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	feedID := mustUpsertFeed(t, db, "http://example.com/tombstones", "Tombstones")
	old := time.Now().UTC().Add(-365 * 24 * time.Hour)

	_, err := db.ExecContext(context.Background(), `
INSERT INTO tombstones (feed_id, guid, deleted_at)
VALUES (?, 'old-a', ?), (?, 'old-b', ?)
`, feedID, old, feedID, old.Add(time.Second))
	if err != nil {
		t.Fatalf("insert tombstones: %v", err)
	}

	deleted, err := cleanupTombstonesBeyondLimit(context.Background(), db)
	if err != nil {
		t.Fatalf("cleanupTombstonesBeyondLimit: %v", err)
	}

	if deleted != 0 {
		t.Fatalf("expected no deleted tombstones, got %d", deleted)
	}

	if !existsInTombstones(t, db, feedID, "old-a") {
		t.Fatal("expected first old tombstone to remain")
	}

	if !existsInTombstones(t, db, feedID, "old-b") {
		t.Fatal("expected second old tombstone to remain")
	}
}

func TestCleanupTombstonesEnforcesPerFeedLimit(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	largeFeedID := mustUpsertFeed(t, db, "http://example.com/large-tombstones", "Large Tombstones")
	smallFeedID := mustUpsertFeed(t, db, "http://example.com/small-tombstones", "Small Tombstones")
	base := time.Now().UTC().Add(-time.Duration(MaxFeedItems+10) * time.Minute)

	insertTestTombstones(t, db, largeFeedID, MaxFeedItems+2, base)
	insertTestTombstones(t, db, smallFeedID, MaxFeedItems+2, base.Add(-24*time.Hour))

	deleted, err := cleanupTombstonesBeyondLimit(context.Background(), db)
	if err != nil {
		t.Fatalf("cleanupTombstonesBeyondLimit: %v", err)
	}

	if deleted != 4 {
		t.Fatalf("expected 4 deleted tombstones, got %d", deleted)
	}

	assertTombstoneCleanupState(t, db, largeFeedID, smallFeedID)

	deleted, err = cleanupTombstonesBeyondLimit(context.Background(), db)
	if err != nil {
		t.Fatalf("second cleanupTombstonesBeyondLimit: %v", err)
	}

	if deleted != 0 {
		t.Fatalf("expected idempotent cleanup, deleted %d additional tombstones", deleted)
	}
}

func assertTombstoneCleanupState(t *testing.T, db *sql.DB, largeFeedID, smallFeedID int64) {
	t.Helper()

	if got := countTombstonesForFeed(t, db, largeFeedID); got != MaxFeedItems {
		t.Fatalf("expected %d large-feed tombstones, got %d", MaxFeedItems, got)
	}

	if got := countTombstonesForFeed(t, db, smallFeedID); got != MaxFeedItems {
		t.Fatalf("expected %d small-feed tombstones, got %d", MaxFeedItems, got)
	}

	if existsInTombstones(t, db, largeFeedID, "tombstone-0000") {
		t.Fatal("expected oldest large-feed tombstone to be deleted")
	}

	if existsInTombstones(t, db, largeFeedID, "tombstone-0001") {
		t.Fatal("expected second-oldest large-feed tombstone to be deleted")
	}

	if !existsInTombstones(t, db, largeFeedID, "tombstone-0002") {
		t.Fatal("expected retained large-feed boundary tombstone")
	}

	if existsInTombstones(t, db, smallFeedID, "tombstone-0000") {
		t.Fatal("expected oldest small-feed tombstone to be deleted")
	}

	if !existsInTombstones(t, db, smallFeedID, "tombstone-0002") {
		t.Fatal("expected retained small-feed boundary tombstone")
	}
}

func TestCleanupTombstonesPreventsEarlierOneItemGenerationFromReturning(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	feedID := mustUpsertFeed(t, db, "http://example.com/slow-feed", "Slow Feed")
	earlierPublished := time.Now().UTC().Add(-60 * 24 * time.Hour)
	earlierItem := newGofeedItem(
		"Earlier item",
		"http://example.com/earlier-item",
		"earlier-item",
		"<p>Summary</p>",
		&earlierPublished,
	)

	mustUpsertTestItems(t, db, feedID, []*gofeed.Item{earlierItem})
	markTestItemRead(t, db, feedID, earlierItem.GUID, time.Now().UTC().Add(-31*time.Minute))

	err := CleanupReadItems(db)
	if err != nil {
		t.Fatalf("CleanupReadItems for earlier generation: %v", err)
	}

	laterPublished := time.Now().UTC().Add(-24 * time.Hour)
	laterItem := newGofeedItem(
		"Later item",
		"http://example.com/later-item",
		"later-item",
		"<p>Summary</p>",
		&laterPublished,
	)

	mustUpsertTestItems(t, db, feedID, []*gofeed.Item{laterItem})
	markTestItemRead(t, db, feedID, laterItem.GUID, time.Now().UTC().Add(-31*time.Minute))

	err = CleanupReadItems(db)
	if err != nil {
		t.Fatalf("CleanupReadItems for later generation: %v", err)
	}

	err = CleanupTombstones(db)
	if err != nil {
		t.Fatalf("CleanupTombstones: %v", err)
	}

	inserted, err := UpsertItems(context.Background(), db, feedID, []*gofeed.Item{earlierItem})
	if err != nil {
		t.Fatalf("UpsertItems after tombstone cleanup: %v", err)
	}

	if inserted != 0 {
		t.Fatalf("expected tombstoned item not to be reinserted, got %d inserted rows", inserted)
	}

	if existsByGUID(t, db, feedID, earlierItem.GUID) {
		t.Fatal("expected earlier read item to remain absent")
	}

	if !existsInTombstones(t, db, feedID, earlierItem.GUID) {
		t.Fatal("expected earlier generation tombstone to remain")
	}
}

func TestCleanupTombstonesShortFetchesCannotReduceRetention(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		fetchedCount int
	}{
		{name: "one_item", fetchedCount: 1},
		{name: "empty", fetchedCount: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assertShortFetchKeepsTombstones(t, test.name, test.fetchedCount)
		})
	}
}

func assertShortFetchKeepsTombstones(t *testing.T, name string, fetchedCount int) {
	t.Helper()

	db := openTestDB(t)
	feedID := mustUpsertFeed(t, db, "http://example.com/"+name, name)
	base := time.Now().UTC().Add(-time.Hour)
	tombstonedItem := newGofeedItem(
		"Previously removed",
		"http://example.com/previously-removed",
		"tombstone-0000",
		"<p>Summary</p>",
		&base,
	)

	insertTestTombstones(t, db, feedID, 3, base)
	mustUpsertTestItems(t, db, feedID, sequentialItems(fetchedCount))

	err := CleanupTombstones(db)
	if err != nil {
		t.Fatalf("CleanupTombstones: %v", err)
	}

	inserted, err := UpsertItems(context.Background(), db, feedID, []*gofeed.Item{tombstonedItem})
	if err != nil {
		t.Fatalf("UpsertItems tombstoned item: %v", err)
	}

	if inserted != 0 {
		t.Fatalf("expected retained tombstone to prevent resurrection, inserted %d item", inserted)
	}

	if got := countTombstonesForFeed(t, db, feedID); got != 3 {
		t.Fatalf("expected all 3 tombstones to remain, got %d", got)
	}
}

func TestInitRemovesLegacyTombstonePruneTrigger(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	_, err := db.ExecContext(context.Background(), `
CREATE TRIGGER tombstones_prune
AFTER INSERT ON tombstones
BEGIN
	DELETE FROM tombstones WHERE datetime(deleted_at) <= datetime('now', '-30 days');
END
`)
	if err != nil {
		t.Fatalf("create legacy trigger: %v", err)
	}

	err = Init(db)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	var triggerCount int

	err = db.QueryRowContext(context.Background(), `
SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = 'tombstones_prune'
`).Scan(&triggerCount)
	if err != nil {
		t.Fatalf("count legacy trigger: %v", err)
	}

	if triggerCount != 0 {
		t.Fatal("expected legacy tombstone trigger to be removed")
	}
}

func TestListUnreadItemsAllFeeds(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	alphaID := mustUpsertFeed(t, db, "http://example.com/alpha", "Alpha")
	bravoID := mustUpsertFeed(t, db, "http://example.com/bravo", "Bravo")

	now := time.Now().UTC()
	alphaOld := now.Add(-2 * time.Hour)
	alphaNew := now.Add(-time.Hour)
	bravoNewest := now.Add(-10 * time.Minute)

	_, err := UpsertItems(context.Background(), db, alphaID, []*gofeed.Item{
		newGofeedItem("Alpha Old", "http://example.com/a-old", "a-old", "<p>Summary</p>", &alphaOld),
		newGofeedItem("Alpha New", "http://example.com/a-new", "a-new", "<p>Summary</p>", &alphaNew),
	})
	if err != nil {
		t.Fatalf("UpsertItems alpha: %v", err)
	}

	_, err = UpsertItems(context.Background(), db, bravoID, []*gofeed.Item{
		newGofeedItem("Bravo Newest", "http://example.com/b-new", "b-new", "<p>Summary</p>", &bravoNewest),
	})
	if err != nil {
		t.Fatalf("UpsertItems bravo: %v", err)
	}

	_, err = db.ExecContext(
		context.Background(),
		"UPDATE items SET read_at = ? WHERE feed_id = ? AND guid = ?",
		now,
		alphaID,
		"a-old",
	)
	if err != nil {
		t.Fatalf("set read_at: %v", err)
	}

	items, err := ListUnreadItemsAllFeeds(context.Background(), db, 10)
	if err != nil {
		t.Fatalf("ListUnreadItemsAllFeeds: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 unread items, got %d", len(items))
	}

	assertUnreadItem(t, &items[0], "Bravo Newest", "Bravo", "first")
	assertUnreadItem(t, &items[1], "Alpha New", "Alpha", "second")
}

func assertUnreadItem(t *testing.T, item *view.ItemView, wantTitle, wantFeed, position string) {
	t.Helper()

	if item.Title != wantTitle || item.FeedTitle != wantFeed {
		t.Fatalf("unexpected %s unread item: %#v", position, item)
	}
}

func TestListUnreadItemsByFeed(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	alphaID := mustUpsertFeed(t, db, "http://example.com/alpha", "Alpha")
	bravoID := mustUpsertFeed(t, db, "http://example.com/bravo", "Bravo")

	now := time.Now().UTC()
	alphaOld := now.Add(-2 * time.Hour)
	alphaNew := now.Add(-time.Hour)
	bravoNewest := now.Add(-10 * time.Minute)

	_, err := UpsertItems(context.Background(), db, alphaID, []*gofeed.Item{
		newGofeedItem("Alpha Old", "http://example.com/a-old", "a-old", "<p>Summary</p>", &alphaOld),
		newGofeedItem("Alpha New", "http://example.com/a-new", "a-new", "<p>Summary</p>", &alphaNew),
	})
	if err != nil {
		t.Fatalf("UpsertItems alpha: %v", err)
	}

	_, err = UpsertItems(context.Background(), db, bravoID, []*gofeed.Item{
		newGofeedItem("Bravo Newest", "http://example.com/b-new", "b-new", "<p>Summary</p>", &bravoNewest),
	})
	if err != nil {
		t.Fatalf("UpsertItems bravo: %v", err)
	}

	_, err = db.ExecContext(
		context.Background(),
		"UPDATE items SET read_at = ? WHERE feed_id = ? AND guid = ?",
		now,
		alphaID,
		"a-old",
	)
	if err != nil {
		t.Fatalf("set read_at: %v", err)
	}

	items, err := ListUnreadItemsByFeed(context.Background(), db, alphaID, 10)
	if err != nil {
		t.Fatalf("ListUnreadItemsByFeed: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 unread item, got %d", len(items))
	}

	if items[0].Title != "Alpha New" || items[0].FeedTitle != "Alpha" {
		t.Fatalf("unexpected unread item: %#v", items[0])
	}
}

func TestListUnreadFeedSectionsUsesSavedOrderAndBoundedPages(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	firstID := mustUpsertFeed(t, db, "http://example.com/first-busy", "First Busy")
	zeroUnreadID := mustUpsertFeed(t, db, "http://example.com/zero-unread", "Zero Unread")
	secondID := mustUpsertFeed(t, db, "http://example.com/second-small", "Second Small")
	thirdID := mustUpsertFeed(t, db, "http://example.com/third-small", "Third Small")

	err := UpdateFeedOrder(context.Background(), db, []int64{firstID, zeroUnreadID, secondID, thirdID})
	if err != nil {
		t.Fatalf("UpdateFeedOrder: %v", err)
	}

	base := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	mustUpsertTestItems(t, db, firstID, []*gofeed.Item{
		newGofeedItem("First Oldest", "http://example.com/first/oldest", "first-oldest", "", new(base)),
		newGofeedItem(
			"First Older", "http://example.com/first/older", "first-older", "", new(base.Add(time.Minute)),
		),
		newGofeedItem(
			"First Newer", "http://example.com/first/newer", "first-newer", "", new(base.Add(2*time.Minute)),
		),
		newGofeedItem(
			"First Newest", "http://example.com/first/newest", "first-newest", "", new(base.Add(3*time.Minute)),
		),
	})
	mustUpsertTestItems(t, db, zeroUnreadID, []*gofeed.Item{
		newGofeedItem(
			"Already Read", "http://example.com/read", "already-read", "", new(base.Add(4*time.Minute)),
		),
	})
	markTestItemRead(t, db, zeroUnreadID, "already-read", base.Add(5*time.Minute))
	mustUpsertTestItems(t, db, secondID, []*gofeed.Item{
		newGofeedItem(
			"Second Globally Newest", "http://example.com/second", "second", "", new(base.Add(2*time.Hour)),
		),
	})
	mustUpsertTestItems(t, db, thirdID, []*gofeed.Item{
		newGofeedItem(
			"Third Globally Newer", "http://example.com/third", "third", "", new(base.Add(time.Hour)),
		),
	})

	page := mustListUnreadFeedSections(t, db, nil, 2, 2)
	assertUnreadSectionCount(t, page, 2)
	assertUnreadSection(t, page.Sections[0], firstID, "First Busy", "First Newest", "First Newer")
	assertUnreadSection(t, page.Sections[1], secondID, "Second Small", "Second Globally Newest")
	requireUnreadItemCursor(t, page.Sections[0].Next)
	assertNoUnreadItemCursor(t, page.Sections[1].Next)

	nextCursor := requireUnreadFeedCursor(t, page.Next)
	assertUnreadFeedCursor(t, nextCursor, secondID)
	nextPage := mustListUnreadFeedSections(t, db, nextCursor, 2, 2)
	assertUnreadSectionCount(t, nextPage, 1)
	assertUnreadSection(t, nextPage.Sections[0], thirdID, "Third Small", "Third Globally Newer")
	assertNoUnreadFeedCursor(t, nextPage.Next)
}

func TestListUnreadItemsByFeedPageUsesStableKeysetAfterAnchorDeletion(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	feedID := mustUpsertFeed(t, db, "http://example.com/keyset", "Keyset")
	base := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)

	items := make([]*gofeed.Item, 0, 5)

	for index := 1; index <= 5; index++ {
		published := base.Add(time.Duration(index) * time.Minute)
		items = append(items, newGofeedItem(
			fmt.Sprintf("Item %d", index),
			fmt.Sprintf("http://example.com/keyset/%d", index),
			fmt.Sprintf("keyset-%d", index),
			"",
			&published,
		))
	}

	mustUpsertTestItems(t, db, feedID, items)

	firstPage := mustListUnreadItemsByFeedPage(t, db, feedID, nil)
	assertUnreadItemTitles(t, firstPage.Items, "Item 5", "Item 4")
	firstCursor := requireUnreadItemCursor(t, firstPage.Next)
	mustDeleteTestItem(t, db, firstCursor.ItemID)

	newest := base.Add(10 * time.Minute)
	mustUpsertTestItems(t, db, feedID, []*gofeed.Item{
		newGofeedItem("New Arrival", "http://example.com/keyset/new", "keyset-new", "", &newest),
	})

	secondPage := mustListUnreadItemsByFeedPage(t, db, feedID, firstCursor)
	assertUnreadItemTitles(t, secondPage.Items, "Item 3", "Item 2")
	secondCursor := requireUnreadItemCursor(t, secondPage.Next)
	finalPage := mustListUnreadItemsByFeedPage(t, db, feedID, secondCursor)
	assertUnreadItemTitles(t, finalPage.Items, "Item 1")
	assertNoUnreadItemCursor(t, finalPage.Next)
}

func TestListUnreadItemsByFeedPageBreaksTimestampTiesByID(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	feedID := mustUpsertFeed(t, db, "http://example.com/ties", "Ties")
	published := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	mustUpsertTestItems(t, db, feedID, []*gofeed.Item{
		newGofeedItem("First Insert", "http://example.com/ties/first", "ties-first", "", &published),
		newGofeedItem("Second Insert", "http://example.com/ties/second", "ties-second", "", &published),
		newGofeedItem("Third Insert", "http://example.com/ties/third", "ties-third", "", &published),
	})

	page := mustListUnreadItemsByFeedPage(t, db, feedID, nil)
	assertUnreadItemTitles(t, page.Items, "Third Insert", "Second Insert")
	nextCursor := requireUnreadItemCursor(t, page.Next)
	finalPage := mustListUnreadItemsByFeedPage(t, db, feedID, nextCursor)
	assertUnreadItemTitles(t, finalPage.Items, "First Insert")
	assertNoUnreadItemCursor(t, finalPage.Next)
}

func TestUnreadAggregatePageLimitsAreBounded(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	_, err := ListUnreadFeedSections(context.Background(), db, nil, maxUnreadFeedPageSize+1, 1)
	if !errors.Is(err, errInvalidUnreadPageLimit) {
		t.Fatalf("expected bounded feed limit error, got %v", err)
	}

	_, err = ListUnreadFeedSections(context.Background(), db, nil, 1, maxUnreadItemPageSize+1)
	if !errors.Is(err, errInvalidUnreadPageLimit) {
		t.Fatalf("expected bounded section item limit error, got %v", err)
	}

	_, err = ListUnreadItemsByFeedPage(context.Background(), db, 1, nil, maxUnreadItemPageSize+1)
	if !errors.Is(err, errInvalidUnreadPageLimit) {
		t.Fatalf("expected bounded item page limit error, got %v", err)
	}
}

func assertUnreadSection(
	t *testing.T,
	section UnreadFeedSection,
	wantFeedID int64,
	wantFeedTitle string,
	wantItemTitles ...string,
) {
	t.Helper()

	if section.FeedID != wantFeedID || section.FeedTitle != wantFeedTitle {
		t.Fatalf("unexpected unread section: %#v", section)
	}

	assertUnreadItemTitles(t, section.Items, wantItemTitles...)
}

func mustListUnreadFeedSections(
	t *testing.T,
	db *sql.DB,
	after *UnreadFeedCursor,
	feedLimit int,
	itemLimit int,
) UnreadFeedSectionsPage {
	t.Helper()

	page, err := ListUnreadFeedSections(context.Background(), db, after, feedLimit, itemLimit)
	if err != nil {
		t.Fatalf("ListUnreadFeedSections: %v", err)
	}

	return page
}

func mustListUnreadItemsByFeedPage(
	t *testing.T,
	db *sql.DB,
	feedID int64,
	after *UnreadItemCursor,
) UnreadItemsPage {
	t.Helper()

	page, err := ListUnreadItemsByFeedPage(context.Background(), db, feedID, after, 2)
	if err != nil {
		t.Fatalf("ListUnreadItemsByFeedPage: %v", err)
	}

	return page
}

func assertUnreadSectionCount(t *testing.T, page UnreadFeedSectionsPage, want int) {
	t.Helper()

	if len(page.Sections) != want {
		t.Fatalf("expected %d unread sections, got %d", want, len(page.Sections))
	}
}

func requireUnreadFeedCursor(t *testing.T, cursor *UnreadFeedCursor) *UnreadFeedCursor {
	t.Helper()

	if cursor == nil {
		t.Fatal("expected unread feed continuation cursor")
	}

	return cursor
}

func requireUnreadItemCursor(t *testing.T, cursor *UnreadItemCursor) *UnreadItemCursor {
	t.Helper()

	if cursor == nil {
		t.Fatal("expected unread item continuation cursor")
	}

	return cursor
}

func assertUnreadFeedCursor(t *testing.T, cursor *UnreadFeedCursor, wantFeedID int64) {
	t.Helper()

	if cursor.FeedID != wantFeedID {
		t.Fatalf("expected feed continuation after %d, got %#v", wantFeedID, cursor)
	}
}

func assertNoUnreadFeedCursor(t *testing.T, cursor *UnreadFeedCursor) {
	t.Helper()

	if cursor != nil {
		t.Fatalf("expected no unread feed continuation, got %#v", cursor)
	}
}

func assertNoUnreadItemCursor(t *testing.T, cursor *UnreadItemCursor) {
	t.Helper()

	if cursor != nil {
		t.Fatalf("expected no unread item continuation, got %#v", cursor)
	}
}

func assertUnreadItemTitles(t *testing.T, items []view.ItemView, want ...string) {
	t.Helper()

	if len(items) != len(want) {
		t.Fatalf("expected %d unread items, got %d: %#v", len(want), len(items), items)
	}

	for index, title := range want {
		if items[index].Title != title {
			t.Fatalf("expected unread item %d title %q, got %q", index, title, items[index].Title)
		}
	}
}

func mustUpsertTestItems(t *testing.T, db *sql.DB, feedID int64, items []*gofeed.Item) {
	t.Helper()

	_, err := UpsertItems(context.Background(), db, feedID, items)
	if err != nil {
		t.Fatalf("UpsertItems feed %d: %v", feedID, err)
	}
}

func markTestItemRead(t *testing.T, db *sql.DB, feedID int64, guid string, readAt time.Time) {
	t.Helper()

	_, err := db.ExecContext(
		context.Background(),
		"UPDATE items SET read_at = ? WHERE feed_id = ? AND guid = ?",
		readAt,
		feedID,
		guid,
	)
	if err != nil {
		t.Fatalf("mark test item read: %v", err)
	}
}

func mustDeleteTestItem(t *testing.T, db *sql.DB, itemID int64) {
	t.Helper()

	_, err := db.ExecContext(context.Background(), "DELETE FROM items WHERE id = ?", itemID)
	if err != nil {
		t.Fatalf("delete test item %d: %v", itemID, err)
	}
}

func TestGetUnreadStreamItem(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	feedID := mustUpsertFeed(t, db, "http://example.com/rss", "Feed Title")
	published := time.Now().UTC().Add(-30 * time.Minute)

	_, err := UpsertItems(context.Background(), db, feedID, []*gofeed.Item{
		newGofeedItem("Title", "http://example.com/item", "guid-1", "<p>Summary</p>", &published),
	})
	if err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	items, err := ListItems(context.Background(), db, feedID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}

	item, err := GetUnreadStreamItem(context.Background(), db, items[0].ID)
	if err != nil {
		t.Fatalf("GetUnreadStreamItem: %v", err)
	}

	if item.FeedID != feedID {
		t.Fatalf("expected feed ID %d, got %d", feedID, item.FeedID)
	}

	if item.FeedTitle != "Feed Title" {
		t.Fatalf("expected feed title %q, got %q", "Feed Title", item.FeedTitle)
	}

	if item.Title != "Title" {
		t.Fatalf("expected item title %q, got %q", "Title", item.Title)
	}
}

func TestMarkItemRead(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	feedID := mustUpsertFeed(t, db, "http://example.com/rss", "Feed")
	published := time.Now().UTC()

	_, err := UpsertItems(context.Background(), db, feedID, []*gofeed.Item{
		newGofeedItem("Title", "http://example.com/item", "guid-1", "<p>Summary</p>", &published),
	})
	if err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	items, err := ListItems(context.Background(), db, feedID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}

	itemID := items[0].ID

	err = MarkItemRead(context.Background(), db, itemID)
	if err != nil {
		t.Fatalf("MarkItemRead first call: %v", err)
	}

	err = MarkItemRead(context.Background(), db, itemID)
	if err != nil {
		t.Fatalf("MarkItemRead second call: %v", err)
	}

	reloaded, err := GetItem(context.Background(), db, itemID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}

	if !reloaded.IsRead {
		t.Fatal("expected item to be read after mark-read")
	}
}

func TestMarkItemReadMissingItem(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	err := MarkItemRead(context.Background(), db, 9999)
	if err == nil {
		t.Fatal("expected mark-read to fail for missing item")
	}

	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
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

func countTombstonesForFeed(t *testing.T, db *sql.DB, feedID int64) int {
	t.Helper()

	var count int

	err := db.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM tombstones WHERE feed_id = ?",
		feedID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count tombstones for feed %d: %v", feedID, err)
	}

	return count
}

func insertTestTombstones(t *testing.T, db *sql.DB, feedID int64, count int, base time.Time) {
	t.Helper()

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tombstone insert transaction: %v", err)
	}

	defer rollbackTx(tx)

	for index := range count {
		_, err = tx.ExecContext(context.Background(), `
INSERT INTO tombstones (feed_id, guid, deleted_at)
VALUES (?, ?, ?)
`,
			feedID,
			fmt.Sprintf("tombstone-%04d", index),
			base.Add(time.Duration(index)*time.Minute),
		)
		if err != nil {
			t.Fatalf("insert tombstone %d: %v", index, err)
		}
	}

	err = tx.Commit()
	if err != nil {
		t.Fatalf("commit tombstone insert transaction: %v", err)
	}
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

func assertNoTombstoneLimitColumn(t *testing.T, db *sql.DB) {
	t.Helper()

	var hasTombstoneLimit int

	queryErr := db.QueryRowContext(context.Background(), `
SELECT COUNT(*)
FROM pragma_table_info('feeds')
WHERE name = 'tombstone_limit'
`).Scan(&hasTombstoneLimit)
	if queryErr != nil {
		t.Fatalf("check tombstone_limit column: %v", queryErr)
	}

	if hasTombstoneLimit != 0 {
		t.Fatal("expected tombstone_limit column not to be added")
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

func TestUpsertItemsRejectsPathologicalInputBeforeWrites(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	feedID := mustUpsertFeed(t, db, "https://example.com/limited", "Limited")
	items := []*gofeed.Item{
		{GUID: "valid", Title: "valid"},
		{GUID: "large", Content: strings.Repeat("x", maxItemContentBytes+1)},
	}

	inserted, err := UpsertItems(context.Background(), db, feedID, items)
	if err == nil {
		t.Fatal("expected oversized item to fail")
	}

	if inserted != 0 {
		t.Fatalf("expected no inserts, got %d", inserted)
	}

	var count int

	err = db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM items WHERE feed_id = ?`, feedID).Scan(&count)
	if err != nil {
		t.Fatalf("count items: %v", err)
	}

	if count != 0 {
		t.Fatalf("expected no partial writes, got %d items", count)
	}
}

func TestValidateItemsRejectsExcessiveItemCount(t *testing.T) {
	t.Parallel()

	items := make([]*gofeed.Item, MaxFeedItems+1)
	for index := range items {
		items[index] = new(gofeed.Item)
		items[index].GUID = fmt.Sprintf("item-%d", index)
	}

	err := ValidateItems(items)
	if err == nil {
		t.Fatal("expected excessive item count to fail")
	}
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
