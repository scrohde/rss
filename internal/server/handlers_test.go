//nolint:testpackage // Handler integration tests intentionally exercise unexported helpers.
package server

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"

	"rss/internal/content"
	feedpkg "rss/internal/feed"
	"rss/internal/opml"
	"rss/internal/store"
	"rss/internal/testutil"
	"rss/internal/view"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

const (
	pathParentDir        = ".."
	pathIndex            = "/"
	pathPulseFeeds       = "/feeds/pulse"
	pathFeedEditMode     = "/feeds/edit-mode"
	pathEditModeCancel   = "/feeds/edit-mode/cancel"
	pathEditModeSave     = "/feeds/edit-mode/save"
	pathMobileStream     = "/mobile/stream"
	pathMobilePulse      = "/mobile/pulse"
	errIndexStatusFmt    = "index status: %d"
	expectedNoItems      = 0
	expectedSingleFeed   = 1
	expectedSingleItem   = 1
	firstFeedIndex       = 0
	firstItemIndex       = 0
	expectedTwoItems     = 2
	expectedTwoUnread    = 2
	expectedOneUnread    = 1
	errStoreListFeeds    = "store.ListFeeds: %v"
	errStoreUpsertFeed   = "store.UpsertFeed: %v"
	errStoreUpsertItems  = "store.UpsertItems: %v"
	errStoreListItems    = "store.ListItems: %v"
	headerContentType    = "Content-Type"
	headerSetCookie      = "Set-Cookie"
	formURLEncoded       = "application/x-www-form-urlencoded"
	formSelectedFeedID   = "selected_feed_id"
	classIsActive        = "is-active"
	classFeedListEdit    = `class="feed-list edit-mode"`
	decimalBase          = 10
	sqlItemReadAtByID    = "SELECT read_at FROM items WHERE id = ?"
	sqlUpdateItemReadAt  = "UPDATE items SET read_at = ? WHERE id = ?"
	expectedTombstoneMsg = "expected tombstone to be recorded"
	exampleRSSURL        = "http://example.com/rss"
	sourceTitle          = "Source Title"
	customTitle          = "Custom Title"
	manualRefreshTitle   = "Manual Refresh Feed"
	sweepOtherFeedURL    = "http://example.com/other"
	sweepGUIDKeep        = "1"
	sweepGUIDA           = "2"
	sweepGUIDB           = "3"
	sweepGUIDOther       = "4"
	deleteFeedTitle      = "Delete Feed"
	itemLimitFeedTitle   = "Feed"
	pollFeedTitle        = "Poll Feed"
	pulseFeedOneTitle    = "Pulse Feed One"
	pulseFeedTwoTitle    = "Pulse Feed Two"
	emptyStateNoFeed     = "Pick a feed to start reading."
	newFeedTitle         = "New Title"
	itemLimitTotal       = 210
	itemLimitPruned      = 10
	itemLimitKept        = 200
	itemLimitFirstGUID   = "guid-010"
	feedListIDAttr       = `id="feed-list"`
	feedListSwapAttr     = `hx-swap-oob="innerHTML"`
	contentPanelIDAttr   = `id="content-panel"`
	contentPanelSwapAttr = `hx-swap-oob="outerHTML"`
	msgFeedListOOB       = "expected feed list OOB update"
	msgFeedListOOBSwap   = "expected OOB innerHTML swap for feed list"
	expectedItemsFmt     = "expected %d items, got %d"
	msgPollStatus        = "poll status"
	msgFeedItemsStatus   = "feed items status"
	valueEnabled         = "1"
	cookieClearedToken   = "Max-Age=0"
	imageProxyURLQuery   = "?url="
	examplePublicIP      = "93.184.216.34"
	selectedItemIDParam  = "selected_item_id"
	collapseItemIDParam  = "collapse_item_id"
	selectedItemIDPlain  = int64(42)
	selectedItemIDRaw    = "42"
	selectedItemIDPrefix = "item-42"
	threeUnits           = 3
	hoursInThreeDays     = 72
	sqlCountFeedByID     = "SELECT COUNT(*) FROM feeds WHERE id = ?"
	sqlCountItemsByFeed  = "SELECT COUNT(*) FROM items WHERE feed_id = ?"
	sqlCountTombByFeed   = "SELECT COUNT(*) FROM tombstones WHERE feed_id = ?"
)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func editModeCookie() *http.Cookie {
	cookie := new(http.Cookie)
	cookie.Name = feedEditModeCookie
	cookie.Value = "1"

	return cookie
}

func testIPAddr(raw string) net.IPAddr {
	var addr net.IPAddr

	addr.IP = net.ParseIP(raw)

	return addr
}

func newTestHTTPClient(transport roundTripperFunc) *http.Client {
	client := new(http.Client)
	client.Transport = transport

	return client
}

func newTestHTTPResponse(
	req *http.Request,
	statusCode int,
	header http.Header,
	body io.Reader,
) *http.Response {
	resp := new(http.Response)
	resp.StatusCode = statusCode

	resp.Header = header
	if resp.Header == nil {
		resp.Header = make(http.Header)
	}

	if body == nil {
		resp.Body = http.NoBody
	} else {
		resp.Body = io.NopCloser(body)
	}

	resp.Request = req

	return resp
}

//nolint:unparam // Test helper keeps explicit feed-like argument shape.
func newGofeedItem(
	title,
	link,
	guid,
	description string,
	published *time.Time,
) *gofeed.Item {
	item := new(gofeed.Item)
	item.Title = title
	item.Link = link
	item.GUID = guid
	item.Description = description
	item.PublishedParsed = published

	return item
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	db := testutil.OpenTestDB(t)
	tmpl := templateMust()

	return New(db, tmpl)
}

func templateMust() *template.Template {
	tmpl := template.Must(template.ParseGlob(filepath.Join(
		pathParentDir,
		pathParentDir,
		"templates",
		"*.html",
	)))

	return template.Must(tmpl.ParseGlob(filepath.Join(
		pathParentDir,
		pathParentDir,
		"templates",
		"partials",
		"*.html",
	)))
}

func assertSingleFeedCounts(
	t *testing.T,
	db *sql.DB,
	wantItems int,
	wantUnread int,
) {
	t.Helper()

	feeds, err := store.ListFeeds(context.Background(), db)
	if err != nil {
		t.Fatalf(errStoreListFeeds, err)
	}

	if len(feeds) != expectedSingleFeed {
		t.Fatalf("expected 1 feed, got %d", len(feeds))
	}

	if feeds[firstFeedIndex].ItemCount != wantItems {
		t.Fatalf(
			expectedItemsFmt,
			wantItems,
			feeds[firstFeedIndex].ItemCount,
		)
	}

	if feeds[firstFeedIndex].UnreadCount != wantUnread {
		t.Fatalf(
			"expected %d unread items, got %d",
			wantUnread,
			feeds[firstFeedIndex].UnreadCount,
		)
	}
}

func assertContains(t *testing.T, body, token, message string) {
	t.Helper()

	if !strings.Contains(body, token) {
		t.Fatal(message)
	}
}

func assertFeedListOOBUpdate(t *testing.T, body string) {
	t.Helper()

	assertContains(t, body, feedListIDAttr, msgFeedListOOB)
	assertContains(
		t,
		body,
		feedListSwapAttr,
		msgFeedListOOBSwap,
	)
}

func assertMobileStreamSelectorInHeader(t *testing.T, body string) {
	t.Helper()

	headerStart := strings.Index(body, `<header class="mobile-stream-header">`)
	formStart := strings.Index(body, `<form class="mobile-stream-filter"`)

	listStart := strings.Index(body, `<div class="mobile-stream-list">`)
	if headerStart == -1 || formStart == -1 || listStart == -1 {
		t.Fatal("expected mobile stream header, inline filter form, and stream list in response")
	}

	if formStart < headerStart || formStart > listStart {
		t.Fatal("expected feed selector form to render inline within the mobile stream header")
	}
}

type clearedMobileFeedFixture struct {
	app    *App
	feedID int64
}

func newAppWithClearedSingleFeed(
	t *testing.T,
	feedURL, feedTitle, storyTitle, storyURL, guid string,
) clearedMobileFeedFixture {
	t.Helper()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, feedURL, feedTitle)
	mustUpsertSingleStory(
		t,
		app,
		feedID,
		storyTitle,
		storyURL,
		guid,
		time.Now().UTC().Add(-time.Hour),
	)
	mustMarkFeedItemRead(t, app, feedID, guid)

	return clearedMobileFeedFixture{
		app:    app,
		feedID: feedID,
	}
}

func assertGenericMobileEmptyState(t *testing.T, body string) {
	t.Helper()

	assertContains(t, body, "Nothing is owed.", "expected generic empty-state heading to remain")
	assertContains(
		t,
		body,
		"Your stream is clear for now. Come back whenever you want to read again.",
		"expected generic all-feeds empty-state copy",
	)
}

func assertFilteredMobileEmptyState(
	t *testing.T,
	body, feedTitle string,
	selectedFeedID, availableFeedID int64,
) {
	t.Helper()

	assertContains(
		t,
		body,
		feedTitle+" is caught up.",
		"expected feed-specific empty-state heading",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf("There is nothing unread in %s right now.", feedTitle),
		"expected feed-specific empty-state copy",
	)
	assertNotContains(
		t,
		body,
		"Your stream is clear for now. Come back whenever you want to read again.",
		"expected filtered empty state to differ from all-feeds copy",
	)
	assertContains(
		t,
		body,
		">All feeds</option>",
		"expected all-feeds option to remain available",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`<option value="%d"`, availableFeedID),
		"expected selector to keep unread feeds available while filtered feed is empty",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`<option value="%d" selected>%s (caught up)</option>`, selectedFeedID, feedTitle),
		"expected caught-up feed to stay selected in the filter",
	)
}

func assertContentPanelOOBUpdate(t *testing.T, body string) {
	t.Helper()

	assertContains(t, body, contentPanelIDAttr, "expected content panel update")
	assertContains(t, body, contentPanelSwapAttr, "expected content panel OOB swap")
}

func requireNoErr(t *testing.T, err error, format string) {
	t.Helper()

	if err != nil {
		t.Fatalf(format, err)
	}
}

func assertNotContains(t *testing.T, body, token, message string) {
	t.Helper()

	if strings.Contains(body, token) {
		t.Fatal(message)
	}
}

func assertItemCount(t *testing.T, items []view.ItemView, want int) {
	t.Helper()

	if len(items) != want {
		t.Fatalf(expectedItemsFmt, want, len(items))
	}
}

func assertResponseCode(
	t *testing.T,
	rec *httptest.ResponseRecorder,
	message string,
) {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("%s: %d", message, rec.Code)
	}
}

func postRequest(app *App, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	return rec
}

func postHTMXRequest(app *App, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, http.NoBody)
	req.Header.Set("Hx-Request", "true")

	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	return rec
}

func waitForPulseIdle(t *testing.T, app *App) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !app.isPulseRunning() {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatal("timed out waiting for pulse to finish")
}

func getRequest(
	app *App,
	target string,
	cookies ...*http.Cookie,
) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	return rec
}

func getHTMXRequest(app *App, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	req.Header.Set("Hx-Request", "true")

	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	return rec
}

func postFormRequest(
	app *App,
	target string,
	form url.Values,
	cookies ...*http.Cookie,
) *httptest.ResponseRecorder {
	req := newURLEncodedRequest(target, form)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	return rec
}

func pollItemsPath(feedID, newestID int64) string {
	return fmt.Sprintf(
		"/feeds/%d/items/poll?after_id=%d",
		feedID,
		newestID,
	)
}

func newItemsPath(feedID, newestID int64) string {
	return fmt.Sprintf(
		"/feeds/%d/items/new?after_id=%d",
		feedID,
		newestID,
	)
}

func feedItemsPath(feedID int64) string {
	return fmt.Sprintf("/feeds/%d/items", feedID)
}

