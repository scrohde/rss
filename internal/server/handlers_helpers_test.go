//nolint:testpackage // Handler integration tests intentionally exercise unexported helpers.
package server

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"

	"rss/internal/outbound"
	"rss/internal/store"
	"rss/internal/testutil"
	"rss/internal/view"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

const (
	pathParentDir         = ".."
	pathIndex             = "/"
	pathPulseFeeds        = "/feeds/pulse"
	pathPulseStatus       = "/feeds/pulse/status"
	pathFeedEditMode      = "/feeds/edit-mode"
	pathEditModeCancel    = "/feeds/edit-mode/cancel"
	pathEditModeSave      = "/feeds/edit-mode/save"
	pathMobileStream      = "/mobile/stream"
	pathMobilePulse       = "/mobile/pulse"
	pathMobileFeedRefresh = "/mobile/feeds/%d/refresh"
	errIndexStatusFmt     = "index status: %d"
	expectedNoItems       = 0
	expectedSingleFeed    = 1
	expectedSingleItem    = 1
	firstFeedIndex        = 0
	firstItemIndex        = 0
	expectedTwoItems      = 2
	expectedTwoUnread     = 2
	expectedOneUnread     = 1
	errStoreListFeeds     = "store.ListFeeds: %v"
	errStoreUpsertFeed    = "store.UpsertFeed: %v"
	errStoreUpsertItems   = "store.UpsertItems: %v"
	errStoreListItems     = "store.ListItems: %v"
	headerContentType     = "Content-Type"
	headerSetCookie       = "Set-Cookie"
	formURLEncoded        = "application/x-www-form-urlencoded"
	formSelectedFeedID    = "selected_feed_id"
	classIsActive         = "is-active"
	classFeedListEdit     = `class="feed-list edit-mode"`
	decimalBase           = 10
	sqlItemReadAtByID     = "SELECT read_at FROM items WHERE id = ?"
	sqlUpdateItemReadAt   = "UPDATE items SET read_at = ? WHERE id = ?"
	expectedTombstoneMsg  = "expected tombstone to be recorded"
	exampleRSSURL         = "http://example.com/rss"
	sourceTitle           = "Source Title"
	customTitle           = "Custom Title"
	manualRefreshTitle    = "Manual Refresh Feed"
	sweepOtherFeedURL     = "http://example.com/other"
	sweepGUIDKeep         = "1"
	sweepGUIDA            = "2"
	sweepGUIDB            = "3"
	sweepGUIDOther        = "4"
	deleteFeedTitle       = "Delete Feed"
	itemLimitFeedTitle    = "Feed"
	pollFeedTitle         = "Poll Feed"
	pulseFeedOneTitle     = "Pulse Feed One"
	pulseFeedTwoTitle     = "Pulse Feed Two"
	emptyStateNoFeed      = "Pick a feed to start reading."
	newFeedTitle          = "New Title"
	itemLimitTotal        = 210
	itemLimitPruned       = 10
	itemLimitKept         = 200
	itemLimitFirstGUID    = "guid-010"
	feedListIDAttr        = `id="feed-list"`
	feedListSwapAttr      = `hx-swap-oob="innerHTML"`
	contentPanelIDAttr    = `id="content-panel"`
	contentPanelSwapAttr  = `hx-swap-oob="outerHTML"`
	msgFeedListOOB        = "expected feed list OOB update"
	msgFeedListOOBSwap    = "expected OOB innerHTML swap for feed list"
	expectedItemsFmt      = "expected %d items, got %d"
	msgPollStatus         = "poll status"
	msgFeedItemsStatus    = "feed items status"
	valueEnabled          = "1"
	cookieClearedToken    = "Max-Age=0"
	imageProxyURLQuery    = "?url="
	examplePublicIP       = "93.184.216.34"
	selectedItemIDParam   = "selected_item_id"
	collapseItemIDParam   = "collapse_item_id"
	selectedItemIDPlain   = int64(42)
	selectedItemIDRaw     = "42"
	selectedItemIDPrefix  = "item-42"
	threeUnits            = 3
	hoursInThreeDays      = 72
	sqlCountFeedByID      = "SELECT COUNT(*) FROM feeds WHERE id = ?"
	sqlCountItemsByFeed   = "SELECT COUNT(*) FROM items WHERE feed_id = ?"
	sqlCountTombByFeed    = "SELECT COUNT(*) FROM tombstones WHERE feed_id = ?"
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
	app := New(db, tmpl)
	app.outboundResolver = outbound.LookupIPAddrFunc(
		func(_ context.Context, _ string) ([]net.IPAddr, error) {
			return []net.IPAddr{testIPAddr(examplePublicIP)}, nil
		},
	)

	return app
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

	headerStart := requireBodyIndex(t, body, `<header class="topbar">`, "expected topbar header in response")
	slotStart := requireBodyIndex(t, body, `id="topbar-mobile-slot"`, "expected mobile topbar slot in response")
	formStart := requireBodyIndex(
		t,
		body,
		`id="mobile-stream-feed-filter"`,
		"expected mobile feed selector in response",
	)
	sideStart := requireBodyIndex(t, body, `class="topbar-side"`, "expected topbar menu area in response")
	contentStart := requireBodyIndex(
		t,
		body,
		`id="mobile-stream-content"`,
		"expected mobile stream content in response",
	)

	if slotStart < headerStart || slotStart > sideStart {
		t.Fatal("expected mobile topbar slot to render between the brand and menu")
	}

	if formStart < slotStart || formStart > sideStart {
		t.Fatal("expected feed selector to render inside the shared mobile topbar slot")
	}

	if formStart > contentStart {
		t.Fatal("expected feed selector to render outside the mobile stream body")
	}
}

