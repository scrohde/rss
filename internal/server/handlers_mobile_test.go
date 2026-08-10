//nolint:testpackage // Handler integration tests intentionally exercise unexported helpers.
package server

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"

	feedpkg "rss/internal/feed"
	"rss/internal/store"
	"rss/internal/testutil"
	"rss/internal/view"
)

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

func TestMobileAggregateUsesSavedFeedOrderAndSkipsCaughtUpFeeds(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	priorityID := mustUpsertFeed(t, app, "http://example.com/mobile-priority", "Priority Feed")
	quietID := mustUpsertFeed(t, app, "http://example.com/mobile-quiet", "Quiet Feed")
	laterID := mustUpsertFeed(t, app, "http://example.com/mobile-later", "Later Feed")

	err := store.UpdateFeedOrder(context.Background(), app.db, []int64{priorityID, quietID, laterID})
	requireNoErr(t, err, "store.UpdateFeedOrder: %v")

	base := time.Now().UTC().Add(-6 * time.Hour)
	priorityItems := seedMobileAggregateItems(t, app, priorityID, "Priority", 2, base.Add(2*time.Hour))
	quietItems := seedMobileAggregateItems(t, app, quietID, "Quiet", 1, base.Add(3*time.Hour))
	laterItems := seedMobileAggregateItems(t, app, laterID, "Later", 1, base.Add(5*time.Hour))

	err = store.MarkItemRead(context.Background(), app.db, quietItems[0].ID)
	requireNoErr(t, err, "store.MarkItemRead quiet item: %v")

	rec := getHTMXRequest(app, pathMobileStream)
	assertResponseCode(t, rec, "mobile aggregate saved-order status")

	body := rec.Body.String()
	assertMobileFeedSectionOrder(t, body, priorityID, laterID)
	assertContains(t, body, `aria-label="Priority Feed"`, "expected compact feed section accessible name")
	assertNotContains(t, body, `class="mobile-feed-section-header"`, "expected redundant feed heading to be omitted")
	assertNotContains(
		t,
		body,
		fmt.Sprintf(`id="mobile-feed-section-%d"`, quietID),
		"expected caught-up feed section to be skipped",
	)
	assertBodyTokenOrder(
		t,
		body,
		fmt.Sprintf(`id="mobile-card-%d"`, priorityItems[0].ID),
		fmt.Sprintf(`id="mobile-card-%d"`, priorityItems[1].ID),
	)
	assertBodyTokenOrder(
		t,
		body,
		fmt.Sprintf(`id="mobile-feed-section-%d"`, priorityID),
		fmt.Sprintf(`id="mobile-card-%d"`, laterItems[0].ID),
	)
}

//revive:disable:function-length The initial and continuation bounds form one contract.
//nolint:funlen // This integration scenario keeps initial and continuation bounds in one contract test.
func TestMobileAggregateInitialBoundsAndFeedSectionContinuation(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedIDs := make([]int64, 0, mobileAggregateFeedPageLimit+1)
	feedItems := make([][]view.ItemView, 0, mobileAggregateFeedPageLimit+1)
	base := time.Now().UTC().Add(-24 * time.Hour)

	for index := range mobileAggregateFeedPageLimit + 1 {
		feedID := mustUpsertFeed(
			t,
			app,
			fmt.Sprintf("http://example.com/mobile-bounded-%d", index),
			fmt.Sprintf("Bounded Feed %d", index),
		)

		itemCount := 1
		if index == 0 {
			itemCount = mobileAggregateItemPageLimit + 1
		}

		feedIDs = append(feedIDs, feedID)
		feedItems = append(
			feedItems,
			seedMobileAggregateItems(t, app, feedID, fmt.Sprintf("Bounded %d", index), itemCount, base),
		)
	}

	rec := getHTMXRequest(app, pathMobileStream)
	assertResponseCode(t, rec, "mobile aggregate bounded initial status")

	body := rec.Body.String()
	if got := strings.Count(body, `data-mobile-feed-section`); got != mobileAggregateFeedPageLimit {
		t.Fatalf("expected %d initial feed sections, got %d", mobileAggregateFeedPageLimit, got)
	}

	firstSection := requireMobileFeedSectionBody(t, body, feedIDs[0])
	if got := strings.Count(firstSection, `data-mobile-item-id=`); got != mobileAggregateItemPageLimit {
		t.Fatalf("expected %d items in the first bounded section, got %d", mobileAggregateItemPageLimit, got)
	}

	assertNotContains(
		t,
		firstSection,
		fmt.Sprintf(`id="mobile-card-%d"`, feedItems[0][mobileAggregateItemPageLimit].ID),
		"expected first feed overflow item to stay lazy",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`id="mobile-card-%d"`, feedItems[1][0].ID),
		"expected a later feed to remain reachable despite the first feed backlog",
	)
	assertNotContains(
		t,
		body,
		fmt.Sprintf(`id="mobile-feed-section-%d"`, feedIDs[mobileAggregateFeedPageLimit]),
		"expected overflow feed section to stay lazy",
	)
	assertContains(t, firstSection, `data-mobile-feed-next`, "expected older-items continuation")
	assertContains(t, body, `data-mobile-sections-next`, "expected next-feed continuation")

	page, err := store.ListUnreadFeedSections(
		context.Background(),
		app.db,
		nil,
		mobileAggregateFeedPageLimit,
		mobileAggregateItemPageLimit,
	)
	requireNoErr(t, err, "store.ListUnreadFeedSections initial: %v")

	if page.Next == nil {
		t.Fatal("expected initial aggregate page cursor")
	}

	nextPath := mobileFeedSectionsPagePath(page.Next)
	nextRec := getHTMXRequest(app, nextPath)
	assertResponseCode(t, nextRec, "mobile aggregate next-feed page status")

	wantURL := mobileStreamStatePath(0, mobileAggregateState{
		FeedCursor: page.Next,
		ItemCursor: nil,
		FeedID:     0,
	})
	if got := nextRec.Header().Get("Hx-Push-Url"); got != wantURL {
		t.Fatalf("expected HX-Push-Url %q, got %q", wantURL, got)
	}

	nextBody := nextRec.Body.String()
	assertNotContains(t, nextBody, "<!doctype html>", "expected feed continuation partial")
	assertContains(t, nextBody, `id="mobile-stream-sections"`, "expected bounded sections replacement")
	assertContains(
		t,
		nextBody,
		fmt.Sprintf(`id="mobile-feed-section-%d"`, feedIDs[mobileAggregateFeedPageLimit]),
		"expected overflow feed on the next section page",
	)
	assertNotContains(
		t,
		nextBody,
		fmt.Sprintf(`id="mobile-feed-section-%d"`, feedIDs[0]),
		"expected earlier feed sections to be replaced",
	)
	assertContains(t, nextBody, `data-mobile-sections-reset`, "expected a route back to the first feed batch")
	assertNotContains(t, nextBody, `data-mobile-sections-next`, "expected terminal feed batch")
	assertContains(
		t,
		nextBody,
		`hx-post="`+template.HTMLEscapeString(mobilePulseStatePath(0, mobileAggregateState{
			FeedCursor: page.Next,
			ItemCursor: nil,
			FeedID:     0,
		}))+`"`,
		"expected feed continuation to update the pulse aggregate cursor",
	)
}

