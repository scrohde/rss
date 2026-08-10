//nolint:testpackage // Handler integration tests intentionally exercise unexported helpers.
package server

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"

	feedpkg "rss/internal/feed"
	"rss/internal/store"
	"rss/internal/testutil"
)

func TestManualFeedRefresh(t *testing.T) {
	t.Parallel()

	base := time.Now().UTC().Add(-2 * time.Hour)
	feedServer, feedURL := testutil.NewFeedServer(
		t,
		manualRefreshInitialXML(base),
	)
	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(), app.db, feedURL, manualRefreshTitle)
	requireNoErr(t, err, errStoreUpsertFeed)

	_, refreshErr := feedpkg.Refresh(context.Background(), app.db, feedID)
	requireNoErr(t, refreshErr, "feedpkg.Refresh initial: %v")

	feedServer.SetFeedXML(manualRefreshUpdatedXML(base))

	rec := postRequest(
		app,
		fmt.Sprintf("/feeds/%d/refresh", feedID),
	)
	assertResponseCode(t, rec, "manual refresh status")

	assertManualRefreshBody(t, rec.Body.String(), feedID)

	items := mustListItems(t, app, feedID)
	assertItemCount(t, items, expectedTwoItems)
}

func TestManualFeedRefreshWhenRobotsBlocksFeed(t *testing.T) {
	t.Parallel()

	base := time.Now().UTC().Add(-2 * time.Hour)
	feedServer, feedURL := testutil.NewFeedServer(t, manualRefreshInitialXML(base))
	feedServer.SetRobotsTxt("User-agent: *\nDisallow: /\n")

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(
		context.Background(),
		app.db,
		feedURL,
		manualRefreshTitle,
	)
	requireNoErr(t, err, errStoreUpsertFeed)

	rec := postRequest(
		app,
		fmt.Sprintf("/feeds/%d/refresh", feedID),
	)
	assertResponseCode(t, rec, "manual refresh blocked status")
	assertContains(
		t,
		rec.Body.String(),
		"Polling blocked by robots.txt",
		"expected robots block message in manual refresh response",
	)

	if feedServer.FeedRequestCount() != 0 {
		t.Fatalf("expected feed polling to be skipped, got %d feed requests", feedServer.FeedRequestCount())
	}
}

type pulseFeedXML struct {
	initial string
	updated string
}

type pulseRefreshFixture struct {
	feedServerStale *testutil.FeedServer
	staleUpdated    string
	recentFeedID    int64
	staleFeedID     int64
}

func pulseStaleFeedXML(base time.Time) pulseFeedXML {
	initial := testutil.RSSXML(pulseFeedTwoTitle, []testutil.RSSItem{{
		Title:       "Two First",
		Link:        "http://example.com/two/1",
		GUID:        "two-1",
		PubDate:     base.Format(time.RFC1123Z),
		Description: "<p>Two First</p>",
	}})
	updated := testutil.RSSXML(pulseFeedTwoTitle, []testutil.RSSItem{
		{
			Title:       "Two Second",
			Link:        "http://example.com/two/2",
			GUID:        "two-2",
			PubDate:     base.Add(time.Minute).Format(time.RFC1123Z),
			Description: "<p>Two Second</p>",
		},
		{
			Title:       "Two First",
			Link:        "http://example.com/two/1",
			GUID:        "two-1",
			PubDate:     base.Format(time.RFC1123Z),
			Description: "<p>Two First</p>",
		},
	})

	return pulseFeedXML{
		initial: initial,
		updated: updated,
	}
}

func setPulseRefreshState(t *testing.T, app *App, feedID int64, refreshedAt time.Time, lastError string) {
	t.Helper()

	_, err := app.db.ExecContext(
		context.Background(),
		"UPDATE feeds SET last_refreshed_at = ?, last_error = ? WHERE id = ?",
		refreshedAt,
		lastError,
		feedID,
	)
	requireNoErr(t, err, "set pulse refresh state: %v")
}

func setupPulseRefreshFixture(t *testing.T, app *App, base time.Time) pulseRefreshFixture {
	t.Helper()

	staleFeedXML := pulseStaleFeedXML(base)
	feedServerStale, feedURLStale := testutil.NewFeedServer(t, staleFeedXML.initial)

	recentFeedID := mustUpsertFeed(t, app, "not a valid feed url", pulseFeedOneTitle)
	staleFeedID := mustUpsertFeed(t, app, feedURLStale, pulseFeedTwoTitle)

	_, refreshErr := feedpkg.Refresh(context.Background(), app.db, staleFeedID)
	requireNoErr(t, refreshErr, "feedpkg.Refresh initial stale: %v")

	now := time.Now().UTC()
	setPulseRefreshState(t, app, staleFeedID, now.Add(-2*time.Hour), "")
	setPulseRefreshState(t, app, recentFeedID, now, "")

	return pulseRefreshFixture{
		feedServerStale: feedServerStale,
		recentFeedID:    recentFeedID,
		staleFeedID:     staleFeedID,
		staleUpdated:    staleFeedXML.updated,
	}
}