func requireBodyIndex(t *testing.T, body, token, message string) int {
	t.Helper()

	index := strings.Index(body, token)
	if index == -1 {
		t.Fatal(message)
	}

	return index
}

func assertMenuSectionOrder(t *testing.T, body string, sections ...string) {
	t.Helper()

	previous := -1

	for _, section := range sections {
		index := requireBodyIndex(
			t,
			body,
			fmt.Sprintf(`data-menu-section=%q`, section),
			fmt.Sprintf("expected %s menu section", section),
		)
		if index <= previous {
			t.Fatalf("expected %s menu section after the preceding section", section)
		}

		previous = index
	}
}

func assertMobileTopBarOOBUpdate(t *testing.T, body string) {
	t.Helper()

	assertContains(
		t,
		body,
		`id="topbar-mobile-slot" class="topbar-mobile-slot is-active" hx-swap-oob="outerHTML"`,
		"expected mobile selector slot OOB update",
	)
	assertContains(
		t,
		body,
		`id="topbar-brand-slot" class="topbar-brand-slot" hx-swap-oob="outerHTML"`,
		"expected mobile brand slot OOB update",
	)
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

type mobileStreamSelectorFixture struct {
	app      *App
	quietID  int64
	brightID int64
}

func newMobileStreamSelectorFixture(t *testing.T) mobileStreamSelectorFixture {
	t.Helper()

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

	return mobileStreamSelectorFixture{
		app:      app,
		quietID:  quietID,
		brightID: brightID,
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
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, target, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	return rec
}

func postHTMXRequest(app *App, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, target, http.NoBody)
	req.Header.Set("Hx-Request", "true")

	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	return rec
}

func extractUndoToken(t *testing.T, body string) string {
	t.Helper()

	matches := regexp.MustCompile(`"undo_token":"([0-9a-f]+)"`).FindStringSubmatch(body)
	if len(matches) != expectedTwoItems {
		t.Fatal("expected undo token in mark-all-read response")
	}

	return matches[1]
}

func assertMarkAllReadButtonVisible(t *testing.T, body, message string) {
	t.Helper()

	assertContains(t, body, `data-mark-all-read-button`, message)
	assertNotContains(
		t,
		body,
		`data-mark-all-read-button hidden`,
		"expected mark-all-read button to be visible",
	)
}

func assertUndoTokenCleared(t *testing.T, body, message string) {
	t.Helper()

	assertNotContains(t, body, `undo_token`, message)
}

func assertUndoRestoresReadStates(
	t *testing.T,
	items []view.ItemView,
	initialReadItemID int64,
	initialUnreadItemID int64,
) {
	t.Helper()

	readStates := make(map[int64]bool, len(items))
	for idx := range items {
		item := items[idx]
		readStates[item.ID] = item.IsRead
	}

	if !readStates[initialReadItemID] {
		t.Fatal("expected previously read item to stay read after undo")
	}

	if readStates[initialUnreadItemID] {
		t.Fatal("expected previously unread item to be restored to unread after undo")
	}
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
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, http.NoBody)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	return rec
}

func getHTMXRequest(app *App, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, http.NoBody)
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
	assertContains(
		t,
		body,
		`class="feed-edit-hint">Rename, reorder, or mark feeds for removal. Save to apply changes.</p>`,
		"expected edit mode helper text",
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
	req := httptest.NewRequestWithContext(context.Background(),
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
		`data-content-panel-mark-read="true"`,
		"expected mark-read action in expanded content panel",
	)
	assertContains(
		t,
		body,
		`aria-label="Mark read and open next article"`,
		"expected mark-read action accessible label",
	)
	assertContains(
		t,
		body,
		`title="Mark read and open next article"`,
		"expected mark-read action title",
	)
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

	articleHTML := itemArticleHTML(t, body, itemID)
	if strings.Contains(articleHTML, classIsActive) {
		t.Fatalf("expected item-%d article to not include %q", itemID, classIsActive)
	}
}

func itemArticleHTML(t *testing.T, body string, itemID int64) string {
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

	return body[articleStart:articleEnd]
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
