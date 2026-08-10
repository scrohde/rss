//go:build smoke

//nolint:testpackage // Smoke tests intentionally exercise unexported test helpers and wiring.
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/mmcdole/gofeed"

	"rss/internal/store"
)

const (
	smokeBrowserPathEnv = "PULSE_SMOKE_BROWSER_BIN"
	smokeWaitTimeout    = 10 * time.Second
)

type smokeFixture struct {
	primaryFeedID          int64
	archiveFeedID          int64
	secondaryFeedID        int64
	tertiaryFeedID         int64
	quaternaryFeedID       int64
	primaryFirstItemID     int64
	secondaryFirstItemID   int64
	secondarySecondItemID  int64
	secondaryNoReaderID    int64
	secondarySummaryItemID int64
}

type mobileAggregateSmokeFixture struct {
	olderState       mobileAggregateState
	highFeedID       int64
	quietFeedID      int64
	laterFeedID      int64
	highNewestItemID int64
	highOldestItemID int64
	laterItemID      int64
}

type mobileReaderNavigationSmokeFixture struct {
	itemIDs []int64
}

func newSmokeApp(t *testing.T) *App {
	t.Helper()

	app := newTestApp(t)
	staticRoot := filepath.Join(pathParentDir, pathParentDir, "static")
	app.SetStaticFS(os.DirFS(staticRoot))

	return app
}

func seedSmokePulseStatuses(t *testing.T, app *App, fixture smokeFixture) {
	t.Helper()

	longTitle := "Primary Feed With An Extraordinarily Long Name For Pulse Layout Verification"
	err := store.UpdateFeedTitle(context.Background(), app.db, fixture.primaryFeedID, longTitle)
	if err != nil {
		t.Fatalf("store.UpdateFeedTitle: %v", err)
	}

	app.resetPulseStatuses(
		[]int64{
			fixture.primaryFeedID,
			fixture.secondaryFeedID,
			fixture.tertiaryFeedID,
			fixture.quaternaryFeedID,
			fixture.archiveFeedID,
		},
		[]int64{fixture.secondaryFeedID},
	)
	app.markPulseFeedStatus(fixture.tertiaryFeedID, pulseFeedStatusError)
}

func newSmokeServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for smoke server: %v", err)
	}

	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()

	return server
}

func seedSmokeFixture(t *testing.T, app *App) smokeFixture {
	t.Helper()

	base := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	primaryFeedID := mustUpsertFeed(t, app, "https://example.com/feed-primary.xml", "Primary Feed")
	secondaryFeedID := mustUpsertFeed(t, app, "https://example.com/feed-secondary.xml", "Secondary Feed")
	tertiaryFeedID := mustUpsertFeed(t, app, "https://example.com/feed-tertiary.xml", "Tertiary Feed")
	quaternaryFeedID := mustUpsertFeed(t, app, "https://example.com/feed-quaternary.xml", "Quaternary Feed")
	archiveFeedID := mustUpsertFeed(t, app, "https://example.com/feed-archive.xml", "Archive Feed")

	mustUpsertItems(t, app, primaryFeedID, []*gofeed.Item{
		newSmokeItem("Primary One", "https://example.com/p1", "primary-1", base.Add(-3*time.Hour)),
	})
	mustUpsertItems(t, app, secondaryFeedID, []*gofeed.Item{
		newSmokeItem("Secondary One", "https://example.com/s1", "secondary-1", base),
		newSmokeItem("Secondary Two", "https://example.com/s2", "secondary-2", base.Add(-time.Hour)),
		newSmokeNoReaderItem(
			"Secondary No Reader",
			"https://example.com/s-no-reader",
			"secondary-no-reader",
			base.Add(-90*time.Minute),
		),
		newSmokeSummaryOnlyItem(
			"Secondary Summary Only",
			"https://example.com/s3",
			"secondary-3",
			base.Add(-2*time.Hour),
		),
	})
	mustUpsertItems(t, app, tertiaryFeedID, []*gofeed.Item{
		newSmokeItem("Tertiary One", "https://example.com/t1", "tertiary-1", base.Add(-2*time.Hour)),
	})
	mustUpsertItems(t, app, quaternaryFeedID, []*gofeed.Item{
		newSmokeItem("Quaternary One", "https://example.com/q1", "quaternary-1", base.Add(-4*time.Hour)),
	})

	primaryItems := mustListItems(t, app, primaryFeedID)
	assertItemCount(t, primaryItems, 1)

	secondaryItems := mustListItems(t, app, secondaryFeedID)
	assertItemCount(t, secondaryItems, 4)

	return smokeFixture{
		primaryFeedID:          primaryFeedID,
		archiveFeedID:          archiveFeedID,
		secondaryFeedID:        secondaryFeedID,
		tertiaryFeedID:         tertiaryFeedID,
		quaternaryFeedID:       quaternaryFeedID,
		primaryFirstItemID:     primaryItems[0].ID,
		secondaryFirstItemID:   secondaryItems[0].ID,
		secondarySecondItemID:  secondaryItems[1].ID,
		secondaryNoReaderID:    secondaryItems[2].ID,
		secondarySummaryItemID: secondaryItems[3].ID,
	}
}