//revive:enable:function-length

//revive:disable:function-length The bounded batch and canonical continuation form one contract.
//nolint:funlen // The bounded batch and canonical continuation contract are asserted together.
func TestMobileAggregateItemContinuationReturnsBoundedBatch(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	firstID := mustUpsertFeed(t, app, "http://example.com/mobile-item-pages", "Paged Feed")
	laterID := mustUpsertFeed(t, app, "http://example.com/mobile-item-later", "Later Feed")
	items := seedMobileAggregateItems(
		t,
		app,
		firstID,
		"Paged",
		mobileAggregateItemPageLimit*2+1,
		time.Now().UTC(),
	)
	laterItems := seedMobileAggregateItems(t, app, laterID, "Later", 1, time.Now().UTC().Add(time.Hour))

	page, err := store.ListUnreadFeedSections(
		context.Background(),
		app.db,
		nil,
		mobileAggregateFeedPageLimit,
		mobileAggregateItemPageLimit,
	)
	requireNoErr(t, err, "store.ListUnreadFeedSections for item continuation: %v")

	if len(page.Sections) == 0 || page.Sections[0].Next == nil {
		t.Fatal("expected first feed to expose an older-items cursor")
	}

	state := mobileAggregateState{
		FeedCursor: nil,
		ItemCursor: page.Sections[0].Next,
		FeedID:     firstID,
	}
	rec := getHTMXRequest(app, mobileFeedItemsPagePath(firstID, state))
	assertResponseCode(t, rec, "mobile aggregate older-items status")

	if got, want := rec.Header().Get("Hx-Replace-Url"), mobileStreamStatePath(0, state); got != want {
		t.Fatalf("expected HX-Replace-Url %q, got %q", want, got)
	}

	if got := rec.Header().Get("Hx-Retarget"); got != "#mobile-stream-sections" {
		t.Fatalf("expected aggregate batch retarget, got %q", got)
	}

	wantReswap := fmt.Sprintf("outerHTML show:#mobile-feed-section-%d:top", firstID)
	if got := rec.Header().Get("Hx-Reswap"); got != wantReswap {
		t.Fatalf("expected aggregate batch reswap %q, got %q", wantReswap, got)
	}

	body := rec.Body.String()
	assertNotContains(t, body, "<!doctype html>", "expected item continuation partial")
	assertContains(t, body, `id="mobile-stream-sections"`, "expected bounded aggregate batch response")
	assertContains(
		t,
		body,
		fmt.Sprintf(`id="mobile-feed-section-%d"`, firstID),
		"expected requested feed section",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`id="mobile-feed-section-%d"`, laterID),
		"expected later feed section to remain reachable in the bounded batch",
	)

	firstSection := requireMobileFeedSectionBody(t, body, firstID)
	if got := strings.Count(firstSection, `data-mobile-item-id=`); got != mobileAggregateItemPageLimit {
		t.Fatalf("expected %d items in continuation response, got %d", mobileAggregateItemPageLimit, got)
	}

	assertNotContains(
		t,
		firstSection,
		fmt.Sprintf(`id="mobile-card-%d"`, items[0].ID),
		"expected newest page item to be replaced",
	)
	assertContains(
		t,
		firstSection,
		fmt.Sprintf(`id="mobile-card-%d"`, items[mobileAggregateItemPageLimit].ID),
		"expected first item from the older page",
	)
	assertNotContains(
		t,
		firstSection,
		fmt.Sprintf(`id="mobile-card-%d"`, items[mobileAggregateItemPageLimit*2].ID),
		"expected next overflow item to remain lazy",
	)
	assertContains(t, body, `data-mobile-feed-newest`, "expected newest-page action")
	assertContains(t, body, `data-mobile-feed-next`, "expected another older-page action")
	assertContains(
		t,
		body,
		fmt.Sprintf(
			`hx-get="/mobile/items/%d/reader?aggregate_feed_id=%d&amp;before_item_id=%d&amp;before_item_sort=`,
			laterItems[0].ID,
			firstID,
			state.ItemCursor.ItemID,
		),
		"expected sibling reader action to preserve the canonical older-page state",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`section_feed_id=%d&amp;section_only=1`, laterID),
		"expected sibling mark-read action to target its own feed without replacing canonical state",
	)

	wantPulsePrefix := fmt.Sprintf(
		`hx-post="/mobile/pulse?aggregate_feed_id=%d&amp;before_item_id=%d&amp;before_item_sort=`,
		firstID,
		state.ItemCursor.ItemID,
	)
	assertContains(t, body, wantPulsePrefix, "expected item continuation to update the pulse item cursor")
}

