package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

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

// MarkItemRead is part of the store package API.
func MarkItemRead(ctx context.Context, db *sql.DB, itemID int64) error {
	ctx = contextOrBackground(ctx)

	result, err := db.ExecContext(
		ctx,
		"UPDATE items SET read_at = ? WHERE id = ? AND read_at IS NULL",
		time.Now().UTC(),
		itemID,
	)
	if err != nil {
		return fmt.Errorf("mark item %d read: %w", itemID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count mark-read rows for item %d: %w", itemID, err)
	}

	if affected > 0 {
		return nil
	}

	var exists int

	existsErr := db.QueryRowContext(
		ctx,
		"SELECT 1 FROM items WHERE id = ?",
		itemID,
	).Scan(&exists)
	if existsErr != nil {
		if errors.Is(existsErr, sql.ErrNoRows) {
			return fmt.Errorf("lookup item %d for mark-read: %w", itemID, existsErr)
		}

		return fmt.Errorf("lookup item %d for mark-read: %w", itemID, existsErr)
	}

	return nil
}

// MarkAllReadWithUndo marks the unread items in a feed as read and returns the
// item IDs that were unread at the start of the operation.
func MarkAllReadWithUndo(ctx context.Context, db *sql.DB, feedID int64) ([]int64, error) {
	ctx = contextOrBackground(ctx)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin mark-all-read transaction for feed %d: %w", feedID, err)
	}

	defer func() {
		if err != nil {
			rollbackTx(tx)
		}
	}()

	unreadItemIDs, err := markAllReadWithUndoTx(ctx, tx, feedID)
	if err != nil {
		return nil, err
	}

	err = tx.Commit()
	if err != nil {
		return nil, fmt.Errorf("commit mark-all-read transaction for feed %d: %w", feedID, err)
	}

	return unreadItemIDs, nil
}

func markAllReadWithUndoTx(ctx context.Context, tx *sql.Tx, feedID int64) ([]int64, error) {
	unreadItemIDs, err := unreadItemIDsForFeedTx(ctx, tx, feedID)
	if err != nil {
		return nil, err
	}

	if len(unreadItemIDs) == 0 {
		return nil, nil
	}

	err = markUnreadItemsReadTx(ctx, tx, feedID)
	if err != nil {
		return nil, err
	}

	return unreadItemIDs, nil
}

func unreadItemIDsForFeedTx(ctx context.Context, tx *sql.Tx, feedID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT id
FROM items
WHERE feed_id = ? AND read_at IS NULL
ORDER BY id ASC
	`, feedID)
	if err != nil {
		return nil, fmt.Errorf("list unread items for mark-all-read feed %d: %w", feedID, err)
	}
	defer rows.Close() //nolint:errcheck // Read-only query rows are closed when iteration completes.

	var unreadItemIDs []int64

	for rows.Next() {
		var itemID int64

		scanErr := rows.Scan(&itemID)
		if scanErr != nil {
			return nil, fmt.Errorf("scan unread mark-all-read item for feed %d: %w", feedID, scanErr)
		}

		unreadItemIDs = append(unreadItemIDs, itemID)
	}

	rowsErr := rows.Err()
	if rowsErr != nil {
		return nil, fmt.Errorf("iterate unread mark-all-read items for feed %d: %w", feedID, rowsErr)
	}

	return unreadItemIDs, nil
}

func markUnreadItemsReadTx(ctx context.Context, tx *sql.Tx, feedID int64) error {
	_, err := tx.ExecContext(ctx, `
UPDATE items
SET read_at = ?
WHERE feed_id = ? AND read_at IS NULL
	`, time.Now().UTC(), feedID)
	if err != nil {
		return fmt.Errorf("mark all items read for feed %d: %w", feedID, err)
	}

	return nil
}

// MarkAllRead is part of the store package API.
func MarkAllRead(ctx context.Context, db *sql.DB, feedID int64) error {
	_, err := MarkAllReadWithUndo(ctx, db, feedID)
	if err != nil {
		return err
	}

	return nil
}

// MarkItemsUnread is part of the store package API.
func MarkItemsUnread(ctx context.Context, db *sql.DB, feedID int64, itemIDs []int64) error {
	ctx = contextOrBackground(ctx)

	if len(itemIDs) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mark-items-unread transaction for feed %d: %w", feedID, err)
	}

	defer func() {
		if err != nil {
			rollbackTx(tx)
		}
	}()

	err = markItemsUnreadTx(ctx, tx, feedID, itemIDs)
	if err != nil {
		return err
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("commit mark-items-unread transaction for feed %d: %w", feedID, err)
	}

	return nil
}

func markItemsUnreadTx(ctx context.Context, tx *sql.Tx, feedID int64, itemIDs []int64) error {
	stmt, err := tx.PrepareContext(ctx, `
UPDATE items
SET read_at = NULL
WHERE feed_id = ? AND id = ?
	`)
	if err != nil {
		return fmt.Errorf("prepare mark-items-unread statement for feed %d: %w", feedID, err)
	}

	defer func() {
		closeErr := stmt.Close()
		if closeErr != nil {
			slog.Warn("statement close failed", "err", closeErr)
		}
	}()

	for _, itemID := range itemIDs {
		_, execErr := stmt.ExecContext(ctx, feedID, itemID)
		if execErr != nil {
			return fmt.Errorf("mark item %d unread for feed %d: %w", itemID, feedID, execErr)
		}
	}

	return nil
}
