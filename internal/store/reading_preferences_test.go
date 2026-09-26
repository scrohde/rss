//nolint:testpackage // Store tests exercise package-internal helpers directly.
package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func requireReadingPreferenceNoError(t *testing.T, err error, operation string) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", operation, err)
	}
}

func loadReadingPreferencesForTest(t *testing.T, db *sql.DB) ReadingPreferences {
	t.Helper()

	prefs, err := GetReadingPreferences(context.Background(), db)
	requireReadingPreferenceNoError(t, err, "GetReadingPreferences")

	return prefs
}

//nolint:revive // Assertion helpers intentionally take the expected state as a boolean.
func requireReadingPreferenceState(t *testing.T, condition bool, message string, prefs ReadingPreferences) {
	t.Helper()

	if !condition {
		t.Fatalf("%s: %+v", message, prefs)
	}
}

func TestReadingPreferencesDefaultToQuietAndRemainAfterRepeatedInit(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	prefs := loadReadingPreferencesForTest(t, db)

	requireReadingPreferenceState(
		t,
		!prefs.ShowExactUnreadCounts && !prefs.TodaySetupCompleted && prefs.TodayFeedIDs != nil &&
			len(prefs.TodayFeedIDs) == 0,
		"unexpected default reading preferences",
		prefs,
	)

	requireReadingPreferenceNoError(t, SetTodayFeedIDs(ctx, db, nil), "save empty initial Today selection")
	prefs = loadReadingPreferencesForTest(t, db)

	requireReadingPreferenceState(
		t,
		!prefs.TodaySetupCompleted && len(prefs.TodayFeedIDs) == 0,
		"empty initial selection completed Today setup",
		prefs,
	)

	feedID := mustUpsertFeed(t, db, "https://example.com/selected", "Selected")
	requireReadingPreferenceNoError(t, SetShowExactUnreadCounts(ctx, db, true), "SetShowExactUnreadCounts")
	requireReadingPreferenceNoError(t, SetTodayFeedIDs(ctx, db, []int64{feedID}), "SetTodayFeedIDs")
	requireReadingPreferenceNoError(t, Init(db), "repeat Init")
	prefs = loadReadingPreferencesForTest(t, db)

	requireReadingPreferenceState(
		t,
		prefs.ShowExactUnreadCounts && prefs.TodaySetupCompleted &&
			len(prefs.TodayFeedIDs) == 1 && prefs.TodayFeedIDs[0] == feedID,
		"Init did not preserve reading preferences",
		prefs,
	)
}

func TestReadingPreferencesPersistAcrossDatabaseReopen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "reading-preferences.db")
	firstDB, err := Open(path)
	requireReadingPreferenceNoError(t, err, "Open first DB")
	t.Cleanup(func() {
		closeErr := firstDB.Close()
		if closeErr != nil {
			t.Errorf("close first DB: %v", closeErr)
		}
	})
	requireReadingPreferenceNoError(t, Init(firstDB), "Init first DB")
	feedID := mustUpsertFeed(t, firstDB, "https://example.com/restart", "Restart")
	requireReadingPreferenceNoError(
		t,
		SetShowExactUnreadCounts(context.Background(), firstDB, true),
		"SetShowExactUnreadCounts",
	)
	requireReadingPreferenceNoError(
		t,
		SetTodayFeedIDs(context.Background(), firstDB, []int64{feedID}),
		"SetTodayFeedIDs",
	)
	requireReadingPreferenceNoError(t, firstDB.Close(), "close first DB")

	reopenedDB, err := Open(path)
	requireReadingPreferenceNoError(t, err, "reopen DB")
	t.Cleanup(func() {
		closeErr := reopenedDB.Close()
		if closeErr != nil {
			t.Errorf("close reopened DB: %v", closeErr)
		}
	})
	requireReadingPreferenceNoError(t, Init(reopenedDB), "Init reopened DB")
	prefs := loadReadingPreferencesForTest(t, reopenedDB)

	requireReadingPreferenceState(
		t,
		prefs.ShowExactUnreadCounts && prefs.TodaySetupCompleted &&
			len(prefs.TodayFeedIDs) == 1 && prefs.TodayFeedIDs[0] == feedID,
		"reopened DB did not preserve reading preferences",
		prefs,
	)
}