//revive:enable:function-length

//revive:disable:function-length Reader and mark-read assertions share one persisted fixture.
//nolint:funlen // Reader and mark-read assertions share one persisted aggregate-window fixture.
func TestMobileAggregateReaderAndMarkReadPreserveWindowState(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedIDs := make([]int64, 0, mobileAggregateFeedPageLimit+1)
	feedItems := make([][]view.ItemView, 0, mobileAggregateFeedPageLimit+1)

	for index := range mobileAggregateFeedPageLimit + 1 {
		feedID := mustUpsertFeed(
			t,
			app,
			fmt.Sprintf("http://example.com/mobile-window-%d", index),
			fmt.Sprintf("Window Feed %d", index),
		)
		feedIDs = append(feedIDs, feedID)
		feedItems = append(
			feedItems,
			seedMobileAggregateItems(t, app, feedID, fmt.Sprintf("Window %d", index), 1, time.Now().UTC()),
		)
	}

	page, err := store.ListUnreadFeedSections(
		context.Background(),
		app.db,
		nil,
		mobileAggregateFeedPageLimit,
		mobileAggregateItemPageLimit,
	)
	requireNoErr(t, err, "store.ListUnreadFeedSections for reader state: %v")

	if page.Next == nil {
		t.Fatal("expected reader fixture feed-window cursor")
	}

	lastIndex := mobileAggregateFeedPageLimit
	itemID := feedItems[lastIndex][0].ID
	state := mobileAggregateState{
		FeedCursor: page.Next,
		ItemCursor: nil,
		FeedID:     feedIDs[lastIndex],
	}
	readerPath := mobileReaderItemPath(itemID, 0, state)
	rec := getHTMXRequest(app, readerPath)
	assertResponseCode(t, rec, "mobile aggregate reader state status")

	if got := rec.Header().Get("Hx-Push-Url"); got != readerPath {
		t.Fatalf("expected HX-Push-Url %q, got %q", readerPath, got)
	}

	backPath := mobileStreamStatePath(0, state)
	markPath := mobileMarkReadItemPath(itemID, 0, state)
	body := rec.Body.String()
	assertContains(
		t,
		body,
		`hx-get="`+template.HTMLEscapeString(backPath)+`"`,
		"expected reader back action to preserve aggregate window state",
	)
	assertContains(
		t,
		body,
		`hx-post="`+template.HTMLEscapeString(markPath)+`"`,
		"expected reader mark-read action to preserve aggregate window state",
	)

	markRec := postHTMXRequest(app, markPath)
	assertResponseCode(t, markRec, "mobile aggregate reader mark-read status")

	if got := markRec.Header().Get("Hx-Replace-Url"); got != backPath {
		t.Fatalf("expected HX-Replace-Url %q, got %q", backPath, got)
	}

	markBody := markRec.Body.String()
	assertContains(t, markBody, `data-mobile-stream="true"`, "expected aggregate stream after reader mark-read")
	assertNotContains(t, markBody, fmt.Sprintf(`id="mobile-card-%d"`, itemID), "expected marked item removed")
	assertNotContains(
		t,
		markBody,
		fmt.Sprintf(`id="mobile-feed-section-%d"`, feedIDs[0]),
		"expected reader mark-read to preserve the later feed window",
	)
}

//revive:enable:function-length

func TestMobileAggregateCardMarkReadUpdatesOnlyOwningSection(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	firstID := mustUpsertFeed(t, app, "http://example.com/mobile-local-first", "Local First")
	laterID := mustUpsertFeed(t, app, "http://example.com/mobile-local-later", "Local Later")
	firstItems := seedMobileAggregateItems(t, app, firstID, "Local First", 2, time.Now().UTC())
	seedMobileAggregateItems(t, app, laterID, "Local Later", 1, time.Now().UTC().Add(time.Hour))

	state := mobileAggregateState{
		FeedCursor: nil,
		ItemCursor: nil,
		FeedID:     firstID,
	}
	markPath := mobileSectionMarkReadItemPath(firstItems[0].ID, firstID, state)
	rec := postHTMXRequest(app, markPath)
	assertResponseCode(t, rec, "mobile aggregate section-local mark-read status")

	body := rec.Body.String()
	assertNotContains(t, body, `data-mobile-stream="true"`, "expected section-local response")
	assertContains(
		t,
		body,
		fmt.Sprintf(`id="mobile-feed-section-%d"`, firstID),
		"expected owning section to rerender",
	)
	assertNotContains(
		t,
		body,
		fmt.Sprintf(`id="mobile-feed-section-%d"`, laterID),
		"expected unrelated section to stay mounted",
	)
	assertNotContains(
		t,
		body,
		fmt.Sprintf(`id="mobile-card-%d"`, firstItems[0].ID),
		"expected marked card removed from owning section",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`id="mobile-card-%d"`, firstItems[1].ID),
		"expected remaining card in owning section",
	)
	assertMobileTopBarOOBUpdate(t, body)

	reloaded, err := store.GetItem(context.Background(), app.db, firstItems[0].ID)
	requireNoErr(t, err, "store.GetItem marked aggregate card: %v")

	if !reloaded.IsRead {
		t.Fatal("expected aggregate card item to be marked read")
	}
}

