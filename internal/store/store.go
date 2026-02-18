// Package store provides SQLite-backed persistence helpers for feeds and items.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	_ "modernc.org/sqlite" // Register the sqlite database/sql driver.

	"rss/internal/view"
)

const (
	maxItemsPerFeed = 200
	readRetention   = 30 * time.Minute
)

// Open is part of the store package API.
func Open(path string) (*sql.DB, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	// SQLite behaves best with a single connection for this workload.
	db.SetMaxOpenConns(1)

	_, err = db.ExecContext(context.Background(), "PRAGMA journal_mode=WAL;")
	if err != nil {
		return nil, fmt.Errorf("enable WAL mode: %w", err)
	}

	return db, nil
}

// Init is part of the store package API.
func Init(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS feeds (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	url TEXT NOT NULL UNIQUE,
	title TEXT NOT NULL,
	custom_title TEXT,
	sort_order INTEGER NOT NULL DEFAULT 0,
	created_at DATETIME NOT NULL,
	etag TEXT,
	last_modified TEXT,
	last_refreshed_at DATETIME,
	last_error TEXT,
	unchanged_count INTEGER NOT NULL DEFAULT 0,
	next_refresh_at DATETIME
);

CREATE TABLE IF NOT EXISTS items (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	feed_id INTEGER NOT NULL,
	guid TEXT NOT NULL,
	title TEXT NOT NULL,
	link TEXT NOT NULL,
	summary TEXT,
	content TEXT,
	published_at DATETIME,
	read_at DATETIME,
	created_at DATETIME NOT NULL,
	UNIQUE(feed_id, guid),
	FOREIGN KEY(feed_id) REFERENCES feeds(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS tombstones (
	feed_id INTEGER NOT NULL,
	guid TEXT NOT NULL,
	deleted_at DATETIME NOT NULL,
	PRIMARY KEY (feed_id, guid),
	FOREIGN KEY(feed_id) REFERENCES feeds(id) ON DELETE CASCADE
);

CREATE TRIGGER IF NOT EXISTS tombstones_prune
AFTER INSERT ON tombstones
BEGIN
	DELETE FROM tombstones
	WHERE datetime(deleted_at) <= datetime('now', '-30 days');
END;
`

	_, err := db.ExecContext(context.Background(), schema)
	if err != nil {
		return fmt.Errorf("initialize schema: %w", err)
	}

	err = ensureFeedOrderColumn(db)
	if err != nil {
		return err
	}

	return nil
}

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

// UpsertItems is part of the store package API.
func UpsertItems(ctx context.Context, db *sql.DB, feedID int64, items []*gofeed.Item) (int, error) {
	ctx = contextOrBackground(ctx)

	now := time.Now().UTC()

	stmt, err := db.PrepareContext(ctx, `
INSERT OR IGNORE INTO items
(feed_id, guid, title, link, summary, content, published_at, created_at)
SELECT ?, ?, ?, ?, ?, ?, ?, ?
WHERE NOT EXISTS (
	SELECT 1 FROM tombstones WHERE feed_id = ? AND guid = ?
)
	`)
	if err != nil {
		return 0, fmt.Errorf("prepare item upsert statement: %w", err)
	}

	defer func() {
		closeErr := stmt.Close()
		if closeErr != nil {
			slog.Warn("stmt close failed", "err", closeErr)
		}
	}()

	inserted := 0

	for idx, item := range items {
		added, execErr := upsertItemWithStmt(ctx, stmt, feedID, idx, item, now)
		if execErr != nil {
			return inserted, execErr
		}

		inserted += added
	}

	return inserted, nil
}

func upsertItemWithStmt(
	ctx context.Context,
	stmt *sql.Stmt,
	feedID int64,
	idx int,
	item *gofeed.Item,
	now time.Time,
) (int, error) {
	guid := deriveItemGUID(feedID, idx, item)
	publishedAt := deriveItemPublishedAt(item)

	res, execErr := stmt.ExecContext(ctx,
		feedID,
		guid,
		fallbackString(item.Title, "(untitled)"),
		fallbackString(item.Link, "#"),
		strings.TrimSpace(item.Description),
		strings.TrimSpace(item.Content),
		nullTimeToValue(publishedAt),
		now,
		feedID,
		guid,
	)
	if execErr != nil {
		return 0, fmt.Errorf("execute item upsert statement: %w", execErr)
	}

	affected, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return 0, fmt.Errorf("count upserted item rows: %w", rowsErr)
	}

	if affected <= 0 {
		return 0, nil
	}

	return int(affected), nil
}

func deriveItemGUID(feedID int64, idx int, item *gofeed.Item) string {
	candidates := []string{
		strings.TrimSpace(item.GUID),
		strings.TrimSpace(item.Link),
		strings.TrimSpace(item.Title),
	}
	for _, guid := range candidates {
		if guid != "" {
			return guid
		}
	}

	if item.PublishedParsed != nil {
		return item.PublishedParsed.UTC().Format(time.RFC3339Nano)
	}

	return fmt.Sprintf("feed-%d-item-%d", feedID, idx)
}

func deriveItemPublishedAt(item *gofeed.Item) sql.NullTime {
	switch {
	case item.PublishedParsed != nil:
		return sql.NullTime{Time: item.PublishedParsed.UTC(), Valid: true}
	case item.UpdatedParsed != nil:
		return sql.NullTime{Time: item.UpdatedParsed.UTC(), Valid: true}
	default:
		return sql.NullTime{
			Time:  time.Time{},
			Valid: false,
		}
	}
}

// EnforceItemLimit is part of the store package API.
func EnforceItemLimit(
	ctx context.Context,
	db *sql.DB,
	feedID int64,
) error {
	ctx = contextOrBackground(ctx)

	now := time.Now().UTC()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin enforce item limit transaction: %w", err)
	}

	defer func() {
		if err != nil {
			rollbackTx(tx)
		}
	}()

	_, err = tx.ExecContext(ctx, `
INSERT OR IGNORE INTO tombstones (feed_id, guid, deleted_at)
SELECT feed_id, guid, ?
FROM items
WHERE feed_id = ?
  AND id NOT IN (
	SELECT id FROM items
	WHERE feed_id = ?
	ORDER BY COALESCE(published_at, created_at) DESC, id DESC
	LIMIT ?
  )
	`, now, feedID, feedID, maxItemsPerFeed)
	if err != nil {
		return fmt.Errorf("insert tombstones for pruned items: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
DELETE FROM items
WHERE feed_id = ?
  AND id NOT IN (
	SELECT id FROM items
	WHERE feed_id = ?
	ORDER BY COALESCE(published_at, created_at) DESC, id DESC
	LIMIT ?
  )
	`, feedID, feedID, maxItemsPerFeed)
	if err != nil {
		return fmt.Errorf("delete items beyond item limit: %w", err)
	}

	commitErr := tx.Commit()
	if commitErr != nil {
		return fmt.Errorf("commit enforce item limit transaction: %w", commitErr)
	}

	return nil
}

// ListFeeds is part of the store package API.
func ListFeeds(ctx context.Context, db *sql.DB) ([]view.FeedView, error) {
	ctx = contextOrBackground(ctx)

	rows, err := db.QueryContext(ctx, `
SELECT f.id, COALESCE(f.custom_title, f.title) AS display_title, f.title, f.url,
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
		Feed:     feed,
		Items:    items,
		NewestID: newestID,
		NewItems: view.NewItemsData{FeedID: feed.ID, Count: 0, SwapOOB: false},
	}, nil
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
       (SELECT COUNT(*) FROM items i WHERE i.feed_id = f.id) AS item_count,
       (SELECT COUNT(*) FROM items i WHERE i.feed_id = f.id AND i.read_at IS NULL) AS unread_count,
       f.last_refreshed_at,
       f.last_error
FROM feeds f
WHERE f.id = ?
`, feedID)

	var (
		id            int64
		title         string
		originalTitle string
		url           string
		itemCount     int
		unreadCount   int
		lastChecked   sql.NullTime
		lastError     sql.NullString
	)

	err := row.Scan(&id, &title, &originalTitle, &url, &itemCount, &unreadCount, &lastChecked, &lastError)
	if err != nil {
		return view.FeedView{}, fmt.Errorf("scan feed %d: %w", feedID, err)
	}

	slog.Info("db get feed", "feed_id", feedID)

	return view.BuildFeedView(id, title, originalTitle, url, itemCount, unreadCount, lastChecked, lastError), nil
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

// ToggleRead is part of the store package API.
func ToggleRead(ctx context.Context, db *sql.DB, itemID int64) error {
	ctx = contextOrBackground(ctx)

	var readAt sql.NullTime

	err := db.QueryRowContext(ctx, "SELECT read_at FROM items WHERE id = ?", itemID).Scan(&readAt)
	if err != nil {
		return fmt.Errorf("lookup read state for item %d: %w", itemID, err)
	}

	if readAt.Valid {
		_, err = db.ExecContext(ctx, "UPDATE items SET read_at = NULL WHERE id = ?", itemID)
		if err != nil {
			return fmt.Errorf("mark item %d unread: %w", itemID, err)
		}

		return nil
	}

	_, err = db.ExecContext(ctx, "UPDATE items SET read_at = ? WHERE id = ?", time.Now().UTC(), itemID)
	if err != nil {
		return fmt.Errorf("mark item %d read: %w", itemID, err)
	}

	return nil
}

// MarkAllRead is part of the store package API.
func MarkAllRead(ctx context.Context, db *sql.DB, feedID int64) error {
	ctx = contextOrBackground(ctx)

	_, err := db.ExecContext(ctx, `
UPDATE items
SET read_at = ?
WHERE feed_id = ? AND read_at IS NULL
	`, time.Now().UTC(), feedID)
	if err != nil {
		return fmt.Errorf("mark all items read for feed %d: %w", feedID, err)
	}

	return nil
}

// SweepReadItems is part of the store package API.
func SweepReadItems(ctx context.Context, db *sql.DB, feedID int64) (int64, error) {
	ctx = contextOrBackground(ctx)

	now := time.Now().UTC()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin sweep read items transaction: %w", err)
	}

	defer func() {
		if err != nil {
			rollbackTx(tx)
		}
	}()

	_, err = tx.ExecContext(ctx, `
INSERT OR IGNORE INTO tombstones (feed_id, guid, deleted_at)
SELECT feed_id, guid, ?
FROM items
WHERE feed_id = ? AND read_at IS NOT NULL
	`, now, feedID)
	if err != nil {
		return 0, fmt.Errorf("insert sweep tombstones for feed %d: %w", feedID, err)
	}

	deleteResult, err := tx.ExecContext(ctx, `
DELETE FROM items
WHERE feed_id = ? AND read_at IS NOT NULL
	`, feedID)
	if err != nil {
		return 0, fmt.Errorf("delete read items for feed %d: %w", feedID, err)
	}

	commitErr := tx.Commit()
	if commitErr != nil {
		return 0, fmt.Errorf("commit sweep read items transaction: %w", commitErr)
	}

	deleted, err := deleteResult.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted read items for feed %d: %w", feedID, err)
	}

	return deleted, nil
}

