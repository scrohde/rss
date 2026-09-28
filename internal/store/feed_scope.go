package store

import (
	"time"

	"rss/internal/view"
)

const feedItemScopeSQL = "(feed_id = ? OR (? AND COALESCE(published_at, created_at) BETWEEN ? AND ?))"

// feedItemScopeArgs shares the virtual feed filter between listing and mutations.
func feedItemScopeArgs(feedID int64, now time.Time) []any {
	return []any{feedID, feedID == view.TodayFeedID, now.UTC().Add(-24 * time.Hour), now.UTC()}
}
