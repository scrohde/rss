package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"rss/internal/view"
)

// UpsertFeed is part of the store package API.
func UpsertFeed(ctx context.Context, db *sql.DB, feedURL, title string) (int64, error) {
	ctx = contextOrBackground(ctx)

	now := time.Now().UTC()

	_, err := db.ExecContext(ctx, `
INSERT INTO feeds (url, title, sort_order, created_at)
VALUES (?, ?, COALESCE((SELECT MAX(sort_order) + 1 FROM feeds), 1), ?)
ON CONFLICT(url) DO UPDATE SET title = excluded.title
`, feedURL, title, now)
	if err != nil {
		return 0, fmt.Errorf("upsert feed row: %w", err)
	}

	var id int64

	err = db.QueryRowContext(ctx, "SELECT id FROM feeds WHERE url = ?", feedURL).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("lookup feed id by URL: %w", err)
	}

	return id, nil
}

// UpdateFeedTitle is part of the store package API.
func UpdateFeedTitle(ctx context.Context, db *sql.DB, feedID int64, title string) error {
	ctx = contextOrBackground(ctx)

	_, err := db.ExecContext(ctx, "UPDATE feeds SET custom_title = ? WHERE id = ?", nullString(title), feedID)
	if err != nil {
		return fmt.Errorf("update feed title: %w", err)
	}

	return nil
}

// DeleteFeed is part of the store package API.
func DeleteFeed(ctx context.Context, db *sql.DB, feedID int64) error {
	ctx = contextOrBackground(ctx)

	_, err := db.ExecContext(ctx, "DELETE FROM feeds WHERE id = ?", feedID)
	if err != nil {
		return fmt.Errorf("delete feed: %w", err)
	}

	return nil
}

// UpdateFeedOrder is part of the store package API.
func UpdateFeedOrder(ctx context.Context, db *sql.DB, orderedFeedIDs []int64) error {
	ctx = contextOrBackground(ctx)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update feed order transaction: %w", err)
	}

	committed := false

	defer func() {
		if !committed {
			rollbackTx(tx)
		}
	}()

	existingIDs, existing, orderErr := loadFeedOrderIDs(ctx, tx)
	if orderErr != nil {
		return orderErr
	}

	finalOrder := mergeFeedOrder(orderedFeedIDs, existingIDs, existing)

	applyErr := applyFeedOrder(ctx, tx, finalOrder)
	if applyErr != nil {
		return applyErr
	}

	commitErr := tx.Commit()
	if commitErr != nil {
		return fmt.Errorf("commit update feed order transaction: %w", commitErr)
	}

	committed = true

	return nil
}

//nolint:gocritic // Pair return keeps call sites simple and explicit.
func loadFeedOrderIDs(ctx context.Context, tx *sql.Tx) ([]int64, map[int64]struct{}, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id FROM feeds ORDER BY sort_order ASC, id ASC")
	if err != nil {
		return nil, nil, fmt.Errorf("query existing feed order IDs: %w", err)
	}

	defer func() {
		closeErr := rows.Close()
		if closeErr != nil {
			slog.Warn("rows close failed", "err", closeErr)
		}
	}()

	existingIDs := make([]int64, 0)
	existing := make(map[int64]struct{})

	for rows.Next() {
		var id int64

		scanErr := rows.Scan(&id)
		if scanErr != nil {
			return nil, nil, fmt.Errorf("scan feed order ID: %w", scanErr)
		}

		existingIDs = append(existingIDs, id)
		existing[id] = struct{}{}
	}

	rowsErr := rows.Err()
	if rowsErr != nil {
		return nil, nil, fmt.Errorf("iterate feed order rows: %w", rowsErr)
	}

	return existingIDs, existing, nil
}

func mergeFeedOrder(orderedFeedIDs, existingIDs []int64, existing map[int64]struct{}) []int64 {
	finalOrder, seen := mergeRequestedFeedOrder(orderedFeedIDs, existingIDs, existing)

	return appendMissingFeedOrder(finalOrder, seen, existingIDs)
}