func assertRecentFeedSkippedAfterPulse(t *testing.T, app *App, recentFeedID int64) {
	t.Helper()

	recentItems := mustListItems(t, app, recentFeedID)
	assertItemCount(t, recentItems, expectedNoItems)

	recentFeed, feedErr := store.GetFeed(context.Background(), app.db, recentFeedID)
	requireNoErr(t, feedErr, "store.GetFeed recent: %v")

	if recentFeed.LastError != "" {
		t.Fatalf("expected recent feed to be skipped, got last_error %q", recentFeed.LastError)
	}
}

func assertPulseFeedStatus(t *testing.T, app *App, feedID int64, want pulseFeedStatus) {
	t.Helper()

	if got := app.pulseFeedStatus(feedID); got != want {
		t.Fatalf("expected pulse status %q for feed %d, got %q", want, feedID, got)
	}
}

func TestPulseRefreshAllFeeds(t *testing.T) {
	t.Parallel()

	base := time.Now().UTC().Add(-2 * time.Hour)
	app := newTestApp(t)
	fixture := setupPulseRefreshFixture(t, app, base)

	fixture.feedServerStale.SetFeedXML(fixture.staleUpdated)

	rec := postRequest(app, pathPulseFeeds)
	assertResponseCode(t, rec, "pulse refresh status")
	assertNotContains(t, rec.Body.String(), "Pulse started:", "expected no pulse success message")

	waitForPulseIdle(t, app)

	assertRecentFeedSkippedAfterPulse(t, app, fixture.recentFeedID)

	staleItems := mustListItems(t, app, fixture.staleFeedID)
	assertItemCount(t, staleItems, expectedTwoItems)
}

func TestPulseStatusModelTracksSkippedPendingAndFreshFeeds(t *testing.T) {
	t.Parallel()

	base := time.Now().UTC().Add(-2 * time.Hour)
	app := newTestApp(t)
	fixture := setupPulseRefreshFixture(t, app, base)

	fixture.feedServerStale.SetFeedXML(fixture.staleUpdated)

	app.refreshMu.Lock()
	rec := postRequest(app, pathPulseFeeds)
	assertResponseCode(t, rec, "pulse refresh status")

	assertPulseFeedStatus(t, app, fixture.recentFeedID, pulseFeedStatusFresh)
	assertPulseFeedStatus(t, app, fixture.staleFeedID, pulseFeedStatusPending)
	assertPulseFeedStatus(t, app, fixture.staleFeedID+999, pulseFeedStatusNone)

	app.refreshMu.Unlock()
	waitForPulseIdle(t, app)

	assertPulseFeedStatus(t, app, fixture.recentFeedID, pulseFeedStatusFresh)
	assertPulseFeedStatus(t, app, fixture.staleFeedID, pulseFeedStatusFresh)
}

func TestPulseResponseRendersFeedListIndicatorsAndPoller(t *testing.T) {
	t.Parallel()

	base := time.Now().UTC().Add(-2 * time.Hour)
	app := newTestApp(t)
	fixture := setupPulseRefreshFixture(t, app, base)

	app.refreshMu.Lock()
	rec := postRequest(app, pathPulseFeeds)
	assertResponseCode(t, rec, "pulse status response status")

	body := rec.Body.String()
	assertContains(t, body, `id="feed-list" hx-swap-oob="innerHTML"`, "expected feed-list OOB update")
	assertContains(t, body, fmt.Sprintf(`data-feed-id="%d"`, fixture.recentFeedID), "expected recent feed row")
	assertContains(t, body, fmt.Sprintf(`data-feed-id="%d"`, fixture.staleFeedID), "expected stale feed row")
	assertContains(t, body, `class="feed-pulse-indicator fresh"`, "expected fresh feed indicator")
	assertContains(t, body, `role="img"`, "expected pulse indicators to expose accessible status labels")
	assertContains(t, body, `aria-label="Fresh"`, "expected fresh feed status label")
	assertContains(t, body, `class="feed-pulse-indicator pending"`, "expected pending feed indicator")
	assertContains(t, body, `aria-label="Refreshing"`, "expected pending feed status label")
	assertContains(t, body, `id="pulse-status-poller"`, "expected pulse poller slot update")
	assertContains(t, body, `hx-get="/feeds/pulse/status"`, "expected pulse status polling")
	assertContains(t, body, "Pulse running.", "expected pulse running message")

	app.refreshMu.Unlock()
	waitForPulseIdle(t, app)
}