//nolint:funlen // Batch repair, URL reset, bounds, and OOB state form one response contract.
func TestMobileAggregateCardMarkReadRecomputesBatchWhenFeedCatchesUp(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedIDs := make([]int64, 0, mobileAggregateFeedPageLimit+1)
	feedItems := make([][]view.ItemView, 0, mobileAggregateFeedPageLimit+1)

	for index := range mobileAggregateFeedPageLimit + 1 {
		feedID := mustUpsertFeed(
			t,
			app,
			fmt.Sprintf("http://example.com/mobile-retarget-%d", index),
			fmt.Sprintf("Retarget Feed %d", index),
		)

		feedIDs = append(feedIDs, feedID)
		feedItems = append(
			feedItems,
			seedMobileAggregateItems(t, app, feedID, fmt.Sprintf("Retarget %d", index), 1, time.Now().UTC()),
		)
	}

	state := mobileAggregateState{
		FeedCursor: nil,
		ItemCursor: nil,
		FeedID:     feedIDs[0],
	}
	rec := postHTMXRequest(app, mobileSectionMarkReadItemPath(feedItems[0][0].ID, feedIDs[0], state))
	assertResponseCode(t, rec, "mobile aggregate caught-up section mark-read status")

	if got := rec.Header().Get("Hx-Retarget"); got != "#mobile-stream-sections" {
		t.Fatalf("expected aggregate batch retarget, got %q", got)
	}

	if got := rec.Header().Get("Hx-Reswap"); got != "outerHTML show:top" {
		t.Fatalf("expected aggregate batch outerHTML show-top reswap, got %q", got)
	}

	if got := rec.Header().Get("Hx-Replace-Url"); got != pathMobileStream {
		t.Fatalf("expected caught-up item cursor to reset to %q, got %q", pathMobileStream, got)
	}

	body := rec.Body.String()
	assertNotContains(
		t,
		body,
		fmt.Sprintf(`id="mobile-feed-section-%d"`, feedIDs[0]),
		"expected newly caught-up section removed",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`id="mobile-feed-section-%d"`, feedIDs[mobileAggregateFeedPageLimit]),
		"expected next unread feed promoted into the recomputed batch",
	)

	if got := strings.Count(body, `data-mobile-feed-section`); got != mobileAggregateFeedPageLimit {
		t.Fatalf(
			"expected recomputed batch to remain bounded at %d sections, got %d",
			mobileAggregateFeedPageLimit,
			got,
		)
	}

	assertMobileTopBarOOBUpdate(t, body)
}

func TestMobileAggregateSiblingCatchUpPreservesCanonicalItemPage(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	firstID := mustUpsertFeed(t, app, "http://example.com/mobile-canonical-first", "Canonical First")
	siblingID := mustUpsertFeed(t, app, "http://example.com/mobile-canonical-sibling", "Canonical Sibling")
	firstItems := seedMobileAggregateItems(
		t,
		app,
		firstID,
		"Canonical First",
		mobileAggregateItemPageLimit+1,
		time.Now().UTC(),
	)
	siblingItems := seedMobileAggregateItems(t, app, siblingID, "Canonical Sibling", 1, time.Now().UTC())

	page, err := store.ListUnreadFeedSections(
		context.Background(), app.db, nil, mobileAggregateFeedPageLimit, mobileAggregateItemPageLimit,
	)
	requireNoErr(t, err, "store.ListUnreadFeedSections canonical sibling: %v")

	state := mobileAggregateState{FeedCursor: nil, ItemCursor: page.Sections[0].Next, FeedID: firstID}
	target := mobileSectionMarkReadItemPath(siblingItems[0].ID, siblingID, state)
	rec := postHTMXRequest(app, target)
	assertResponseCode(t, rec, "mobile aggregate sibling caught-up mark-read status")

	if got, want := rec.Header().Get("Hx-Replace-Url"), mobileStreamStatePath(0, state); got != want {
		t.Fatalf("expected canonical item-page URL %q, got %q", want, got)
	}

	body := rec.Body.String()
	assertNotContains(t, body, fmt.Sprintf(`id="mobile-feed-section-%d"`, siblingID), "expected sibling removed")
	assertContains(t, body, fmt.Sprintf(`id="mobile-card-%d"`, firstItems[mobileAggregateItemPageLimit].ID),
		"expected canonical older item page preserved")
	assertNotContains(t, body, fmt.Sprintf(`id="mobile-card-%d"`, firstItems[0].ID),
		"expected canonical page not reset to newest")
}

func TestMobileStreamHTMXHistoryRestoreReturnsFullPage(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "http://example.com/mobile-history-miss", "History Miss")
	seedMobileAggregateItems(t, app, feedID, "History Miss", 1, time.Now().UTC())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, pathMobileStream, http.NoBody)
	req.Header.Set("Hx-Request", "true")
	req.Header.Set("Hx-History-Restore-Request", "true")

	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	assertResponseCode(t, rec, "mobile history restore status")
	assertContains(t, rec.Body.String(), "<!doctype html>", "expected full page for mobile history cache miss")
	assertContains(t, rec.Body.String(), `data-mobile-stream="true"`, "expected mobile stream on history cache miss")

	if got := rec.Header().Get("Hx-Replace-Url"); got != "" {
		t.Fatalf("expected no HX-Replace-Url for mobile history restore, got %q", got)
	}
}

func TestMobileReaderHTMXHistoryRestoreReturnsFullPage(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "http://example.com/mobile-reader-history", "Reader History")
	items := seedMobileAggregateItems(t, app, feedID, "Reader History", 1, time.Now().UTC())
	state := mobileAggregateState{FeedCursor: nil, ItemCursor: nil, FeedID: feedID}
	target := mobileReaderItemPath(items[0].ID, 0, state)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, http.NoBody)
	req.Header.Set("Hx-Request", "true")
	req.Header.Set("Hx-History-Restore-Request", "true")

	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	assertResponseCode(t, rec, "mobile reader history restore status")
	assertContains(t, rec.Body.String(), "<!doctype html>", "expected full page for reader history cache miss")
	assertContains(t, rec.Body.String(), `data-mobile-reader="true"`, "expected reader on history cache miss")

	if got := rec.Header().Get("Hx-Push-Url"); got != "" {
		t.Fatalf("expected no HX-Push-Url for reader history restore, got %q", got)
	}
}

