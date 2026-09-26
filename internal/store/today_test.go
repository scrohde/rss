//nolint:testpackage // Store tests exercise the package API with shared test fixtures.
package store

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

//nolint:cyclop,funlen,gocognit,revive // One scenario covers selection, eligibility, and query purity.
func TestListTodayItemsUsesSelectedFeedsAndEffectiveTimeWindow(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	selectedFeedID := mustUpsertFeed(t, db, "https://example.com/today-selected", "Selected")
	hourFeedID := mustUpsertFeed(t, db, "https://example.com/today-hour", "One hour")
	missingFeedID := mustUpsertFeed(t, db, "https://example.com/today-missing", "Missing publication")
	sixHoursFeedID := mustUpsertFeed(t, db, "https://example.com/today-six-hours", "Six hours")
	cutoffFeedID := mustUpsertFeed(t, db, "https://example.com/today-cutoff", "At lower boundary")
	oldFeedID := mustUpsertFeed(t, db, "https://example.com/today-old", "Before lower boundary")
	futureFeedID := mustUpsertFeed(t, db, "https://example.com/today-future", "Future")
	readFeedID := mustUpsertFeed(t, db, "https://example.com/today-read", "Already read")
	otherFeedID := mustUpsertFeed(t, db, "https://example.com/today-other", "Other")
	now := time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-todayWindow)

	nowPublished := now
	hourAgo := now.Add(-time.Hour)
	sixHoursAgo := now.Add(-6 * time.Hour)
	beforeCutoff := cutoff.Add(-time.Second)
	future := now.Add(time.Second)
	readPublished := now.Add(-30 * time.Minute)

	mustUpsertTestItems(t, db, selectedFeedID, []*gofeed.Item{
		newGofeedItem("At now", "https://example.com/now", "now", "", &nowPublished),
	})
	mustUpsertTestItems(t, db, hourFeedID, []*gofeed.Item{
		newGofeedItem("One hour", "https://example.com/hour", "hour", "", &hourAgo),
	})
	mustUpsertTestItems(t, db, missingFeedID, []*gofeed.Item{
		newGofeedItem("Missing publication", "https://example.com/missing", "missing", "", nil),
	})
	mustUpsertTestItems(t, db, sixHoursFeedID, []*gofeed.Item{
		newGofeedItem("Six hours", "https://example.com/six", "six", "", &sixHoursAgo),
	})
	mustUpsertTestItems(t, db, cutoffFeedID, []*gofeed.Item{
		newGofeedItem("At lower boundary", "https://example.com/cutoff", "cutoff", "", &cutoff),
	})
	mustUpsertTestItems(t, db, oldFeedID, []*gofeed.Item{
		newGofeedItem("Before lower boundary", "https://example.com/old", "old", "", &beforeCutoff),
	})
	mustUpsertTestItems(t, db, futureFeedID, []*gofeed.Item{
		newGofeedItem("Future", "https://example.com/future", "future", "", &future),
	})
	mustUpsertTestItems(t, db, readFeedID, []*gofeed.Item{
		newGofeedItem("Already read", "https://example.com/read", "read", "", &readPublished),
	})
	mustUpsertTestItems(t, db, otherFeedID, []*gofeed.Item{
		newGofeedItem("Unselected", "https://example.com/unselected", "unselected", "", &nowPublished),
	})

	_, err := db.ExecContext(ctx, `
UPDATE items SET created_at = ? WHERE feed_id = ? AND guid = 'missing'
	`, now.Add(-2*time.Hour), missingFeedID)
	if err != nil {
		t.Fatalf("set missing-publication ingestion time: %v", err)
	}

	markTestItemRead(t, db, readFeedID, "read", now)

	items, err := ListTodayItems(ctx, db, now)
	if err != nil {
		t.Fatalf("ListTodayItems without selections: %v", err)
	}

	if len(items) != 0 {
		t.Fatalf("expected no stories without selected feeds, got %#v", items)
	}

	selectedFeedIDs := []int64{
		selectedFeedID,
		hourFeedID,
		missingFeedID,
		sixHoursFeedID,
		cutoffFeedID,
		oldFeedID,
		futureFeedID,
		readFeedID,
	}

	err = SetTodayFeedIDs(ctx, db, selectedFeedIDs)
	if err != nil {
		t.Fatalf("select Today feed: %v", err)
	}

	var unreadBefore int

	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM items WHERE read_at IS NULL").Scan(&unreadBefore)
	if err != nil {
		t.Fatalf("count unread items before Today query: %v", err)
	}

	items, err = ListTodayItems(ctx, db, now)
	if err != nil {
		t.Fatalf("ListTodayItems: %v", err)
	}

	wantTitles := []string{"At now", "One hour", "Missing publication", "Six hours", "At lower boundary"}

	if len(items) != len(wantTitles) {
		t.Fatalf("expected %d Today stories, got %d: %#v", len(wantTitles), len(items), items)
	}

	wantFeedIDs := map[string]int64{
		"At now":              selectedFeedID,
		"One hour":            hourFeedID,
		"Missing publication": missingFeedID,
		"Six hours":           sixHoursFeedID,
		"At lower boundary":   cutoffFeedID,
	}

	wantFeedTitles := map[string]string{
		"At now":              "Selected",
		"One hour":            "One hour",
		"Missing publication": "Missing publication",
		"Six hours":           "Six hours",
		"At lower boundary":   "At lower boundary",
	}

	for index, wantTitle := range wantTitles {
		if items[index].Title != wantTitle {
			t.Errorf("Today story %d: got %q, want %q", index, items[index].Title, wantTitle)
		}

		if items[index].FeedID != wantFeedIDs[wantTitle] || items[index].FeedTitle != wantFeedTitles[wantTitle] {
			t.Errorf("Today story %q is missing feed identity: %+v", wantTitle, items[index])
		}
	}

	var unreadAfter int

	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM items WHERE read_at IS NULL").Scan(&unreadAfter)
	if err != nil {
		t.Fatalf("count unread items after Today query: %v", err)
	}

	if unreadAfter != unreadBefore {
		t.Fatalf("Today query changed unread state: before %d, after %d", unreadBefore, unreadAfter)
	}
}