func TestPulseStatusEndpointStopsPollingWhenIdle(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, pulseFeedOneTitle)
	app.resetPulseStatuses([]int64{feedID}, nil)

	rec := getRequest(app, pathPulseStatus)
	assertResponseCode(t, rec, "pulse idle status response")

	body := rec.Body.String()
	assertContains(t, body, `id="subscribe-message"`, "expected message OOB response")
	assertContains(t, body, `hx-swap-oob="outerHTML"`, "expected message OOB swap")
	assertContains(t, body, `id="feed-list" hx-swap-oob="innerHTML"`, "expected feed-list OOB update")
	assertContains(t, body, `class="feed-pulse-indicator fresh"`, "expected fresh status to remain visible")
	assertContains(t, body, `id="pulse-status-poller"`, "expected poller slot")
	assertNotContains(t, body, `hx-get="/feeds/pulse/status"`, "expected idle response to stop polling")
}

func TestPulseStatusViewsExpireFreshIndicatorsAfterWindow(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, pulseFeedOneTitle)
	now := time.Now().UTC()

	app.markPulseFeedStatusAt(feedID, pulseFeedStatusFresh, now.Add(-pulseRecentRefreshWindow))

	views := app.pulseStatusViewsAt(now)
	if _, ok := views[feedID]; ok {
		t.Fatalf("expected fresh pulse status to expire after %s", pulseRecentRefreshWindow)
	}

	app.markPulseFeedStatusAt(feedID, pulseFeedStatusFresh, now.Add(-pulseRecentRefreshWindow+time.Second))

	views = app.pulseStatusViewsAt(now)
	if _, ok := views[feedID]; !ok {
		t.Fatalf("expected fresh pulse status inside %s to remain visible", pulseRecentRefreshWindow)
	}
}

func TestPulseStatusModelMarksFailedFeedsError(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "not a valid feed url", pulseFeedOneTitle)

	rec := postRequest(app, pathPulseFeeds)
	assertResponseCode(t, rec, "pulse refresh status")

	waitForPulseIdle(t, app)

	assertPulseFeedStatus(t, app, feedID, pulseFeedStatusError)
}

func TestPulseStatusEndpointRendersFailedFeedErrorWithoutUpstreamText(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "not a valid feed url", "Broken Pulse Feed")

	rec := postRequest(app, pathPulseFeeds)
	assertResponseCode(t, rec, "pulse refresh status")
	waitForPulseIdle(t, app)

	statusRec := getRequest(app, pathPulseStatus)
	assertResponseCode(t, statusRec, "pulse failed status response")

	body := statusRec.Body.String()
	assertContains(t, body, fmt.Sprintf(`data-feed-id="%d"`, feedID), "expected failed feed row")
	assertContains(t, body, `class="feed-pulse-indicator error"`, "expected failed feed indicator")
	assertContains(t, body, `aria-label="Refresh failed"`, "expected generic failed feed status label")
	assertNotContains(t, body, "unsupported protocol scheme", "expected upstream error detail to stay hidden")
	assertNotContains(t, body, "not a valid feed url", "expected upstream feed URL to stay hidden")
}

func TestPulseStatusSnapshotIsIndependent(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, pulseFeedOneTitle)
	app.resetPulseStatuses([]int64{feedID}, nil)

	statuses := app.pulseFeedStatuses()
	statuses[feedID] = pulseFeedStatusError

	assertPulseFeedStatus(t, app, feedID, pulseFeedStatusFresh)
}