func mustLoadItemList(t *testing.T, app *App, feedID int64) *view.ItemListData {
	t.Helper()

	list, err := store.LoadItemList(context.Background(), app.db, feedID)
	requireNoErr(t, err, "store.LoadItemList: %v")

	return list
}

func assertFeedRowCount(
	t *testing.T,
	db *sql.DB,
	query string,
	feedID int64,
	want int,
	label string,
) {
	t.Helper()

	var got int

	err := db.QueryRowContext(
		context.Background(),
		query,
		feedID,
	).Scan(&got)
	if err != nil {
		t.Fatalf("%s count: %v", label, err)
	}

	if got != want {
		t.Fatalf("expected %s count %d, got %d", label, want, got)
	}
}

func assertFirstFeedTitle(
	t *testing.T,
	db *sql.DB,
	want string,
	message string,
) {
	t.Helper()

	feeds, err := store.ListFeeds(context.Background(), db)
	requireNoErr(t, err, errStoreListFeeds)

	if len(feeds) == expectedNoItems {
		t.Fatal("expected at least one feed")
	}

	if feeds[firstFeedIndex].Title != want {
		t.Fatalf(message, feeds[firstFeedIndex].Title)
	}
}

func assertEditModeCookieSet(t *testing.T, setCookie string) {
	t.Helper()

	assertContains(
		t,
		setCookie,
		feedEditModeCookie+"="+valueEnabled,
		"expected edit mode cookie to be set",
	)
}

func assertEditModeCookieCleared(t *testing.T, setCookie string) {
	t.Helper()

	assertContains(
		t,
		setCookie,
		feedEditModeCookie+"=",
		"expected edit mode cookie to be cleared",
	)
	assertContains(
		t,
		setCookie,
		cookieClearedToken,
		"expected edit mode cookie to be cleared",
	)
}

func setSelectedFeedID(form url.Values, feedID int64) {
	form.Set(
		formSelectedFeedID,
		strconv.FormatInt(feedID, decimalBase),
	)
}

func assertEnterFeedEditModeBody(t *testing.T, body string, feedID int64) {
	t.Helper()

	assertEnterFeedEditModeLayout(t, body)
	assertEnterFeedEditModePerFeedControls(t, body, feedID)
	assertEnterFeedEditModeGlobalControls(t, body)
}

func assertEnterFeedEditModeLayout(t *testing.T, body string) {
	t.Helper()

	assertContains(
		t,
		body,
		classFeedListEdit,
		"expected edit mode class in feed list",
	)
	assertContains(
		t,
		body,
		`class="feed-edit-actions"`,
		"expected edit actions in edit mode",
	)
	assertContains(t, body, `id="feed-edit-form"`, "expected edit mode form")
	assertContains(
		t,
		body,
		`name="feed_title_`,
		"expected inline feed title input in edit mode",
	)
}

func assertEnterFeedEditModePerFeedControls(
	t *testing.T,
	body string,
	feedID int64,
) {
	t.Helper()

	deleteToggle := fmt.Sprintf(
		`data-feed-delete-toggle="feed-delete-%d"`,
		feedID,
	)
	assertContains(
		t,
		body,
		deleteToggle,
		"expected delete toggle control in edit mode",
	)

	deleteMarker := fmt.Sprintf(`name="feed_delete_%d"`, feedID)
	assertContains(
		t,
		body,
		deleteMarker,
		"expected delete marker input in edit mode",
	)
	assertContains(
		t,
		body,
		`class="feed-drag-handle"`,
		"expected drag handle control in edit mode",
	)

	orderField := fmt.Sprintf(`name="feed_order" value="%d"`, feedID)
	assertContains(
		t,
		body,
		orderField,
		"expected persisted order field in edit mode",
	)

	deleteEndpoint := fmt.Sprintf(`hx-post="/feeds/%d/delete"`, feedID)
	assertNotContains(
		t,
		body,
		deleteEndpoint,
		"expected edit mode delete control to defer deletion until save",
	)
}

func assertEnterFeedEditModeGlobalControls(t *testing.T, body string) {
	t.Helper()

	assertNotContains(
		t,
		body,
		`class="feed-title-revert"`,
		"expected no revert controls when feeds have no custom title overrides",
	)
	assertNotContains(
		t,
		body,
		"feed-rename-button",
		"expected rename button to be removed in edit mode",
	)
	assertContains(
		t,
		body,
		`hx-post="/feeds/edit-mode/cancel"`,
		"expected cancel action in edit mode",
	)
	assertNotContains(
		t,
		body,
		"feed-more-button",
		"expected no More section in edit mode",
	)
	assertNotContains(
		t,
		body,
		`feed-count">`,
		"expected unread counts to be hidden in edit mode",
	)
	assertContains(
		t,
		body,
		"Zero Feed",
		"expected zero unread feeds to be visible in edit mode",
	)
}

func assertFeedEditModeSaveDeleteBody(t *testing.T, body string) {
	t.Helper()

	assertNotContains(
		t,
		body,
		"Delete Me",
		"expected deleted feed to be absent from save response",
	)
	assertContains(
		t,
		body,
		"Keep Me",
		"expected remaining feed in save response",
	)
	assertContains(
		t,
		body,
		`id="main-content" hx-swap-oob="innerHTML"`,
		"expected main content update when selected feed is deleted",
	)
	assertContains(
		t,
		body,
		"Keep Item",
		"expected replacement selected feed item list in response",
	)
	assertNotContains(
		t,
		body,
		classFeedListEdit,
		"expected edit mode to be cleared on save",
	)
}

func assertAllItemsRead(t *testing.T, app *App, feedID int64) {
	t.Helper()

	items := mustListItems(t, app, feedID)
	for idx := range items {
		if !items[idx].IsRead {
			t.Fatal("expected read_at to be set for all items")
		}
	}
}

func assertGUIDExists(
	t *testing.T,
	db *sql.DB,
	feedID int64,
	guid string,
	message string,
) {
	t.Helper()

	if !existsByGUID(t, db, feedID, guid) {
		t.Fatal(message)
	}
}

func assertGUIDMissing(
	t *testing.T,
	db *sql.DB,
	feedID int64,
	guid string,
	message string,
) {
	t.Helper()

	if existsByGUID(t, db, feedID, guid) {
		t.Fatal(message)
	}
}

func assertTombstoneExists(
	t *testing.T,
	db *sql.DB,
	feedID int64,
	guid string,
	message string,
) {
	t.Helper()

	if !existsInTombstones(t, db, feedID, guid) {
		t.Fatal(message)
	}
}

func setupSweepReadFixture(
	t *testing.T,
	app *App,
) sweepReadFixtureIDs {
	t.Helper()

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Sweep Feed")
	otherFeedID := mustUpsertFeed(t, app, sweepOtherFeedURL, "Other Feed")

	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Keep me",
		Link:            "http://example.com/1",
		GUID:            sweepGUIDKeep,
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Sweep me A",
		Link:            "http://example.com/2",
		GUID:            sweepGUIDA,
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}, {
		Title:           "Sweep me B",
		Link:            "http://example.com/3",
		GUID:            sweepGUIDB,
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-3 * time.Hour)),
	}})

	mustUpsertItems(t, app, otherFeedID, []*gofeed.Item{{
		Title:           "Other Feed Item",
		Link:            "http://example.com/4",
		GUID:            sweepGUIDOther,
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})

	return sweepReadFixtureIDs{
		feedID:      feedID,
		otherFeedID: otherFeedID,
	}
}

func markSweepItemsRead(
	t *testing.T,
	app *App,
	feedID int64,
	otherFeedID int64,
	now time.Time,
) {
	t.Helper()

	_, err := app.db.ExecContext(
		context.Background(),
		"UPDATE items SET read_at = ? WHERE feed_id = ? AND guid IN (?, ?)",
		now,
		feedID,
		sweepGUIDA,
		sweepGUIDB,
	)
	requireNoErr(t, err, "set read_at feed: %v")

	_, err = app.db.ExecContext(
		context.Background(),
		"UPDATE items SET read_at = ? WHERE feed_id = ? AND guid = ?",
		now,
		otherFeedID,
		sweepGUIDOther,
	)
	requireNoErr(t, err, "set read_at other feed: %v")
}

func assertSweepReadResults(
	t *testing.T,
	app *App,
	feedID int64,
	otherFeedID int64,
	body string,
) {
	t.Helper()

	sweepAction := fmt.Sprintf(`hx-post="/feeds/%d/items/sweep"`, feedID)
	assertContains(
		t,
		body,
		sweepAction,
		"expected sweep action to remain in response",
	)

	assertGUIDExists(
		t,
		app.db,
		feedID,
		sweepGUIDKeep,
		"expected unread item to remain",
	)
	assertGUIDMissing(
		t,
		app.db,
		feedID,
		sweepGUIDA,
		"expected read items to be deleted from selected feed",
	)
	assertGUIDMissing(
		t,
		app.db,
		feedID,
		sweepGUIDB,
		"expected read items to be deleted from selected feed",
	)
	assertTombstoneExists(
		t,
		app.db,
		feedID,
		sweepGUIDA,
		"expected deleted read items to be tombstoned",
	)
	assertTombstoneExists(
		t,
		app.db,
		feedID,
		sweepGUIDB,
		"expected deleted read items to be tombstoned",
	)
	assertGUIDExists(
		t,
		app.db,
		otherFeedID,
		sweepGUIDOther,
		"expected other feed to be unchanged",
	)
}

func manualRefreshInitialXML(base time.Time) string {
	return testutil.RSSXML(manualRefreshTitle, []testutil.RSSItem{{
		Title:       "First",
		Link:        "http://example.com/1",
		GUID:        "1",
		PubDate:     base.Format(time.RFC1123Z),
		Description: "<p>First summary</p>",
	}})
}

func manualRefreshUpdatedXML(base time.Time) string {
	return testutil.RSSXML(manualRefreshTitle, []testutil.RSSItem{
		{
			Title:       "Second",
			Link:        "http://example.com/2",
			GUID:        "2",
			PubDate:     base.Add(time.Minute).Format(time.RFC1123Z),
			Description: "<p>Second summary</p>",
		},
		{
			Title:       "First",
			Link:        "http://example.com/1",
			GUID:        "1",
			PubDate:     base.Format(time.RFC1123Z),
			Description: "<p>First summary</p>",
		},
	})
}

func assertManualRefreshBody(t *testing.T, body string, feedID int64) {
	t.Helper()

	assertContains(
		t,
		body,
		"Second",
		"expected refreshed item in response",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`hx-post="/feeds/%d/refresh"`, feedID),
		"expected manual refresh button in response",
	)
	assertFeedListOOBUpdate(t, body)
}

type feedSelectionFixtureIDs struct {
	otherFeedID    int64
	selectedFeedID int64
}

type sweepReadFixtureIDs struct {
	feedID      int64
	otherFeedID int64
}

type pollingFixtureIDs struct {
	feedID   int64
	newestID int64
}

type feedOrderFixtureIDs struct {
	firstID  int64
	secondID int64
	thirdID  int64
}

func queryItemReadAt(t *testing.T, db *sql.DB, itemID int64) sql.NullTime {
	t.Helper()

	var readAt sql.NullTime

	err := db.QueryRowContext(
		context.Background(),
		sqlItemReadAtByID,
		itemID,
	).Scan(&readAt)
	if err != nil {
		t.Fatalf("read_at query: %v", err)
	}

	return readAt
}

func newURLEncodedRequest(
	target string,
	form url.Values,
) *http.Request {
	req := httptest.NewRequest(
		http.MethodPost,
		target,
		strings.NewReader(form.Encode()),
	)
	req.Header.Set(headerContentType, formURLEncoded)

	return req
}

func mustUpsertFeed(t *testing.T, app *App, feedURL, title string) int64 {
	t.Helper()

	feedID, err := store.UpsertFeed(context.Background(), app.db, feedURL, title)
	if err != nil {
		t.Fatalf(errStoreUpsertFeed, err)
	}

	return feedID
}

