//nolint:testpackage // Feed tests exercise package-internal helpers directly.
package feed

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"rss/internal/store"
	"rss/internal/testutil"
)

const testRetryAfterSeconds = 3600

type refreshState struct {
	LastCheckedAt  sql.NullTime
	NextRefreshAt  sql.NullTime
	LastError      sql.NullString
	UnchangedCount int
}

func TestRefreshFetchErrorSetsBackoffMetadata(t *testing.T) {
	t.Parallel()

	feedServer, feedURL := testutil.NewFeedServer(t, "")
	feedServer.SetHTTPResponse(http.StatusServiceUnavailable, nil)

	database := testutil.OpenTestDB(t)
	feedID := mustUpsertFeed(t, database, feedURL, "broken feed")
	requireRefreshError(t, database, feedID)

	first := loadRefreshState(t, database, feedID)
	assertUnchangedCount(t, &first, 1)
	assertRefreshTimes(t, &first)
	assertBackoffBeyondBaseInterval(t, &first)
}

func TestRefreshFetchErrorIncrementsUnchangedCountOnRepeatedFailures(t *testing.T) {
	t.Parallel()

	feedServer, feedURL := testutil.NewFeedServer(t, "")
	feedServer.SetHTTPResponse(http.StatusServiceUnavailable, nil)

	database := testutil.OpenTestDB(t)
	feedID := mustUpsertFeed(t, database, feedURL, "broken feed")

	requireRefreshError(t, database, feedID)
	requireRefreshError(t, database, feedID)

	second := loadRefreshState(t, database, feedID)
	assertUnchangedCount(t, &second, 2)
}

func TestRefreshTooManyRequestsHonorsRetryAfter(t *testing.T) {
	t.Parallel()

	headers := http.Header{"Retry-After": []string{"3600"}}
	feedServer, feedURL := testutil.NewFeedServer(t, "")
	feedServer.SetHTTPResponse(http.StatusTooManyRequests, headers)

	database := testutil.OpenTestDB(t)

	feedID, err := store.UpsertFeed(context.Background(), database, feedURL, "rate-limited feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	_, refreshErr := Refresh(context.Background(), database, feedID)
	if refreshErr == nil {
		t.Fatal("expected refresh error")
	}

	state := loadRefreshState(t, database, feedID)
	assertRefreshTimes(t, &state)

	retryAfter := time.Duration(testRetryAfterSeconds) * time.Second

	scheduledDelay := state.NextRefreshAt.Time.Sub(state.LastCheckedAt.Time)
	if scheduledDelay < retryAfter {
		t.Fatalf(
			"expected next_refresh_at delay >= %v from Retry-After, got %v",
			retryAfter,
			scheduledDelay,
		)
	}
}

func mustUpsertFeed(t *testing.T, database *sql.DB, feedURL, title string) int64 {
	t.Helper()

	feedID, err := store.UpsertFeed(context.Background(), database, feedURL, title)
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	return feedID
}

func requireRefreshError(t *testing.T, database *sql.DB, feedID int64) {
	t.Helper()

	_, refreshErr := Refresh(context.Background(), database, feedID)
	if refreshErr == nil {
		t.Fatal("expected refresh error")
	}
}

func assertUnchangedCount(t *testing.T, state *refreshState, want int) {
	t.Helper()

	if state.UnchangedCount != want {
		t.Fatalf("expected unchanged_count %d, got %d", want, state.UnchangedCount)
	}
}

func assertRefreshTimes(t *testing.T, state *refreshState) {
	t.Helper()

	if !state.LastCheckedAt.Valid || !state.NextRefreshAt.Valid {
		t.Fatal("expected refresh timestamps to be persisted")
	}
}

func assertBackoffBeyondBaseInterval(t *testing.T, state *refreshState) {
	t.Helper()

	if !state.NextRefreshAt.Time.After(state.LastCheckedAt.Time.Add(RefreshInterval)) {
		t.Fatalf(
			"expected next_refresh_at to exceed base interval after error, got last=%s next=%s",
			state.LastCheckedAt.Time,
			state.NextRefreshAt.Time,
		)
	}
}

func loadRefreshState(t *testing.T, database *sql.DB, feedID int64) refreshState {
	t.Helper()

	var state refreshState

	err := database.QueryRowContext(
		context.Background(),
		`SELECT last_error, unchanged_count, last_refreshed_at, next_refresh_at FROM feeds WHERE id = ?`,
		feedID,
	).Scan(
		&state.LastError,
		&state.UnchangedCount,
		&state.LastCheckedAt,
		&state.NextRefreshAt,
	)
	if err != nil {
		t.Fatalf("query refresh metadata: %v", err)
	}

	return state
}