func seedMobileAggregateItems(
	t *testing.T,
	app *App,
	feedID int64,
	prefix string,
	count int,
	newest time.Time,
) []view.ItemView {
	t.Helper()

	items := make([]*gofeed.Item, 0, count)
	for index := range count {
		title := fmt.Sprintf("%s %02d", prefix, index)
		published := newest.Add(-time.Duration(index) * time.Minute)
		items = append(
			items,
			newGofeedItem(
				title,
				fmt.Sprintf("http://example.com/%d/%d", feedID, index),
				fmt.Sprintf("mobile-aggregate-%d-%d", feedID, index),
				"<p>Summary</p>",
				&published,
			),
		)
	}

	mustUpsertItems(t, app, feedID, items)

	stored := mustListItems(t, app, feedID)
	if len(stored) != count {
		t.Fatalf("expected %d mobile aggregate items, got %d", count, len(stored))
	}

	return stored
}

func assertMobileFeedSectionOrder(t *testing.T, body string, feedIDs ...int64) {
	t.Helper()

	markers := make([]string, 0, len(feedIDs))

	for _, feedID := range feedIDs {
		markers = append(markers, fmt.Sprintf(`id="mobile-feed-section-%d"`, feedID))
	}

	assertBodyTokenOrder(t, body, markers...)
}

func assertBodyTokenOrder(t *testing.T, body string, tokens ...string) {
	t.Helper()

	previous := -1

	for _, token := range tokens {
		index := requireBodyIndex(t, body, token, fmt.Sprintf("expected body token %q", token))
		if index <= previous {
			t.Fatalf("expected body token %q after the preceding token", token)
		}

		previous = index
	}
}

func requireMobileFeedSectionBody(t *testing.T, body string, feedID int64) string {
	t.Helper()

	marker := fmt.Sprintf(`id="mobile-feed-section-%d"`, feedID)
	start := requireBodyIndex(t, body, marker, fmt.Sprintf("expected mobile feed section %d", feedID))

	endOffset := strings.Index(body[start:], "</section>")
	if endOffset == -1 {
		t.Fatalf("expected closing tag for mobile feed section %d", feedID)
	}

	return body[start : start+endOffset+len("</section>")]
}

func TestMobileStreamRendersCompactPreviewForSummaryOnlyItems(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(), app.db, "http://example.com/mobile-preview", "Mobile Preview")
	requireNoErr(t, err, errStoreUpsertFeed)

	now := time.Now().UTC()
	_, err = store.UpsertItems(context.Background(), app.db, feedID, []*gofeed.Item{{
		Title: "Summary Only",
		Link:  "http://example.com/mobile-summary-only",
		GUID:  "mobile-summary-only",
		Description: mobileSummaryHTML(
			"Mobile summary heading",
			"Mobile summary preview.",
			"/hero.jpg",
			"hero image",
		),
		PublishedParsed: new(now),
	}})
	requireNoErr(t, err, errStoreUpsertItems)

	rec := getRequest(app, pathMobileStream)
	assertResponseCode(t, rec, "mobile summary-only preview status")

	body := rec.Body.String()
	assertContains(
		t,
		body,
		"<p>Mobile summary heading Mobile summary preview.</p>",
		"expected summary-only mobile card to render compact preview text",
	)
	assertNotContains(
		t,
		body,
		"<h2>Mobile summary heading</h2>",
		"expected summary-only mobile card to avoid full summary heading markup",
	)
	assertNotContains(
		t,
		body,
		`<img src="/hero.jpg" alt="hero image">`,
		"expected summary-only mobile card to avoid summary media markup",
	)
}

func TestMobileStreamRendersCompactPreviewForItemsWithSummaryAndContent(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(
		context.Background(),
		app.db,
		"http://example.com/mobile-combined",
		"Mobile Combined",
	)
	requireNoErr(t, err, errStoreUpsertFeed)

	published := time.Now().UTC()
	_, err = store.UpsertItems(context.Background(), app.db, feedID, []*gofeed.Item{{
		Title: "Summary And Content",
		Link:  "http://example.com/mobile-summary-content",
		GUID:  "mobile-summary-content",
		Description: mobileSummaryHTML(
			"Combined summary heading",
			"Combined summary preview.",
			"/combined.jpg",
			"combined image",
		),
		Content:         "<p>Combined content body that should stay out of the stream preview.</p>",
		PublishedParsed: new(published),
	}})
	requireNoErr(t, err, errStoreUpsertItems)

	rec := getRequest(app, pathMobileStream)
	assertResponseCode(t, rec, "mobile summary-and-content preview status")

	body := rec.Body.String()
	assertContains(
		t,
		body,
		"<p>Combined summary heading Combined summary preview.</p>",
		"expected summary-plus-content mobile card to render summary preview text",
	)
	assertNotContains(
		t,
		body,
		"Combined content body that should stay out of the stream preview.",
		"expected mobile stream preview to use summary text instead of content HTML",
	)
}

func mobileSummaryHTML(heading, preview, imagePath, altText string) string {
	return `<div><h2>` + heading + `</h2><p>` + preview + `</p>` +
		`<img src="` + imagePath + `" alt="` + altText + `"></div>`
}