func mustUpsertItems(
	t *testing.T,
	app *App,
	feedID int64,
	items []*gofeed.Item,
) {
	t.Helper()

	_, err := store.UpsertItems(context.Background(), app.db, feedID, items)
	if err != nil {
		t.Fatalf(errStoreUpsertItems, err)
	}
}

func mustUpsertSingleStory(
	t *testing.T,
	app *App,
	feedID int64,
	title, link, guid string,
	published time.Time,
) {
	t.Helper()

	mustUpsertItems(t, app, feedID, []*gofeed.Item{
		newGofeedItem(title, link, guid, "<p>Summary</p>", &published),
	})
}

func mustMarkFeedItemRead(t *testing.T, app *App, feedID int64, guid string) {
	t.Helper()

	_, err := app.db.ExecContext(
		context.Background(),
		"UPDATE items SET read_at = ? WHERE feed_id = ? AND guid = ?",
		time.Now().UTC(),
		feedID,
		guid,
	)
	requireNoErr(t, err, "set read_at: %v")
}

func mustListItems(t *testing.T, app *App, feedID int64) []view.ItemView {
	t.Helper()

	items, err := store.ListItems(context.Background(), app.db, feedID)
	if err != nil {
		t.Fatalf(errStoreListItems, err)
	}

	return items
}

func upsertSingleCleanupItem(t *testing.T, app *App, feedID int64) {
	t.Helper()

	_, err := store.UpsertItems(context.Background(), app.db, feedID, []*gofeed.Item{{
		Title:           "Item",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
		UpdatedParsed:   new(time.Now().Add(-time.Hour)),
	}})

	requireNoErr(t, err, errStoreUpsertItems)
}

func assertToggleReadFeedListBody(t *testing.T, body string) {
	t.Helper()

	assertFeedListOOBUpdate(t, body)
	assertContentPanelOOBUpdate(t, body)
	assertNotContains(
		t,
		body,
		`feed-count">2`,
		"expected unread count to decrease",
	)
	assertContains(
		t,
		body,
		`feed-count">1`,
		"expected unread count to be 1",
	)
	assertContains(
		t,
		body,
		classIsActive,
		"expected toggled item to stay active",
	)
}

func assertExpandedItemBody(t *testing.T, body string, itemID int64) {
	t.Helper()

	assertContains(
		t,
		body,
		"item-entry is-expanded",
		"expected expanded item row class",
	)
	assertContains(
		t,
		body,
		classIsActive,
		"expected expanded item to include active class",
	)

	expectedVals := fmt.Sprintf(
		`hx-vals='{"selected_item_id":"item-%d"}'`,
		itemID,
	)
	assertContains(
		t,
		body,
		expectedVals,
		"expected expanded item collapse request to include selected_item_id",
	)
}

func assertExpandedPanelActions(t *testing.T, body string, itemID int64) {
	t.Helper()

	assertContains(
		t,
		body,
		`data-content-panel-full-toggle="true"`,
		"expected full-page toggle action in expanded content panel",
	)
	assertContains(
		t,
		body,
		`aria-pressed="false"`,
		"expected full-page toggle to render with default unpressed state",
	)
	assertContains(
		t,
		body,
		`data-content-panel-close="true"`,
		"expected close action in expanded content panel",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`hx-get="/items/%d/compact"`, itemID),
		"expected close action to request compact row response",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`hx-target="#item-%d"`, itemID),
		"expected close action to target the expanded row",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`hx-vals='{"selected_item_id":"item-%d"}'`, itemID),
		"expected close action to preserve selected item id",
	)
}

func assertItemArticleNotActive(t *testing.T, body string, itemID int64) {
	t.Helper()

	marker := fmt.Sprintf(`id="item-%d"`, itemID)

	itemIndex := strings.Index(body, marker)
	if itemIndex == -1 {
		t.Fatalf("expected item article marker %q in response", marker)
	}

	articleStart := strings.LastIndex(body[:itemIndex], "<article")
	if articleStart == -1 {
		t.Fatalf("expected article start before item marker %q", marker)
	}

	articleEndOffset := strings.Index(body[itemIndex:], "</article>")
	if articleEndOffset == -1 {
		t.Fatalf("expected article end after item marker %q", marker)
	}

	articleEnd := itemIndex + articleEndOffset + len("</article>")

	articleHTML := body[articleStart:articleEnd]
	if strings.Contains(articleHTML, classIsActive) {
		t.Fatalf("expected item-%d article to not include %q", itemID, classIsActive)
	}
}

func subscribeFeedItems(now time.Time) []testutil.RSSItem {
	return []testutil.RSSItem{
		{
			Title:       "Alpha",
			Link:        "http://example.com/alpha",
			GUID:        "alpha",
			PubDate:     now.UTC().Format(time.RFC1123Z),
			Description: "<p>Alpha summary</p>",
		},
		{
			Title:       "Beta",
			Link:        "http://example.com/beta",
			GUID:        "beta",
			PubDate:     now.Add(-time.Hour).UTC().Format(time.RFC1123Z),
			Description: "<p>Beta summary</p>",
		},
	}
}

func activeFeedButton(feedID int64) string {
	return fmt.Sprintf(
		"class=\"feed-link active\" type=\"button\" data-feed-id=\"%d\"",
		feedID,
	)
}

func setupFeedSelectionFixtures(
	t *testing.T,
	app *App,
) feedSelectionFixtureIDs {
	t.Helper()

	otherFeedID, err := store.UpsertFeed(context.Background(),
		app.db,
		"http://example.com/rss-other",
		"Other Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed other: %v", err)
	}

	selectedFeedID, err := store.UpsertFeed(context.Background(),
		app.db,
		"http://example.com/rss-selected",
		"Selected Feed")
	if err != nil {
		t.Fatalf("store.UpsertFeed selected: %v", err)
	}

	_, upsertErr := store.UpsertItems(context.Background(), app.db, otherFeedID, []*gofeed.Item{{
		Title:           "Other Item",
		Link:            "http://example.com/other",
		GUID:            "other-item",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})
	if upsertErr != nil {
		t.Fatalf("store.UpsertItems other: %v", upsertErr)
	}

	_, upsertErr = store.UpsertItems(context.Background(), app.db, selectedFeedID, []*gofeed.Item{{
		Title:           "Selected Item",
		Link:            "http://example.com/selected",
		GUID:            "selected-item",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	if upsertErr != nil {
		t.Fatalf("store.UpsertItems selected: %v", upsertErr)
	}

	return feedSelectionFixtureIDs{
		otherFeedID:    otherFeedID,
		selectedFeedID: selectedFeedID,
	}
}

func TestSubscribeAndList(t *testing.T) {
	t.Parallel()

	items := subscribeFeedItems(time.Now())
	_, feedURL := testutil.NewFeedServer(t, testutil.RSSXML("Test Feed", items))

	app := newTestApp(t)

	form := url.Values{}
	form.Set("url", feedURL)
	req := httptest.NewRequest(
		http.MethodPost,
		"/feeds",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set(headerContentType, formURLEncoded)

	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if strings.Contains(rec.Body.String(), "Subscribed to ") {
		t.Fatal("expected subscribe success message to be omitted")
	}

	feeds, err := store.ListFeeds(context.Background(), app.db)
	if err != nil {
		t.Fatalf(errStoreListFeeds, err)
	}

	if len(feeds) != expectedSingleFeed {
		t.Fatalf("expected 1 feed, got %d", len(feeds))
	}

	if feeds[firstFeedIndex].Title != "Test Feed" {
		t.Fatalf("expected feed title, got %q", feeds[firstFeedIndex].Title)
	}

	itemsInDB, err := store.ListItems(context.Background(), app.db, feeds[firstFeedIndex].ID)
	if err != nil {
		t.Fatalf(errStoreListItems, err)
	}

	if len(itemsInDB) != expectedTwoItems {
		t.Fatalf("expected 2 items, got %d", len(itemsInDB))
	}
}

func TestSubscribeBlockedByRobotsPolicy(t *testing.T) {
	t.Parallel()

	items := subscribeFeedItems(time.Now())
	feedServer, feedURL := testutil.NewFeedServer(t, testutil.RSSXML("Test Feed", items))
	feedServer.SetRobotsTxt("User-agent: *\nDisallow: /\n")

	app := newTestApp(t)

	form := url.Values{}
	form.Set("url", feedURL)
	req := httptest.NewRequest(
		http.MethodPost,
		"/feeds",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set(headerContentType, formURLEncoded)

	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	assertContains(
		t,
		rec.Body.String(),
		"Polling blocked by robots.txt",
		"expected robots block message in subscribe response",
	)

	feeds, err := store.ListFeeds(context.Background(), app.db)
	if err != nil {
		t.Fatalf(errStoreListFeeds, err)
	}

	if len(feeds) != 0 {
		t.Fatalf("expected 0 feeds when subscription is blocked, got %d", len(feeds))
	}
}

func TestListFeedsUnreadCount(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(),
		app.db,
		exampleRSSURL,
		"Unread Feed")
	if err != nil {
		t.Fatalf(errStoreUpsertFeed, err)
	}

	_, upsertErr := store.UpsertItems(context.Background(), app.db, feedID, []*gofeed.Item{{
		Title:           "Unread A",
		Link:            "http://example.com/a",
		GUID:            "a",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Unread B",
		Link:            "http://example.com/b",
		GUID:            "b",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})
	if upsertErr != nil {
		t.Fatalf(errStoreUpsertItems, upsertErr)
	}

	assertSingleFeedCounts(
		t,
		app.db,
		expectedTwoItems,
		expectedTwoUnread,
	)

	items, err := store.ListItems(context.Background(), app.db, feedID)
	if err != nil {
		t.Fatalf(errStoreListItems, err)
	}

	toggleErr := store.ToggleRead(context.Background(), app.db, items[firstFeedIndex].ID)
	if toggleErr != nil {
		t.Fatalf("store.ToggleRead: %v", toggleErr)
	}

	assertSingleFeedCounts(
		t,
		app.db,
		expectedTwoItems,
		expectedOneUnread,
	)
}

func TestFeedItemsUpdatesFeedListSelection(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	fixtureIDs := setupFeedSelectionFixtures(t, app)
	otherFeedID := fixtureIDs.otherFeedID
	selectedFeedID := fixtureIDs.selectedFeedID

	req := httptest.NewRequest(
		http.MethodGet,
		feedItemsPath(selectedFeedID),
		http.NoBody,
	)
	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	assertResponseCode(t, rec, msgFeedItemsStatus)

	body := rec.Body.String()
	assertContains(
		t,
		body,
		"Selected Item",
		"expected selected feed items in response",
	)
	assertFeedListOOBUpdate(t, body)

	selectedButton := activeFeedButton(selectedFeedID)
	assertContains(
		t,
		body,
		selectedButton,
		"expected selected feed to be active in feed list",
	)

	otherButton := activeFeedButton(otherFeedID)
	assertNotContains(
		t,
		body,
		otherButton,
		"expected non-selected feed not to be active",
	)
}

func TestRenameFeedOverridesSourceTitle(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(),
		app.db,
		exampleRSSURL,
		sourceTitle)
	if err != nil {
		t.Fatalf(errStoreUpsertFeed, err)
	}

	err = store.UpdateFeedTitle(context.Background(), app.db, feedID, customTitle)
	if err != nil {
		t.Fatalf("store.UpdateFeedTitle: %v", err)
	}

	feeds, err := store.ListFeeds(context.Background(), app.db)
	if err != nil {
		t.Fatalf(errStoreListFeeds, err)
	}

	if feeds[firstFeedIndex].Title != customTitle {
		t.Fatalf(
			"expected custom title, got %q",
			feeds[firstFeedIndex].Title,
		)
	}

	_, err = store.UpsertFeed(context.Background(),
		app.db,
		exampleRSSURL,
		"Updated Source")
	if err != nil {
		t.Fatalf("store.UpsertFeed update: %v", err)
	}

	feeds, err = store.ListFeeds(context.Background(), app.db)
	if err != nil {
		t.Fatalf("store.ListFeeds again: %v", err)
	}

	if feeds[firstFeedIndex].Title != customTitle {
		t.Fatalf(
			"expected custom title after refresh, got %q",
			feeds[firstFeedIndex].Title,
		)
	}
}

func TestToggleReadUpdatesFeedList(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Toggle Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "One",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Two",
		Link:            "http://example.com/2",
		GUID:            "2",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})

	items := mustListItems(t, app, feedID)

	assertItemCount(t, items, expectedTwoItems)

	form := url.Values{}
	form.Set("view", "compact")
	form.Set(
		selectedItemIDParam,
		fmt.Sprintf("item-%d", items[firstItemIndex].ID),
	)
	req := newURLEncodedRequest(
		fmt.Sprintf("/items/%d/toggle", items[firstItemIndex].ID),
		form,
	)

	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("toggle read status: %d", rec.Code)
	}

	body := rec.Body.String()
	assertToggleReadFeedListBody(t, body)
}

func TestToggleReadExpandedView(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Toggle Expanded Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Expanded",
		Link:            "http://example.com/expanded",
		GUID:            "expanded",
		Description:     "<p>Expanded summary</p>",
		Content:         "<p>Expanded content</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	items := mustListItems(t, app, feedID)

	assertItemCount(t, items, expectedSingleItem)

	form := url.Values{}
	form.Set("view", "expanded")
	form.Set(
		selectedItemIDParam,
		strconv.FormatInt(items[firstItemIndex].ID, decimalBase),
	)
	req := newURLEncodedRequest(
		fmt.Sprintf("/items/%d/toggle", items[firstItemIndex].ID),
		form,
	)

	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("toggle read status: %d", rec.Code)
	}

	body := rec.Body.String()
	assertContains(
		t,
		body,
		"item-entry is-expanded",
		"expected expanded item response",
	)
	assertContains(
		t,
		body,
		classIsActive,
		"expected expanded toggled item to stay active",
	)
	assertContains(
		t,
		body,
		"content-panel is-open",
		"expected expanded toggle response to open content panel",
	)
	assertContentPanelOOBUpdate(t, body)
}

