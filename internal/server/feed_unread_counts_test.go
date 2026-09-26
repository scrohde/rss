//nolint:testpackage,wsl_v5 // Integration tests exercise package fixtures with sequential response assertions.
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

	"github.com/mmcdole/gofeed"

	"rss/internal/store"
)

type unreadCountFeedIDs struct {
	active int64
	quiet  int64
}

//nolint:exhaustruct_v5 // GoFeed fixture fields not used by the tests stay zero-valued.
func seedUnreadCountDisplayFeeds(t *testing.T, app *App) unreadCountFeedIDs {
	t.Helper()

	activeFeedID := mustUpsertFeed(t, app, "http://example.com/unread-counts", "Unread Count Feed")
	quietFeedID := mustUpsertFeed(t, app, "http://example.com/quiet-counts", "Quiet Count Feed")
	olderThanNewLabel := time.Now().UTC().AddDate(0, 0, -90)
	mustUpsertItems(t, app, activeFeedID, []*gofeed.Item{
		//nolint:exhaustruct_v5 // Only the fields used by the fixture are populated.
		{
			Title:           "Older unread one",
			Link:            "http://example.com/unread-counts/1",
			GUID:            "unread-counts-1",
			PublishedParsed: &olderThanNewLabel,
		},
		//nolint:exhaustruct_v5 // Only the fields used by the fixture are populated.
		{
			Title:           "Older unread two",
			Link:            "http://example.com/unread-counts/2",
			GUID:            "unread-counts-2",
			PublishedParsed: new(olderThanNewLabel.Add(-time.Hour)),
		},
	})

	return unreadCountFeedIDs{active: activeFeedID, quiet: quietFeedID}
}

func TestSidebarUnreadCountsUseNewByDefaultAndExactCountsAfterOptIn(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedIDs := seedUnreadCountDisplayFeeds(t, app)
	activeFeedID := feedIDs.active

	defaultResponse := getRequest(app, pathIndex)
	assertResponseCode(t, defaultResponse, "default sidebar unread count status")
	defaultBody := defaultResponse.Body.String()
	assertContains(t, defaultBody, `data-show-exact-unread-counts="false"`, "expected default count mode")
	assertContains(t, defaultBody, `class="feed-count-new">New</span>`, "expected New badge by default")
	assertContains(t, defaultBody, `class="feed-count-exact">2</span>`, "expected exact count to be available")
	assertContains(t, defaultBody, "regardless of article age", "expected age-independent New explanation")
	assertContains(t, defaultBody, `class="feed-details"`, "expected a touch-friendly feed details disclosure")
	if got := strings.Count(defaultBody, `class="feed-count"`); got != 1 {
		t.Fatalf("expected one badge for the positive feed and none for the zero feed, got %d", got)
	}
	assertContains(
		t,
		defaultBody,
		fmt.Sprintf(`data-feed-id="%d"`, activeFeedID),
		"expected the older unread feed in the sidebar",
	)

	form := url.Values{"enabled": {"true"}, "return_to": {pathIndex}}
	request := readingPreferenceRequest(http.MethodPost, "/preferences/reading/counts", form)
	response := httptest.NewRecorder()
	app.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("expected opt-in preference redirect, got %d", response.Code)
	}

	exactResponse := getRequest(app, pathIndex)
	assertResponseCode(t, exactResponse, "opt-in sidebar unread count status")
	exactBody := exactResponse.Body.String()
	assertContains(t, exactBody, `data-show-exact-unread-counts="true"`, "expected saved exact-count mode")
	assertContains(t, exactBody, `class="feed-count-exact">2</span>`, "expected the exact unread count")
	assertNotContains(
		t,
		exactBody,
		`class="feed-details"`,
		"expected details disclosure to be unnecessary in count mode",
	)
	if got := strings.Count(exactBody, `class="feed-count"`); got != 1 {
		t.Fatalf("expected zero-unread feed to remain badge-free in count mode, got %d badges", got)
	}
}