func TestPulseAlreadyRunningDoesNotReplaceStatuses(t *testing.T) {
	t.Parallel()

	base := time.Now().UTC().Add(-2 * time.Hour)
	app := newTestApp(t)
	fixture := setupPulseRefreshFixture(t, app, base)

	app.refreshMu.Lock()
	rec := postRequest(app, pathPulseFeeds)
	assertResponseCode(t, rec, "pulse refresh status")

	assertPulseFeedStatus(t, app, fixture.recentFeedID, pulseFeedStatusFresh)
	assertPulseFeedStatus(t, app, fixture.staleFeedID, pulseFeedStatusPending)

	secondRec := postRequest(app, pathPulseFeeds)
	assertResponseCode(t, secondRec, "pulse already running status")
	secondBody := secondRec.Body.String()
	assertContains(
		t,
		secondBody,
		"Pulse already running.",
		"expected pulse already running message",
	)
	assertContains(t, secondBody, `id="feed-list" hx-swap-oob="innerHTML"`, "expected feed-list status UI")
	assertContains(t, secondBody, `class="feed-pulse-indicator fresh"`, "expected fresh indicator to remain")
	assertContains(t, secondBody, `class="feed-pulse-indicator pending"`, "expected pending indicator to remain")
	assertContains(t, secondBody, `hx-get="/feeds/pulse/status"`, "expected polling to continue")
	assertPulseFeedStatus(t, app, fixture.recentFeedID, pulseFeedStatusFresh)
	assertPulseFeedStatus(t, app, fixture.staleFeedID, pulseFeedStatusPending)

	app.refreshMu.Unlock()
	waitForPulseIdle(t, app)
}

func TestPulseRefreshAlreadyRunning(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	_ = mustUpsertFeed(t, app, exampleRSSURL, pulseFeedOneTitle)

	app.pulseMu.Lock()
	app.pulseRunning = true
	app.pulseMu.Unlock()
	t.Cleanup(func() {
		app.pulseMu.Lock()
		app.pulseRunning = false
		app.pulseMu.Unlock()
	})

	rec := postRequest(app, pathPulseFeeds)
	assertResponseCode(t, rec, "pulse already running status")
	assertContains(
		t,
		rec.Body.String(),
		"Pulse already running.",
		"expected pulse already running message",
	)
	assertContains(
		t,
		rec.Body.String(),
		`hx-get="/feeds/pulse/status"`,
		"expected already-running response to include status polling",
	)
}

func seedDeleteFeedFixture(t *testing.T, app *App) int64 {
	t.Helper()

	feedID := mustUpsertFeed(t, app, exampleRSSURL, deleteFeedTitle)
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Item A",
		Link:            "http://example.com/a",
		GUID:            "a",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})

	_, err := app.db.ExecContext(
		context.Background(),
		"INSERT INTO tombstones (feed_id, guid, deleted_at) VALUES (?, ?, ?)",
		feedID,
		"gone",
		time.Now().UTC(),
	)
	requireNoErr(t, err, "insert tombstone: %v")

	return feedID
}

func deleteFeedRequest(
	app *App,
	feedID int64,
) *httptest.ResponseRecorder {
	form := url.Values{}
	setSelectedFeedID(form, feedID)

	target := fmt.Sprintf("/feeds/%d/delete", feedID)
	req := newURLEncodedRequest(target, form)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	return rec
}

func assertFeedDeleteCascade(t *testing.T, app *App, feedID int64) {
	t.Helper()

	assertFeedRowCount(
		t,
		app.db,
		sqlCountFeedByID,
		feedID,
		expectedNoItems,
		"feeds",
	)
	assertFeedRowCount(
		t,
		app.db,
		sqlCountItemsByFeed,
		feedID,
		expectedNoItems,
		"items",
	)
	assertFeedRowCount(
		t,
		app.db,
		sqlCountTombByFeed,
		feedID,
		expectedNoItems,
		"tombstones",
	)
}

func TestDeleteFeedRemovesData(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := seedDeleteFeedFixture(t, app)

	rec := deleteFeedRequest(app, feedID)
	assertResponseCode(t, rec, "delete feed status")
	assertContains(
		t,
		rec.Body.String(),
		emptyStateNoFeed,
		"expected empty state after deleting last feed",
	)

	assertFeedDeleteCascade(t, app, feedID)
}

func buildItemLimitItems(base time.Time) []*gofeed.Item {
	items := make([]*gofeed.Item, expectedNoItems, itemLimitTotal)
	for i := range itemLimitTotal {
		published := base.Add(time.Duration(i) * time.Minute)
		items = append(items, newGofeedItem(
			fmt.Sprintf("Item %03d", i),
			fmt.Sprintf("http://example.com/%d", i),
			fmt.Sprintf("guid-%03d", i),
			"<p>Summary</p>",
			&published,
		))
	}

	return items
}

func assertOldestItemGUIDsDeleted(t *testing.T, app *App, feedID int64) {
	t.Helper()

	for i := range itemLimitPruned {
		guid := fmt.Sprintf("guid-%03d", i)
		assertGUIDMissing(
			t,
			app.db,
			feedID,
			guid,
			fmt.Sprintf("expected %s to be deleted", guid),
		)
	}
}

