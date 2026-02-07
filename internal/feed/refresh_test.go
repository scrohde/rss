package feed

import (
	"testing"
	"time"

	"rss/internal/store"
	"rss/internal/testutil"
)

func TestRefreshInsertsNewItems(t *testing.T) {
	base := time.Now().UTC().Add(-2 * time.Hour)
	fs, feedURL := testutil.NewFeedServer(t, testutil.RSSXML("Refresh Feed", []testutil.RSSItem{{
		Title:       "First",
		Link:        "http://example.com/1",
		GUID:        "1",
		PubDate:     base.Format(time.RFC1123Z),
		Description: "<p>First summary</p>",
	}}))
	db := testutil.OpenTestDB(t)

	feedID, err := store.UpsertFeed(db, feedURL, "Refresh Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	if _, err := Refresh(db, feedID); err != nil {
		t.Fatalf("Refresh initial: %v", err)
	}
	items, err := store.ListItems(db, feedID)
	if err != nil {
		t.Fatalf("store.ListItems initial: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item after first refresh, got %d", len(items))
	}

	fs.SetFeedXML(testutil.RSSXML("Refresh Feed", []testutil.RSSItem{{
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
	}}))

	if _, err := Refresh(db, feedID); err != nil {
		t.Fatalf("Refresh second: %v", err)
	}
	items, err = store.ListItems(db, feedID)
	if err != nil {
		t.Fatalf("store.ListItems second: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items after second refresh, got %d", len(items))
	}
}
