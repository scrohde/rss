package store

import (
	"context"
	"database/sql"
	"fmt"
)

const initSchemaSQL = `
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

DROP TRIGGER IF EXISTS tombstones_prune;
`

// Init is part of the store package API.
func Init(db *sql.DB) error {
	_, err := db.ExecContext(context.Background(), initSchemaSQL)
	if err != nil {
		return fmt.Errorf("initialize schema: %w", err)
	}

	err = ensureFeedOrderColumn(db)
	if err != nil {
		return err
	}

	err = ensureMobileAggregateIndexes(db)
	if err != nil {
		return err
	}

	err = ensureAuthSchema(db)
	if err != nil {
		return err
	}

	return nil
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

func ensureMobileAggregateIndexes(db *sql.DB) error {
	_, err := db.ExecContext(context.Background(), `
CREATE INDEX IF NOT EXISTS idx_feeds_sort_order_id
ON feeds(sort_order, id);

CREATE INDEX IF NOT EXISTS idx_items_unread_feed_order
ON items(
	feed_id,
	CAST(COALESCE(published_at, created_at) AS TEXT) DESC,
	id DESC
)
WHERE read_at IS NULL;
	`)
	if err != nil {
		return fmt.Errorf("create mobile aggregate indexes: %w", err)
	}

	return nil
}