func TestItemLimit(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, itemLimitFeedTitle)

	base := time.Now().UTC().Add(-itemLimitTotal * time.Minute)
	items := buildItemLimitItems(base)
	mustUpsertItems(t, app, feedID, items)

	err := store.EnforceItemLimit(context.Background(), app.db, feedID)
	requireNoErr(t, err, "store.EnforceItemLimit: %v")

	itemsInDB := mustListItems(t, app, feedID)
	assertItemCount(t, itemsInDB, itemLimitKept)
	assertOldestItemGUIDsDeleted(t, app, feedID)
	assertGUIDExists(
		t,
		app.db,
		feedID,
		itemLimitFirstGUID,
		"expected guid-010 to remain",
	)
}

func seedPollingFeed(
	t *testing.T,
	app *App,
	base time.Time,
) pollingFixtureIDs {
	t.Helper()

	feedID := mustUpsertFeed(t, app, exampleRSSURL, pollFeedTitle)
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "First",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>First summary</p>",
		PublishedParsed: new(base),
	}, {
		Title:           "Second",
		Link:            "http://example.com/2",
		GUID:            "2",
		Description:     "<p>Second summary</p>",
		PublishedParsed: new(base.Add(time.Minute)),
	}})

	list := mustLoadItemList(t, app, feedID)

	return pollingFixtureIDs{
		feedID:   feedID,
		newestID: list.NewestID,
	}
}

func assertInitialPollBanner(t *testing.T, body string) {
	t.Helper()

	assertContains(
		t,
		body,
		"New items (0)",
		"expected banner to show zero new items",
	)
	assertFeedListOOBUpdate(t, body)
	assertContains(
		t,
		body,
		`id="item-last-refresh"`,
		"expected last refresh OOB update",
	)
	assertContains(
		t,
		body,
		`id="item-last-error"`,
		"expected last error OOB update",
	)
	assertContains(t, body, `feed-count">2`, "expected unread count to be 2")
}

func addThirdPollItem(t *testing.T, app *App, feedID int64, base time.Time) {
	t.Helper()

	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Third",
		Link:            "http://example.com/3",
		GUID:            "3",
		Description:     "<p>Third summary</p>",
		PublishedParsed: new(base.Add(2 * time.Minute)),
	}})
}

func assertUpdatedPollBanner(t *testing.T, body string) {
	t.Helper()

	assertContains(
		t,
		body,
		"New items (1)",
		"expected banner to show new items",
	)
	assertContains(t, body, `feed-count">3`, "expected unread count to be 3")
}

func assertNewItemsResponse(t *testing.T, body string) {
	t.Helper()

	assertContains(t, body, "Third", "expected new item in response")
	assertContains(t, body, "hx-swap-oob", "expected OOB cursor update")
}

func TestPollingAndNewItemsBanner(t *testing.T) {
	t.Parallel()

	base := time.Now().UTC().Add(-2 * time.Hour)
	app := newTestApp(t)
	fixture := seedPollingFeed(t, app, base)
	feedID := fixture.feedID
	newestID := fixture.newestID

	pollRec := getRequest(app, pollItemsPath(feedID, newestID))
	assertResponseCode(t, pollRec, msgPollStatus)
	assertInitialPollBanner(t, pollRec.Body.String())

	addThirdPollItem(t, app, feedID, base)

	pollRec = getRequest(app, pollItemsPath(feedID, newestID))
	assertResponseCode(t, pollRec, msgPollStatus)
	assertUpdatedPollBanner(t, pollRec.Body.String())

	newRec := getRequest(app, newItemsPath(feedID, newestID))
	assertResponseCode(t, newRec, "new items status")
	assertNewItemsResponse(t, newRec.Body.String())
}

func TestPollingInFeedEditModeDoesNotSwapFeedList(t *testing.T) {
	t.Parallel()

	base := time.Now().UTC().Add(-2 * time.Hour)
	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Poll Edit Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "First",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>First summary</p>",
		PublishedParsed: new(base),
	}})
	list := mustLoadItemList(t, app, feedID)
	rec := getRequest(
		app,
		pollItemsPath(feedID, list.NewestID),
		editModeCookie(),
	)
	assertResponseCode(t, rec, msgPollStatus)

	body := rec.Body.String()
	assertNotContains(
		t,
		body,
		feedListIDAttr,
		"expected no feed list OOB update in edit mode",
	)
	assertContains(t, body, "New items (0)", "expected banner to be present")
	assertContains(
		t,
		body,
		`id="item-last-refresh"`,
		"expected last refresh OOB update",
	)
	assertContains(
		t,
		body,
		`id="item-last-error"`,
		"expected last error OOB update",
	)
}
