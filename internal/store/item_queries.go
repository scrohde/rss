package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"rss/internal/view"
)

const unreadItemsDefaultCap = 200

// LoadItemList is part of the store package API.
func LoadItemList(
	ctx context.Context,
	db *sql.DB,
	feedID int64,
) (*view.ItemListData, error) {
	ctx = contextOrBackground(ctx)

	feed, err := GetFeed(ctx, db, feedID)
	if err != nil {
		return nil, err
	}

	items, err := ListItems(ctx, db, feedID)
	if err != nil {
		return nil, err
	}

	newestID := maxItemID(items)

	return &view.ItemListData{
		MarkAllReadUndoToken: "",
		Feed:                 feed,
		Items:                items,
		NewestID:             newestID,
		NewItems:             view.NewItemsData{FeedID: feed.ID, Count: 0, SwapOOB: false},
		Continuation:         view.BuildFeedContinuation(feed.ID, nil),
	}, nil
}

// ListItems is part of the store package API.
func ListItems(
	ctx context.Context,
	db *sql.DB,
	feedID int64,
) ([]view.ItemView, error) {
	ctx = contextOrBackground(ctx)

	rows, err := db.QueryContext(ctx, `
SELECT id, title, link, summary, content, published_at, read_at
FROM items
WHERE feed_id = ?
ORDER BY COALESCE(published_at, created_at) DESC, id DESC
	`, feedID)
	if err != nil {
		return nil, fmt.Errorf("query items for feed %d: %w", feedID, err)
	}

	defer func() {
		closeErr := rows.Close()
		if closeErr != nil {
			slog.Warn("rows close failed", "err", closeErr)
		}
	}()

	var items []view.ItemView

	for rows.Next() {
		item, scanErr := scanItemView(rows)
		if scanErr != nil {
			return nil, scanErr
		}

		items = append(items, item)
	}

	rowsErr := rows.Err()
	if rowsErr != nil {
		return nil, fmt.Errorf("iterate items for feed %d: %w", feedID, rowsErr)
	}

	slog.Info("db list items", "feed_id", feedID, "count", len(items))

	return items, nil
}

// ListItemsAfter is part of the store package API.
func ListItemsAfter(
	ctx context.Context,
	db *sql.DB,
	feedID, afterID int64,
) ([]view.ItemView, error) {
	ctx = contextOrBackground(ctx)

	rows, err := db.QueryContext(ctx, `
SELECT id, title, link, summary, content, published_at, read_at
FROM items
WHERE feed_id = ? AND id > ?
ORDER BY COALESCE(published_at, created_at) DESC, id DESC
	`, feedID, afterID)
	if err != nil {
		return nil, fmt.Errorf("query items for feed %d after %d: %w", feedID, afterID, err)
	}

	defer func() {
		closeErr := rows.Close()
		if closeErr != nil {
			slog.Warn("rows close failed", "err", closeErr)
		}
	}()

	var items []view.ItemView

	for rows.Next() {
		item, scanErr := scanItemView(rows)
		if scanErr != nil {
			return nil, scanErr
		}

		items = append(items, item)
	}

	rowsErr := rows.Err()
	if rowsErr != nil {
		return nil, fmt.Errorf("iterate items for feed %d after %d: %w", feedID, afterID, rowsErr)
	}

	slog.Info("db list items after", "feed_id", feedID, "after_id", afterID, "count", len(items))

	return items, nil
}

// ListUnreadItemsAllFeeds is part of the store package API.
func ListUnreadItemsAllFeeds(
	ctx context.Context,
	db *sql.DB,
	limit int,
) ([]view.ItemView, error) {
	ctx = contextOrBackground(ctx)
	resolvedLimit := resolveUnreadItemsLimit(limit)

	rows, err := unreadItemsAllFeedsRows(ctx, db, resolvedLimit)
	if err != nil {
		return nil, err
	}

	items, err := collectUnreadItemsAcrossFeeds(rows)
	if err != nil {
		return nil, err
	}

	return items, nil
}

// ListUnreadItemsByFeed is part of the store package API.
func ListUnreadItemsByFeed(
	ctx context.Context,
	db *sql.DB,
	feedID int64,
	limit int,
) ([]view.ItemView, error) {
	ctx = contextOrBackground(ctx)
	resolvedLimit := resolveUnreadItemsLimit(limit)

	rows, err := unreadItemsByFeedRows(ctx, db, feedID, resolvedLimit)
	if err != nil {
		return nil, err
	}

	items, err := collectUnreadItemsAcrossFeeds(rows)
	if err != nil {
		return nil, err
	}

	return items, nil
}

func resolveUnreadItemsLimit(limit int) int {
	if limit <= 0 {
		return unreadItemsDefaultCap
	}

	return limit
}

