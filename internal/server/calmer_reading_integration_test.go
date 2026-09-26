//nolint:testpackage // Cross-feature regression uses the shared app and HTTP fixtures.
package server

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"testing"
	"time"

	"rss/internal/store"
)

func TestCalmerReadingPreferencesSurviveCatchUpAndTodayRefresh(t *testing.T) {
	t.Parallel()

	for _, exact := range []bool{false, true} {
		t.Run(strconv.FormatBool(exact), func(t *testing.T) {
			t.Parallel()

			verifyCalmerReadingJourney(t, strconv.FormatBool(exact))
		})
	}
}

func verifyCalmerReadingJourney(t *testing.T, countMode string) {
	t.Helper()

	exact := countMode == "true"

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "https://example.com/integration", "Integration feed")
	published := time.Now().UTC().Add(-2 * time.Hour)
	mustUpsertSingleStory(t, app, feedID, "Integration story", "https://example.com/story", "story", published)
	items := mustListItems(t, app, feedID)
	requireNoErr(t, store.SetTodayFeedIDs(context.Background(), app.db, []int64{feedID}), "save Today: %v")
	requireNoErr(t, store.SetShowExactUnreadCounts(context.Background(), app.db, exact), "save counts: %v")

	batchPath := todayPath([]int64{items[0].ID}, false)
	batch := getHTMXRequest(app, batchPath)
	assertResponseCode(t, batch, "initial Today batch")
	assertContains(t, batch.Body.String(), "Integration story", "Today includes the recent story")

	form := url.Values{catchUpCutoffField: {published.Add(time.Hour).Format(time.RFC3339Nano)}}
	apply := postHTMXFormRequest(app, fmt.Sprintf("/feeds/%d/items/catch-up", feedID), form)
	assertResponseCode(t, apply, "Catch up from the selected feed")
	assertUnreadStateByGUID(t, app, feedID, "story", true)

	token, ok := app.activeMarkAllReadUndo(feedID)
	if !ok {
		t.Fatal("Catch up must retain Undo when the feed has no unread items")
	}

	undo := postHTMXFormRequest(app, fmt.Sprintf("/feeds/%d/items/read/undo", feedID),
		url.Values{"undo_token": {token}})
	assertResponseCode(t, undo, "Undo before leaving the feed")
	assertContains(t, undo.Body.String(), `data-show-exact-unread-counts="`+strconv.FormatBool(exact)+`"`,
		"Undo sidebar swap preserves the count preference")
	assertUnreadStateByGUID(t, app, feedID, "story", false)

	apply = postHTMXFormRequest(app, fmt.Sprintf("/feeds/%d/items/catch-up", feedID), form)
	assertResponseCode(t, apply, "Catch up again before returning to Today")

	stable := getHTMXRequest(app, batchPath)
	assertResponseCode(t, stable, "return to the existing Today batch")
	assertContains(t, stable.Body.String(), "Integration story", "read story remains in the stable batch")
	assertContains(t, stable.Body.String(), "is-read", "Catch up updates the existing Today card")

	refreshed := getHTMXRequest(app, "/today?refresh=1")
	assertResponseCode(t, refreshed, "explicit Today refresh after Catch up")
	assertNotContains(t, refreshed.Body.String(), "Integration story", "refresh removes the read story")

	prefs, err := store.GetReadingPreferences(context.Background(), app.db)
	requireNoErr(t, err, "load preferences after combined flow: %v")

	if prefs.ShowExactUnreadCounts != exact || !prefs.TodaySetupCompleted || len(prefs.TodayFeedIDs) != 1 {
		t.Fatalf("combined flow changed reading preferences: %+v", prefs)
	}
}
