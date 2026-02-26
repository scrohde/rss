//nolint:testpackage // Feed tests exercise package-internal helpers directly.
package feed

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"rss/internal/store"
	"rss/internal/testutil"
)

func TestRefreshSkipsFeedDisallowedByRobots(t *testing.T) {
	t.Parallel()

	base := time.Now().UTC().Add(-2 * time.Hour)
	feedServer, feedURL := testutil.NewFeedServer(
		t,
		testutil.RSSXML("Robots Feed", []testutil.RSSItem{{
			Title:       "First",
			Link:        "http://example.com/1",
			GUID:        "1",
			PubDate:     base.Format(time.RFC1123Z),
			Description: "<p>First summary</p>",
		}}),
	)
	feedServer.SetRobotsTxt("User-agent: *\nDisallow: /\n")

	database := testutil.OpenTestDB(t)

	feedID, err := store.UpsertFeed(context.Background(), database, feedURL, "Robots Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	_, refreshErr := Refresh(context.Background(), database, feedID)
	if !errors.Is(refreshErr, errFeedBlockedByRobots) {
		t.Fatalf("expected robots blocked error, got %v", refreshErr)
	}

	if feedServer.FeedRequestCount() != 0 {
		t.Fatalf("expected disallowed feed not to be fetched, got %d requests", feedServer.FeedRequestCount())
	}

	state := loadRefreshState(t, database, feedID)
	if !state.LastError.Valid || !strings.Contains(state.LastError.String, "Polling blocked by robots.txt") {
		t.Fatalf("expected robots block message to be persisted, got %q", state.LastError.String)
	}
}

func TestRefreshResumesWhenRobotsPolicyAllowsFeed(t *testing.T) {
	t.Parallel()

	base := time.Now().UTC().Add(-2 * time.Hour)
	feedServer, feedURL := testutil.NewFeedServer(
		t,
		testutil.RSSXML("Robots Feed", []testutil.RSSItem{{
			Title:       "First",
			Link:        "http://example.com/1",
			GUID:        "1",
			PubDate:     base.Format(time.RFC1123Z),
			Description: "<p>First summary</p>",
		}}),
	)
	feedServer.SetRobotsTxt("User-agent: *\nDisallow: /\n")

	database := testutil.OpenTestDB(t)

	feedID, err := store.UpsertFeed(context.Background(), database, feedURL, "Robots Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed: %v", err)
	}

	requireRefreshError(t, database, feedID)

	feedServer.SetRobotsTxt("User-agent: *\nAllow: /\n")
	expireRobotsCacheForFeed(t, feedURL)

	_, refreshErr := Refresh(context.Background(), database, feedID)
	if refreshErr != nil {
		t.Fatalf("Refresh after robots allow: %v", refreshErr)
	}

	if feedServer.FeedRequestCount() == 0 {
		t.Fatal("expected feed to be fetched after robots policy was updated")
	}

	assertFeedItemCount(t, database, feedID, expectedInitialItemCount, "robots policy update")

	state := loadRefreshState(t, database, feedID)
	if state.LastError.Valid && strings.TrimSpace(state.LastError.String) != "" {
		t.Fatalf("expected robots block error to clear after successful refresh, got %q", state.LastError.String)
	}
}

func TestBuildRobotsBlockedMessageAddsRedditAPIGuidance(t *testing.T) {
	t.Parallel()

	message := buildRobotsBlockedMessage("https://www.reddit.com/r/golang/.rss", "/r/")
	if !strings.Contains(message, "official API integration") {
		t.Fatalf("expected Reddit API guidance in robots message, got %q", message)
	}
}

func TestCheckRobotsAllowedUsesOneDayCache(t *testing.T) {
	t.Parallel()

	feedServer, feedURL := testutil.NewFeedServer(
		t,
		testutil.RSSXML("Robots Feed", []testutil.RSSItem{}),
	)
	feedServer.SetRobotsTxt("User-agent: *\nDisallow: /\n")

	firstErr := CheckRobotsAllowed(context.Background(), feedURL)
	if !errors.Is(firstErr, errFeedBlockedByRobots) {
		t.Fatalf("expected robots blocked error on first check, got %v", firstErr)
	}

	feedServer.SetRobotsTxt("User-agent: *\nAllow: /\n")

	secondErr := CheckRobotsAllowed(context.Background(), feedURL)
	if !errors.Is(secondErr, errFeedBlockedByRobots) {
		t.Fatalf("expected cached robots policy to remain blocked, got %v", secondErr)
	}

	if feedServer.RobotsRequestCount() != 1 {
		t.Fatalf("expected robots.txt to be fetched once due cache, got %d", feedServer.RobotsRequestCount())
	}
}

func expireRobotsCacheForFeed(t *testing.T, feedURL string) {
	t.Helper()

	parsedURL, err := url.Parse(feedURL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}

	key := robotsCacheKey(parsedURL)

	robotsCache.mu.Lock()
	defer robotsCache.mu.Unlock()

	entry, ok := robotsCache.entries[key]
	if !ok {
		return
	}

	entry.CachedAt = time.Now().UTC().Add(-robotsCacheTTL - time.Second)
	robotsCache.entries[key] = entry
}