func TestListTodayItemsReturnsEmptyWhenNoStoriesAreEligible(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	feedID := mustUpsertFeed(t, db, "https://example.com/today-empty", "Empty")
	now := time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	old := now.Add(-todayWindow - time.Second)
	future := now.Add(time.Second)
	mustUpsertTestItems(t, db, feedID, []*gofeed.Item{
		newGofeedItem("Too old", "https://example.com/too-old", "old", "", &old),
		newGofeedItem("Too new", "https://example.com/too-new", "future", "", &future),
	})

	err := SetTodayFeedIDs(ctx, db, []int64{feedID})
	if err != nil {
		t.Fatalf("select Today feed: %v", err)
	}

	items, err := ListTodayItems(ctx, db, now)
	if err != nil {
		t.Fatalf("ListTodayItems: %v", err)
	}

	if len(items) != 0 {
		t.Fatalf("expected no eligible stories, got %#v", items)
	}
}

//nolint:cyclop,funlen,gocognit,revive // The scenario verifies per-feed and global ranking together.
func TestListTodayItemsEnforcesPerFeedAndTotalCapsWithIDTies(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	published := now.Add(-time.Hour)

	const feedCount = 8

	const storiesPerFeed = 4

	feedIDs := make([]int64, 0, feedCount)

	wantIDs := make([]int64, 0, feedCount*todayItemsPerFeed)

	for feedIndex := range feedCount {
		feedName := fmt.Sprintf("feed-%c", rune('a'+feedIndex))
		feedID := mustUpsertFeed(
			t,
			db,
			"https://example.com/today-tie-"+feedName,
			"Tie feed",
		)
		feedIDs = append(feedIDs, feedID)

		feedItems := make([]*gofeed.Item, 0, storiesPerFeed)
		for storyIndex := range storiesPerFeed {
			guid := fmt.Sprintf("%s-story-%d", feedName, storyIndex)
			feedItems = append(feedItems, newGofeedItem(
				guid,
				"https://example.com/"+guid,
				guid,
				"",
				&published,
			))
		}

		mustUpsertTestItems(t, db, feedID, feedItems)

		for storyIndex := 1; storyIndex < storiesPerFeed; storyIndex++ {
			guid := fmt.Sprintf("%s-story-%d", feedName, storyIndex)

			var itemID int64

			err := db.QueryRowContext(ctx, `
SELECT id FROM items WHERE feed_id = ? AND guid = ?
			`, feedID, guid).Scan(&itemID)
			if err != nil {
				t.Fatalf("load tied item ID for %q: %v", guid, err)
			}

			wantIDs = append(wantIDs, itemID)
		}
	}

	err := SetTodayFeedIDs(ctx, db, feedIDs)
	if err != nil {
		t.Fatalf("select Today feeds: %v", err)
	}

	items, err := ListTodayItems(ctx, db, now)
	if err != nil {
		t.Fatalf("ListTodayItems: %v", err)
	}

	if len(items) != todayItemsTotalLimit {
		t.Fatalf("expected total cap of %d stories, got %d", todayItemsTotalLimit, len(items))
	}

	slices.Sort(wantIDs)
	slices.Reverse(wantIDs)
	wantIDs = wantIDs[:todayItemsTotalLimit]

	feedItemCounts := make(map[int64]int)

	for index, item := range items {
		if item.ID != wantIDs[index] {
			t.Errorf("story %d: got ID %d, want deterministic ID %d", index, item.ID, wantIDs[index])
		}

		if item.FeedID == 0 || item.FeedTitle == "" {
			t.Errorf("Today story is missing feed identity: %+v", item)
		}

		feedItemCounts[item.FeedID]++
	}

	for feedID, count := range feedItemCounts {
		if count > todayItemsPerFeed {
			t.Errorf("feed %d returned %d stories, above per-feed cap %d", feedID, count, todayItemsPerFeed)
		}
	}
}

func TestListTodayItemsIgnoresDeletedFeedSelections(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	feedID := mustUpsertFeed(t, db, "https://example.com/today-deleted", "Deleted")
	now := time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC)
	published := now.Add(-time.Hour)
	mustUpsertTestItems(t, db, feedID, []*gofeed.Item{
		newGofeedItem("Removed feed story", "https://example.com/removed", "removed", "", &published),
	})

	err := SetTodayFeedIDs(ctx, db, []int64{feedID})
	if err != nil {
		t.Fatalf("select Today feed: %v", err)
	}

	err = DeleteFeed(ctx, db, feedID)
	if err != nil {
		t.Fatalf("delete selected feed: %v", err)
	}

	items, err := ListTodayItems(ctx, db, now)
	if err != nil {
		t.Fatalf("ListTodayItems after feed deletion: %v", err)
	}

	if len(items) != 0 {
		t.Fatalf("deleted feed selection returned Today stories: %#v", items)
	}

	preferences, err := GetReadingPreferences(ctx, db)
	if err != nil {
		t.Fatalf("GetReadingPreferences after feed deletion: %v", err)
	}

	if len(preferences.TodayFeedIDs) != 0 {
		t.Fatalf("deleted feed remained selected: %v", preferences.TodayFeedIDs)
	}
}