func TestInitAddsReadingPreferenceMigrationColumnsWithoutResettingData(t *testing.T) {
	t.Parallel()

	db := openLegacySchemaDB(t)
	_, err := db.ExecContext(context.Background(), `
INSERT INTO feeds (url, title, created_at) VALUES ('https://example.com/legacy', 'Legacy', CURRENT_TIMESTAMP);
CREATE TABLE reading_preferences (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	show_exact_unread_counts INTEGER NOT NULL DEFAULT 0
);
INSERT INTO reading_preferences (id, show_exact_unread_counts) VALUES (1, 1);
`)
	requireReadingPreferenceNoError(t, err, "prepare legacy reading preferences")

	var feedID int64

	err = db.QueryRowContext(context.Background(), "SELECT id FROM feeds").Scan(&feedID)
	requireReadingPreferenceNoError(t, err, "load legacy feed ID")
	requireReadingPreferenceNoError(t, Init(db), "Init legacy schema")
	prefs := loadReadingPreferencesForTest(t, db)

	requireReadingPreferenceState(
		t,
		prefs.ShowExactUnreadCounts && !prefs.TodaySetupCompleted && len(prefs.TodayFeedIDs) == 0,
		"migration did not preserve existing data and default new fields",
		prefs,
	)

	requireReadingPreferenceNoError(
		t,
		SetTodayFeedIDs(context.Background(), db, []int64{feedID}),
		"SetTodayFeedIDs after migration",
	)
	requireReadingPreferenceNoError(t, Init(db), "repeat migrated Init")
	prefs = loadReadingPreferencesForTest(t, db)

	requireReadingPreferenceState(
		t,
		prefs.ShowExactUnreadCounts && prefs.TodaySetupCompleted &&
			len(prefs.TodayFeedIDs) == 1 && prefs.TodayFeedIDs[0] == feedID,
		"repeat migration changed saved data",
		prefs,
	)
}

func TestTodayFeedSelectionValidationIsAtomicAndDeletionPrunesSelection(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	selectedFeedID := mustUpsertFeed(t, db, "https://example.com/selected", "Selected")
	otherFeedID := mustUpsertFeed(t, db, "https://example.com/other", "Other")
	requireReadingPreferenceNoError(t, SetShowExactUnreadCounts(ctx, db, true), "SetShowExactUnreadCounts")
	requireReadingPreferenceNoError(
		t,
		SetTodayFeedIDs(ctx, db, []int64{selectedFeedID, selectedFeedID}),
		"SetTodayFeedIDs with duplicate ID",
	)

	err := SetTodayFeedIDs(ctx, db, []int64{otherFeedID, 999999})
	if !errors.Is(err, ErrInvalidTodayFeedID) {
		t.Fatalf("expected invalid feed error, got %v", err)
	}

	prefs := loadReadingPreferencesForTest(t, db)
	requireReadingPreferenceState(
		t,
		prefs.ShowExactUnreadCounts && prefs.TodaySetupCompleted &&
			len(prefs.TodayFeedIDs) == 1 && prefs.TodayFeedIDs[0] == selectedFeedID,
		"invalid save partially changed preferences",
		prefs,
	)

	requireReadingPreferenceNoError(t, DeleteFeed(ctx, db, selectedFeedID), "DeleteFeed")
	prefs = loadReadingPreferencesForTest(t, db)
	requireReadingPreferenceState(
		t,
		prefs.TodaySetupCompleted && len(prefs.TodayFeedIDs) == 0,
		"deleted feed remained selected or reset setup state",
		prefs,
	)

	newFeedID := mustUpsertFeed(t, db, "https://example.com/new", "New")
	prefs = loadReadingPreferencesForTest(t, db)
	requireReadingPreferenceState(
		t,
		len(prefs.TodayFeedIDs) == 0 && newFeedID != selectedFeedID,
		"new feed was implicitly selected",
		prefs,
	)
}
