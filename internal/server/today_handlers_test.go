//nolint:testpackage // Handler tests exercise route wiring and stored reading preferences.
package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"rss/internal/store"
)

func TestTodayIncludesAllFeedsWithoutSetup(t *testing.T) {
	t.Parallel()
	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "https://example.com/today", "Today feed")
	empty := getRequest(app, "/today")
	assertContains(t, empty.Body.String(), "No recent stories", "expected empty state without setup")
	mustUpsertSingleStory(t, app, feedID, "Recent story", "https://example.com/story", "story",
		time.Now().UTC().Add(-time.Hour))

	requireNoErr(t, store.SetTodayFeedIDs(context.Background(), app.db, []int64{feedID}), "save legacy selection: %v")
	newFeedID := mustUpsertFeed(t, app, "https://example.com/new-feed", "New feed")
	mustUpsertSingleStory(t, app, newFeedID, "New feed story", "https://example.com/new-story", "new-story",
		time.Now().UTC().Add(-time.Hour))

	for _, path := range []string{"/today", "/today?layout=mobile"} {
		response := getRequest(app, path)
		assertResponseCode(t, response, "Today without setup")
		assertContains(t, response.Body.String(), "Recent story", "expected story without feed selection")
		assertContains(t, response.Body.String(), "New feed story", "new subscriptions join Today automatically")
		assertNotContains(t, response.Body.String(), "Choose feeds", "obsolete selection control")
		assertNotContains(t, response.Body.String(), `id="today-all-feeds"`, "obsolete desktop directory")
	}
}

func TestTodayDefaultLandingPreservesExplicitFeedNavigation(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "https://example.com/today-default", "Default feed")
	mustUpsertSingleStory(t, app, feedID, "Default story", "https://example.com/default-story", "default-story",
		time.Now().UTC().Add(-time.Hour))

	beforeSetup := getRequest(app, "/")
	assertResponseCode(t, beforeSetup, "index before Today setup")
	assertContains(t, beforeSetup.Body.String(), "Pick a feed to start reading", "expected original first-run landing")

	err := store.SetTodayFeedIDs(context.Background(), app.db, []int64{feedID})
	requireNoErr(t, err, "save initial Today feed selection: %v")

	root := getRequest(app, "/")
	if root.Code != http.StatusSeeOther || root.Header().Get("Location") != "/today" {
		t.Fatalf(
			"expected root to redirect to Today, got status %d location %q",
			root.Code,
			root.Header().Get("Location"),
		)
	}

	hxRoot := getHTMXRequest(app, "/")
	assertResponseCode(t, hxRoot, "HTMX root navigation")

	if got := hxRoot.Header().Get("Hx-Redirect"); got != "/today" {
		t.Fatalf("expected HTMX root to redirect to Today, got %q", got)
	}

	explicitPath := fmt.Sprintf("/feeds/%d/items?from_today=1", feedID)
	explicit := getRequest(app, explicitPath)
	assertResponseCode(t, explicit, "explicit feed URL after setup")
	assertContains(t, explicit.Body.String(), "Default story", "expected explicit feed selection to remain honored")
	assertNotContains(t, explicit.Body.String(), `<h1 id="today-title">Today</h1>`,
		"explicit feed URL should not redirect to Today")

	selection := httptest.NewRequestWithContext(context.Background(), http.MethodGet, explicitPath, http.NoBody)
	selection.Header.Set("Hx-Request", "true")

	response := httptest.NewRecorder()
	app.Routes().ServeHTTP(response, selection)
	assertResponseCode(t, response, "HTMX feed selection from Today")

	if got := response.Header().Get("Hx-Push-Url"); got != explicitPath {
		t.Fatalf("expected Today feed selection to push %q, got %q", explicitPath, got)
	}

	assertContains(t, response.Body.String(), "Default story", "expected normal reader partial after feed selection")
}

func TestTodayKeepsStableBatchUntilExplicitRefresh(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "https://example.com/today-batch", "Batch feed")
	mustUpsertSingleStory(t, app, feedID, "Original batch story", "https://example.com/original", "original",
		time.Now().UTC().Add(-time.Hour))

	items := mustListItems(t, app, feedID)
	if len(items) != 1 {
		t.Fatalf("expected one initial story, got %d", len(items))
	}

	itemID := items[0].ID
	batchPath := "/today?batch_ids=" + strconv.FormatInt(itemID, 10)

	err := store.SetTodayFeedIDs(context.Background(), app.db, []int64{feedID})
	requireNoErr(t, err, "save Today feed selection: %v")
	err = store.MarkItemRead(context.Background(), app.db, itemID)
	requireNoErr(t, err, "mark original Today story read: %v")
	mustUpsertSingleStory(t, app, feedID, "New story", "https://example.com/new", "new",
		time.Now().UTC())

	stable := getHTMXRequest(app, batchPath)
	assertResponseCode(t, stable, "stable Today batch request")
	assertContains(t, stable.Body.String(), "Original batch story", "expected original batch story to remain")
	assertContains(t, stable.Body.String(), `is-read`,
		"expected read card to stay in the in-progress batch")
	assertNotContains(t, stable.Body.String(), "New story", "unexpected new story in existing Today batch")

	refreshed := getHTMXRequest(app, "/today?refresh=1")
	assertResponseCode(t, refreshed, "explicit Today refresh")
	assertContains(t, refreshed.Body.String(), "New story", "expected explicit refresh to recompute Today batch")
	assertContains(t, refreshed.Body.String(), "Original batch story",
		"read stories remain until cleared, like other feeds")

	refreshedPath := todayPath(itemsToIDs(mustListItems(t, app, feedID)), false)
	if got := refreshed.Header().Get("Hx-Replace-Url"); got != refreshedPath {
		t.Fatalf("expected refreshed URL to match new batch, got %q", got)
	}
}