func TestItemExpandedKeepsActiveClass(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Expanded Active Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Expanded",
		Link:            "http://example.com/expanded",
		GUID:            "expanded-active",
		Description:     "<p>Expanded summary</p>",
		Content:         "<p>Expanded content</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	items := mustListItems(t, app, feedID)

	assertItemCount(t, items, expectedSingleItem)

	itemPath := fmt.Sprintf(
		"/items/%d?selected_item_id=item-%d",
		items[firstItemIndex].ID,
		items[firstItemIndex].ID,
	)
	req := httptest.NewRequest(http.MethodGet, itemPath, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expanded status: %d", rec.Code)
	}

	body := rec.Body.String()
	assertExpandedItemBody(t, body, items[firstItemIndex].ID)
	assertContentPanelOOBUpdate(t, body)
}

func TestItemExpandedRendersPanelActionButtons(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Expanded Actions Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Expanded Actions Item",
		Link:            "http://example.com/expanded-actions",
		GUID:            "expanded-actions",
		Description:     "<p>Expanded actions summary</p>",
		Content:         "<p>Expanded actions content</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	items := mustListItems(t, app, feedID)

	assertItemCount(t, items, expectedSingleItem)

	itemID := items[firstItemIndex].ID
	itemPath := fmt.Sprintf("/items/%d?selected_item_id=item-%d", itemID, itemID)
	req := httptest.NewRequest(http.MethodGet, itemPath, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expanded status: %d", rec.Code)
	}

	body := rec.Body.String()
	assertExpandedPanelActions(t, body, itemID)
}

func TestItemExpandedCompactsCollapsedItemViaOOB(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Expanded Collapse Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Expanded Item",
		Link:            "http://example.com/expanded-oob",
		GUID:            "expanded-oob",
		Description:     "<p>Expanded summary</p>",
		Content:         "<p>Expanded content</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Collapsed Item",
		Link:            "http://example.com/collapsed-oob",
		GUID:            "collapsed-oob",
		Description:     "<p>Collapsed summary</p>",
		Content:         "<p>Collapsed content</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})
	items := mustListItems(t, app, feedID)

	assertItemCount(t, items, expectedTwoItems)

	targetID := items[firstItemIndex].ID
	collapseID := items[firstItemIndex+1].ID
	itemPath := fmt.Sprintf(
		"/items/%d?selected_item_id=item-%d&%s=%d",
		targetID,
		targetID,
		collapseItemIDParam,
		collapseID,
	)
	req := httptest.NewRequest(http.MethodGet, itemPath, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expanded status: %d", rec.Code)
	}

	body := rec.Body.String()
	assertExpandedItemBody(t, body, targetID)
	assertContentPanelOOBUpdate(t, body)
	assertContains(
		t,
		body,
		fmt.Sprintf(`id="item-%d"`, collapseID),
		"expected collapsed row OOB update in expanded response",
	)
	assertContains(
		t,
		body,
		"item-entry item-entry-compact",
		"expected collapsed row to render compact markup",
	)
	assertItemArticleNotActive(t, body, collapseID)
}

func TestItemCompactClosesContentPanel(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Compact Panel Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Compact Panel Item",
		Link:            "http://example.com/compact-panel",
		GUID:            "compact-panel",
		Description:     "<p>Compact panel summary</p>",
		Content:         "<p>Compact panel content</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	items := mustListItems(t, app, feedID)
	assertItemCount(t, items, expectedSingleItem)

	itemPath := fmt.Sprintf(
		"/items/%d/compact?selected_item_id=item-%d",
		items[firstItemIndex].ID,
		items[firstItemIndex].ID,
	)
	req := httptest.NewRequest(http.MethodGet, itemPath, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("compact status: %d", rec.Code)
	}

	body := rec.Body.String()
	assertContentPanelOOBUpdate(t, body)
	assertContains(
		t,
		body,
		classIsActive,
		"expected compact response to keep selected item active",
	)
	assertNotContains(
		t,
		body,
		"content-panel is-open",
		"expected compact response to close content panel",
	)
}

func TestItemCompactExpandRequestIncludesSelectedItemID(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(),
		app.db,
		exampleRSSURL,
		"Compact Selected Feed")
	if err != nil {
		t.Fatalf(errStoreUpsertFeed, err)
	}

	_, upsertErr := store.UpsertItems(context.Background(), app.db, feedID, []*gofeed.Item{{
		Title:           "Compact Item",
		Link:            "http://example.com/compact",
		GUID:            "compact-selected",
		Description:     "<p>Compact summary</p>",
		Content:         "<p>Compact content</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	if upsertErr != nil {
		t.Fatalf(errStoreUpsertItems, upsertErr)
	}

	itemsPath := feedItemsPath(feedID)
	req := httptest.NewRequest(http.MethodGet, itemsPath, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	assertResponseCode(t, rec, msgFeedItemsStatus)

	body := rec.Body.String()
	assertContains(
		t,
		body,
		`hx-vals='{"selected_item_id":"item-`,
		"expected compact item expand request to include selected_item_id",
	)
}

func TestToggleReadAndCleanup(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(), app.db, exampleRSSURL, itemLimitFeedTitle)
	requireNoErr(t, err, errStoreUpsertFeed)

	upsertSingleCleanupItem(t, app, feedID)

	items, err := store.ListItems(context.Background(), app.db, feedID)
	requireNoErr(t, err, errStoreListItems)

	assertItemCount(t, items, expectedSingleItem)

	itemID := items[firstItemIndex].ID
	err = store.ToggleRead(context.Background(), app.db, itemID)
	requireNoErr(t, err, "store.ToggleRead: %v")

	readAt := queryItemReadAt(t, app.db, itemID)
	if !readAt.Valid {
		t.Fatal("expected read_at to be set")
	}

	err = store.ToggleRead(context.Background(), app.db, itemID)
	requireNoErr(t, err, "store.ToggleRead again: %v")

	readAt = queryItemReadAt(t, app.db, itemID)
	if readAt.Valid {
		t.Fatal("expected read_at to be cleared")
	}

	// Mark item as read in the past to trigger cleanup.
	past := time.Now().UTC().Add(-3 * time.Hour)
	_, err = app.db.ExecContext(
		context.Background(),
		sqlUpdateItemReadAt,
		past,
		itemID,
	)
	requireNoErr(t, err, "set read_at: %v")

	err = store.CleanupReadItems(app.db)
	requireNoErr(t, err, "store.CleanupReadItems: %v")

	items, err = store.ListItems(context.Background(), app.db, feedID)
	requireNoErr(t, err, "store.ListItems after cleanup: %v")

	assertItemCount(t, items, expectedNoItems)

	if !existsInTombstones(t, app.db, feedID, "1") {
		t.Fatal(expectedTombstoneMsg)
	}

	upsertSingleCleanupItem(t, app, feedID)

	items, err = store.ListItems(context.Background(), app.db, feedID)
	requireNoErr(t, err, "store.ListItems after reinserting: %v")

	assertItemCount(t, items, expectedNoItems)
}

func TestMarkAllRead(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, itemLimitFeedTitle)

	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Item A",
		Link:            "http://example.com/1",
		GUID:            "1",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}, {
		Title:           "Item B",
		Link:            "http://example.com/2",
		GUID:            "2",
		Description:     "<p>Summary</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})

	items := mustListItems(t, app, feedID)
	assertItemCount(t, items, expectedTwoItems)

	past := time.Now().UTC().Add(-30 * time.Minute)
	_, err := app.db.ExecContext(
		context.Background(),
		sqlUpdateItemReadAt,
		past,
		items[firstItemIndex].ID,
	)
	requireNoErr(t, err, "set read_at: %v")

	rec := postRequest(
		app,
		fmt.Sprintf("/feeds/%d/items/read", feedID),
	)
	assertResponseCode(t, rec, "mark all read status")

	assertAllItemsRead(t, app, feedID)
}

func TestSweepReadItems(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	fixture := setupSweepReadFixture(t, app)
	feedID := fixture.feedID
	otherFeedID := fixture.otherFeedID

	now := time.Now().UTC()
	markSweepItemsRead(t, app, feedID, otherFeedID, now)

	rec := postRequest(
		app,
		fmt.Sprintf("/feeds/%d/items/sweep", feedID),
	)
	assertResponseCode(t, rec, "sweep read status")

	assertSweepReadResults(t, app, feedID, otherFeedID, rec.Body.String())
}

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

func TestEnterFeedEditMode(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Edit Mode Feed")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Unread",
		Link:            "http://example.com/unread",
		GUID:            "unread",
		Description:     "<p>Unread summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})

	zeroFeedID := mustUpsertFeed(t, app, "http://example.com/zero", "Zero Feed")
	if zeroFeedID == expectedNoItems {
		t.Fatal("expected zero feed id to be set")
	}

	form := url.Values{}
	setSelectedFeedID(form, feedID)
	rec := postFormRequest(app, pathFeedEditMode, form)
	assertResponseCode(t, rec, "edit mode status")

	body := rec.Body.String()
	assertEnterFeedEditModeBody(t, body, feedID)
	assertEditModeCookieSet(t, rec.Header().Get(headerSetCookie))

	itemsPath := feedItemsPath(feedID)
	itemsRec := getRequest(app, itemsPath, editModeCookie())
	assertResponseCode(t, itemsRec, msgFeedItemsStatus)
	assertContains(
		t,
		itemsRec.Body.String(),
		classFeedListEdit,
		"expected edit mode to persist while cookie is set",
	)
}

