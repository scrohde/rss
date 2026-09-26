//nolint:testpackage // Store tests intentionally share package-level fixtures.
package store

import (
	"context"
	"database/sql"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

//nolint:funlen // One scenario verifies every cutoff predicate and cross-feed exclusion together.
func TestCountAndMarkUnreadItemsBeforeUsesStrictFeedScopedCutoff(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	feedID := mustUpsertFeed(t, db, "http://example.com/catch-up", "Catch up")
	otherFeedID := mustUpsertFeed(t, db, "http://example.com/catch-up-other", "Other")
	cutoff := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	before := cutoff.Add(-time.Hour)
	after := cutoff.Add(time.Hour)

	mustUpsertTestItems(t, db, feedID, []*gofeed.Item{
		newGofeedItem("Before", "https://example.com/before", "before", "", &before),
		newGofeedItem("At cutoff", "https://example.com/at-cutoff", "at-cutoff", "", &cutoff),
		newGofeedItem("After", "https://example.com/after", "after", "", &after),
		newGofeedItem("Missing publication", "https://example.com/missing", "missing", "", nil),
		newGofeedItem("Already read", "https://example.com/read", "read", "", &before),
	})
	mustUpsertTestItems(t, db, otherFeedID, []*gofeed.Item{
		newGofeedItem("Other feed", "https://example.com/other", "other", "", &before),
	})

	_, err := db.ExecContext(context.Background(), `
UPDATE items SET created_at = ? WHERE feed_id = ? AND guid = 'missing'
	`, cutoff.Add(-time.Minute), feedID)
	if err != nil {
		t.Fatalf("set missing-publication ingestion time: %v", err)
	}

	_, err = db.ExecContext(context.Background(), `
UPDATE items SET read_at = ? WHERE feed_id = ? AND guid = 'read'
	`, cutoff.Add(-30*time.Minute), feedID)
	if err != nil {
		t.Fatalf("mark item already read: %v", err)
	}

	offsetCutoff := cutoff.In(time.FixedZone("offset", -7*60*60))

	beforeCount, err := CountUnreadItemsBefore(context.Background(), db, feedID, offsetCutoff)
	if err != nil {
		t.Fatalf("CountUnreadItemsBefore: %v", err)
	}

	if beforeCount != 2 {
		t.Fatalf("expected old and missing-publication items in preview, got %d", beforeCount)
	}

	result, err := MarkUnreadItemsBefore(context.Background(), db, feedID, cutoff)
	if err != nil {
		t.Fatalf("MarkUnreadItemsBefore: %v", err)
	}

	if result.AffectedCount != 2 {
		t.Fatalf("expected affected count 2, got %d", result.AffectedCount)
	}

	assertInt64SetEqual(t, result.ChangedItemIDs, itemIDsByGUID(t, db, feedID, "before", "missing"))

	assertReadStateByGUID(t, db, feedID, "before", true)
	assertReadStateByGUID(t, db, feedID, "missing", true)
	assertReadStateByGUID(t, db, feedID, "at-cutoff", false)
	assertReadStateByGUID(t, db, feedID, "after", false)
	assertReadStateByGUID(t, db, feedID, "read", true)
	assertReadStateByGUID(t, db, otherFeedID, "other", false)
}

//nolint:revive // Covers a changed preview and a no-op apply as one contract.
func TestMarkUnreadItemsBeforeRecalculatesAfterPreviewAndHandlesNoOp(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	feedID := mustUpsertFeed(t, db, "http://example.com/catch-up-arrivals", "Arrivals")
	cutoff := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	before := cutoff.Add(-time.Minute)
	mustUpsertTestItems(t, db, feedID, []*gofeed.Item{
		newGofeedItem("First", "https://example.com/first", "first", "", &before),
	})

	previewCount, err := CountUnreadItemsBefore(context.Background(), db, feedID, cutoff)
	if err != nil {
		t.Fatalf("CountUnreadItemsBefore: %v", err)
	}

	if previewCount != 1 {
		t.Fatalf("expected one preview item, got %d", previewCount)
	}

	newArrival := cutoff.Add(-2 * time.Minute)
	mustUpsertTestItems(t, db, feedID, []*gofeed.Item{
		newGofeedItem("New arrival", "https://example.com/new-arrival", "new-arrival", "", &newArrival),
	})

	result, err := MarkUnreadItemsBefore(context.Background(), db, feedID, cutoff)
	if err != nil {
		t.Fatalf("MarkUnreadItemsBefore: %v", err)
	}

	if result.AffectedCount != 2 || len(result.ChangedItemIDs) != 2 {
		t.Fatalf("expected apply to recalculate two eligible items, got %#v", result)
	}

	noop, err := MarkUnreadItemsBefore(context.Background(), db, feedID, cutoff)
	if err != nil {
		t.Fatalf("MarkUnreadItemsBefore no-op: %v", err)
	}

	if noop.AffectedCount != 0 || len(noop.ChangedItemIDs) != 0 {
		t.Fatalf("expected an empty no-op result, got %#v", noop)
	}
}

func TestMarkUnreadItemsBeforeRollsBackOnUpdateFailure(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	feedID := mustUpsertFeed(t, db, "http://example.com/catch-up-failure", "Failure")
	cutoff := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	before := cutoff.Add(-time.Hour)
	mustUpsertTestItems(t, db, feedID, []*gofeed.Item{
		newGofeedItem("First", "https://example.com/first-failure", "first", "", &before),
		newGofeedItem("Failing", "https://example.com/failing", "failing", "", &before),
	})

	_, err := db.ExecContext(context.Background(), `
CREATE TRIGGER fail_catch_up AFTER UPDATE OF read_at ON items
WHEN OLD.guid = 'failing'
BEGIN
	SELECT RAISE(ABORT, 'forced catch-up failure');
END;
	`)
	if err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	result, err := MarkUnreadItemsBefore(context.Background(), db, feedID, cutoff)
	if err == nil {
		t.Fatalf("expected update failure, got result %#v", result)
	}

	assertReadStateByGUID(t, db, feedID, "first", false)
	assertReadStateByGUID(t, db, feedID, "failing", false)
}

//nolint:revive // The shared assertion keeps the test matrix compact.
func assertReadStateByGUID(t *testing.T, db *sql.DB, feedID int64, guid string, wantRead bool) {
	t.Helper()

	var readAt sql.NullTime

	err := db.QueryRowContext(context.Background(), `
SELECT read_at FROM items WHERE feed_id = ? AND guid = ?
	`, feedID, guid).Scan(&readAt)
	if err != nil {
		t.Fatalf("load read state for %q: %v", guid, err)
	}

	if readAt.Valid != wantRead {
		t.Fatalf("expected guid %q read state %t, got %t", guid, wantRead, readAt.Valid)
	}
}

func itemIDsByGUID(t *testing.T, db *sql.DB, feedID int64, guids ...string) []int64 {
	t.Helper()

	itemIDs := make([]int64, 0, len(guids))
	for _, guid := range guids {
		var itemID int64

		err := db.QueryRowContext(context.Background(), `
SELECT id FROM items WHERE feed_id = ? AND guid = ?
		`, feedID, guid).Scan(&itemID)
		if err != nil {
			t.Fatalf("load item ID for %q: %v", guid, err)
		}

		itemIDs = append(itemIDs, itemID)
	}

	return itemIDs
}

func assertInt64SetEqual(t *testing.T, got, want []int64) {
	t.Helper()

	gotCopy := append([]int64(nil), got...)
	wantCopy := append([]int64(nil), want...)

	slices.Sort(gotCopy)
	slices.Sort(wantCopy)

	if !reflect.DeepEqual(gotCopy, wantCopy) {
		t.Fatalf("expected item ID set %v, got %v", wantCopy, gotCopy)
	}
}
