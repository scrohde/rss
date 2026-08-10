package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

const readRetention = 30 * time.Minute

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

// CleanupTombstones keeps each feed's newest tombstones up to the feed item input cap.
func CleanupTombstones(db *sql.DB) error {
	deleted, err := cleanupTombstonesBeyondLimit(context.Background(), db)
	if err != nil {
		return err
	}

	if deleted > 0 {
		slog.Info("cleanup tombstones beyond fixed per-feed limit", "deleted", deleted)
	}

	return nil
}

func cleanupTombstonesBeyondLimit(ctx context.Context, db *sql.DB) (int64, error) {
	result, err := db.ExecContext(ctx, `
WITH ranked AS (
	SELECT
		feed_id,
		guid,
		ROW_NUMBER() OVER (
			PARTITION BY feed_id
			ORDER BY deleted_at DESC, guid DESC
		) AS tombstone_rank
	FROM tombstones
)
DELETE FROM tombstones
WHERE EXISTS (
	SELECT 1
	FROM ranked
	WHERE ranked.feed_id = tombstones.feed_id
	  AND ranked.guid = tombstones.guid
	  AND ranked.tombstone_rank > ?
)
	`, MaxFeedItems)
	if err != nil {
		return 0, fmt.Errorf("delete tombstones beyond per-feed limit: %w", err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count tombstones deleted beyond per-feed limit: %w", err)
	}

	return deleted, nil
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