func TestTodayMobileReloadReaderAndReadCardKeepTodayContext(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "https://example.com/today-mobile", "Mobile Today feed")
	mustUpsertSingleStory(t, app, feedID, "Mobile Today story", "https://example.com/mobile-story", "mobile-story",
		time.Now().UTC().Add(-time.Hour))
	items := mustListItems(t, app, feedID)
	itemID := items[0].ID
	err := store.SetTodayFeedIDs(context.Background(), app.db, []int64{feedID})
	requireNoErr(t, err, "save mobile Today feed selection: %v")

	fullPage := getRequest(app, "/today?layout=mobile")
	assertResponseCode(t, fullPage, "full mobile Today page")
	assertContains(t, fullPage.Body.String(), `data-mobile-stream="true"`, "expected mobile stream shell on reload")
	assertContains(t, fullPage.Body.String(), `data-today-view="true"`, "expected Today context on mobile reload")
	assertContains(t, fullPage.Body.String(), "Mobile Today story", "expected selected story in mobile batch")
	assertContains(t, fullPage.Body.String(), `id="mobile-today-all-feeds"`,
		"expected accessible All feeds action in mobile view")

	staleReload := getRequest(app, todayPath([]int64{itemID}, true))
	if staleReload.Code != http.StatusSeeOther || staleReload.Header().Get("Location") != "/today?layout=mobile" {
		t.Fatalf("expected mobile reload to start a fresh batch, got status %d location %q",
			staleReload.Code, staleReload.Header().Get("Location"))
	}

	transitionPath := todayPath([]int64{itemID}, true) + "&transition=1"
	transition := getHTMXRequest(app, transitionPath)
	assertResponseCode(t, transition, "mobile Today transition")

	if got, want := transition.Header().Get("Hx-Replace-Url"), todayPath([]int64{itemID}, true); got != want {
		t.Fatalf("expected mobile Today transition URL %q, got %q", want, got)
	}

	reader := getHTMXRequest(app, fmt.Sprintf("/mobile/items/%d/reader?today=1&batch_ids=%d", itemID, itemID))
	assertResponseCode(t, reader, "Today mobile reader")
	assertContains(t, reader.Body.String(), `data-today-reader="true"`, "expected Today state on mobile reader")

	expectedBackPath := strings.ReplaceAll(todayPath([]int64{itemID}, true), "&", "&amp;")
	assertContains(t, reader.Body.String(), fmt.Sprintf("hx-get=%q", expectedBackPath),
		"expected mobile reader back path to preserve Today batch")

	readPath := fmt.Sprintf("/mobile/items/%d/read?today=1&batch_ids=%d", itemID, itemID)
	read := httptest.NewRequestWithContext(context.Background(), http.MethodPost, readPath, http.NoBody)
	read.Header.Set("Hx-Request", "true")

	readResponse := httptest.NewRecorder()
	app.Routes().ServeHTTP(readResponse, read)
	assertResponseCode(t, readResponse, "mark Today reader item read")
	assertContains(t, readResponse.Body.String(), `class="mobile-card today-mobile-card is-read"`,
		"expected read story to remain visible in the stable mobile batch")
	assertContains(t, readResponse.Body.String(), "Read", "expected accessible read status")
}

func TestTodayRejectsMalformedBatchIDs(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/today?batch_ids=1,1", http.NoBody)
	request.Header.Set("Hx-Request", "true")

	response := httptest.NewRecorder()
	app.Routes().ServeHTTP(response, request)

	failedToLoadToday := strings.Contains(response.Body.String(), "failed to load Today stories")
	if response.Code != http.StatusBadRequest || !failedToLoadToday {
		t.Fatalf("expected malformed batch to fail as 400, got %d: %s", response.Code, response.Body.String())
	}
}

func TestTodayQueryStringsPreserveMobileAllFeedMode(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/mobile/stream?view=all",
		http.NoBody,
	)
	request.Header.Set("Hx-Request", "true")
	request.Header.Set("Hx-Trigger", "mobile-today-all-feeds")

	state := mobileAggregateState{
		FeedCursor: nil,
		ItemCursor: nil,
		FeedID:     0,
	}
	if got := mobileStreamStatePathForRequest(request, 0, state); got != "/mobile/stream?view=all" {
		t.Fatalf("unexpected all-feed stream URL: %q", got)
	}

	if got := mobilePulseStatePathForRequest(request, 0, state); got != "/mobile/pulse?view=all" {
		t.Fatalf("unexpected all-feed refresh URL: %q", got)
	}

	if !isMobileAllFeedsRequest(request) {
		t.Fatal("expected all-feed mode to be recognized from the request query")
	}

	request.URL.RawQuery = url.Values{"selected_feed_id": {"4"}, "view": {"all"}}.Encode()
	if got := mobileStreamStatePathForRequest(request, 4, state); got != "/mobile/stream?selected_feed_id=4&view=all" {
		t.Fatalf("unexpected selected all-feed URL: %q", got)
	}
}