//nolint:gocritic // Returning order and seen-set avoids recomputing in caller.
func mergeRequestedFeedOrder(
	orderedFeedIDs []int64,
	existingIDs []int64,
	existing map[int64]struct{},
) ([]int64, map[int64]struct{}) {
	seen := make(map[int64]struct{})
	finalOrder := make([]int64, 0, len(existingIDs))

	for _, id := range orderedFeedIDs {
		if !shouldIncludeFeedInOrder(id, existing, seen) {
			continue
		}

		seen[id] = struct{}{}
		finalOrder = append(finalOrder, id)
	}

	return finalOrder, seen
}

func appendMissingFeedOrder(finalOrder []int64, seen map[int64]struct{}, existingIDs []int64) []int64 {
	for _, id := range existingIDs {
		if _, ok := seen[id]; ok {
			continue
		}

		finalOrder = append(finalOrder, id)
	}

	return finalOrder
}

func shouldIncludeFeedInOrder(id int64, existing, seen map[int64]struct{}) bool {
	if id <= 0 {
		return false
	}

	if _, ok := existing[id]; !ok {
		return false
	}

	if _, dup := seen[id]; dup {
		return false
	}

	return true
}

func applyFeedOrder(ctx context.Context, tx *sql.Tx, finalOrder []int64) error {
	stmt, err := tx.PrepareContext(ctx, "UPDATE feeds SET sort_order = ? WHERE id = ?")
	if err != nil {
		return fmt.Errorf("prepare feed order update statement: %w", err)
	}

	defer func() {
		closeErr := stmt.Close()
		if closeErr != nil {
			slog.Warn("stmt close failed", "err", closeErr)
		}
	}()

	for idx, id := range finalOrder {
		_, execErr := stmt.ExecContext(ctx, idx+1, id)
		if execErr != nil {
			return fmt.Errorf("execute feed order update statement: %w", execErr)
		}
	}

	return nil
}