func TestDesktopIndexIgnoresMobileSelectedFeedQuery(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID := mustUpsertFeed(t, app, "http://example.com/desktop", "Desktop Feed")
	mustUpsertSingleStory(
		t,
		app,
		feedID,
		"Desktop Story",
		"http://example.com/desktop-story",
		"desktop-story",
		time.Now().UTC().Add(-time.Hour),
	)

	rec := getRequest(app, fmt.Sprintf("%s?selected_feed_id=%d", pathIndex, feedID))
	assertResponseCode(t, rec, "desktop index selected-feed query status")

	body := rec.Body.String()
	assertContains(t, body, "Desktop Feed", "expected desktop feed list to render")
	assertContains(t, body, emptyStateNoFeed, "expected desktop index empty state to remain")
	assertNotContains(t, body, `data-mobile-stream="true"`, "expected mobile stream to stay off desktop route")
	assertNotContains(
		t,
		body,
		`class="mobile-stream-filter"`,
		"expected mobile filter controls to stay off desktop route",
	)
}

func TestDesktopIndexHTMXRestoresReaderLayout(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	firstFeedID := mustUpsertFeed(t, app, "http://example.com/desktop-first", "Desktop First")
	selectedFeedID := mustUpsertFeed(t, app, "http://example.com/desktop-selected", "Desktop Selected")
	mustUpsertSingleStory(
		t,
		app,
		selectedFeedID,
		"Selected Desktop Story",
		"http://example.com/selected-desktop-story",
		"selected-desktop-story",
		time.Now().UTC().Add(-time.Hour),
	)

	assertDesktopIndexHTMXRestore(
		t,
		app,
		fmt.Sprintf("%s?selected_feed_id=%d", pathIndex, selectedFeedID),
		selectedFeedID,
		"Selected Desktop Story",
	)
	assertDesktopIndexHTMXRestore(t, app, pathIndex, firstFeedID, "Desktop First")
}

func assertDesktopIndexHTMXRestore(t *testing.T, app *App, target string, wantFeedID int64, wantTitle string) {
	t.Helper()

	rec := getHTMXRequest(app, target)
	assertResponseCode(t, rec, "desktop index htmx restore status")

	if got := rec.Header().Get("Hx-Replace-Url"); got != pathIndex {
		t.Fatalf("expected HX-Replace-Url %q, got %q", pathIndex, got)
	}

	body := rec.Body.String()
	assertNotContains(t, body, "<!doctype html>", "expected desktop restore to be an htmx partial")
	assertContains(
		t,
		body,
		fmt.Sprintf(`id="item-list" tabindex="-1" data-feed-id="%d"`, wantFeedID),
		"expected desktop restore to load the selected feed",
	)
	assertContains(t, body, wantTitle, "expected desktop restore feed content")
	assertContains(t, body, `hx-post="/feeds/pulse"`, "expected desktop pulse action to be restored")
	assertNotContains(t, body, `hx-post="/mobile/pulse`, "expected mobile pulse action to be removed")
	assertContains(
		t,
		body,
		`id="topbar-mobile-slot" class="topbar-mobile-slot" hx-swap-oob="outerHTML"`,
		"expected mobile selector slot to be cleared",
	)
	assertContains(t, body, feedListSwapAttr, msgFeedListOOBSwap)
	assertContains(t, body, contentPanelSwapAttr, "expected desktop content panel OOB reset")
	assertNotContains(t, body, `data-mobile-stream="true"`, "expected mobile stream to be removed")
}

func TestDesktopIndexHTMXRestoresEmptyReaderLayout(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	rec := getHTMXRequest(app, pathIndex)
	assertResponseCode(t, rec, "empty desktop index htmx restore status")

	body := rec.Body.String()
	assertContains(t, body, emptyStateNoFeed, "expected desktop empty state")
	assertContains(t, body, `hx-post="/feeds/pulse"`, "expected desktop pulse action")
	assertContains(t, body, contentPanelSwapAttr, "expected empty desktop content panel OOB reset")
	assertNotContains(t, body, `class="mobile-stream-filter"`, "expected mobile selector to be cleared")
}

func TestDesktopIndexHTMXHistoryRestoreReturnsFullPage(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, pathIndex, http.NoBody)
	req.Header.Set("Hx-Request", "true")
	req.Header.Set("Hx-History-Restore-Request", "true")

	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	assertResponseCode(t, rec, "desktop index htmx history restore status")
	assertContains(t, rec.Body.String(), "<!doctype html>", "expected full page for htmx history cache miss")

	if got := rec.Header().Get("Hx-Replace-Url"); got != "" {
		t.Fatalf("expected no HX-Replace-Url for history restore, got %q", got)
	}
}