// CleanupReadItems is part of the store package API.
func CleanupReadItems(db *sql.DB) error {
	cutoff := time.Now().UTC().Add(-readRetention)

	deleted, err := cleanupReadItemsBefore(context.Background(), db, cutoff)
	if err != nil {
		return err
	}

	logCleanupReadItemsDeleted(deleted)

	return nil
}

func cleanupReadItemsBefore(ctx context.Context, db *sql.DB, cutoff time.Time) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin cleanup read items transaction: %w", err)
	}

	deleteResult, err := cleanupReadItemsInTx(ctx, tx, cutoff)
	if err != nil {
		rollbackTx(tx)

		return 0, err
	}

	commitErr := tx.Commit()
	if commitErr != nil {
		return 0, fmt.Errorf("commit cleanup read items transaction: %w", commitErr)
	}

	deleted, rowsErr := deleteResult.RowsAffected()
	if rowsErr != nil {
		return 0, fmt.Errorf("count cleaned read items: %w", rowsErr)
	}

	return deleted, nil
}

func cleanupReadItemsInTx(ctx context.Context, tx *sql.Tx, cutoff time.Time) (sql.Result, error) {
	_, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO tombstones (feed_id, guid, deleted_at)
SELECT feed_id, guid, ?
FROM items
WHERE read_at IS NOT NULL AND read_at <= ?
	`, time.Now().UTC(), cutoff)
	if err != nil {
		return nil, fmt.Errorf("insert cleanup tombstones: %w", err)
	}

	deleteResult, err := tx.ExecContext(
		ctx,
		"DELETE FROM items WHERE read_at IS NOT NULL AND read_at <= ?",
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("delete stale read items: %w", err)
	}

	return deleteResult, nil
}

func logCleanupReadItemsDeleted(deleted int64) {
	if deleted <= 0 {
		return
	}

	slog.Info("cleanup read items", "deleted", deleted)
}

func scanItemView(rows *sql.Rows) (view.ItemView, error) {
	var (
		id        int64
		title     string
		link      string
		summary   sql.NullString
		content   sql.NullString
		published sql.NullTime
		readAt    sql.NullTime
	)

	err := rows.Scan(&id, &title, &link, &summary, &content, &published, &readAt)
	if err != nil {
		return view.ItemView{}, fmt.Errorf("scan item row: %w", err)
	}

	return view.BuildItemView(id, title, link, summary, content, published, readAt), nil
}

func scanFeedView(rows *sql.Rows) (view.FeedView, error) {
	var (
		id            int64
		title         string
		originalTitle string
		url           string
		itemCount     int
		unreadCount   int
		lastChecked   sql.NullTime
		lastError     sql.NullString
	)

	err := rows.Scan(&id, &title, &originalTitle, &url, &itemCount, &unreadCount, &lastChecked, &lastError)
	if err != nil {
		return view.FeedView{}, fmt.Errorf("scan feed row: %w", err)
	}

	return view.BuildFeedView(
		id,
		title,
		originalTitle,
		url,
		itemCount,
		unreadCount,
		lastChecked,
		lastError,
	), nil
}

func maxItemID(items []view.ItemView) int64 {
	var maxID int64
	for _, item := range items {
		if item.ID > maxID {
			maxID = item.ID
		}
	}

	return maxID
}

func ensureFeedOrderColumn(db *sql.DB) error {
	var hasSortOrder int

	err := db.QueryRowContext(context.Background(), `
SELECT COUNT(*)
FROM pragma_table_info('feeds')
WHERE name = 'sort_order'
	`).Scan(&hasSortOrder)
	if err != nil {
		return fmt.Errorf("check feeds.sort_order column: %w", err)
	}

	if hasSortOrder == 0 {
		_, execErr := db.ExecContext(
			context.Background(),
			"ALTER TABLE feeds ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0",
		)
		if execErr != nil {
			return fmt.Errorf("add feeds.sort_order column: %w", execErr)
		}
	}

	_, err = db.ExecContext(context.Background(), `
WITH ranked AS (
	SELECT
		id,
		ROW_NUMBER() OVER (ORDER BY COALESCE(custom_title, title) COLLATE NOCASE, id) AS sort_position
	FROM feeds
)
UPDATE feeds
SET sort_order = (
	SELECT sort_position
	FROM ranked
	WHERE ranked.id = feeds.id
	)
	WHERE sort_order <= 0
	`)
	if err != nil {
		return fmt.Errorf("backfill feeds.sort_order values: %w", err)
	}

	return nil
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}

	return ctx
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}

	return value
}

func nullTimeToValue(value sql.NullTime) any {
	if value.Valid {
		return value.Time
	}

	return nil
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	return value
}

func rollbackTx(tx *sql.Tx) {
	err := tx.Rollback()
	if err != nil && !errors.Is(err, sql.ErrTxDone) {
		slog.Warn("tx rollback failed", "err", err)
	}
}
