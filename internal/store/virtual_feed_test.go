//nolint:testpackage // Exercises the shared store operations with existing fixtures.
package store

import (
	"context"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"

	"rss/internal/view"
)

//nolint:cyclop,gocognit,revive // One journey verifies the scope of read, undo, and clear operations.
func TestTodaySharedReadUndoAndClearScope(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()
	recent := time.Now().UTC().Add(-time.Hour)
	old := recent.Add(-48 * time.Hour)

	for _, name := range []string{"one", "two"} {
		feedID := mustUpsertFeed(t, db, "https://example.com/"+name, name)
		mustUpsertTestItems(t, db, feedID, []*gofeed.Item{
			newGofeedItem("Recent", "https://example.com/recent", "recent", "", &recent),
			newGofeedItem("Old", "https://example.com/old", "old", "", &old),
		})
	}

	ids, err := MarkAllReadWithUndo(ctx, db, view.TodayFeedID)
	if err != nil || len(ids) != 2 {
		t.Fatalf("mark Today read: ids=%v err=%v", ids, err)
	}

	err = MarkItemsUnread(ctx, db, view.TodayFeedID, ids)
	if err != nil {
		t.Fatal(err)
	}

	var unread int

	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM items WHERE read_at IS NULL").Scan(&unread)
	if err != nil || unread != 4 {
		t.Fatalf("undo: unread=%d err=%v", unread, err)
	}

	err = MarkAllRead(ctx, db, view.TodayFeedID)
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := SweepReadItems(ctx, db, view.TodayFeedID)
	if err != nil || deleted != 2 {
		t.Fatalf("clear Today: deleted=%d err=%v", deleted, err)
	}

	var remaining int

	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM items WHERE read_at IS NULL").Scan(&remaining)
	if err != nil || remaining != 2 {
		t.Fatalf("old items remain unread: count=%d err=%v", remaining, err)
	}
}