func TestParseSelectedFeedID(t *testing.T) {
	t.Parallel()

	formBody := strings.NewReader(url.Values{formSelectedFeedID: {"42"}}.Encode())
	formRequest := httptest.NewRequestWithContext(context.Background(), http.MethodPost, pathMobileStream, formBody)
	formRequest.Header.Set(headerContentType, formURLEncoded)

	testCases := []struct {
		req  *http.Request
		name string
		want int64
	}{
		{
			name: "query parameter",
			req: httptest.NewRequestWithContext(
				context.Background(), http.MethodGet,
				pathMobileStream+"?selected_feed_id=21", http.NoBody,
			),
			want: 21,
		},
		{
			name: "form body",
			req:  formRequest,
			want: 42,
		},
		{
			name: "zero value",
			req: httptest.NewRequestWithContext(
				context.Background(), http.MethodGet,
				pathMobileStream+"?selected_feed_id=0", http.NoBody,
			),
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
	assertNotContains(t, body, "Read what is here now.", "expected mobile hero copy to be removed")
	assertNotContains(t, body, ">Refresh</button>", "expected in-stream refresh button to be removed")
	assertMobileTopBarOOBUpdate(t, body)
	assertContains(
		t,
		body,
		`id="topbar-brand-button"`,
		"expected mobile stream response to refresh the shared brand button",
	)
	assertContains(
		t,
		body,
		`hx-post="/mobile/pulse"`,
		"expected mobile stream response to wire the brand button to mobile pulse",
	)
	assertContains(
		t,
		body,
		`hx-indicator="#topbar-brand-button"`,
		"expected brand button to show request feedback while pulsing",
	)
	assertNotContains(t, body, `class="mobile-stream-refresh-button"`, "expected separate refresh button to be removed")
	assertContains(
		t,
		body,
		`aria-label="Refresh all feeds"`,
		"expected all-feeds refresh label on the brand button",
	)
	assertContains(
		t,
		body,
		`<span class="brand-subtitle-pending" aria-live="polite">Refreshing all feeds</span>`,
		"expected all-feeds refreshing feedback text",
	)
}

func TestMobileStreamSelectorHTMXKeepsActiveSelectorMounted(t *testing.T) {
	t.Parallel()

	fixture := newMobileStreamSelectorFixture(t)
	target := fmt.Sprintf("%s?selected_feed_id=%d", pathMobileStream, fixture.brightID)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, http.NoBody)
	req.Header.Set("Hx-Request", "true")
	req.Header.Set("Hx-Trigger", "mobile-stream-feed-filter")

	rec := httptest.NewRecorder()
	fixture.app.Routes().ServeHTTP(rec, req)
	assertResponseCode(t, rec, "mobile stream selector htmx status")

	if got := rec.Header().Get("Hx-Replace-Url"); got != target {
		t.Fatalf("expected HX-Replace-Url %q, got %q", target, got)
	}

	body := rec.Body.String()
	assertContains(t, body, "Bright Story", "expected filtered stream item in selector response")
	assertContains(
		t,
		body,
		`id="topbar-brand-slot" class="topbar-brand-slot" hx-swap-oob="outerHTML"`,
		"expected selector response to refresh the brand slot",
	)
	assertNotContains(
		t,
		body,
		`id="topbar-mobile-slot" class="topbar-mobile-slot is-active" hx-swap-oob="outerHTML"`,
		"expected selector-triggered response to leave the active mobile selector mounted",
	)
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
	assertNotContains(t, body, `data-mobile-feed-section`, "expected specific-feed mode to remain a flat stream")
	assertContains(
		t,
		body,
		fmt.Sprintf(`id="mobile-card-%d" data-mobile-item-id="%d"`, alphaItems[0].ID, alphaItems[0].ID),
		"expected selected-feed mobile card to expose stable item hooks",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`/mobile/items/%d/reader?selected_feed_id=%d`, alphaItems[0].ID, alphaID),
		"expected reader action to preserve selected feed",
	)
}

func TestMobileStreamSelectorRendersInlineInHeaderAndShowsUnreadFeedsOnly(t *testing.T) {
	t.Parallel()

	fixture := newMobileStreamSelectorFixture(t)

	rec := getRequest(fixture.app, pathMobileStream)
	assertResponseCode(t, rec, "mobile stream selector status")

	body := rec.Body.String()
	assertMobileStreamSelectorInHeader(t, body)
	assertContains(
		t,
		body,
		`id="topbar-brand-button"`,
		"expected mobile route to render the shared brand button",
	)
	assertContains(
		t,
		body,
		`hx-post="/mobile/pulse"`,
		"expected mobile route brand button to use mobile pulse",
	)
	assertContains(t, body, `<option value="0"`, "expected all-feeds option")
	assertContains(t, body, `>All feeds</option>`, "expected all-feeds option label")
	assertContains(
		t,
		body,
		fmt.Sprintf(`<option value="%d"`, fixture.brightID),
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
		`>Show</button>`,
		"expected mobile selector to submit on selection without a show button",
	)
	assertNotContains(
		t,
		body,
		fmt.Sprintf(`<option value="%d">Quiet Feed</option>`, fixture.quietID),
		"expected read-only feed to be omitted from selector",
	)
	assertNotContains(t, body, "Read what is here now.", "expected stream hero copy to be removed")
	assertNotContains(t, body, ">Refresh</button>", "expected in-stream refresh button to be removed")
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
	assertContains(
		t,
		rec.Body.String(),
		fmt.Sprintf(`hx-post="/mobile/feeds/%d/refresh?selected_feed_id=%d"`, feedID, feedID),
		"expected brand button to refresh the selected feed",
	)
	assertContains(
		t,
		rec.Body.String(),
		`<span class="brand-subtitle-pending" aria-live="polite">Refreshing Bright Feed</span>`,
		"expected selected-feed refreshing feedback text",
	)
}

func TestMobileStreamRendersFlatRowsWithIconMarkRead(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "http://example.com/a-feed", "Alpha Feed")
	mustUpsertSingleStory(
		t,
		app,
		feedID,
		"Alpha Story",
		"http://example.com/alpha-story",
		"alpha-story",
		time.Now().UTC().Add(-time.Hour),
	)

	rec := getRequest(app, pathMobileStream)
	assertResponseCode(t, rec, "mobile flat row status")

	body := rec.Body.String()
	assertNotContains(
		t,
		body,
		`class="mobile-feed-status-panel"`,
		"expected mobile feed status panel to be removed",
	)
	assertNotContains(t, body, `class="mobile-stream-refresh-button"`, "expected separate refresh button to be removed")
	assertNotContains(t, body, `class="mobile-card-actions"`, "expected trailing card action row to be removed")
	assertContains(t, body, `data-mobile-pull-refresh data-state="idle"`, "expected idle pull-refresh indicator")
	assertContains(t, body, `data-mobile-pull-announcement`, "expected pull-refresh live region")
	assertContains(t, body, `aria-live="polite"`, "expected polite pull-refresh announcements")
	assertContains(t, body, `class="mobile-card-title-row"`, "expected title row to hold item actions")
	assertContains(t, body, `class="mobile-card-mark-read"`, "expected icon-only mark-read button")
	assertContains(t, body, `aria-label="Mark Alpha Story read"`, "expected mark-read button accessible name")
	assertContains(t, body, `<svg class="icon"`, "expected mark-read button to render an icon")
	assertContains(
		t,
		body,
		`hx-post="/mobile/pulse"`,
		"expected all-feeds refresh action to stay on the brand button",
	)
	assertContains(
		t,
		body,
		`aria-label="Refresh all feeds"`,
		"expected all-feeds brand button accessible name",
	)
}