func seedMobileAggregateSmokeFixture(t *testing.T, app *App) mobileAggregateSmokeFixture {
	t.Helper()

	base := time.Date(2026, time.January, 2, 12, 0, 0, 0, time.UTC)
	highFeedID := mustUpsertFeed(t, app, "https://example.com/mobile-aggregate-high.xml", "Aggregate High")
	quietFeedID := mustUpsertFeed(t, app, "https://example.com/mobile-aggregate-quiet.xml", "Aggregate Quiet")
	laterFeedID := mustUpsertFeed(t, app, "https://example.com/mobile-aggregate-later.xml", "Aggregate Later")

	err := store.UpdateFeedOrder(context.Background(), app.db, []int64{highFeedID, quietFeedID, laterFeedID})
	if err != nil {
		t.Fatalf("store.UpdateFeedOrder mobile aggregate: %v", err)
	}

	highItems := seedMobileAggregateItems(
		t,
		app,
		highFeedID,
		"Aggregate High",
		mobileAggregateItemPageLimit+1,
		base.Add(-time.Hour),
	)
	quietItems := seedMobileAggregateItems(t, app, quietFeedID, "Aggregate Quiet", 1, base)
	laterItems := seedMobileAggregateItems(t, app, laterFeedID, "Aggregate Later", 1, base.Add(time.Hour))

	err = store.MarkItemRead(context.Background(), app.db, quietItems[0].ID)
	if err != nil {
		t.Fatalf("store.MarkItemRead quiet aggregate item: %v", err)
	}

	sectionPage, err := store.ListUnreadFeedSections(
		context.Background(),
		app.db,
		nil,
		mobileAggregateFeedPageLimit,
		mobileAggregateItemPageLimit,
	)
	if err != nil {
		t.Fatalf("store.ListUnreadFeedSections mobile aggregate fixture: %v", err)
	}

	if len(sectionPage.Sections) == 0 || sectionPage.Sections[0].Next == nil {
		t.Fatal("expected high-volume aggregate feed to expose an older page")
	}

	return mobileAggregateSmokeFixture{
		olderState: mobileAggregateState{
			FeedCursor: nil,
			FeedID:     highFeedID,
			ItemCursor: sectionPage.Sections[0].Next,
		},
		highFeedID:       highFeedID,
		quietFeedID:      quietFeedID,
		laterFeedID:      laterFeedID,
		highNewestItemID: highItems[0].ID,
		highOldestItemID: highItems[mobileAggregateItemPageLimit].ID,
		laterItemID:      laterItems[0].ID,
	}
}

