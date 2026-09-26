//nolint:wsl_v5 // Grouped transaction steps make atomic Today-selection updates explicit.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
)

// ErrInvalidTodayFeedID indicates that a Today selection contains an invalid or missing feed.
var ErrInvalidTodayFeedID = errors.New("invalid Today feed ID")

// ReadingPreferences are synchronized across browsers for the single Pulse RSS owner.
type ReadingPreferences struct {
	TodayFeedIDs          []int64
	ShowExactUnreadCounts bool
	TodaySetupCompleted   bool
}

func ensureReadingPreferencesSchema(db *sql.DB) error {
	_, err := db.ExecContext(context.Background(), `
CREATE TABLE IF NOT EXISTS reading_preferences (
	id INTEGER PRIMARY KEY CHECK (id = 1)
);
`)
	if err != nil {
		return fmt.Errorf("create reading preferences table: %w", err)
	}

	for _, column := range []struct {
		name       string
		definition string
	}{
		{
			name:       "show_exact_unread_counts",
			definition: "INTEGER NOT NULL DEFAULT 0 CHECK (show_exact_unread_counts IN (0, 1))",
		},
		{
			name:       "today_setup_completed",
			definition: "INTEGER NOT NULL DEFAULT 0 CHECK (today_setup_completed IN (0, 1))",
		},
	} {
		err = ensureReadingPreferenceColumn(db, column.name, column.definition)
		if err != nil {
			return err
		}
	}

	_, err = db.ExecContext(context.Background(), `
CREATE TABLE IF NOT EXISTS reading_preference_today_feeds (
	feed_id INTEGER PRIMARY KEY,
	FOREIGN KEY(feed_id) REFERENCES feeds(id) ON DELETE CASCADE
);
INSERT OR IGNORE INTO reading_preferences (id) VALUES (1);
`)
	if err != nil {
		return fmt.Errorf("initialize reading preference selections: %w", err)
	}

	return nil
}

func ensureReadingPreferenceColumn(db *sql.DB, columnName, definition string) error {
	hasColumn, err := authTableHasColumn(db, "reading_preferences", columnName)
	if err != nil {
		return fmt.Errorf("check reading preference column %q: %w", columnName, err)
	}

	if hasColumn {
		return nil
	}

	_, err = db.ExecContext(context.Background(),
		fmt.Sprintf("ALTER TABLE reading_preferences ADD COLUMN %s %s", columnName, definition))
	if err != nil {
		return fmt.Errorf("add reading preference column %q: %w", columnName, err)
	}

	return nil
}

// GetReadingPreferences loads the singleton owner's synchronized reading preferences.
func GetReadingPreferences(ctx context.Context, db *sql.DB) (ReadingPreferences, error) {
	ctx = contextOrBackground(ctx)

	prefs := ReadingPreferences{
		TodayFeedIDs:          make([]int64, 0),
		ShowExactUnreadCounts: false,
		TodaySetupCompleted:   false,
	}

	var showExactCounts int
	var todaySetupCompleted int

	err := db.QueryRowContext(ctx, `
SELECT show_exact_unread_counts, today_setup_completed
FROM reading_preferences
WHERE id = 1
`).Scan(&showExactCounts, &todaySetupCompleted)
	if err != nil {
		return ReadingPreferences{}, fmt.Errorf("load reading preferences: %w", err)
	}

	prefs.ShowExactUnreadCounts = showExactCounts != 0
	prefs.TodaySetupCompleted = todaySetupCompleted != 0

	feedIDs, err := loadTodayFeedIDs(ctx, db)
	if err != nil {
		return ReadingPreferences{}, err
	}
	prefs.TodayFeedIDs = feedIDs

	return prefs, nil
}

func loadTodayFeedIDs(ctx context.Context, db *sql.DB) ([]int64, error) {
	rows, err := db.QueryContext(ctx, `
SELECT selection.feed_id
FROM reading_preference_today_feeds AS selection
JOIN feeds AS feed ON feed.id = selection.feed_id
ORDER BY feed.sort_order ASC, feed.id ASC
`)
	if err != nil {
		return nil, fmt.Errorf("load Today feed selections: %w", err)
	}

	defer func() {
		closeErr := rows.Close()
		if closeErr != nil {
			slog.Warn("close Today feed selection rows", "err", closeErr)
		}
	}()

	feedIDs := make([]int64, 0)
	for rows.Next() {
		var feedID int64

		scanErr := rows.Scan(&feedID)
		if scanErr != nil {
			return nil, fmt.Errorf("scan Today feed selection: %w", scanErr)
		}

		feedIDs = append(feedIDs, feedID)
	}

	rowsErr := rows.Err()
	if rowsErr != nil {
		return nil, fmt.Errorf("iterate Today feed selections: %w", rowsErr)
	}

	return feedIDs, nil
}

