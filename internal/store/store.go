package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"rss/internal/view"

	_ "modernc.org/sqlite"
)

const (
	maxItemsPerFeed = 200
	readRetention   = 30 * time.Minute
)

func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite behaves best with a single connection for this workload.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, err
	}
	return db, nil
}

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
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	if err := ensureFeedOrderColumn(db); err != nil {
		return err
	}
	return nil
}

func UpsertFeed(db *sql.DB, feedURL, title string) (int64, error) {
	now := time.Now().UTC()
	_, err := db.Exec(`
INSERT INTO feeds (url, title, sort_order, created_at)
VALUES (?, ?, COALESCE((SELECT MAX(sort_order) + 1 FROM feeds), 1), ?)
ON CONFLICT(url) DO UPDATE SET title = excluded.title
`, feedURL, title, now)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := db.QueryRow("SELECT id FROM feeds WHERE url = ?", feedURL).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func UpdateFeedTitle(db *sql.DB, feedID int64, title string) error {
	_, err := db.Exec("UPDATE feeds SET custom_title = ? WHERE id = ?", nullString(title), feedID)
	return err
}

func DeleteFeed(db *sql.DB, feedID int64) error {
	_, err := db.Exec("DELETE FROM feeds WHERE id = ?", feedID)
	return err
}

func UpdateFeedOrder(db *sql.DB, orderedFeedIDs []int64) (err error) {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	rows, err := tx.Query("SELECT id FROM feeds ORDER BY sort_order ASC, id ASC")
	if err != nil {
		return err
	}
	existingIDs := make([]int64, 0)
	existing := make(map[int64]struct{})
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		existingIDs = append(existingIDs, id)
		existing[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	seen := make(map[int64]struct{})
	finalOrder := make([]int64, 0, len(existingIDs))
	for _, id := range orderedFeedIDs {
		if id <= 0 {
			continue
		}
		if _, ok := existing[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		finalOrder = append(finalOrder, id)
	}
	for _, id := range existingIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		finalOrder = append(finalOrder, id)
	}

	stmt, err := tx.Prepare("UPDATE feeds SET sort_order = ? WHERE id = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for idx, id := range finalOrder {
		if _, err := stmt.Exec(idx+1, id); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func UpsertItems(db *sql.DB, feedID int64, items []*gofeed.Item) (int, error) {
	now := time.Now().UTC()
	stmt, err := db.Prepare(`
INSERT OR IGNORE INTO items
(feed_id, guid, title, link, summary, content, published_at, created_at)
SELECT ?, ?, ?, ?, ?, ?, ?, ?
WHERE NOT EXISTS (
	SELECT 1 FROM tombstones WHERE feed_id = ? AND guid = ?
)
`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	inserted := 0
	for idx, item := range items {
		guid := strings.TrimSpace(item.GUID)
		if guid == "" {
			guid = strings.TrimSpace(item.Link)
		}
		if guid == "" {
			guid = strings.TrimSpace(item.Title)
		}
		if guid == "" && item.PublishedParsed != nil {
			guid = item.PublishedParsed.UTC().Format(time.RFC3339Nano)
		}
		if guid == "" {
			guid = fmt.Sprintf("feed-%d-item-%d", feedID, idx)
		}

		publishedAt := sql.NullTime{}
		if item.PublishedParsed != nil {
			publishedAt = sql.NullTime{Time: item.PublishedParsed.UTC(), Valid: true}
		} else if item.UpdatedParsed != nil {
			publishedAt = sql.NullTime{Time: item.UpdatedParsed.UTC(), Valid: true}
		}

		summary := strings.TrimSpace(item.Description)
		content := strings.TrimSpace(item.Content)
		res, err := stmt.Exec(
			feedID,
			guid,
			fallbackString(item.Title, "(untitled)"),
			fallbackString(item.Link, "#"),
			summary,
			content,
			nullTimeToValue(publishedAt),
			now,
			feedID,
			guid,
		)
		if err != nil {
			return inserted, err
		}
		if affected, err := res.RowsAffected(); err == nil && affected > 0 {
			inserted += int(affected)
		}
	}

	return inserted, nil
}

func EnforceItemLimit(db *sql.DB, feedID int64) error {
	now := time.Now().UTC()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(`
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
`, now, feedID, feedID, maxItemsPerFeed); err != nil {
		return err
	}
	if _, err = tx.Exec(`
DELETE FROM items
WHERE feed_id = ?
  AND id NOT IN (
	SELECT id FROM items
	WHERE feed_id = ?
	ORDER BY COALESCE(published_at, created_at) DESC, id DESC
	LIMIT ?
  )
`, feedID, feedID, maxItemsPerFeed); err != nil {
		return err
	}
	return tx.Commit()
}

func ListFeeds(db *sql.DB) ([]view.FeedView, error) {
	rows, err := db.Query(`
SELECT f.id, COALESCE(f.custom_title, f.title) AS display_title, f.title, f.url,
       (SELECT COUNT(*) FROM items i WHERE i.feed_id = f.id) AS item_count,
       (SELECT COUNT(*) FROM items i WHERE i.feed_id = f.id AND i.read_at IS NULL) AS unread_count,
       f.last_refreshed_at,
       f.last_error
FROM feeds f
ORDER BY f.sort_order ASC, display_title COLLATE NOCASE, f.id ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var feeds []view.FeedView
	for rows.Next() {
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
		if err := rows.Scan(&id, &title, &originalTitle, &url, &itemCount, &unreadCount, &lastChecked, &lastError); err != nil {
			return nil, err
		}
		feeds = append(feeds, view.BuildFeedView(id, title, originalTitle, url, itemCount, unreadCount, lastChecked, lastError))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slog.Info("db list feeds", "count", len(feeds))
	return feeds, nil
}

func SelectRemainingFeed(selectedID, deletedID int64, feeds []view.FeedView) int64 {
	if len(feeds) == 0 {
		return 0
	}
	if selectedID != 0 && selectedID != deletedID {
		for _, feed := range feeds {
			if feed.ID == selectedID {
				return selectedID
			}
		}
	}
	return feeds[0].ID
}

func LoadItemList(db *sql.DB, feedID int64) (*view.ItemListData, error) {
	feed, err := GetFeed(db, feedID)
	if err != nil {
		return nil, err
	}
	items, err := ListItems(db, feedID)
	if err != nil {
		return nil, err
	}
	newestID := maxItemID(items)
	return &view.ItemListData{
		Feed:     feed,
		Items:    items,
		NewestID: newestID,
		NewItems: view.NewItemsData{FeedID: feed.ID, Count: 0},
	}, nil
}

func GetFeed(db *sql.DB, feedID int64) (view.FeedView, error) {
	row := db.QueryRow(`
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
	if err := row.Scan(&id, &title, &originalTitle, &url, &itemCount, &unreadCount, &lastChecked, &lastError); err != nil {
		return view.FeedView{}, err
	}
	slog.Info("db get feed", "feed_id", feedID)
	return view.BuildFeedView(id, title, originalTitle, url, itemCount, unreadCount, lastChecked, lastError), nil
}

func GetFeedURL(db *sql.DB, feedID int64) (string, error) {
	var u string
	if err := db.QueryRow("SELECT url FROM feeds WHERE id = ?", feedID).Scan(&u); err != nil {
		return "", err
	}
	return u, nil
}

func ListDueFeeds(db *sql.DB, now time.Time, limit int) ([]int64, error) {
	rows, err := db.Query(`
	SELECT id
	FROM feeds
	WHERE next_refresh_at IS NULL OR next_refresh_at <= ?
	ORDER BY COALESCE(next_refresh_at, created_at)
	LIMIT ?
	`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func ListItems(db *sql.DB, feedID int64) ([]view.ItemView, error) {
	rows, err := db.Query(`
SELECT id, title, link, summary, content, published_at, read_at
FROM items
WHERE feed_id = ?
ORDER BY COALESCE(published_at, created_at) DESC, id DESC
`, feedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []view.ItemView
	for rows.Next() {
		item, err := scanItemView(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slog.Info("db list items", "feed_id", feedID, "count", len(items))
	return items, nil
}

func ListItemsAfter(db *sql.DB, feedID, afterID int64) ([]view.ItemView, error) {
	rows, err := db.Query(`
SELECT id, title, link, summary, content, published_at, read_at
FROM items
WHERE feed_id = ? AND id > ?
ORDER BY COALESCE(published_at, created_at) DESC, id DESC
`, feedID, afterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []view.ItemView
	for rows.Next() {
		item, err := scanItemView(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slog.Info("db list items after", "feed_id", feedID, "after_id", afterID, "count", len(items))
	return items, nil
}

func CountItemsAfter(db *sql.DB, feedID, afterID int64) (int, error) {
	var count int
	if err := db.QueryRow(`
SELECT COUNT(*)
FROM items
WHERE feed_id = ? AND id > ?
`, feedID, afterID).Scan(&count); err != nil {
		return 0, err
	}
	slog.Info("db count items after", "feed_id", feedID, "after_id", afterID, "count", count)
	return count, nil
}

func GetItem(db *sql.DB, itemID int64) (view.ItemView, error) {
	row := db.QueryRow(`
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
	if err := row.Scan(&id, &title, &link, &summary, &content, &published, &readAt); err != nil {
		return view.ItemView{}, err
	}
	slog.Info("db get item", "item_id", itemID)
	return view.BuildItemView(id, title, link, summary, content, published, readAt), nil
}

func GetFeedIDByItem(db *sql.DB, itemID int64) (int64, error) {
	var feedID int64
	if err := db.QueryRow("SELECT feed_id FROM items WHERE id = ?", itemID).Scan(&feedID); err != nil {
		return 0, err
	}
	return feedID, nil
}

func ToggleRead(db *sql.DB, itemID int64) error {
	var readAt sql.NullTime
	if err := db.QueryRow("SELECT read_at FROM items WHERE id = ?", itemID).Scan(&readAt); err != nil {
		return err
	}
	if readAt.Valid {
		_, err := db.Exec("UPDATE items SET read_at = NULL WHERE id = ?", itemID)
		return err
	}
	_, err := db.Exec("UPDATE items SET read_at = ? WHERE id = ?", time.Now().UTC(), itemID)
	return err
}

func MarkAllRead(db *sql.DB, feedID int64) error {
	_, err := db.Exec(`
UPDATE items
SET read_at = ?
WHERE feed_id = ? AND read_at IS NULL
`, time.Now().UTC(), feedID)
	return err
}

func SweepReadItems(db *sql.DB, feedID int64) (int64, error) {
	now := time.Now().UTC()
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(`
INSERT OR IGNORE INTO tombstones (feed_id, guid, deleted_at)
SELECT feed_id, guid, ?
FROM items
WHERE feed_id = ? AND read_at IS NOT NULL
`, now, feedID); err != nil {
		return 0, err
	}
	deleteResult, err := tx.Exec(`
DELETE FROM items
WHERE feed_id = ? AND read_at IS NOT NULL
`, feedID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	deleted, err := deleteResult.RowsAffected()
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

func CleanupReadItems(db *sql.DB) error {
	cutoff := time.Now().UTC().Add(-readRetention)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(`
INSERT OR IGNORE INTO tombstones (feed_id, guid, deleted_at)
SELECT feed_id, guid, ?
FROM items
WHERE read_at IS NOT NULL AND read_at <= ?
`, time.Now().UTC(), cutoff); err != nil {
		return err
	}
	deleteResult, err := tx.Exec("DELETE FROM items WHERE read_at IS NOT NULL AND read_at <= ?", cutoff)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if deleted, err := deleteResult.RowsAffected(); err == nil && deleted > 0 {
		slog.Info("cleanup read items", "deleted", deleted)
	}
	return nil
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
	if err := rows.Scan(&id, &title, &link, &summary, &content, &published, &readAt); err != nil {
		return view.ItemView{}, err
	}
	return view.BuildItemView(id, title, link, summary, content, published, readAt), nil
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
	if err := db.QueryRow(`
SELECT COUNT(*)
FROM pragma_table_info('feeds')
WHERE name = 'sort_order'
`).Scan(&hasSortOrder); err != nil {
		return err
	}

	if hasSortOrder == 0 {
		if _, err := db.Exec("ALTER TABLE feeds ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}

	_, err := db.Exec(`
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
	return err
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