// ListFeeds is part of the store package API.
func ListFeeds(ctx context.Context, db *sql.DB) ([]view.FeedView, error) {
	ctx = contextOrBackground(ctx)

	rows, err := db.QueryContext(ctx, `
SELECT f.id, f.sort_order, COALESCE(f.custom_title, f.title) AS display_title, f.title, f.url,
       (SELECT COUNT(*) FROM items i WHERE i.feed_id = f.id) AS item_count,
       (SELECT COUNT(*) FROM items i WHERE i.feed_id = f.id AND i.read_at IS NULL) AS unread_count,
       f.last_refreshed_at,
       f.last_error
FROM feeds f
ORDER BY f.sort_order ASC, display_title COLLATE NOCASE, f.id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query feeds: %w", err)
	}

	defer func() {
		closeErr := rows.Close()
		if closeErr != nil {
			slog.Warn("rows close failed", "err", closeErr)
		}
	}()

	var feeds []view.FeedView

	for rows.Next() {
		nextFeed, scanErr := scanFeedView(rows)
		if scanErr != nil {
			return nil, scanErr
		}

		feeds = append(feeds, nextFeed)
	}

	rowsErr := rows.Err()
	if rowsErr != nil {
		return nil, fmt.Errorf("iterate feed rows: %w", rowsErr)
	}

	slog.Info("db list feeds", "count", len(feeds))

	return feeds, nil
}

// SelectRemainingFeed is part of the store package API.
func SelectRemainingFeed(selectedID, deletedID int64, feeds []view.FeedView) int64 {
	if len(feeds) == 0 {
		return 0
	}

	if shouldKeepSelectedFeed(selectedID, deletedID, feeds) {
		return selectedID
	}

	return feeds[0].ID
}

func shouldKeepSelectedFeed(selectedID, deletedID int64, feeds []view.FeedView) bool {
	if selectedID == 0 || selectedID == deletedID {
		return false
	}

	return containsFeedID(feeds, selectedID)
}

func containsFeedID(feeds []view.FeedView, targetID int64) bool {
	for _, feed := range feeds {
		if feed.ID == targetID {
			return true
		}
	}

	return false
}

// GetFeed is part of the store package API.
func GetFeed(
	ctx context.Context,
	db *sql.DB,
	feedID int64,
) (view.FeedView, error) {
	ctx = contextOrBackground(ctx)

	row := db.QueryRowContext(ctx, `
SELECT f.id, COALESCE(f.custom_title, f.title) AS display_title, f.title, f.url,
       f.sort_order,
       (SELECT COUNT(*) FROM items i WHERE i.feed_id = f.id) AS item_count,
       (SELECT COUNT(*) FROM items i WHERE i.feed_id = f.id AND i.read_at IS NULL) AS unread_count,
       f.last_refreshed_at,
       f.last_error
FROM feeds f
WHERE f.id = ?
`, feedID)

	var (
		id            int64
		sortOrder     int
		title         string
		originalTitle string
		url           string
		itemCount     int
		unreadCount   int
		lastChecked   sql.NullTime
		lastError     sql.NullString
	)

	err := row.Scan(
		&id,
		&title,
		&originalTitle,
		&url,
		&sortOrder,
		&itemCount,
		&unreadCount,
		&lastChecked,
		&lastError,
	)
	if err != nil {
		return view.FeedView{}, fmt.Errorf("scan feed %d: %w", feedID, err)
	}

	slog.Info("db get feed", "feed_id", feedID)

	return view.BuildFeedView(
		id,
		sortOrder,
		title,
		originalTitle,
		url,
		itemCount,
		unreadCount,
		view.FeedStatus{
			LastChecked: lastChecked,
			LastError:   lastError,
		},
	), nil
}

// GetFeedURL is part of the store package API.
func GetFeedURL(
	ctx context.Context,
	db *sql.DB,
	feedID int64,
) (string, error) {
	var u string

	err := db.QueryRowContext(ctx, "SELECT url FROM feeds WHERE id = ?", feedID).Scan(&u)
	if err != nil {
		return "", fmt.Errorf("lookup feed URL for %d: %w", feedID, err)
	}

	return u, nil
}

// ListDueFeeds is part of the store package API.
func ListDueFeeds(db *sql.DB, now time.Time, limit int) ([]int64, error) {
	rows, err := db.QueryContext(context.Background(), `
	SELECT id
	FROM feeds
	WHERE next_refresh_at IS NULL OR next_refresh_at <= ?
	ORDER BY COALESCE(next_refresh_at, created_at)
	LIMIT ?
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("query due feeds: %w", err)
	}

	defer func() {
		closeErr := rows.Close()
		if closeErr != nil {
			slog.Warn("rows close failed", "err", closeErr)
		}
	}()

	var ids []int64

	for rows.Next() {
		var id int64

		scanErr := rows.Scan(&id)
		if scanErr != nil {
			return nil, fmt.Errorf("scan due feed ID: %w", scanErr)
		}

		ids = append(ids, id)
	}

	rowsErr := rows.Err()
	if rowsErr != nil {
		return nil, fmt.Errorf("iterate due feed rows: %w", rowsErr)
	}

	return ids, nil
}

// ListPulseFeedIDs returns pulse-eligible feed IDs in display order.
// Feeds refreshed after cutoff are excluded.
func ListPulseFeedIDs(ctx context.Context, db *sql.DB, cutoff time.Time) ([]int64, error) {
	ctx = contextOrBackground(ctx)

	rows, err := db.QueryContext(ctx, `
	SELECT id
	FROM feeds
	WHERE last_refreshed_at IS NULL OR last_refreshed_at <= ?
	ORDER BY sort_order ASC, id ASC
	`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query pulse feed IDs: %w", err)
	}

	defer func() {
		closeErr := rows.Close()
		if closeErr != nil {
			slog.Warn("rows close failed", "err", closeErr)
		}
	}()

	var ids []int64

	for rows.Next() {
		var id int64

		scanErr := rows.Scan(&id)
		if scanErr != nil {
			return nil, fmt.Errorf("scan pulse feed ID: %w", scanErr)
		}

		ids = append(ids, id)
	}

	rowsErr := rows.Err()
	if rowsErr != nil {
		return nil, fmt.Errorf("iterate pulse feed ID rows: %w", rowsErr)
	}

	return ids, nil
}