// SetShowExactUnreadCounts updates only the exact-count preference.
func SetShowExactUnreadCounts(ctx context.Context, db *sql.DB, enabled bool) error {
	ctx = contextOrBackground(ctx)
	result, err := db.ExecContext(ctx, `
UPDATE reading_preferences
SET show_exact_unread_counts = ?
WHERE id = 1
`, enabled)
	if err != nil {
		return fmt.Errorf("update exact unread-count preference: %w", err)
	}

	return requireReadingPreferenceRow(result, "update exact unread-count preference")
}

// SetTodayFeedIDs replaces the selected Today feeds atomically after validating every ID.
func SetTodayFeedIDs(ctx context.Context, db *sql.DB, feedIDs []int64) error {
	ctx = contextOrBackground(ctx)

	uniqueIDs, err := uniqueTodayFeedIDs(feedIDs)
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update Today feed selections: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			rollbackTx(tx)
		}
	}()

	saveErr := saveTodayFeedSelections(ctx, tx, uniqueIDs)
	if saveErr != nil {
		return saveErr
	}

	commitErr := tx.Commit()
	if commitErr != nil {
		return fmt.Errorf("commit Today feed selections: %w", commitErr)
	}
	committed = true

	return nil
}

func saveTodayFeedSelections(ctx context.Context, tx *sql.Tx, feedIDs []int64) error {
	validationErr := validateTodayFeedIDs(ctx, tx, feedIDs)
	if validationErr != nil {
		return validationErr
	}

	selectionErr := replaceTodayFeedSelections(ctx, tx, feedIDs)
	if selectionErr != nil {
		return selectionErr
	}

	return updateTodaySetupCompleted(ctx, tx, len(feedIDs) > 0)
}

func validateTodayFeedIDs(ctx context.Context, tx *sql.Tx, feedIDs []int64) error {
	for _, feedID := range feedIDs {
		var exists int
		queryErr := tx.QueryRowContext(ctx, "SELECT 1 FROM feeds WHERE id = ?", feedID).Scan(&exists)
		if errors.Is(queryErr, sql.ErrNoRows) {
			return fmt.Errorf("%w: %d", ErrInvalidTodayFeedID, feedID)
		}

		if queryErr != nil {
			return fmt.Errorf("validate Today feed ID %d: %w", feedID, queryErr)
		}
	}

	return nil
}

func replaceTodayFeedSelections(ctx context.Context, tx *sql.Tx, feedIDs []int64) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM reading_preference_today_feeds")
	if err != nil {
		return fmt.Errorf("clear Today feed selections: %w", err)
	}

	for _, feedID := range feedIDs {
		_, err = tx.ExecContext(ctx, "INSERT INTO reading_preference_today_feeds (feed_id) VALUES (?)", feedID)
		if err != nil {
			return fmt.Errorf("save Today feed selection %d: %w", feedID, err)
		}
	}

	return nil
}

func updateTodaySetupCompleted(ctx context.Context, tx *sql.Tx, hasSelection bool) error {
	result, err := tx.ExecContext(ctx, `
UPDATE reading_preferences
SET today_setup_completed = CASE
	WHEN today_setup_completed = 1 OR ? = 1 THEN 1
	ELSE 0
END
WHERE id = 1
	`, hasSelection)
	if err != nil {
		return fmt.Errorf("update Today setup preference: %w", err)
	}

	return requireReadingPreferenceRow(result, "update Today setup preference")
}

func uniqueTodayFeedIDs(feedIDs []int64) ([]int64, error) {
	uniqueIDs := make([]int64, 0, len(feedIDs))
	seen := make(map[int64]struct{}, len(feedIDs))

	for _, feedID := range feedIDs {
		if feedID <= 0 {
			return nil, fmt.Errorf("%w: %d", ErrInvalidTodayFeedID, feedID)
		}
		if _, exists := seen[feedID]; exists {
			continue
		}

		seen[feedID] = struct{}{}
		uniqueIDs = append(uniqueIDs, feedID)
	}

	return uniqueIDs, nil
}

func requireReadingPreferenceRow(result sql.Result, operation string) error {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count %s rows: %w", operation, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%s: %w", operation, sql.ErrNoRows)
	}

	return nil
}
