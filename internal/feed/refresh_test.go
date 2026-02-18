//nolint:testpackage // Feed tests exercise package-internal helpers directly.
package feed

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"rss/internal/store"
	"rss/internal/testutil"
)

const (
	refreshFeedTitle         = "Refresh Feed"
	expectedInitialItemCount = 1
	expectedUpdatedItemCount = 2
)

func TestRefreshInsertsNewItems(t *testing.T) {
	t.Parallel()

	base := time.Now().UTC().Add(-2 * time.Hour)
	feedServer, feedURL := testutil.NewFeedServer(
		t,
		testutil.RSSXML(refreshFeedTitle, []testutil.RSSItem{{
			Title:       "First",
			Link:        "http://example.com/1",
			GUID:        "1",
			PubDate:     base.Format(time.RFC1123Z),
			Description: "<p>First summary</p>",
		}}),
	)
	database := testutil.OpenTestDB(t)

	feedID, err := store.UpsertFeed(context.Background(), database, feedURL, refreshFeedTitle)
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	_, refreshErr := Refresh(context.Background(), database, feedID)
	if refreshErr != nil {
		t.Fatalf("Refresh initial: %v", refreshErr)
	}

	assertFeedItemCount(t, database, feedID, expectedInitialItemCount, "first")

	feedServer.SetFeedXML(
		testutil.RSSXML(refreshFeedTitle, []testutil.RSSItem{{
			Title:       "Second",
			Link:        "http://example.com/2",
			GUID:        "2",
			PubDate:     base.Add(time.Minute).Format(time.RFC1123Z),
			Description: "<p>Second summary</p>",
		}, {
			Title:       "First",
			Link:        "http://example.com/1",
			GUID:        "1",
			PubDate:     base.Format(time.RFC1123Z),
			Description: "<p>First summary</p>",
		}}),
	)

	_, refreshErr = Refresh(context.Background(), database, feedID)
	if refreshErr != nil {
		t.Fatalf("Refresh second: %v", refreshErr)
	}

	assertFeedItemCount(t, database, feedID, expectedUpdatedItemCount, "second")
}

func assertFeedItemCount(
	t *testing.T,
	database *sql.DB,
	feedID int64,
	want int,
	phase string,
) {
	t.Helper()

	items, err := store.ListItems(context.Background(), database, feedID)
	if err != nil {
		t.Fatalf("store.ListItems %s: %v", phase, err)
	}

	if len(items) != want {
		t.Fatalf(
			"expected %d items after %s refresh, got %d",
			want,
			phase,
			len(items),
		)
	}
}