func TestCancelFeedEditModeEndpoint(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(
		t,
		app,
		exampleRSSURL,
		"Cancel Edit Mode Feed",
	)

	form := url.Values{}
	setSelectedFeedID(form, feedID)
	rec := postFormRequest(
		app,
		pathEditModeCancel,
		form,
		editModeCookie(),
	)
	assertResponseCode(t, rec, "cancel edit mode status")

	body := rec.Body.String()
	assertNotContains(
		t,
		body,
		classFeedListEdit,
		"expected edit mode class to be cleared",
	)
	assertNotContains(
		t,
		body,
		`class="feed-title-revert"`,
		"expected no revert controls outside edit mode",
	)
	assertContains(
		t,
		body,
		`class="edit-feeds-button"`,
		"expected pencil edit control after cancel",
	)
	assertNotContains(
		t,
		body,
		`class="feed-drag-handle"`,
		"expected drag handles to be hidden outside edit mode",
	)

	assertEditModeCookieCleared(t, rec.Header().Get(headerSetCookie))
}

func TestFeedEditModeCancelDiscardsPendingRenames(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Cancel Feed")

	form := url.Values{}
	setSelectedFeedID(form, feedID)
	form.Set(fmt.Sprintf("feed_title_%d", feedID), "Changed But Canceled")
	form.Set(fmt.Sprintf("feed_delete_%d", feedID), valueEnabled)
	rec := postFormRequest(
		app,
		pathEditModeCancel,
		form,
		editModeCookie(),
	)
	assertResponseCode(t, rec, "cancel status")

	body := rec.Body.String()
	assertNotContains(
		t,
		body,
		classFeedListEdit,
		"expected edit mode to be cleared on cancel",
	)
	assertEditModeCookieCleared(t, rec.Header().Get(headerSetCookie))

	feeds, err := store.ListFeeds(context.Background(), app.db)
	requireNoErr(t, err, errStoreListFeeds)

	if len(feeds) != expectedSingleFeed {
		t.Fatalf(
			"expected feed to remain after cancel, got %d feeds",
			len(feeds),
		)
	}

	if feeds[firstFeedIndex].Title != "Cancel Feed" {
		t.Fatalf(
			"expected pending rename to be discarded, got %q",
			feeds[firstFeedIndex].Title,
		)
	}
}

func TestFeedEditModeSaveAppliesRenamesAndExits(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, "Old Title")
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title:           "Unread",
		Link:            "http://example.com/unread",
		GUID:            "unread",
		Description:     "<p>Unread summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})

	form := url.Values{}
	form.Set(fmt.Sprintf("feed_title_%d", feedID), newFeedTitle)
	setSelectedFeedID(form, feedID)
	rec := postFormRequest(app, pathEditModeSave, form, editModeCookie())
	assertResponseCode(t, rec, "save status")

	body := rec.Body.String()
	assertContains(t, body, newFeedTitle, "expected renamed title in response")
	assertNotContains(
		t,
		body,
		classFeedListEdit,
		"expected edit mode to be cleared on save",
	)
	assertEditModeCookieCleared(t, rec.Header().Get(headerSetCookie))

	feeds, err := store.ListFeeds(context.Background(), app.db)
	requireNoErr(t, err, errStoreListFeeds)

	if feeds[firstFeedIndex].Title != newFeedTitle {
		t.Fatalf(
			"expected rename to persist on save, got %q",
			feeds[firstFeedIndex].Title,
		)
	}
}

func TestFeedEditModeSaveDeletesMarkedFeeds(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	deleteFeedID := mustUpsertFeed(
		t,
		app,
		"http://example.com/delete",
		"Delete Me",
	)
	keepFeedID := mustUpsertFeed(
		t,
		app,
		"http://example.com/keep",
		"Keep Me",
	)
	mustUpsertItems(t, app, keepFeedID, []*gofeed.Item{{
		Title:           "Keep Item",
		Link:            "http://example.com/keep-item",
		GUID:            "keep-item",
		Description:     "<p>Keep summary</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})

	form := url.Values{}
	setSelectedFeedID(form, deleteFeedID)
	form.Set(fmt.Sprintf("feed_delete_%d", deleteFeedID), valueEnabled)
	rec := postFormRequest(app, pathEditModeSave, form, editModeCookie())
	assertResponseCode(t, rec, "save status")

	body := rec.Body.String()
	assertFeedEditModeSaveDeleteBody(t, body)
	assertEditModeCookieCleared(t, rec.Header().Get(headerSetCookie))

	feeds, err := store.ListFeeds(context.Background(), app.db)
	requireNoErr(t, err, errStoreListFeeds)

	if len(feeds) != expectedSingleFeed {
		t.Fatalf("expected one feed after save delete, got %d", len(feeds))
	}

	if feeds[firstFeedIndex].ID != keepFeedID {
		t.Fatalf(
			"expected remaining feed %d, got %d",
			keepFeedID,
			feeds[firstFeedIndex].ID,
		)
	}
}

func TestFeedEditModeSavePersistsFeedOrder(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	fixture := seedFeedOrderFixtures(t, app)

	assertFeedEditModeOrderRequest(
		t,
		app,
		pathEditModeSave,
		fixture.firstID,
		[]int64{fixture.thirdID, fixture.firstID, fixture.secondID},
		[]int64{fixture.thirdID, fixture.firstID, fixture.secondID},
		"save",
	)
}

func TestFeedEditModeCancelIgnoresPendingFeedOrder(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	fixture := seedFeedOrderFixtures(t, app)

	assertFeedEditModeOrderRequest(
		t,
		app,
		pathEditModeCancel,
		fixture.firstID,
		[]int64{fixture.thirdID, fixture.firstID, fixture.secondID},
		[]int64{fixture.firstID, fixture.secondID, fixture.thirdID},
		"cancel",
	)
}

func seedFeedOrderFixtures(t *testing.T, app *App) feedOrderFixtureIDs {
	t.Helper()

	firstID := mustUpsertFeed(t, app, "http://example.com/first", "First")
	secondID := mustUpsertFeed(t, app, "http://example.com/second", "Second")
	thirdID := mustUpsertFeed(t, app, "http://example.com/third", "Third")

	return feedOrderFixtureIDs{
		firstID:  firstID,
		secondID: secondID,
		thirdID:  thirdID,
	}
}

func newEditModeOrderRequest(
	t *testing.T,
	path string,
	selectedID int64,
	orderedFeedIDs ...int64,
) *http.Request {
	t.Helper()

	form := url.Values{}
	setSelectedFeedID(form, selectedID)

	for _, feedID := range orderedFeedIDs {
		form.Add("feed_order", strconv.FormatInt(feedID, decimalBase))
	}

	req := newURLEncodedRequest(path, form)
	req.AddCookie(editModeCookie())

	return req
}

func assertFeedEditModeOrderRequest(
	t *testing.T,
	app *App,
	path string,
	selectedID int64,
	pendingOrder []int64,
	expectedOrder []int64,
	action string,
) {
	t.Helper()

	req := newEditModeOrderRequest(t, path, selectedID, pendingOrder...)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	assertResponseCode(t, rec, action+" status")

	feeds, err := store.ListFeeds(context.Background(), app.db)
	requireNoErr(t, err, errStoreListFeeds)

	if len(feeds) != len(expectedOrder) {
		t.Fatalf("expected %d feeds, got %d", len(expectedOrder), len(feeds))
	}

	for idx, feedID := range expectedOrder {
		if feeds[idx].ID == feedID {
			continue
		}

		gotOrder := []int64{feeds[0].ID, feeds[1].ID, feeds[2].ID}
		t.Fatalf("unexpected feed order after %s: got %v", action, gotOrder)
	}
}

func assertFeedEditModeRevertUI(t *testing.T, body string, feedID int64) {
	t.Helper()

	target := fmt.Sprintf(`data-feed-title-input="feed-title-%d"`, feedID)
	assertContains(t, body, target, "expected revert control target")
	assertContains(
		t,
		body,
		fmt.Sprintf(`data-original-title=%q`, sourceTitle),
		"expected canonical source title in revert control",
	)
	assertContains(
		t,
		body,
		`title="Revert to original feed title"`,
		"expected revert control title text",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`aria-label="Revert feed name to original title: %s"`, sourceTitle),
		"expected revert control aria label to include canonical title",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`value=%q`, customTitle),
		"expected editable value to remain the current custom title",
	)
}

func TestFeedEditModeShowsRevertToCanonicalTitle(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, sourceTitle)
	err := store.UpdateFeedTitle(context.Background(), app.db, feedID, customTitle)
	requireNoErr(t, err, "store.UpdateFeedTitle: %v")

	form := url.Values{}
	setSelectedFeedID(form, feedID)
	rec := postFormRequest(app, pathFeedEditMode, form)
	assertResponseCode(t, rec, "edit mode status")

	body := rec.Body.String()
	assertFeedEditModeRevertUI(t, body, feedID)
}

func TestFeedEditModeSaveCanonicalTitleClearsCustomOverride(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, exampleRSSURL, sourceTitle)
	err := store.UpdateFeedTitle(context.Background(), app.db, feedID, customTitle)
	requireNoErr(t, err, "store.UpdateFeedTitle: %v")

	form := url.Values{}
	form.Set(fmt.Sprintf("feed_title_%d", feedID), sourceTitle)
	setSelectedFeedID(form, feedID)
	rec := postFormRequest(app, pathEditModeSave, form, editModeCookie())
	assertResponseCode(t, rec, "save status")
	assertFirstFeedTitle(
		t,
		app.db,
		sourceTitle,
		"expected canonical title after save, got %q",
	)

	_, err = store.UpsertFeed(context.Background(),
		app.db,
		exampleRSSURL,
		"Updated Source Title")

	requireNoErr(t, err, "store.UpsertFeed update: %v")
	assertFirstFeedTitle(
		t,
		app.db,
		"Updated Source Title",
		"expected custom title override to be cleared, got %q",
	)
}

func TestIndexOmitsInlineDeleteControls(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(),
		app.db,
		exampleRSSURL,
		"Delete Control Feed")
	if err != nil {
		t.Fatalf(errStoreUpsertFeed, err)
	}

	req := httptest.NewRequest(http.MethodGet, pathIndex, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(errIndexStatusFmt, rec.Code)
	}

	body := rec.Body.String()
	if strings.Contains(body, fmt.Sprintf(`hx-post="/feeds/%d/delete"`, feedID)) {
		t.Fatal("expected no direct delete action outside edit mode")
	}

	if strings.Contains(body, "/delete/confirm") {
		t.Fatal("expected no delete confirm links in index")
	}
}

func TestDeleteFeedConfirmEndpointRemoved(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(), app.db, exampleRSSURL, "Delete Feed")
	if err != nil {
		t.Fatalf(errStoreUpsertFeed, err)
	}

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/feeds/%d/delete/confirm", feedID), http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("confirm endpoint status: %d", rec.Code)
	}
}

func TestIndexIncludesSubscriptionAndOPMLControls(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	req := httptest.NewRequest(http.MethodGet, pathIndex, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(errIndexStatusFmt, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `hx-post="/feeds"`) {
		t.Fatal("expected subscribe control")
	}

	if !strings.Contains(body, `href="/opml/export"`) {
		t.Fatal("expected OPML export control")
	}

	if !strings.Contains(body, `hx-post="/opml/import"`) {
		t.Fatal("expected OPML import control")
	}
}

func TestExportOPML(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	_, err := store.UpsertFeed(context.Background(), app.db, "https://example.com/alpha.xml", "Alpha")
	if err != nil {
		t.Fatalf("store.UpsertFeed alpha: %v", err)
	}

	_, err = store.UpsertFeed(context.Background(), app.db, "https://example.com/beta.xml", "Beta")
	if err != nil {
		t.Fatalf("store.UpsertFeed beta: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/opml/export", http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("export status: %d", rec.Code)
	}

	if contentType := rec.Header().Get(headerContentType); !strings.Contains(contentType, "opml") {
		t.Fatalf("expected OPML content type, got %q", contentType)
	}

	if contentDisposition := rec.Header().Get("Content-Disposition"); !strings.Contains(contentDisposition, ".opml") {
		t.Fatalf("expected OPML attachment filename, got %q", contentDisposition)
	}

	subscriptions, err := opml.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatalf("opml.Parse export body: %v", err)
	}

	if len(subscriptions) != 2 {
		t.Fatalf("expected 2 subscriptions, got %d", len(subscriptions))
	}
}

func TestImportOPML(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	body, contentType := multipartOPMLRequestBody(t, `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head><title>Imports</title></head>
  <body>
    <outline text="Alpha" xmlUrl="https://example.com/alpha.xml"/>
    <outline text="Beta" xmlUrl="https://example.com/beta.xml"/>
    <outline text="Invalid" xmlUrl="http://"/>
  </body>
</opml>`)

	req := httptest.NewRequest(http.MethodPost, "/opml/import", body)
	req.Header.Set(headerContentType, contentType)

	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("import status: %d", rec.Code)
	}

	responseBody := rec.Body.String()
	if !strings.Contains(responseBody, "Imported 2 feeds (1 skipped)") {
		t.Fatalf("expected import summary message, got %q", responseBody)
	}

	assertContains(t, responseBody, feedListIDAttr, msgFeedListOOB)

	feeds, err := store.ListFeeds(context.Background(), app.db)
	if err != nil {
		t.Fatalf(errStoreListFeeds, err)
	}

	if len(feeds) != 2 {
		t.Fatalf("expected 2 imported feeds, got %d", len(feeds))
	}
}