func unreadItemsAllFeedsRows(ctx context.Context, db *sql.DB, limit int) (*sql.Rows, error) {
	rows, err := db.QueryContext(ctx, `
SELECT i.id,
       i.feed_id,
       COALESCE(f.custom_title, f.title) AS feed_title,
       i.title,
       i.link,
       i.summary,
       i.content,
       i.published_at
FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE i.read_at IS NULL
ORDER BY COALESCE(i.published_at, i.created_at) DESC, i.id DESC
LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query unread items across feeds: %w", err)
	}

	return rows, nil
}

func unreadItemsByFeedRows(
	ctx context.Context,
	db *sql.DB,
	feedID int64,
	limit int,
) (*sql.Rows, error) {
	rows, err := db.QueryContext(ctx, `
SELECT i.id,
       i.feed_id,
       COALESCE(f.custom_title, f.title) AS feed_title,
       i.title,
       i.link,
       i.summary,
       i.content,
       i.published_at
FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE i.read_at IS NULL AND i.feed_id = ?
ORDER BY COALESCE(i.published_at, i.created_at) DESC, i.id DESC
LIMIT ?
	`, feedID, limit)
	if err != nil {
		return nil, fmt.Errorf("query unread items for feed %d: %w", feedID, err)
	}

	return rows, nil
}

func collectUnreadItemsAcrossFeeds(rows *sql.Rows) ([]view.ItemView, error) {
	defer func() {
		closeErr := rows.Close()
		if closeErr != nil {
			slog.Warn("rows close failed", "err", closeErr)
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
		return nil, fmt.Errorf("iterate unread items across feeds: %w", rowsErr)
	}

	return items, nil
}

// GetUnreadStreamItem is part of the store package API.
func GetUnreadStreamItem(
	ctx context.Context,
	db *sql.DB,
	itemID int64,
) (view.ItemView, error) {
	ctx = contextOrBackground(ctx)

	row := db.QueryRowContext(ctx, `
SELECT i.id,
       i.feed_id,
       COALESCE(f.custom_title, f.title) AS feed_title,
       i.title,
       i.link,
       i.summary,
       i.content,
       i.published_at
FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE i.id = ? AND i.read_at IS NULL
`, itemID)

	var (
		id        int64
		feedID    int64
		feedTitle string
		title     string
		link      string
		summary   sql.NullString
		content   sql.NullString
		published sql.NullTime
	)

	err := row.Scan(
		&id,
		&feedID,
		&feedTitle,
		&title,
		&link,
		&summary,
		&content,
		&published,
	)
	if err != nil {
		return view.ItemView{}, fmt.Errorf("scan unread stream item %d: %w", itemID, err)
	}

	item := view.BuildItemView(
		id,
		title,
		link,
		summary,
		content,
		published,
		sql.NullTime{Time: time.Time{}, Valid: false},
	)
	item.FeedID = feedID
	item.FeedTitle = feedTitle

	return item, nil
}

// CountItemsAfter is part of the store package API.
func CountItemsAfter(ctx context.Context, db *sql.DB, feedID, afterID int64) (int, error) {
	ctx = contextOrBackground(ctx)

	var count int

	err := db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM items
WHERE feed_id = ? AND id > ?
	`, feedID, afterID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count items for feed %d after %d: %w", feedID, afterID, err)
	}

	slog.Info("db count items after", "feed_id", feedID, "after_id", afterID, "count", count)

	return count, nil
}

// GetItem is part of the store package API.
func GetItem(ctx context.Context, db *sql.DB, itemID int64) (view.ItemView, error) {
	ctx = contextOrBackground(ctx)

	row := db.QueryRowContext(ctx, `
SELECT id, title, link, summary, content, published_at, read_at
FROM items
WHERE id = ?
`, itemID)

	var (
		id        int64
		title     string
		link      string
		summary   sql.NullString
		content   sql.NullString
		published sql.NullTime
		readAt    sql.NullTime
	)

	err := row.Scan(&id, &title, &link, &summary, &content, &published, &readAt)
	if err != nil {
		return view.ItemView{}, fmt.Errorf("scan item %d: %w", itemID, err)
	}

	slog.Info("db get item", "item_id", itemID)

	return view.BuildItemView(id, title, link, summary, content, published, readAt), nil
}

// GetFeedIDByItem is part of the store package API.
func GetFeedIDByItem(ctx context.Context, db *sql.DB, itemID int64) (int64, error) {
	ctx = contextOrBackground(ctx)

	var feedID int64

	err := db.QueryRowContext(ctx, "SELECT feed_id FROM items WHERE id = ?", itemID).Scan(&feedID)
	if err != nil {
		return 0, fmt.Errorf("lookup feed ID for item %d: %w", itemID, err)
	}

	return feedID, nil
}

func maxItemID(items []view.ItemView) int64 {
	var maxID int64
	for idx := range items {
		if items[idx].ID > maxID {
			maxID = items[idx].ID
		}
	}

	return maxID
}