func TestMobileFeedRefreshPreservesFilteredSelectionAndURL(t *testing.T) {
	t.Parallel()

	base := time.Now().UTC().Add(-2 * time.Hour)
	feedServer, feedURL := testutil.NewFeedServer(t, manualRefreshInitialXML(base))
	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(), app.db, feedURL, manualRefreshTitle)
	requireNoErr(t, err, errStoreUpsertFeed)

	_, refreshErr := feedpkg.Refresh(context.Background(), app.db, feedID)
	requireNoErr(t, refreshErr, "feedpkg.Refresh initial: %v")

	feedServer.SetFeedXML(manualRefreshUpdatedXML(base))

	target := fmt.Sprintf(pathMobileFeedRefresh+"?selected_feed_id=%d", feedID, feedID)
	rec := postHTMXRequest(app, target)
	assertResponseCode(t, rec, "mobile feed refresh status")

	expectedURL := fmt.Sprintf("%s?selected_feed_id=%d", pathMobileStream, feedID)
	if got := rec.Header().Get("Hx-Replace-Url"); got != expectedURL {
		t.Fatalf("expected filtered HX-Replace-Url, got %q", got)
	}

	body := rec.Body.String()
	assertContains(t, body, "Second", "expected refreshed item in mobile stream")
	assertContains(
		t,
		body,
		fmt.Sprintf(`hx-post="/mobile/feeds/%d/refresh?selected_feed_id=%d"`, feedID, feedID),
		"expected mobile refresh response to preserve selected feed on the brand button",
	)

	items := mustListItems(t, app, feedID)
	assertItemCount(t, items, expectedTwoItems)
}

func TestMobilePulseHidesUpstreamRefreshErrorDetails(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	feedID := mustUpsertFeed(t, app, "not a valid feed url", "Broken Mobile Feed")
	setPulseRefreshState(t, app, feedID, time.Now().UTC().Add(-2*time.Hour), "")

	rec := postHTMXRequest(app, pathMobilePulse)
	assertResponseCode(t, rec, "mobile pulse error status")

	body := rec.Body.String()
	assertNotContains(t, body, `class="mobile-stream-status"`, "expected mobile pulse response to omit status pill")
	assertNotContains(t, body, "Updated 0 feeds.", "expected mobile pulse text to stay hidden")
	assertNotContains(t, body, "unsupported protocol scheme", "expected upstream error detail to stay hidden")
	assertNotContains(t, body, "not a valid feed url", "expected upstream feed URL to stay hidden")
	assertPulseFeedStatus(t, app, feedID, pulseFeedStatusError)
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

	body := rec.Body.String()
	assertMobileTopBarOOBUpdate(t, body)
	assertFilteredMobileEmptyState(t, body, "Solo Feed", feedID, otherFeedID)
	assertContains(
		t,
		body,
		fmt.Sprintf(`hx-post="/mobile/feeds/%d/refresh?selected_feed_id=%d"`, feedID, feedID),
		"expected mark-read response to keep brand refresh tied to the selected feed",
	)
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
	assertMobileTopBarOOBUpdate(t, body)
	assertNotContains(t, body, `class="mobile-stream-status"`, "expected filtered pulse response to omit status pill")
	assertNotContains(t, body, "Already fresh enough.", "expected filtered pulse status text to stay hidden")
	assertFilteredMobileEmptyState(t, body, "Caught Up Feed", feedID, otherFeedID)
	assertContains(
		t,
		body,
		fmt.Sprintf(`hx-post="/mobile/feeds/%d/refresh?selected_feed_id=%d"`, feedID, feedID),
		"expected pulse response to preserve selected feed in mobile brand button",
	)
}

func TestMobileReaderView(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	feedID, err := store.UpsertFeed(context.Background(), app.db, "http://example.com/rss", "Feed Title")
	requireNoErr(t, err, errStoreUpsertFeed)

	published := time.Now().UTC().Add(-time.Hour)

	const summaryOnlyReaderText = "Reader fallback summary."

	_, err = store.UpsertItems(context.Background(), app.db, feedID, []*gofeed.Item{
		newGofeedItem(
			"Unread Story",
			"http://example.com/story",
			"story-1",
			"<p>"+summaryOnlyReaderText+"</p>",
			&published,
		),
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
	assertContains(t, body, summaryOnlyReaderText, "expected summary-only reader body content")
	assertContains(t, body, "/mobile/stream", "expected back-to-stream action")
	assertContains(
		t,
		body,
		`id="mobile-stream-feed-filter"`,
		"expected mobile reader page to keep the shared topbar selector visible",
	)
	assertContains(
		t,
		body,
		`hx-post="/mobile/pulse"`,
		"expected full-page mobile reader brand button to use mobile pulse",
	)
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
	assertMobileTopBarOOBUpdate(t, body)
	assertContains(
		t,
		body,
		fmt.Sprintf(`hx-post="/mobile/feeds/%d/refresh?selected_feed_id=%d"`, feedID, feedID),
		"expected reader response to preserve selected feed in mobile brand button",
	)
	assertContains(
		t,
		body,
		fmt.Sprintf(`<option value="%d" selected>Feed Title</option>`, feedID),
		"expected reader response to keep the selected feed visible in the topbar selector",
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
	assertNotContains(t, body, `class="mobile-stream-status"`, "expected mobile pulse response to omit status pill")
	assertNotContains(t, body, "Already fresh enough.", "expected mobile pulse status text to stay hidden")
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