func TestRoutesMethodMismatchReturns405(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	req := httptest.NewRequest(http.MethodGet, "/feeds", http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}

	allow := rec.Header().Get("Allow")
	if !strings.Contains(allow, http.MethodPost) {
		t.Fatalf("expected Allow header to include POST, got %q", allow)
	}
}

func TestRoutesInvalidFeedIDReturns404(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	req := httptest.NewRequest(http.MethodGet, "/feeds/not-a-number/items", http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRoutesInvalidItemIDReturns404(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	req := httptest.NewRequest(http.MethodPost, "/items/not-a-number/toggle", http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func setupFeedListCollapseFixtures(t *testing.T, app *App) {
	t.Helper()

	_ = mustUpsertFeed(t, app, "http://example.com/a-empty", "Aardvark Empty")
	alphaID := mustUpsertFeed(t, app, "http://example.com/b-alpha", "Alpha Active")
	betaID := mustUpsertFeed(t, app, "http://example.com/c-beta", "Beta Active")
	readOnlyID := mustUpsertFeed(t, app, "http://example.com/d-readonly", "Delta Read")

	mustUpsertItems(t, app, alphaID, []*gofeed.Item{{
		Title:           "Alpha item",
		Link:            "http://example.com/alpha-item",
		GUID:            "alpha-item",
		Description:     "<p>Alpha item</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	mustUpsertItems(t, app, betaID, []*gofeed.Item{{
		Title:           "Beta item",
		Link:            "http://example.com/beta-item",
		GUID:            "beta-item",
		Description:     "<p>Beta item</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})
	mustUpsertItems(t, app, readOnlyID, []*gofeed.Item{{
		Title:           "Delta item",
		Link:            "http://example.com/delta-item",
		GUID:            "delta-item",
		Description:     "<p>Delta item</p>",
		PublishedParsed: new(time.Now().Add(-threeUnits * time.Hour)),
	}})

	err := store.MarkAllRead(context.Background(), app.db, readOnlyID)
	requireNoErr(t, err, "store.MarkAllRead read only: %v")
}

func assertCollapsedZeroUnreadFeedList(t *testing.T, body string) {
	t.Helper()

	assertContains(t, body, `class="feed-more-button"`, "expected more button when zero-unread feeds exist")
	assertContains(
		t,
		body,
		`aria-label="Toggle feeds with no unread items"`,
		"expected accessible label for zero-unread toggle",
	)
	assertContains(t, body, `aria-expanded="false"`, "expected collapsed more button by default")
	assertContains(t, body, `aria-controls="feed-zero-list"`, "expected button to control zero-unread list")
	assertContains(t, body, `class="feed-zero-list"`, "expected collapsed zero-unread feed section")
	assertContains(t, body, `id="feed-zero-list"`, "expected zero-unread list id for aria-controls linkage")
	assertContains(t, body, `class="feed-zero-list" hidden`, "expected zero-unread list to start hidden")
	assertNotContains(t, body, "feed-more-label-collapsed", "expected legacy More label markup to be removed")
	assertNotContains(t, body, "feed-more-label-expanded", "expected legacy Less label markup to be removed")
	assertNotContains(t, body, "<summary", "expected native summary element to be removed")
	assertNotContains(t, body, "<details", "expected native details element to be removed")

	alphaIdx := strings.Index(body, "Alpha Active")
	betaIdx := strings.Index(body, "Beta Active")
	moreIdx := strings.Index(body, `class="feed-more-button"`)
	emptyIdx := strings.Index(body, "Aardvark Empty")
	readOnlyIdx := strings.Index(body, "Delta Read")

	if alphaIdx == -1 || betaIdx == -1 || moreIdx == -1 || emptyIdx == -1 || readOnlyIdx == -1 {
		t.Fatal("expected alpha, beta, more button, empty feed, and read-only feed in output")
	}

	if alphaIdx > betaIdx {
		t.Fatal("expected unread feeds to remain alphabetical")
	}

	if betaIdx > moreIdx || moreIdx > emptyIdx || moreIdx > readOnlyIdx {
		t.Fatal("expected zero-unread feeds below unread feeds behind the more section")
	}
}

func TestFeedListCollapsesZeroUnreadFeeds(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	setupFeedListCollapseFixtures(t, app)

	req := httptest.NewRequest(http.MethodGet, pathIndex, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(errIndexStatusFmt, rec.Code)
	}

	assertCollapsedZeroUnreadFeedList(t, rec.Body.String())
}

func TestFeedListHidesMoreButtonWithoutZeroUnreadFeeds(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	alphaID, err := store.UpsertFeed(context.Background(), app.db, "http://example.com/a-alpha", "Alpha Active")
	if err != nil {
		t.Fatalf("store.UpsertFeed alpha: %v", err)
	}

	betaID, err := store.UpsertFeed(context.Background(), app.db, "http://example.com/b-beta", "Beta Active")
	if err != nil {
		t.Fatalf("store.UpsertFeed beta: %v", err)
	}

	_, upsertErr := store.UpsertItems(context.Background(), app.db, alphaID, []*gofeed.Item{{
		Title:           "Alpha item",
		Link:            "http://example.com/alpha-item",
		GUID:            "alpha-item",
		Description:     "<p>Alpha item</p>",
		PublishedParsed: new(time.Now().Add(-time.Hour)),
	}})
	if upsertErr != nil {
		t.Fatalf("store.UpsertItems alpha: %v", upsertErr)
	}

	_, upsertErr = store.UpsertItems(context.Background(), app.db, betaID, []*gofeed.Item{{
		Title:           "Beta item",
		Link:            "http://example.com/beta-item",
		GUID:            "beta-item",
		Description:     "<p>Beta item</p>",
		PublishedParsed: new(time.Now().Add(-2 * time.Hour)),
	}})
	if upsertErr != nil {
		t.Fatalf("store.UpsertItems beta: %v", upsertErr)
	}

	req := httptest.NewRequest(http.MethodGet, pathIndex, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(errIndexStatusFmt, rec.Code)
	}

	body := rec.Body.String()
	if strings.Contains(body, `class="feed-more-button"`) {
		t.Fatal("expected more button to be hidden when all feeds have unread items")
	}
}

func newSelectedItemIDRequest(raw string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, pathIndex, http.NoBody)

	q := req.URL.Query()
	q.Set(selectedItemIDParam, raw)
	req.URL.RawQuery = q.Encode()

	return req
}

func TestParseSelectedItemID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{name: "empty", raw: "", want: 0},
		{name: "plain id", raw: selectedItemIDRaw, want: selectedItemIDPlain},
		{name: "prefixed id", raw: selectedItemIDPrefix, want: selectedItemIDPlain},
		{name: "invalid", raw: "item-abc", want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := newSelectedItemIDRequest(tc.raw)
			if got := parseSelectedItemID(req); got != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

func TestBuildFeedViewLastRefreshDisplay(t *testing.T) {
	t.Parallel()

	var (
		emptyChecked        sql.NullTime
		emptyError, noError sql.NullString
	)

	feed := view.BuildFeedView(
		1,
		0,
		itemLimitFeedTitle,
		itemLimitFeedTitle,
		"https://example.com",
		0,
		0,
		view.FeedStatus{
			LastChecked: emptyChecked,
			LastError:   emptyError,
		},
	)
	if feed.LastRefreshDisplay != "Never" {
		t.Fatalf("expected Never, got %q", feed.LastRefreshDisplay)
	}

	cases := []struct {
		name     string
		wantUnit string
		age      time.Duration
	}{
		{name: "seconds", age: threeUnits * time.Second, wantUnit: "s"},
		{name: "minutes", age: threeUnits * time.Minute, wantUnit: "m"},
		{name: "hours", age: threeUnits * time.Hour, wantUnit: "h"},
		{name: "days", age: hoursInThreeDays * time.Hour, wantUnit: "d"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checked := sql.NullTime{Time: time.Now().Add(-tc.age), Valid: true}
			feedView := view.BuildFeedView(
				1,
				0,
				itemLimitFeedTitle,
				itemLimitFeedTitle,
				"https://example.com",
				0,
				0,
				view.FeedStatus{
					LastChecked: checked,
					LastError:   noError,
				},
			)

			got := feedView.LastRefreshDisplay
			if !strings.HasSuffix(got, tc.wantUnit) {
				t.Fatalf("expected unit %q in %q", tc.wantUnit, got)
			}
		})
	}
}

func TestImageProxyNon2xxLogsAtDebugLevel(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	app.imageProxyLookup = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "cdn-images-1.medium.com" {
			t.Fatalf("unexpected host %q", host)
		}

		return []net.IPAddr{testIPAddr(examplePublicIP)}, nil
	}
	app.imageProxyClient = newTestHTTPClient(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return newTestHTTPResponse(req, http.StatusForbidden, make(http.Header), strings.NewReader("forbidden")), nil
	}))

	var logs bytes.Buffer

	prevLogger := slog.Default()

	options := new(slog.HandlerOptions)
	options.Level = slog.LevelDebug

	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, options)))
	defer slog.SetDefault(prevLogger)

	targetImageURL := "https://cdn-images-1.medium.com/max/1024/example.png"
	proxyURL := content.ImageProxyPath + imageProxyURLQuery + url.QueryEscape(targetImageURL)
	req := httptest.NewRequest(http.MethodGet, proxyURL, http.NoBody)
	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307, got %d", rec.Code)
	}

	if got := rec.Header().Get("Location"); got != targetImageURL {
		t.Fatalf("expected redirect to original image, got %q", got)
	}

	body := logs.String()
	if !strings.Contains(body, "image proxy upstream non-2xx") {
		t.Fatalf("expected debug log for non-2xx upstream response, got %q", body)
	}

	if !strings.Contains(body, "status=403") {
		t.Fatalf("expected status in log entry, got %q", body)
	}
}

func TestImageProxyNon2xxDoesNotLogAtInfoLevel(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	app.imageProxyLookup = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "cdn-images-1.medium.com" {
			t.Fatalf("unexpected host %q", host)
		}

		return []net.IPAddr{testIPAddr(examplePublicIP)}, nil
	}
	app.imageProxyClient = newTestHTTPClient(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return newTestHTTPResponse(req, http.StatusForbidden, make(http.Header), strings.NewReader("forbidden")), nil
	}))

	var logs bytes.Buffer

	prevLogger := slog.Default()

	options := new(slog.HandlerOptions)
	options.Level = slog.LevelInfo

	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, options)))
	defer slog.SetDefault(prevLogger)

	targetImageURL := "https://cdn-images-1.medium.com/max/1024/example.png"
	proxyURL := content.ImageProxyPath + imageProxyURLQuery + url.QueryEscape(targetImageURL)
	req := httptest.NewRequest(http.MethodGet, proxyURL, http.NoBody)
	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307, got %d", rec.Code)
	}

	if got := rec.Header().Get("Location"); got != targetImageURL {
		t.Fatalf("expected redirect to original image, got %q", got)
	}

	if strings.Contains(logs.String(), "image proxy upstream non-2xx") {
		t.Fatalf("expected no non-2xx debug log at info level, got %q", logs.String())
	}
}

