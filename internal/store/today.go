package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"rss/internal/view"
)

const (
	todayWindow          = 24 * time.Hour
	todayItemsPerFeed    = 3
	todayItemsTotalLimit = 20
)

const todayItemsSQL = `
WITH ranked_today_items AS (
	SELECT
		i.id,
		i.feed_id,
		COALESCE(f.custom_title, f.title) AS feed_title,
		i.title,
		i.link,
		i.summary,
		i.content,
		i.published_at,
		COALESCE(i.published_at, i.created_at) AS effective_at,
		ROW_NUMBER() OVER (
			PARTITION BY i.feed_id
			ORDER BY CAST(COALESCE(i.published_at, i.created_at) AS TEXT) DESC, i.id DESC
		) AS feed_rank
	FROM items AS i
	JOIN feeds AS f ON f.id = i.feed_id
	WHERE i.read_at IS NULL
	  AND COALESCE(i.published_at, i.created_at) >= ?
	  AND COALESCE(i.published_at, i.created_at) <= ?
), selected_today_items AS (
	SELECT
		id,
		feed_id,
		feed_title,
		title,
		link,
		summary,
		content,
		published_at,
		effective_at
	FROM ranked_today_items
	WHERE feed_rank <= ?
	ORDER BY CAST(effective_at AS TEXT) DESC, id DESC
	LIMIT ?
)
SELECT id, feed_id, feed_title, title, link, summary, content, published_at
FROM selected_today_items
ORDER BY CAST(effective_at AS TEXT) DESC, id DESC
`

// ListTodayItems returns the bounded unread batch from the all subscribed feeds.
func ListTodayItems(ctx context.Context, db *sql.DB, now time.Time) ([]view.ItemView, error) {
	ctx = contextOrBackground(ctx)
	now = now.UTC()

	rows, err := db.QueryContext(
		ctx,
		todayItemsSQL,
		now.Add(-todayWindow),
		now,
		todayItemsPerFeed,
		todayItemsTotalLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("query Today items: %w", err)
	}

	defer func() {
		closeErr := rows.Close()
		if closeErr != nil {
			slog.Warn("close Today item rows", "err", closeErr)
		}
	}()

	items := make([]view.ItemView, 0)

	for rows.Next() {
		item, scanErr := scanMobileStreamItemView(rows)
		if scanErr != nil {
			return nil, scanErr
		}

		items = append(items, item)
	}

	rowsErr := rows.Err()
	if rowsErr != nil {
		return nil, fmt.Errorf("iterate Today items: %w", rowsErr)
	}

	return items, nil
}