func seedMobileReaderNavigationSmokeFixture(t *testing.T, app *App) mobileReaderNavigationSmokeFixture {
	t.Helper()

	feedID := mustUpsertFeed(
		t,
		app,
		"https://example.com/mobile-reader-navigation.xml",
		"Reader Navigation",
	)
	base := time.Date(2026, time.January, 4, 12, 0, 0, 0, time.UTC)
	feedItems := make([]*gofeed.Item, 0, mobileAggregateItemPageLimit)
	for index := range mobileAggregateItemPageLimit {
		item := newSmokeItem(
			fmt.Sprintf("Reader Navigation %02d", index+1),
			fmt.Sprintf("https://example.com/mobile-reader-navigation/%d", index+1),
			fmt.Sprintf("mobile-reader-navigation-%d", index+1),
			base.Add(-time.Duration(index)*time.Minute),
		)
		item.Content = strings.Repeat(
			fmt.Sprintf("<p>Reader navigation item %d has enough content to preserve document scroll.</p>", index+1),
			32,
		)
		feedItems = append(feedItems, item)
	}
	mustUpsertItems(t, app, feedID, feedItems)
	items := mustListItems(t, app, feedID)
	assertItemCount(t, items, mobileAggregateItemPageLimit)

	itemIDs := make([]int64, 0, len(items))
	for _, item := range items {
		itemIDs = append(itemIDs, item.ID)
	}

	return mobileReaderNavigationSmokeFixture{
		itemIDs: itemIDs,
	}
}

func newSmokeItem(title, link, guid string, publishedAt time.Time) *gofeed.Item {
	return &gofeed.Item{
		Title:           title,
		Link:            link,
		GUID:            guid,
		Description:     fmt.Sprintf("<p>%s summary</p>", title),
		Content:         fmt.Sprintf("<p>%s content</p>", title),
		PublishedParsed: timePtr(publishedAt),
	}
}

func newSmokeSummaryOnlyItem(title, link, guid string, publishedAt time.Time) *gofeed.Item {
	return &gofeed.Item{
		Title:           title,
		Link:            link,
		GUID:            guid,
		Description:     `<div><h2>Summary-only heading</h2><p>Summary-only fallback preview.</p><img src="https://example.com/summary.jpg" alt="summary image"></div>`,
		PublishedParsed: timePtr(publishedAt),
	}
}

func newSmokeNoReaderItem(title, link, guid string, publishedAt time.Time) *gofeed.Item {
	return &gofeed.Item{
		Title:           title,
		Link:            link,
		GUID:            guid,
		PublishedParsed: timePtr(publishedAt),
	}
}

func timePtr(value time.Time) *time.Time {
	ptr := new(time.Time)
	*ptr = value

	return ptr
}

func newSmokeBrowserContext(t *testing.T) context.Context {
	t.Helper()

	browserPath, ok := resolveSmokeBrowserPath()
	if !ok {
		t.Skipf(
			"smoke browser not found; set %s to an installed Chrome/Chromium binary",
			smokeBrowserPathEnv,
		)
	}

	allocatorOptions := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browserPath),
		chromedp.Headless,
		chromedp.DisableGPU,
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("window-size", "1365,1024"),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)

	allocatorCtx, allocatorCancel := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)
	t.Cleanup(allocatorCancel)

	browserCtx, browserCancel := chromedp.NewContext(allocatorCtx)
	t.Cleanup(browserCancel)

	timeoutCtx, timeoutCancel := context.WithTimeout(browserCtx, 60*time.Second)
	t.Cleanup(timeoutCancel)

	return timeoutCtx
}

func resolveSmokeBrowserPath() (string, bool) {
	if browserPath := os.Getenv(smokeBrowserPathEnv); browserPath != "" {
		if isExecutablePath(browserPath) {
			return browserPath, true
		}
	}

	for _, candidate := range smokeBrowserCandidates() {
		if candidate == "" {
			continue
		}
		if filepath.IsAbs(candidate) {
			if isExecutablePath(candidate) {
				return candidate, true
			}

			continue
		}

		binaryPath, err := exec.LookPath(candidate)
		if err == nil {
			return binaryPath, true
		}
	}

	return "", false
}

func smokeBrowserCandidates() []string {
	candidates := []string{
		"google-chrome",
		"chromium",
		"chromium-browser",
		"chrome",
		"microsoft-edge",
	}

	if runtime.GOOS == "darwin" {
		candidates = append(candidates,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		)
	}

	return candidates
}

func isExecutablePath(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}

	return info.Mode()&0o111 != 0
}