func TestImageProxyRejectsResolvedPrivateHost(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	app.imageProxyLookup = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "example.com" {
			t.Fatalf("unexpected host %q", host)
		}

		return []net.IPAddr{testIPAddr("127.0.0.1")}, nil
	}
	app.imageProxyClient = newTestHTTPClient(roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		t.Fatal("unexpected upstream request")

		return nil, http.ErrUseLastResponse
	}))

	proxyURL := content.ImageProxyPath + imageProxyURLQuery + url.QueryEscape("https://example.com/image.png")
	req := httptest.NewRequest(http.MethodGet, proxyURL, http.NoBody)
	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "invalid url") {
		t.Fatalf("expected invalid url response, got %q", rec.Body.String())
	}
}

func TestImageProxyRejectsOversizedImage(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	app.imageProxyLookup = func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{testIPAddr(examplePublicIP)}, nil
	}
	oversized := bytes.Repeat([]byte("a"), int(content.ImageProxyMaxBodyBytes)+1)
	app.imageProxyClient = newTestHTTPClient(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		resp := newTestHTTPResponse(
			req,
			http.StatusOK,
			http.Header{headerContentType: []string{"image/png"}},
			bytes.NewReader(oversized),
		)
		resp.ContentLength = int64(len(oversized))

		return resp, nil
	}))

	proxyURL := content.ImageProxyPath + imageProxyURLQuery + url.QueryEscape("https://example.com/image.png")
	req := httptest.NewRequest(http.MethodGet, proxyURL, http.NoBody)
	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307, got %d", rec.Code)
	}

	if got := rec.Header().Get("Location"); got != "https://example.com/image.png" {
		t.Fatalf("expected redirect to original image, got %q", got)
	}
}

func TestImageProxyServesImageWithinSizeLimit(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	app.imageProxyLookup = func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{testIPAddr(examplePublicIP)}, nil
	}
	imageBody := []byte("png-data")
	app.imageProxyClient = newTestHTTPClient(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		resp := newTestHTTPResponse(
			req,
			http.StatusOK,
			http.Header{
				headerContentType: []string{"image/png"},
				"Cache-Control":   []string{"public, max-age=60"},
				"ETag":            []string{"\"abc123\""},
			},
			bytes.NewReader(imageBody),
		)
		resp.ContentLength = int64(len(imageBody))

		return resp, nil
	}))

	proxyURL := content.ImageProxyPath + imageProxyURLQuery + url.QueryEscape("https://example.com/image.png")
	req := httptest.NewRequest(http.MethodGet, proxyURL, http.NoBody)
	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if body := rec.Body.Bytes(); !bytes.Equal(body, imageBody) {
		t.Fatalf("unexpected response body: got %q want %q", body, imageBody)
	}

	if got := rec.Header().Get(headerContentType); got != "image/png" {
		t.Fatalf("expected image/png content-type, got %q", got)
	}

	if got := rec.Header().Get("Content-Length"); got != "8" {
		t.Fatalf("expected content-length 8, got %q", got)
	}

	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Fatalf("expected cache-control preserved, got %q", got)
	}
}

func TestMobileStreamUnreadOnly(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	alphaID, err := store.UpsertFeed(context.Background(), app.db, "http://example.com/alpha", "Alpha")
	requireNoErr(t, err, errStoreUpsertFeed)
	bravoID, err := store.UpsertFeed(context.Background(), app.db, "http://example.com/bravo", "Bravo")
	requireNoErr(t, err, errStoreUpsertFeed)

	now := time.Now().UTC()
	alphaPublished := now.Add(-time.Hour)
	bravoPublished := now.Add(-30 * time.Minute)

	_, err = store.UpsertItems(context.Background(), app.db, alphaID, []*gofeed.Item{
		newGofeedItem("Alpha Read", "http://example.com/a-read", "alpha-read", "<p>Summary</p>", &alphaPublished),
	})
	requireNoErr(t, err, errStoreUpsertItems)
	_, err = store.UpsertItems(context.Background(), app.db, bravoID, []*gofeed.Item{
		newGofeedItem("Bravo Unread", "http://example.com/b-unread", "bravo-unread", "<p>Summary</p>", &bravoPublished),
	})
	requireNoErr(t, err, errStoreUpsertItems)

	_, err = app.db.ExecContext(
		context.Background(),
		"UPDATE items SET read_at = ? WHERE feed_id = ? AND guid = ?",
		now,
		alphaID,
		"alpha-read",
	)
	requireNoErr(t, err, "set read_at: %v")

	rec := getRequest(app, pathMobileStream)
	assertResponseCode(t, rec, "mobile stream status")

	body := rec.Body.String()
	assertContains(t, body, "<!doctype html>", "expected full-page mobile stream response")
	assertContains(t, body, `data-mobile-stream="true"`, "expected mobile stream container")
	assertContains(t, body, "Bravo Unread", "expected unread item in stream")
	assertNotContains(t, body, "Alpha Read", "expected read item to be excluded from stream")
}

func TestParseSelectedFeedID(t *testing.T) {
	t.Parallel()

	formBody := strings.NewReader(url.Values{formSelectedFeedID: {"42"}}.Encode())
	formRequest := httptest.NewRequest(http.MethodPost, pathMobileStream, formBody)
	formRequest.Header.Set(headerContentType, formURLEncoded)

	testCases := []struct {
		req  *http.Request
		name string
		want int64
	}{
		{
			name: "query parameter",
			req:  httptest.NewRequest(http.MethodGet, pathMobileStream+"?selected_feed_id=21", http.NoBody),
			want: 21,
		},
		{
			name: "form body",
			req:  formRequest,
			want: 42,
		},
		{
			name: "zero value",
			req:  httptest.NewRequest(http.MethodGet, pathMobileStream+"?selected_feed_id=0", http.NoBody),
			want: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := parseSelectedFeedID(tc.req); got != tc.want {
				t.Fatalf("expected selected feed ID %d, got %d", tc.want, got)
			}
		})
	}
}

func TestMobileStreamHTMXReplacesURL(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	rec := getHTMXRequest(app, pathMobileStream)
	assertResponseCode(t, rec, "mobile stream htmx status")

	if got := rec.Header().Get("Hx-Replace-Url"); got != pathMobileStream {
		t.Fatalf("expected HX-Replace-Url %q, got %q", pathMobileStream, got)
	}

	body := rec.Body.String()
	assertNotContains(t, body, "<!doctype html>", "expected partial htmx mobile stream response")
	assertNotContains(t, body, `feed-count">`, "expected unread counters to be absent")
	assertNotContains(t, body, "New items (", "expected desktop new-items UI to be absent")
}

func TestMobileStreamFiltersUnreadItemsBySelectedFeed(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	alphaID, err := store.UpsertFeed(context.Background(), app.db, "http://example.com/alpha", "Alpha")
	requireNoErr(t, err, errStoreUpsertFeed)
	bravoID, err := store.UpsertFeed(context.Background(), app.db, "http://example.com/bravo", "Bravo")
	requireNoErr(t, err, errStoreUpsertFeed)

	now := time.Now().UTC()
	alphaPublished := now.Add(-time.Hour)
	bravoPublished := now.Add(-30 * time.Minute)

	_, err = store.UpsertItems(context.Background(), app.db, alphaID, []*gofeed.Item{
		newGofeedItem("Alpha Unread", "http://example.com/a-unread", "alpha-unread", "<p>Summary</p>", &alphaPublished),
	})
	requireNoErr(t, err, errStoreUpsertItems)
	_, err = store.UpsertItems(context.Background(), app.db, bravoID, []*gofeed.Item{
		newGofeedItem("Bravo Unread", "http://example.com/b-unread", "bravo-unread", "<p>Summary</p>", &bravoPublished),
	})
	requireNoErr(t, err, errStoreUpsertItems)

	alphaItems, err := store.ListItems(context.Background(), app.db, alphaID)
	requireNoErr(t, err, errStoreListItems)

	target := fmt.Sprintf("%s?selected_feed_id=%d", pathMobileStream, alphaID)
	rec := getHTMXRequest(app, target)
	assertResponseCode(t, rec, "mobile filtered stream status")

	if got := rec.Header().Get("Hx-Replace-Url"); got != target {
		t.Fatalf("expected HX-Replace-Url %q, got %q", target, got)
	}

	body := rec.Body.String()
	assertContains(t, body, "Alpha Unread", "expected selected feed unread item in stream")
	assertNotContains(t, body, "Bravo Unread", "expected other feed item excluded from filtered stream")
	assertContains(
		t,
		body,
		fmt.Sprintf(`/mobile/items/%d/reader?selected_feed_id=%d`, alphaItems[0].ID, alphaID),
		"expected reader action to preserve selected feed",
	)
}

func TestMobileStreamSelectorRendersInlineInHeaderAndShowsUnreadFeedsOnly(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	quietID := mustUpsertFeed(t, app, "http://example.com/quiet", "Quiet Feed")
	brightID := mustUpsertFeed(t, app, "http://example.com/bright", "Bright Feed")
	published := time.Now().UTC().Add(-time.Hour)
	mustUpsertSingleStory(
		t,
		app,
		quietID,
		"Quiet Story",
		"http://example.com/quiet-story",
		"quiet-story",
		published,
	)
	mustUpsertSingleStory(
		t,
		app,
		brightID,
		"Bright Story",
		"http://example.com/bright-story",
		"bright-story",
		published,
	)
	mustMarkFeedItemRead(t, app, quietID, "quiet-story")

	rec := getRequest(app, pathMobileStream)
	assertResponseCode(t, rec, "mobile stream selector status")

	body := rec.Body.String()
	assertMobileStreamSelectorInHeader(t, body)
	assertContains(t, body, `<option value="0"`, "expected all-feeds option")
	assertContains(t, body, `>All feeds</option>`, "expected all-feeds option label")
	assertContains(
		t,
		body,
		fmt.Sprintf(`<option value="%d"`, brightID),
		"expected unread feed option",
	)
	assertContains(
		t,
		body,
		`>Bright Feed</option>`,
		"expected unread feed option",
	)
	assertNotContains(
		t,
		body,
		fmt.Sprintf(`<option value="%d">Quiet Feed</option>`, quietID),
		"expected read-only feed to be omitted from selector",
	)
}

func TestMobileStreamSelectorMarksSelectedFeed(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, "http://example.com/bright", "Bright Feed")
	mustUpsertSingleStory(
		t,
		app,
		feedID,
		"Bright Story",
		"http://example.com/bright-story",
		"bright-story",
		time.Now().UTC().Add(-time.Hour),
	)

	rec := getRequest(app, fmt.Sprintf("%s?selected_feed_id=%d", pathMobileStream, feedID))
	assertResponseCode(t, rec, "mobile stream selected option status")

	assertContains(
		t,
		rec.Body.String(),
		fmt.Sprintf(`<option value="%d" selected>Bright Feed</option>`, feedID),
		"expected selected feed option to reflect selected_feed_id",
	)
}

