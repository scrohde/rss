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
)

const (
	maxItemsPerFeed = 200
	// MaxFeedItems is the maximum number of parsed feed items accepted for persistence.
	MaxFeedItems = 1000
)

// UpsertItems is part of the store package API.
func UpsertItems(ctx context.Context, db *sql.DB, feedID int64, items []*gofeed.Item) (int, error) {
	ctx = contextOrBackground(ctx)

	err := ValidateItems(items)
	if err != nil {
		return 0, err
	}

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

const (
	maxItemTitleBytes   = 16 << 10
	maxItemURLBytes     = 16 << 10
	maxItemGUIDBytes    = 16 << 10
	maxItemSummaryBytes = 1 << 20
	maxItemContentBytes = 4 << 20
)

var errInvalidFeedItems = errors.New("feed items exceed resource limits")

// ValidateItems rejects feed data that exceeds persistence resource limits.
func ValidateItems(items []*gofeed.Item) error {
	if len(items) > MaxFeedItems {
		return fmt.Errorf("%w: contains %d items; limit is %d", errInvalidFeedItems, len(items), MaxFeedItems)
	}

	for index, item := range items {
		err := validateItem(item, index)
		if err != nil {
			return err
		}
	}

	return nil
}

func validateItem(item *gofeed.Item, index int) error {
	if item == nil {
		return fmt.Errorf("%w: item %d is nil", errInvalidFeedItems, index)
	}

	fields := []struct {
		name  string
		value string
		limit int
	}{
		{"title", item.Title, maxItemTitleBytes},
		{"URL", item.Link, maxItemURLBytes},
		{"GUID", item.GUID, maxItemGUIDBytes},
		{"summary", item.Description, maxItemSummaryBytes},
		{"content", item.Content, maxItemContentBytes},
	}
	for _, field := range fields {
		if len(field.value) > field.limit {
			return fmt.Errorf(
				"%w: item %d %s exceeds %d-byte limit", errInvalidFeedItems, index, field.name, field.limit,
			)
		}
	}

	return nil
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
