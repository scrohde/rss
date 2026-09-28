package store

import (
	"context"
	"database/sql"
	"time"

	"rss/internal/view"
)

const todayWindow = 24 * time.Hour

// ListTodayItems uses the same item query and read-state handling as any feed.
func ListTodayItems(ctx context.Context, db *sql.DB, now time.Time) ([]view.ItemView, error) {
	return listItemsAt(ctx, db, view.TodayFeedID, now)
}