func TestMobileStreamFilteredEmptyStateNamesSelectedFeed(t *testing.T) {
	t.Parallel()

	genericFixture := newAppWithClearedSingleFeed(
		t,
		"http://example.com/generic",
		"Generic Feed",
		"Generic Story",
		"http://example.com/generic-story",
		"generic-story",
	)

	filteredFixture := newAppWithClearedSingleFeed(
		t,
		"http://example.com/cleared",
		"Cleared Feed",
		"Cleared Story",
		"http://example.com/cleared-story",
		"cleared-story",
	)
	app := filteredFixture.app
	clearedFeedID := filteredFixture.feedID
	activeFeedID := mustUpsertFeed(t, app, "http://example.com/active", "Active Feed")
	mustUpsertSingleStory(
		t,
		app,
		activeFeedID,
		"Active Story",
		"http://example.com/active-story",
		"active-story",
		time.Now().UTC().Add(-time.Hour),
	)

	allFeedsRec := getRequest(genericFixture.app, pathMobileStream)
	assertResponseCode(t, allFeedsRec, "all-feeds mobile stream status")
	assertGenericMobileEmptyState(t, allFeedsRec.Body.String())

	filteredRec := getRequest(app, fmt.Sprintf("%s?selected_feed_id=%d", pathMobileStream, clearedFeedID))
	assertResponseCode(t, filteredRec, "filtered empty-state mobile stream status")

	assertFilteredMobileEmptyState(
		t,
		filteredRec.Body.String(),
		"Cleared Feed",
		clearedFeedID,
		activeFeedID,
	)
}

func TestMobileMarkReadPreservesFilteredSelectionAndURL(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, "http://example.com/solo", "Solo Feed")
	otherFeedID := mustUpsertFeed(t, app, "http://example.com/other", "Other Feed")
	published := time.Now().UTC().Add(-30 * time.Minute)
	mustUpsertSingleStory(t, app, feedID, "Solo Story", "http://example.com/solo-story", "solo-story", published)
	mustUpsertSingleStory(
		t,
		app,
		otherFeedID,
		"Other Story",
		"http://example.com/other-story",
		"other-story",
		published,
	)

	items := mustListItems(t, app, feedID)
	target := fmt.Sprintf("/mobile/items/%d/read?selected_feed_id=%d", items[0].ID, feedID)
	expectedURL := fmt.Sprintf("%s?selected_feed_id=%d", pathMobileStream, feedID)

	rec := postHTMXRequest(app, target)
	assertResponseCode(t, rec, "mobile filtered mark-read status")

	if got := rec.Header().Get("Hx-Replace-Url"); got != expectedURL {
		t.Fatalf("expected filtered HX-Replace-Url, got %q", got)
	}

	assertFilteredMobileEmptyState(t, rec.Body.String(), "Solo Feed", feedID, otherFeedID)
}

func TestMobilePulsePreservesFilteredSelectionAndURL(t *testing.T) {
	t.Parallel()

	caughtUpFixture := newAppWithClearedSingleFeed(
		t,
		"http://example.com/caught-up",
		"Caught Up Feed",
		"Caught Up Story",
		"http://example.com/caught-up-story",
		"caught-up-story",
	)
	app := caughtUpFixture.app
	feedID := caughtUpFixture.feedID
	otherFeedID := mustUpsertFeed(t, app, "http://example.com/other", "Other Feed")
	mustUpsertSingleStory(
		t,
		app,
		otherFeedID,
		"Other Story",
		"http://example.com/other-story",
		"other-story",
		time.Now().UTC().Add(-15*time.Minute),
	)

	_, err := app.db.ExecContext(
		context.Background(),
		"UPDATE feeds SET last_refreshed_at = ? WHERE id IN (?, ?)",
		time.Now().UTC(),
		feedID,
		otherFeedID,
	)
	requireNoErr(t, err, "set last_refreshed_at: %v")

	expectedURL := fmt.Sprintf("%s?selected_feed_id=%d", pathMobileStream, feedID)
	rec := postHTMXRequest(app, fmt.Sprintf("%s?selected_feed_id=%d", pathMobilePulse, feedID))
	assertResponseCode(t, rec, "mobile filtered pulse status")

	if got := rec.Header().Get("Hx-Replace-Url"); got != expectedURL {
		t.Fatalf("expected filtered HX-Replace-Url, got %q", got)
	}

	body := rec.Body.String()
	assertContains(t, body, "Already fresh enough.", "expected calm pulse status")
	assertFilteredMobileEmptyState(t, body, "Caught Up Feed", feedID, otherFeedID)
}

func TestMobileReaderView(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(), app.db, "http://example.com/rss", "Feed Title")
	requireNoErr(t, err, errStoreUpsertFeed)

	published := time.Now().UTC().Add(-time.Hour)
	_, err = store.UpsertItems(context.Background(), app.db, feedID, []*gofeed.Item{
		newGofeedItem("Unread Story", "http://example.com/story", "story-1", "<p>Summary</p>", &published),
	})
	requireNoErr(t, err, errStoreUpsertItems)

	items, err := store.ListItems(context.Background(), app.db, feedID)
	requireNoErr(t, err, errStoreListItems)

	itemID := items[0].ID

	rec := getRequest(app, fmt.Sprintf("/mobile/items/%d/reader", itemID))
	assertResponseCode(t, rec, "mobile reader status")

	body := rec.Body.String()
	assertContains(t, body, "<!doctype html>", "expected full-page mobile reader response")
	assertContains(t, body, `data-mobile-reader="true"`, "expected mobile reader container")
	assertContains(t, body, "Unread Story", "expected reader item title")
	assertContains(t, body, "Feed Title", "expected reader source title")
	assertContains(t, body, "/mobile/stream", "expected back-to-stream action")
}

func TestMobileReaderPreservesSelectedFeedID(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(), app.db, "http://example.com/rss", "Feed Title")
	requireNoErr(t, err, errStoreUpsertFeed)

	published := time.Now().UTC().Add(-time.Hour)
	_, err = store.UpsertItems(context.Background(), app.db, feedID, []*gofeed.Item{
		newGofeedItem("Unread Story", "http://example.com/story", "story-1", "<p>Summary</p>", &published),
	})
	requireNoErr(t, err, errStoreUpsertItems)

	items, err := store.ListItems(context.Background(), app.db, feedID)
	requireNoErr(t, err, errStoreListItems)

	itemID := items[0].ID
	target := fmt.Sprintf("/mobile/items/%d/reader?selected_feed_id=%d", itemID, feedID)

	rec := getHTMXRequest(app, target)
	assertResponseCode(t, rec, "mobile reader selected-feed status")

	if got := rec.Header().Get("Hx-Push-Url"); got != target {
		t.Fatalf("expected HX-Push-Url %q, got %q", target, got)
	}

	body := rec.Body.String()
	assertContains(
		t,
		body,
		fmt.Sprintf(`hx-get="/mobile/stream?selected_feed_id=%d"`, feedID),
		"expected reader back action to preserve selected feed",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`hx-post="/mobile/items/%d/read?selected_feed_id=%d"`, itemID, feedID),
		"expected reader mark-read action to preserve selected feed",
	)
}

func TestMobileReaderHTMXPushesURL(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(), app.db, "http://example.com/rss", "Feed Title")
	requireNoErr(t, err, errStoreUpsertFeed)

	published := time.Now().UTC().Add(-time.Hour)
	_, err = store.UpsertItems(context.Background(), app.db, feedID, []*gofeed.Item{
		newGofeedItem("Unread Story", "http://example.com/story", "story-1", "<p>Summary</p>", &published),
	})
	requireNoErr(t, err, errStoreUpsertItems)

	items, err := store.ListItems(context.Background(), app.db, feedID)
	requireNoErr(t, err, errStoreListItems)

	itemID := items[0].ID
	target := fmt.Sprintf("/mobile/items/%d/reader", itemID)

	rec := getHTMXRequest(app, target)
	assertResponseCode(t, rec, "mobile reader htmx status")

	if got := rec.Header().Get("Hx-Push-Url"); got != target {
		t.Fatalf("expected HX-Push-Url %q, got %q", target, got)
	}

	assertNotContains(t, rec.Body.String(), "<!doctype html>", "expected partial htmx mobile reader response")
}

func TestMobileMarkReadRendersUpdatedStream(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(), app.db, "http://example.com/rss", "Feed Title")
	requireNoErr(t, err, errStoreUpsertFeed)

	published := time.Now().UTC().Add(-30 * time.Minute)
	_, err = store.UpsertItems(context.Background(), app.db, feedID, []*gofeed.Item{
		newGofeedItem("Story To Clear", "http://example.com/story", "story-clear", "<p>Summary</p>", &published),
	})
	requireNoErr(t, err, errStoreUpsertItems)

	items, err := store.ListItems(context.Background(), app.db, feedID)
	requireNoErr(t, err, errStoreListItems)

	itemID := items[0].ID

	rec := postHTMXRequest(app, fmt.Sprintf("/mobile/items/%d/read", itemID))
	assertResponseCode(t, rec, "mobile mark-read status")

	if got := rec.Header().Get("Hx-Replace-Url"); got != pathMobileStream {
		t.Fatalf("expected HX-Replace-Url %q, got %q", pathMobileStream, got)
	}

	body := rec.Body.String()
	assertContains(t, body, `data-mobile-stream="true"`, "expected stream rerender after mark-read")
	assertNotContains(t, body, "Story To Clear", "expected marked item removed from unread stream")

	reloaded, err := store.GetItem(context.Background(), app.db, itemID)
	requireNoErr(t, err, "store.GetItem: %v")

	if !reloaded.IsRead {
		t.Fatal("expected item to be marked read")
	}
}

func TestMobilePulseRendersStreamWithoutCounts(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(), app.db, "http://example.com/rss", "Feed Title")
	requireNoErr(t, err, errStoreUpsertFeed)

	published := time.Now().UTC().Add(-20 * time.Minute)
	_, err = store.UpsertItems(context.Background(), app.db, feedID, []*gofeed.Item{
		newGofeedItem("Pulse Story", "http://example.com/story", "pulse-story", "<p>Summary</p>", &published),
	})
	requireNoErr(t, err, errStoreUpsertItems)

	_, err = app.db.ExecContext(
		context.Background(),
		"UPDATE feeds SET last_refreshed_at = ? WHERE id = ?",
		time.Now().UTC(),
		feedID,
	)
	requireNoErr(t, err, "set last_refreshed_at: %v")

	rec := postHTMXRequest(app, pathMobilePulse)
	assertResponseCode(t, rec, "mobile pulse status")

	if got := rec.Header().Get("Hx-Replace-Url"); got != pathMobileStream {
		t.Fatalf("expected HX-Replace-Url %q, got %q", pathMobileStream, got)
	}

	body := rec.Body.String()
	assertContains(t, body, `data-mobile-stream="true"`, "expected mobile stream from pulse response")
	assertContains(t, body, "Already fresh enough.", "expected calm pulse status when no feed is due")
	assertNotContains(t, body, `feed-count">`, "expected unread counters to be absent")
	assertNotContains(t, body, "New items (", "expected desktop new-items UI to be absent")
}

func existsByGUID(t *testing.T, db *sql.DB, feedID int64, guid string) bool {
	t.Helper()

	var count int

	err := db.QueryRowContext(context.Background(), `
SELECT COUNT(*)
FROM items
WHERE feed_id = ? AND guid = ?
`, feedID, guid).Scan(&count)
	if err != nil {
		t.Fatalf("existsByGUID: %v", err)
	}

	return count > 0
}

func existsInTombstones(t *testing.T, db *sql.DB, feedID int64, guid string) bool {
	t.Helper()

	var count int

	err := db.QueryRowContext(context.Background(), `
SELECT COUNT(*)
FROM tombstones
WHERE feed_id = ? AND guid = ?
`, feedID, guid).Scan(&count)
	if err != nil {
		t.Fatalf("existsInTombstones: %v", err)
	}

	return count > 0
}

//nolint:gocritic // Prefer unnamed returns here to satisfy nonamedreturns.
func multipartOPMLRequestBody(t *testing.T, opmlContent string) (*bytes.Buffer, string) {
	t.Helper()

	body := new(bytes.Buffer)

	writer := multipart.NewWriter(body)

	file, err := writer.CreateFormFile("file", "subscriptions.opml")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}

	_, writeErr := file.Write([]byte(opmlContent))
	if writeErr != nil {
		t.Fatalf("write form file: %v", writeErr)
	}

	closeErr := writer.Close()
	if closeErr != nil {
		t.Fatalf("writer.Close: %v", closeErr)
	}

	return body, writer.FormDataContentType()
}