func TestUnreadCountPreferenceHTMXUpdatesSidebarAndMobileOptions(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedIDs := seedUnreadCountDisplayFeeds(t, app)
	activeFeedID := feedIDs.active
	feedIDText := strconv.FormatInt(activeFeedID, 10)

	form := url.Values{
		"enabled":          {"true"},
		"mobile_view":      {"true"},
		"selected_feed_id": {feedIDText},
	}
	request := readingPreferenceRequest(http.MethodPost, "/preferences/reading/counts", form)
	request.Header.Set("Hx-Request", "true")
	response := httptest.NewRecorder()
	app.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected htmx opt-in response 200, got %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	assertContains(t, body, `id="reading-preferences-state"`, "expected preference state to update out of band")
	assertContains(t, body, `data-show-exact-unread-counts="true"`, "expected exact-count preference state")
	assertContains(t, body, `id="feed-list" hx-swap-oob="innerHTML"`, "expected sidebar to update out of band")
	assertContains(t, body, `data-show-exact-unread-counts="true"`, "expected refreshed sidebar to use exact mode")
	assertContains(t, body, `class="feed-count-exact">2</span>`, "expected refreshed exact count")
	assertContains(t, body, `id="topbar-mobile-slot" class="topbar-mobile-slot is-active" hx-swap-oob="outerHTML"`,
		"expected mobile selector to update out of band")
	assertContains(t, body, fmt.Sprintf(`value=%q selected`, feedIDText), "expected selected mobile feed to persist")
	assertContains(t, body, "2 unread", "expected exact count in mobile feed option")
	assertContains(t, body, "Unread counts are on.", "expected accessible preference save status")

	feedResponse := getHTMXRequest(app, fmt.Sprintf("/feeds/%d/items", activeFeedID))
	assertResponseCode(t, feedResponse, "sidebar replacement with exact counts")
	assertContains(t, feedResponse.Body.String(), `data-show-exact-unread-counts="true"`,
		"expected a subsequent sidebar replacement to retain exact mode")
	assertContains(t, feedResponse.Body.String(), `class="feed-count-exact">2</span>`,
		"expected a subsequent sidebar replacement to retain its exact count")

	disableForm := url.Values{
		"enabled":          {"false"},
		"mobile_view":      {"true"},
		"selected_feed_id": {feedIDText},
	}
	disableRequest := readingPreferenceRequest(http.MethodPost, "/preferences/reading/counts", disableForm)
	disableRequest.Header.Set("Hx-Request", "true")
	disableResponse := httptest.NewRecorder()
	app.Routes().ServeHTTP(disableResponse, disableRequest)
	if disableResponse.Code != http.StatusOK {
		t.Fatalf("expected htmx opt-out response 200, got %d", disableResponse.Code)
	}
	assertContains(t, disableResponse.Body.String(), `data-show-exact-unread-counts="false"`,
		"expected opt-out state to be returned")
	assertContains(t, disableResponse.Body.String(), `class="feed-count-new">New</span>`,
		"expected the sidebar to return to New labels")
	assertContains(t, disableResponse.Body.String(), "(New)", "expected mobile options to return to New labels")

	reloaded := getRequest(app, fmt.Sprintf("%s?selected_feed_id=%s", pathMobileStream, feedIDText))
	assertResponseCode(t, reloaded, "mobile stream reload after count preference change")
	assertContains(t, reloaded.Body.String(), "(New)", "expected the saved default to survive a mobile reload")
}

func TestUnreadCountPreferenceKeepsZeroFeedsUnbadged(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedIDs := seedUnreadCountDisplayFeeds(t, app)
	activeFeedID, quietFeedID := feedIDs.active, feedIDs.quiet
	//nolint:exhaustruct,exhaustruct_v5 // Only fields used by this read fixture are populated.
	readItem := &gofeed.Item{
		Title:           "Read item",
		Link:            "http://example.com/quiet-counts/read",
		GUID:            "quiet-counts-read",
		PublishedParsed: new(time.Now().UTC().Add(-time.Hour)),
	}
	mustUpsertItems(t, app, quietFeedID, []*gofeed.Item{readItem})
	quietItems := mustListItems(t, app, quietFeedID)
	markErr := store.MarkItemRead(context.Background(), app.db, quietItems[0].ID)
	if markErr != nil {
		t.Fatalf("mark zero-count feed item read: %v", markErr)
	}

	preferenceErr := store.SetShowExactUnreadCounts(context.Background(), app.db, true)
	if preferenceErr != nil {
		t.Fatalf("enable exact unread counts: %v", preferenceErr)
	}
	response := getRequest(app, pathIndex)
	assertResponseCode(t, response, "zero-count exact preference status")
	body := response.Body.String()
	if got := strings.Count(body, `class="feed-count"`); got != 1 {
		t.Fatalf("expected only the positive unread feed to have a badge, got %d", got)
	}
	assertContains(t, body, fmt.Sprintf(`data-feed-id="%d"`, activeFeedID), "expected unread feed to remain visible")
	assertContains(t, body, `class="feed-count-exact">2</span>`, "expected exact positive count")
	assertNotContains(t, body, `class="feed-count-exact">0</span>`, "expected zero unread count to stay hidden")
}
