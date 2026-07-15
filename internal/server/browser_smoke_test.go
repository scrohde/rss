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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
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

func TestBrowserSmokeAuthLoginSwitchesFromConditionalToExplicit(t *testing.T) {
	app := newAuthEnabledTestApp(t)
	staticRoot := filepath.Join(pathParentDir, pathParentDir, "static")
	app.SetStaticFS(os.DirFS(staticRoot))
	seedAuthCredential(t, app)

	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	passkeyStub := `(() => {
		window.__conditionalAttempts = 0;
		window.__requiredAttempts = 0;
		window.__requiredHadActivation = false;
		window.__conditionalAborts = 0;
		Object.defineProperty(window, "PublicKeyCredential", {
			configurable: true,
			value: Object.assign(function PublicKeyCredential() {}, {
				isConditionalMediationAvailable: () => Promise.resolve(true),
			}),
		});
		Object.defineProperty(navigator, "credentials", {
			configurable: true,
			value: {
				get: (options) => {
					if (options.mediation === "conditional") {
						window.__conditionalAttempts += 1;
						return new Promise((resolve, reject) => {
							options.signal.addEventListener("abort", () => {
								window.__conditionalAborts += 1;
								reject(new DOMException("aborted", "AbortError"));
							}, { once: true });
						});
					}
					window.__requiredAttempts += 1;
					window.__requiredHadActivation = navigator.userActivation.isActive;
					return new Promise((resolve, reject) => {
						window.__rejectRequired = () => reject(
							new DOMException("prompt dismissed", "NotAllowedError")
						);
					});
				},
			},
		});
	})();`

	runActions(
		t,
		ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(passkeyStub).Do(ctx)

			return err
		}),
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL+"/auth/login"),
	)
	waitForJS(t, ctx, `(() => window.__conditionalAttempts === 1)()`, "conditional passkey request")
	waitForJS(
		t,
		ctx,
		elementVisibleExpression(`[data-auth-passkey-selector]`),
		"conditional passkey selector",
	)
	waitForJS(
		t,
		ctx,
		`(() => {
			const card = document.querySelector(".auth-card");
			const button = document.querySelector("[data-passkey-login='true']");
			const recovery = document.querySelector(".auth-recovery-link");
			const message = document.querySelector("[data-auth-message]");
			if (!card || !button || !recovery || !message) return false;
			const cardRect = card.getBoundingClientRect();
			const buttonRect = button.getBoundingClientRect();
			return cardRect.left >= 0 && cardRect.right <= innerWidth &&
				buttonRect.height >= 44 && buttonRect.width >= cardRect.width * 0.75 &&
				recovery.getBoundingClientRect().height > 0 && message.getBoundingClientRect().height > 0 &&
				message.textContent.trim() === "" && !message.classList.contains("error");
		})()`,
		"responsive login card ready state",
	)
	runActions(
		t,
		ctx,
		chromedp.Click(`[data-passkey-login="true"]`, chromedp.ByQuery),
	)
	waitForJS(t, ctx, `(() => window.__conditionalAborts === 1)()`, "conditional request abort")
	waitForJS(
		t,
		ctx,
		`(() => window.__requiredAttempts === 1 && window.__requiredHadActivation)()`,
		"explicit passkey request with transient activation",
	)
	waitForJS(
		t,
		ctx,
		`(() => {
			const button = document.querySelector("[data-passkey-login='true']");
			const message = document.querySelector("[data-auth-message]");
			return button.disabled && button.getAttribute("aria-busy") === "true" &&
				message.textContent.trim() === "" && !message.classList.contains("error");
		})()`,
		"explicit passkey pending state",
	)
	runActions(t, ctx, chromedp.Evaluate(`window.__rejectRequired()`, nil))
	waitForJS(
		t,
		ctx,
		`(() => {
			const button = document.querySelector("[data-passkey-login='true']");
			const message = document.querySelector("[data-auth-message]");
			return !button.disabled && !button.hasAttribute("aria-busy") &&
				message.classList.contains("error") &&
				message.textContent.includes("canceled") &&
				!message.textContent.includes("private mode");
		})()`,
		"explicit passkey canceled state",
	)
}

func TestBrowserSmokeAuthLoginUnsupportedFallback(t *testing.T) {
	app := newAuthEnabledTestApp(t)
	staticRoot := filepath.Join(pathParentDir, pathParentDir, "static")
	app.SetStaticFS(os.DirFS(staticRoot))
	seedAuthCredential(t, app)

	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	passkeyStub := `(() => {
		window.__passkeyAttempts = 0;
		Object.defineProperty(window, "PublicKeyCredential", {
			configurable: true,
			value: function PublicKeyCredential() {},
		});
		Object.defineProperty(navigator, "credentials", {
			configurable: true,
			value: {
				get: () => {
					window.__passkeyAttempts += 1;
					return new Promise(() => {});
				},
			},
		});
	})();`

	runActions(
		t,
		ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(passkeyStub).Do(ctx)

			return err
		}),
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL+"/auth/login"),
	)
	waitForJS(
		t,
		ctx,
		`(() => window.__passkeyAttempts === 0 &&
			document.querySelector("[data-auth-passkey-selector]").hidden &&
			document.querySelector("[data-passkey-login='true']").getBoundingClientRect().height >= 44 &&
			document.querySelector(".auth-recovery-link").getBoundingClientRect().height > 0)()`,
		"unsupported browser fallback ready state",
	)
	runActions(t, ctx, chromedp.Click(`[data-passkey-login="true"]`, chromedp.ByQuery))
	waitForJS(t, ctx, `(() => window.__passkeyAttempts === 1)()`, "unsupported browser explicit request")
}

func TestBrowserSmokeReaderFlows(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
		chromedp.WaitVisible("#main-content", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout")

	runFeedSelectionFlow(t, ctx, fixture)
	runItemActionHierarchyFlow(t, ctx, fixture)
	runSummaryOnlyDesktopReaderFlow(t, ctx, fixture)
	runMoreTogglePersistenceFlow(t, ctx, fixture)
	runFeedToItemsOutlineEntryFlow(t, ctx, fixture)
	runContentToItemsOutlineEntryFlow(t, ctx, fixture)
	runExpandCollapseFlow(t, ctx, fixture)
	runToggleReadFlow(t, ctx, fixture)
	runKeyboardFlow(t, ctx, fixture)
	runContentPanelMarkReadButtonFlow(t, ctx, fixture)
	runFeedBoundaryKeyboardFlow(t, ctx, fixture)
}

func TestBrowserSmokeReaderFlowsContinuation(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	primaryFeedSelector := fmt.Sprintf(
		`#feed-list .feed-link[data-feed-id="%d"]`,
		fixture.primaryFeedID,
	)
	primaryListSelector := fmt.Sprintf(
		`#main-content #item-list[data-feed-id="%d"]`,
		fixture.primaryFeedID,
	)
	primaryRowSelector := fmt.Sprintf(`#item-%d`, fixture.primaryFirstItemID)
	primaryToggleSelector := fmt.Sprintf(`#item-%d .item-read-toggle`, fixture.primaryFirstItemID)
	secondaryRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)
	continuationSelector := `#feed-continuation [data-feed-continuation]`

	runActions(
		t,
		ctx,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
		chromedp.WaitVisible("#main-content", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for continuation flow")
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout for continuation flow")

	clickElement(t, ctx, primaryFeedSelector, "open primary feed before continuation flow")
	runActions(
		t,
		ctx,
		chromedp.WaitVisible(primaryListSelector, chromedp.ByQuery),
		chromedp.WaitVisible(primaryRowSelector, chromedp.ByQuery),
		chromedp.WaitVisible(continuationSelector, chromedp.ByQuery),
	)
	waitForJS(t, ctx, textPresentExpression("Continue to Secondary Feed"), "next feed continuation label")
	waitForJS(t, ctx, elementAbsentExpression(secondaryRowSelector), "next feed items are not prefetched")

	clickPointerElement(t, ctx, primaryToggleSelector, "mark primary final unread item read")
	waitForJS(t, ctx, hasClassExpression(primaryRowSelector, "is-read"), "primary final item marked read")
	waitForJS(t, ctx, elementPresentExpression(primaryListSelector), "current feed remains after final read")
	waitForJS(t, ctx, elementVisibleExpression(continuationSelector), "continuation remains after final read")

	clickElement(t, ctx, continuationSelector, "continue to next unread feed")
	waitForJS(
		t,
		ctx,
		feedSelectionSettledExpression(fixture.secondaryFeedID, "Secondary Feed"),
		"secondary feed selected by continuation",
	)
	waitForJS(t, ctx, elementPresentExpression(secondaryRowSelector), "secondary feed items loaded after continuation")
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "continuation clears content panel")
}

func TestBrowserSmokeReaderFlowsBreakpointTransitions(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)

	var mobileBootstrapRequests atomic.Int64
	var desktopRestoreRequests atomic.Int64
	var mobileSelectorRequests atomic.Int64
	firstMobileStarted := make(chan struct{}, 1)
	firstMobileCanceled := make(chan struct{}, 1)
	releaseFirstMobile := make(chan struct{})
	firstSelectorStarted := make(chan struct{}, 1)
	firstSelectorCanceled := make(chan struct{}, 1)
	releaseFirstSelector := make(chan struct{})
	t.Cleanup(func() {
		close(releaseFirstMobile)
		close(releaseFirstSelector)
	})

	routes := app.Routes()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isHTMX := r.Header.Get("Hx-Request") == "true"
		isMobileBootstrap := r.URL.Path == pathMobileStream && isHTMX &&
			r.Header.Get("Hx-Trigger") != "mobile-stream-feed-filter"
		if isMobileBootstrap {
			requestNumber := mobileBootstrapRequests.Add(1)
			if requestNumber == 1 {
				firstMobileStarted <- struct{}{}
				select {
				case <-releaseFirstMobile:
				case <-r.Context().Done():
					firstMobileCanceled <- struct{}{}

					return
				}
			}
		}
		isMobileSelector := r.URL.Path == pathMobileStream && isHTMX &&
			r.Header.Get("Hx-Trigger") == "mobile-stream-feed-filter"
		if isMobileSelector {
			requestNumber := mobileSelectorRequests.Add(1)
			if requestNumber == 1 {
				firstSelectorStarted <- struct{}{}
				select {
				case <-releaseFirstSelector:
				case <-r.Context().Done():
					firstSelectorCanceled <- struct{}{}

					return
				}
			}
		}
		if r.URL.Path == pathIndex && isHTMX {
			desktopRestoreRequests.Add(1)
		}

		routes.ServeHTTP(w, r)
	})
	server := newSmokeServer(t, handler)
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(1365, 1024),
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for responsive transitions")
	waitForJS(t, ctx, desktopLayoutExpression(), "initial desktop responsive layout")
	runFeedSelectionFlow(t, ctx, fixture)
	runActions(
		t,
		ctx,
		chromedp.Evaluate(`(() => {
			window.__responsiveDocumentMarker = "responsive-reader-transition";
			window.__responsiveErrors = [];
			window.__responsiveHistoryRestores = 0;
			document.body.addEventListener("htmx:historyRestore", () => {
				window.__responsiveHistoryRestores += 1;
			});
			window.addEventListener("error", (event) => {
				window.__responsiveErrors.push(event.message || "window error");
			});
			window.addEventListener("unhandledrejection", (event) => {
				const reason = event.reason;
				window.__responsiveErrors.push(reason && reason.message ? reason.message : String(reason));
			});
			return true;
		})()`, nil),
	)

	runActions(t, ctx, chromedp.EmulateViewport(390, 844))
	select {
	case <-firstMobileStarted:
	case <-time.After(smokeWaitTimeout):
		t.Fatal("mobile bootstrap request did not start after crossing the breakpoint")
	}

	runActions(t, ctx, chromedp.EmulateViewport(1365, 1024))
	select {
	case <-firstMobileCanceled:
	case <-time.After(smokeWaitTimeout):
		t.Fatal("stale mobile bootstrap request was not canceled after returning to desktop")
	}
	waitForJS(
		t,
		ctx,
		feedSelectionSettledExpression(fixture.secondaryFeedID, "Secondary Feed"),
		"desktop reader remains usable after canceling stale mobile transition",
	)
	if got := desktopRestoreRequests.Load(); got != 0 {
		t.Fatalf("expected no redundant desktop restore request, got %d", got)
	}

	runActions(t, ctx, chromedp.EmulateViewport(390, 844))
	waitForJS(t, ctx, responsiveMobileLayoutExpression(0), "live desktop-to-mobile transition")
	waitForJS(t, ctx, htmxSettledExpression(), "desktop-to-mobile HTMX settle")
	if got := mobileBootstrapRequests.Load(); got != 2 {
		t.Fatalf("expected one canceled and one completed mobile bootstrap request, got %d", got)
	}

	runActions(t, ctx, chromedp.EmulateViewport(420, 844), chromedp.Sleep(150*time.Millisecond))
	if got := mobileBootstrapRequests.Load(); got != 2 {
		t.Fatalf("same-layout resize issued a duplicate mobile bootstrap request: got %d", got)
	}

	waitForJS(
		t,
		ctx,
		htmxElementReadyExpression("#mobile-stream-feed-filter"),
		"responsive mobile feed selector ready",
	)
	selectMobileFeedFilter(t, ctx, fixture.primaryFeedID)
	select {
	case <-firstSelectorStarted:
	case <-time.After(smokeWaitTimeout):
		t.Fatal("mobile selector request did not start")
	}

	runActions(t, ctx, chromedp.EmulateViewport(1365, 1024))
	select {
	case <-firstSelectorCanceled:
	case <-time.After(smokeWaitTimeout):
		t.Fatal("in-flight mobile selector request was not canceled at the desktop breakpoint")
	}
	waitForJS(
		t,
		ctx,
		responsiveDesktopLayoutExpression(fixture.primaryFeedID),
		"live mobile-to-desktop transition",
	)
	waitForJS(t, ctx, pathnameExpression(pathIndex), "desktop restore URL")
	waitForJS(t, ctx, htmxSettledExpression(), "mobile-to-desktop HTMX settle")
	if got := desktopRestoreRequests.Load(); got != 1 {
		t.Fatalf("expected one desktop restore request, got %d", got)
	}
	if got := mobileSelectorRequests.Load(); got != 1 {
		t.Fatalf("expected exactly one mobile selector request, got %d", got)
	}

	primaryReaderButton := fmt.Sprintf("#item-%d .item-read-in-app", fixture.primaryFirstItemID)
	clickElement(t, ctx, primaryReaderButton, "open desktop item after responsive restore")
	waitForJS(
		t,
		ctx,
		contentPanelItemExpression(fixture.primaryFirstItemID),
		"desktop content panel after responsive restore",
	)

	runActions(t, ctx, chromedp.EmulateViewport(390, 844))
	waitForJS(
		t,
		ctx,
		responsiveMobileLayoutExpression(fixture.primaryFeedID),
		"second live desktop-to-mobile transition",
	)
	waitForJS(t, ctx, htmxSettledExpression(), "second desktop-to-mobile HTMX settle")
	if got := mobileBootstrapRequests.Load(); got != 3 {
		t.Fatalf("expected one bootstrap request per completed mobile crossing, got %d", got)
	}

	mobileReaderButton := fmt.Sprintf(
		`.mobile-card-open[hx-get^="/mobile/items/%d/reader"]`,
		fixture.primaryFirstItemID,
	)
	clickElement(t, ctx, mobileReaderButton, "open mobile reader before history restore")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "mobile reader before history restore")

	runActions(t, ctx, chromedp.EmulateViewport(1365, 1024))
	waitForJS(
		t,
		ctx,
		responsiveDesktopLayoutExpression(fixture.primaryFeedID),
		"desktop restore before cached history transition",
	)
	waitForJS(t, ctx, htmxSettledExpression(), "desktop restore before history settle")
	if got := desktopRestoreRequests.Load(); got != 2 {
		t.Fatalf("expected second desktop restore request, got %d", got)
	}

	runActions(t, ctx, chromedp.Evaluate(`history.back()`, nil))
	waitForJS(
		t,
		ctx,
		`(() => window.__responsiveHistoryRestores >= 1)()`,
		"cached mobile history restoration observed",
	)
	waitForJS(
		t,
		ctx,
		responsiveDesktopLayoutExpression(fixture.primaryFeedID),
		"cached mobile history reconciles to desktop",
	)
	waitForJS(t, ctx, htmxSettledExpression(), "cached history desktop reconciliation settle")
	if got := desktopRestoreRequests.Load(); got != 3 {
		t.Fatalf("expected cached history to trigger one desktop restore request, got %d", got)
	}
	waitForJS(
		t,
		ctx,
		`(() => window.__responsiveDocumentMarker === "responsive-reader-transition" &&
			Array.isArray(window.__responsiveErrors) && window.__responsiveErrors.length === 0)()`,
		"responsive transitions preserve the document without JavaScript errors",
	)
}

func TestBrowserSmokeReaderFlowsMenuInteractions(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(1024, 360),
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#topbar-shortcuts-button", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready for general menu interactions")
	waitForJS(t, ctx, desktopLayoutExpression(), "compact desktop menu layout")
	runFeedSelectionFlow(t, ctx, fixture)

	runGeneralMenuKeyboardFlow(t, ctx)
	runGeneralMenuOutsideClickFlow(t, ctx)
}

func TestBrowserSmokeReaderFlowsItemActionTypography(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	runActions(
		t,
		ctx,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
		chromedp.WaitVisible("#main-content", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout")
	runFeedSelectionFlow(t, ctx, fixture)

	readableRow := fmt.Sprintf("#item-%d", fixture.secondaryFirstItemID)
	waitForJS(
		t,
		ctx,
		itemActionHierarchyExpression(readableRow+" .item-read-in-app", readableRow+" .item-source-link"),
		"primary reader action typography hierarchy",
	)
}

func TestBrowserSmokeInactiveFeedContentBoundary(t *testing.T) {
	app := newSmokeApp(t)
	feedID := mustUpsertFeed(t, app, "https://example.com/malicious.xml", "Inactive Boundary Feed")
	published := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	mustUpsertItems(t, app, feedID, []*gofeed.Item{{
		Title: "Inactive Boundary Item",
		Link:  "https://example.com/malicious-item",
		GUID:  "inactive-boundary-item",
		Content: `<p>Inactive boundary smoke content</p>` +
			`<div hx-post="/smoke-unauthorized" hx-trigger="load" hx-swap="none"></div>` +
			`<div data-hx-post="/smoke-unauthorized" data-hx-trigger="every 1ms"></div>`,
		PublishedParsed: &published,
	}})
	items := mustListItems(t, app, feedID)
	assertItemCount(t, items, 1)
	itemID := items[0].ID

	var unauthorizedRequests atomic.Int64
	routes := app.Routes()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/smoke-unauthorized" {
			unauthorizedRequests.Add(1)
			w.WriteHeader(http.StatusNoContent)

			return
		}

		routes.ServeHTTP(w, r)
	})
	server := newSmokeServer(t, handler)
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)
	runActions(
		t,
		ctx,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
		chromedp.WaitVisible("#main-content", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout")

	feedSelector := fmt.Sprintf(`.feed-link[data-feed-id="%d"]`, feedID)
	clickElement(t, ctx, feedSelector, "select inactive-boundary feed")
	rowSelector := fmt.Sprintf("#item-%d", itemID)
	waitForJS(t, ctx, elementPresentExpression(rowSelector), "inactive-boundary item row")
	clickElement(t, ctx, rowSelector, "open inactive-boundary item")
	waitForJS(t, ctx, contentPanelItemExpression(itemID), "inactive-boundary reader")
	waitForJS(t, ctx, inactiveReaderBoundaryExpression(), "inactive reader boundary")
	runActions(t, ctx, chromedp.Sleep(500*time.Millisecond))

	if got := unauthorizedRequests.Load(); got != 0 {
		t.Fatalf("malicious reader content issued %d unauthorized request(s)", got)
	}
}

func TestBrowserSmokeHiddenSelectionFallback(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
		chromedp.WaitVisible("#main-content", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout")

	runFeedSelectionFlow(t, ctx, fixture)
	runHiddenSelectionFallbackFlow(t, ctx, fixture)
}

func TestBrowserSmokeMobileReaderFlows(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, mobileLayoutExpression(), "mobile layout")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "mobile stream loaded")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "mobile stream URL")
	runMobileSummaryOnlyPreviewFlow(t, ctx, fixture)
	waitForJS(
		t,
		ctx,
		textPresentExpression("Secondary One"),
		"secondary item present in mobile stream before reading",
	)

	aggregateState := mobileAggregateState{
		FeedCursor: nil,
		ItemCursor: nil,
		FeedID:     fixture.secondaryFeedID,
	}
	firstReaderSelector := fmt.Sprintf("#mobile-card-%d .mobile-card-open", fixture.secondaryFirstItemID)
	clickElement(t, ctx, firstReaderSelector, "open first mobile aggregate reader")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "mobile reader loaded")
	waitForJS(
		t,
		ctx,
		requestURIExpression(mobileReaderItemPath(fixture.secondaryFirstItemID, 0, aggregateState)),
		"mobile reader URL",
	)
	waitForJS(t, ctx, textPresentExpression("Secondary One"), "reader title present")

	clickElement(t, ctx, ".mobile-reader-mark-read", "mark first aggregate reader item read")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "stream returns after mark read")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "stream URL after mark read")
	waitForJS(
		t,
		ctx,
		textAbsentExpression("Secondary One"),
		"marked item removed from mobile unread stream",
	)

	secondReaderSelector := fmt.Sprintf("#mobile-card-%d .mobile-card-open", fixture.secondarySecondItemID)
	clickElement(t, ctx, secondReaderSelector, "open second mobile aggregate reader")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "reader can open another item")
	waitForJS(
		t,
		ctx,
		requestURIExpression(mobileReaderItemPath(fixture.secondarySecondItemID, 0, aggregateState)),
		"second reader URL",
	)
	runActions(t, ctx, chromedp.Evaluate(`history.back()`, nil))
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "back returns to stream")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "stream URL after history back")
}

//nolint:funlen,revive // One browser journey verifies lazy paging, history, reader back, and local read updates.
func TestBrowserSmokeMobileAggregateFlows(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedMobileAggregateSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, mobileLayoutExpression(), "mobile layout")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "mobile aggregate loaded")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "mobile aggregate URL")
	waitForJS(
		t,
		ctx,
		mobileAggregateSectionOrderExpression(fixture.highFeedID, fixture.laterFeedID),
		"mobile aggregate saved feed order",
	)
	waitForJS(
		t,
		ctx,
		elementAbsentExpression(fmt.Sprintf("#mobile-feed-section-%d", fixture.quietFeedID)),
		"caught-up mobile aggregate feed skipped",
	)
	waitForJS(
		t,
		ctx,
		elementAbsentExpression(fmt.Sprintf("#mobile-card-%d", fixture.highOldestItemID)),
		"first feed overflow item initially lazy",
	)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.laterItemID)),
		"later feed reachable before loading first feed backlog",
	)
	waitForJS(
		t,
		ctx,
		mobileAggregateBoundedExpression(mobileAggregateFeedPageLimit, mobileAggregateItemPageLimit),
		"initial aggregate DOM bounds",
	)

	olderButton := fmt.Sprintf("#mobile-feed-section-%d [data-mobile-feed-next]", fixture.highFeedID)
	clickElement(t, ctx, olderButton, "load older page from high-volume feed")
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.highOldestItemID)),
		"older high-volume item loaded",
	)
	waitForJS(
		t,
		ctx,
		elementAbsentExpression(fmt.Sprintf("#mobile-card-%d", fixture.highNewestItemID)),
		"newest high-volume page replaced",
	)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.laterItemID)),
		"later feed remains mounted after first-feed continuation",
	)
	waitForJS(
		t,
		ctx,
		mobileAggregateBoundedExpression(mobileAggregateFeedPageLimit, mobileAggregateItemPageLimit),
		"continued aggregate DOM bounds",
	)
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression(fmt.Sprintf("#mobile-feed-section-%d", fixture.highFeedID)),
		"continued feed section receives focus",
	)

	readerSelector := fmt.Sprintf("#mobile-card-%d .mobile-card-open", fixture.highOldestItemID)
	readerPath := mobileReaderItemPath(fixture.highOldestItemID, 0, fixture.olderState)

	clickElement(t, ctx, readerSelector, "open older aggregate item reader")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "aggregate reader loaded")
	waitForJS(t, ctx, requestURIExpression(readerPath), "aggregate reader URL preserves page state")

	runActions(t, ctx, chromedp.Evaluate(`history.back()`, nil))
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "history restores aggregate stream")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "history restores aggregate stream URL")
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.highOldestItemID)),
		"history restores older first-feed page",
	)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.laterItemID)),
		"history restores later feed section",
	)

	clickElement(t, ctx, readerSelector, "reopen older aggregate item reader")
	waitForJS(t, ctx, requestURIExpression(readerPath), "reopened aggregate reader URL")
	clickElement(t, ctx, ".mobile-reader-back", "aggregate reader back button")

	streamPath := mobileStreamStatePath(0, fixture.olderState)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(`[data-mobile-stream="true"]`),
		"aggregate reader back returns to stream",
	)
	waitForJS(t, ctx, requestURIExpression(streamPath), "aggregate reader back preserves page state")
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.highOldestItemID)),
		"reader back preserves older first-feed page",
	)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.laterItemID)),
		"reader back preserves later feed reachability",
	)

	markReadSelector := fmt.Sprintf("#mobile-card-%d .mobile-card-mark-read", fixture.highOldestItemID)
	clickElement(t, ctx, markReadSelector, "mark older aggregate item read")
	waitForJS(
		t,
		ctx,
		elementAbsentExpression(fmt.Sprintf("#mobile-card-%d", fixture.highOldestItemID)),
		"marked aggregate item removed",
	)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-feed-section-%d", fixture.highFeedID)),
		"owning section remains on its current page",
	)
	waitForJS(
		t,
		ctx,
		textPresentExpression("No unread items remain on this page."),
		"empty aggregate item page state",
	)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(fmt.Sprintf("#mobile-card-%d", fixture.laterItemID)),
		"section-local mark read leaves later feed mounted",
	)
	waitForJS(
		t,
		ctx,
		elementAbsentExpression(fmt.Sprintf("#mobile-card-%d", fixture.highNewestItemID)),
		"section-local mark read does not reset to newest page",
	)
	waitForJS(t, ctx, mobileFilterValueExpression(0), "aggregate filter remains All feeds")
	waitForJS(t, ctx, requestURIExpression(streamPath), "aggregate mark read retains stream URL")
	waitForJS(
		t,
		ctx,
		mobileAggregateBoundedExpression(mobileAggregateFeedPageLimit, mobileAggregateItemPageLimit),
		"aggregate DOM stays bounded after mark read",
	)
}

func TestBrowserSmokeMobileTopBarFlows(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, mobileLayoutExpression(), "mobile layout")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "mobile stream loaded")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "mobile stream URL")

	runMobileReaderTopBarFlow(t, ctx, fixture)
}

func TestBrowserSmokeMobileFilteredFeedFlows(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(390, 844),
		chromedp.Navigate(server.URL),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, mobileLayoutExpression(), "mobile layout")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "mobile stream loaded")
	waitForJS(t, ctx, pathnameExpression("/mobile/stream"), "mobile stream URL")

	runMobileFilteredHistoryFlow(t, ctx, fixture)
	runMobileFilteredEmptyStateFlow(t, ctx, fixture)
}

func TestBrowserSmokePulseIndicatorFlows(t *testing.T) {
	app := newSmokeApp(t)
	fixture := seedSmokeFixture(t, app)
	seedSmokePulseStatuses(t, app, fixture)
	server := newSmokeServer(t, app.Routes())
	t.Cleanup(server.Close)

	ctx := newSmokeBrowserContext(t)

	runActions(
		t,
		ctx,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("#feed-list", chromedp.ByQuery),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready")
	waitForJS(t, ctx, desktopLayoutExpression(), "desktop layout")
	waitForJS(t, ctx, desktopPulseIndicatorsExpression(fixture), "desktop pulse indicators")

	runActions(
		t,
		ctx,
		chromedp.EmulateViewport(320, 568),
		chromedp.Navigate(server.URL),
	)
	waitForJS(t, ctx, htmxReadyExpression(), "htmx ready after mobile resize")
	waitForJS(t, ctx, mobileLayoutExpression(), "narrow mobile layout")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "mobile stream loaded")

	selectMobileFeedFilter(t, ctx, fixture.secondaryFeedID)
	waitForJS(t, ctx, mobileFlatStreamLayoutExpression(fixture), "narrow mobile flat stream layout")
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

func runFeedSelectionFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	feedSelector := fmt.Sprintf(`#feed-list .feed-link[data-feed-id="%d"]`, fixture.secondaryFeedID)
	itemListSelector := fmt.Sprintf(`#main-content #item-list[data-feed-id="%d"]`, fixture.secondaryFeedID)
	firstItemSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)

	runActions(
		t,
		ctx,
		chromedp.WaitVisible(feedSelector, chromedp.ByQuery),
	)
	clickElement(t, ctx, feedSelector, "open secondary feed in selection flow")
	runActions(
		t,
		ctx,
		chromedp.WaitVisible(itemListSelector, chromedp.ByQuery),
		chromedp.WaitVisible(firstItemSelector, chromedp.ByQuery),
	)
	waitForJS(
		t,
		ctx,
		feedSelectionSettledExpression(fixture.secondaryFeedID, "Secondary Feed"),
		"secondary feed selection settled",
	)
	waitForJS(t, ctx, activeElementMatchesExpression(feedSelector), "secondary feed retains focus after swap")
}

func runGeneralMenuKeyboardFlow(t *testing.T, ctx context.Context) {
	t.Helper()

	runActions(
		t,
		ctx,
		chromedp.Focus("#topbar-shortcuts-button", chromedp.ByQuery),
		chromedp.KeyEvent(kb.Enter),
	)
	waitForJS(t, ctx, generalMenuOpenExpression(), "general menu opens from keyboard")
	waitForJS(t, ctx, generalMenuCompactExpression(), "general menu has compact scrolling")

	runActions(t, ctx, chromedp.KeyEvent(kb.Tab))
	waitForJS(t, ctx, activeElementMatchesExpression("#topbar-shortcuts-panel"), "focus enters general menu")
	waitForJS(t, ctx, focusOutlineVisibleExpression("#topbar-shortcuts-panel"), "general menu focus outline")
	runActions(
		t,
		ctx,
		chromedp.Evaluate(`(() => {
			const active = document.querySelector("#item-list .item-entry.is-active");
			const panel = document.querySelector("#topbar-shortcuts-panel");
			window.__menuReaderActiveID = active ? active.id : "";
			panel.scrollTop = 0;
			return window.__menuReaderActiveID !== "";
		})()`, nil),
		chromedp.KeyEvent(kb.ArrowDown),
	)
	waitForJS(t, ctx, generalMenuArrowScrollExpression(), "menu arrow scroll leaves reader selection unchanged")

	runActions(t, ctx, chromedp.KeyEvent(kb.End))
	waitForJS(t, ctx, generalMenuEndVisibleExpression(), "final shortcut remains reachable in compact menu")
	runActions(t, ctx, chromedp.KeyEvent(kb.Escape))
	waitForJS(t, ctx, generalMenuClosedExpression(), "Escape closes general menu")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression("#topbar-shortcuts-button"),
		"Escape restores menu trigger focus",
	)
}

func runGeneralMenuOutsideClickFlow(t *testing.T, ctx context.Context) {
	t.Helper()

	runActions(
		t,
		ctx,
		chromedp.KeyEvent(kb.Enter),
		chromedp.Evaluate(`(() => {
			const button = document.createElement("button");
			button.id = "menu-outside-target";
			button.type = "button";
			button.textContent = "Outside target";
			document.querySelector("#main-content").prepend(button);
			return true;
		})()`, nil),
	)
	waitForJS(t, ctx, generalMenuOpenExpression(), "general menu reopens")
	runActions(t, ctx, chromedp.Click("#menu-outside-target", chromedp.ByQuery))
	waitForJS(t, ctx, generalMenuClosedExpression(), "outside click closes general menu")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression("#menu-outside-target"),
		"outside click focus is not stolen by menu trigger",
	)
}

func runItemActionHierarchyFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	readableRow := fmt.Sprintf("#item-%d", fixture.secondaryFirstItemID)
	readButton := readableRow + " .item-read-in-app"
	sourceLink := readableRow + " .item-source-link"
	readToggle := readableRow + " .item-read-toggle"
	noReaderRow := fmt.Sprintf("#item-%d", fixture.secondaryNoReaderID)
	noReaderLink := noReaderRow + " .item-source-primary"

	waitForJS(t, ctx, elementPresentExpression(readButton), "semantic in-app reading button")
	waitForJS(t, ctx, elementPresentExpression(sourceLink), "separate external source link")
	waitForJS(t, ctx, elementPresentExpression(noReaderLink), "no-reader primary source link")
	waitForJS(
		t,
		ctx,
		itemReadToggleStateExpression(readToggle, "unread", "Mark read", false),
		"unread item toggle state",
	)
	for _, theme := range []string{"light", "dark", "system"} {
		waitForJS(
			t,
			ctx,
			itemReadToggleThemeExpression(readToggle, theme),
			fmt.Sprintf("read toggle styling in %s theme", theme),
		)
	}
	waitForJS(
		t,
		ctx,
		elementAbsentExpression(noReaderRow+" .item-read-in-app"),
		"no-reader item omits in-app action",
	)

	runActions(
		t,
		ctx,
		chromedp.Focus(readButton, chromedp.ByQuery),
		chromedp.KeyEvent(kb.Tab),
	)
	waitForJS(t, ctx, activeElementMatchesExpression(sourceLink), "source link follows reader button in focus order")
	waitForJS(t, ctx, focusOutlineVisibleExpression(sourceLink), "source link keyboard focus treatment")

	runActions(t, ctx, chromedp.Focus(readButton, chromedp.ByQuery))
	waitForJS(t, ctx, focusOutlineVisibleExpression(readButton), "reader button keyboard focus treatment")
	runActions(t, ctx, chromedp.KeyEvent(kb.Tab), chromedp.KeyEvent(kb.Tab))
	waitForJS(t, ctx, activeElementMatchesExpression(readToggle), "read-state action follows item open actions")

	runActions(t, ctx, chromedp.KeyEvent(kb.Enter))
	waitForJS(t, ctx, hasClassExpression(readableRow, "is-read"), "read-state action activated by keyboard")
	waitForJS(
		t,
		ctx,
		itemReadToggleStateExpression(readToggle, "read", "Mark unread", true),
		"read item toggle state",
	)
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "read-state action does not open reader")
	waitForJS(t, ctx, htmxElementReadyExpression(readToggle), "read-state action ready after keyboard swap")
	runActions(
		t,
		ctx,
		chromedp.Focus(readToggle, chromedp.ByQuery),
		chromedp.KeyEvent(kb.Enter),
	)
	waitForJS(t, ctx, missingClassExpression(readableRow, "is-read"), "read-state action restored by keyboard")
	waitForJS(
		t,
		ctx,
		itemReadToggleStateExpression(readToggle, "unread", "Mark read", false),
		"unread item toggle state restored",
	)

	runActions(
		t,
		ctx,
		chromedp.Focus(readButton, chromedp.ByQuery),
		chromedp.KeyEvent(kb.Enter),
	)
	waitForJS(t, ctx, hasClassExpression(readableRow, "is-expanded"), "reader button opens item by keyboard")
	waitForJS(t, ctx, contentPanelItemExpression(fixture.secondaryFirstItemID), "reader button opens content panel")

	requestHTMX(
		t,
		ctx,
		"GET",
		fmt.Sprintf("/items/%d/compact", fixture.secondaryFirstItemID),
		readableRow,
		fmt.Sprintf("item-%d", fixture.secondaryFirstItemID),
	)
	waitForJS(t, ctx, missingClassExpression(readableRow, "is-expanded"), "reader action test row collapsed")
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "reader action test panel closed")

	armSourceLinkCapture(t, ctx)
	runActions(
		t,
		ctx,
		chromedp.Focus(sourceLink, chromedp.ByQuery),
		chromedp.KeyEvent(kb.Enter),
	)
	waitForJS(t, ctx, sourceLinkCapturedExpression(sourceLink), "external source link keyboard activation")
	waitForJS(t, ctx, missingClassExpression(readableRow, "is-expanded"), "source link does not open reader")

	armSourceLinkCapture(t, ctx)
	runActions(
		t,
		ctx,
		chromedp.Focus(noReaderLink, chromedp.ByQuery),
		chromedp.KeyEvent(kb.Enter),
	)
	waitForJS(t, ctx, sourceLinkCapturedExpression(noReaderLink), "no-reader primary link keyboard activation")
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "no-reader link does not open reader")
}

func runMoreTogglePersistenceFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	rowSelector := fmt.Sprintf("#item-%d", fixture.secondaryFirstItemID)
	itemTarget := fmt.Sprintf("#item-%d", fixture.secondaryFirstItemID)
	selectedItemID := fmt.Sprintf("item-%d", fixture.secondaryFirstItemID)

	runActions(
		t,
		ctx,
		chromedp.WaitVisible("#feed-list .feed-more-button", chromedp.ByQuery),
	)
	waitForJS(t, ctx, feedMoreCollapsedExpression(), "more panel collapsed by default")

	runActions(t, ctx, chromedp.Click("#feed-list .feed-more-button", chromedp.ByQuery))
	waitForJS(t, ctx, feedMoreExpandedExpression(), "more panel expanded after click")

	requestHTMX(
		t,
		ctx,
		"POST",
		fmt.Sprintf("/items/%d/toggle", fixture.secondaryFirstItemID),
		itemTarget,
		selectedItemID,
	)
	waitForJS(t, ctx, hasClassExpression(rowSelector, "is-read"), "row marked read in persistence flow")
	waitForJS(t, ctx, feedMoreExpandedExpression(), "more panel stays expanded after feed-list swap")

	runActions(t, ctx, chromedp.Click("#feed-list .feed-more-button", chromedp.ByQuery))
	waitForJS(t, ctx, feedMoreCollapsedExpression(), "more panel collapsed after second click")

	requestHTMX(
		t,
		ctx,
		"POST",
		fmt.Sprintf("/items/%d/toggle", fixture.secondaryFirstItemID),
		itemTarget,
		selectedItemID,
	)
	waitForJS(t, ctx, missingClassExpression(rowSelector, "is-read"), "row marked unread in persistence flow")
	waitForJS(t, ctx, feedMoreCollapsedExpression(), "more panel stays collapsed after feed-list swap")
}

func runFeedBoundaryKeyboardFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	lastUnreadFeedSelector := fmt.Sprintf(
		`#feed-list .feed-link[data-feed-id="%d"]`,
		fixture.quaternaryFeedID,
	)
	archiveFeedSelector := fmt.Sprintf(
		`#feed-list #feed-zero-list .feed-link[data-feed-id="%d"]`,
		fixture.archiveFeedID,
	)
	archiveListSelector := fmt.Sprintf(
		`#main-content #item-list[data-feed-id="%d"]`,
		fixture.archiveFeedID,
	)

	clickElement(t, ctx, lastUnreadFeedSelector, "open last unread feed before boundary flow")
	waitForJS(t, ctx, hasClassExpression(lastUnreadFeedSelector, "active"), "last unread feed active before boundary down")
	runActions(t, ctx, chromedp.Focus(lastUnreadFeedSelector, chromedp.ByQuery))
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression(lastUnreadFeedSelector),
		"last unread feed focused before boundary down",
	)
	waitForJS(t, ctx, feedMoreCollapsedExpression(), "more panel collapsed before boundary down")

	pressKey(t, ctx, "ArrowDown")
	waitForJS(t, ctx, feedMoreExpandedExpression(), "boundary down expands more panel")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression("#feed-list .feed-more-button"),
		"boundary down focuses more button",
	)

	pressKey(t, ctx, "ArrowDown")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression(archiveFeedSelector),
		"arrow down from more focuses first zero-unread feed",
	)
	waitForJS(t, ctx, elementPresentExpression(archiveListSelector), "archive feed main content loaded")

	pressKey(t, ctx, "ArrowUp")
	waitForJS(t, ctx, feedMoreCollapsedExpression(), "boundary up collapses more panel")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression("#feed-list .feed-more-button"),
		"boundary up focuses more button",
	)

	pressKey(t, ctx, "ArrowUp")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression(lastUnreadFeedSelector),
		"arrow up from more returns focus to last unread feed",
	)
	waitForJS(t, ctx, hasClassExpression(lastUnreadFeedSelector, "active"), "last unread feed active after boundary flow")
	waitForJS(t, ctx, activeElementMatchesExpression(lastUnreadFeedSelector), "last unread feed retains focus")
}

func runHiddenSelectionFallbackFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	secondaryFeedSelector := fmt.Sprintf(`#feed-list .feed-link[data-feed-id="%d"]`, fixture.secondaryFeedID)
	tertiaryFeedSelector := fmt.Sprintf(`#feed-list .feed-link[data-feed-id="%d"]`, fixture.tertiaryFeedID)
	quaternaryFeedSelector := fmt.Sprintf(
		`#feed-list .feed-link[data-feed-id="%d"]`,
		fixture.quaternaryFeedID,
	)
	secondaryListSelector := fmt.Sprintf(`#main-content #item-list[data-feed-id="%d"]`, fixture.secondaryFeedID)
	tertiaryListSelector := fmt.Sprintf(`#main-content #item-list[data-feed-id="%d"]`, fixture.tertiaryFeedID)
	quaternaryListSelector := fmt.Sprintf(`#main-content #item-list[data-feed-id="%d"]`, fixture.quaternaryFeedID)
	secondaryFirstRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)
	secondarySecondRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondarySecondItemID)
	secondaryNoReaderRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryNoReaderID)
	secondarySummaryRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondarySummaryItemID)
	secondaryFirstRowTarget := fmt.Sprintf("#item-%d", fixture.secondaryFirstItemID)
	secondarySecondRowTarget := fmt.Sprintf("#item-%d", fixture.secondarySecondItemID)
	secondaryNoReaderRowTarget := fmt.Sprintf("#item-%d", fixture.secondaryNoReaderID)
	secondarySummaryRowTarget := fmt.Sprintf("#item-%d", fixture.secondarySummaryItemID)
	secondaryFirstSelectedID := fmt.Sprintf("item-%d", fixture.secondaryFirstItemID)
	secondarySecondSelectedID := fmt.Sprintf("item-%d", fixture.secondarySecondItemID)
	secondaryNoReaderSelectedID := fmt.Sprintf("item-%d", fixture.secondaryNoReaderID)
	secondarySummarySelectedID := fmt.Sprintf("item-%d", fixture.secondarySummaryItemID)
	tertiaryListTarget := `#main-content > section.items`

	clickElement(t, ctx, secondaryFeedSelector, "open secondary feed before hidden-selection flow")
	waitForJS(
		t,
		ctx,
		hasClassExpression(secondaryFeedSelector, "active"),
		"secondary feed active before hidden-selection flow",
	)
	waitForJS(
		t,
		ctx,
		elementPresentExpression(secondaryListSelector),
		"secondary items loaded before hidden-selection flow",
	)
	waitForJS(t, ctx, feedMoreCollapsedExpression(), "more panel collapsed before hidden-selection flow")

	requestHTMX(
		t,
		ctx,
		"POST",
		fmt.Sprintf("/items/%d/toggle", fixture.secondaryFirstItemID),
		secondaryFirstRowTarget,
		secondaryFirstSelectedID,
	)
	waitForJS(t, ctx, hasClassExpression(secondaryFirstRowSelector, "is-read"), "secondary first row marked read")

	requestHTMX(
		t,
		ctx,
		"POST",
		fmt.Sprintf("/items/%d/toggle", fixture.secondarySecondItemID),
		secondarySecondRowTarget,
		secondarySecondSelectedID,
	)
	waitForJS(t, ctx, hasClassExpression(secondarySecondRowSelector, "is-read"), "secondary second row marked read")

	requestHTMX(
		t,
		ctx,
		"POST",
		fmt.Sprintf("/items/%d/toggle", fixture.secondaryNoReaderID),
		secondaryNoReaderRowTarget,
		secondaryNoReaderSelectedID,
	)
	waitForJS(t, ctx, hasClassExpression(secondaryNoReaderRowSelector, "is-read"), "secondary no-reader row marked read")

	requestHTMX(
		t,
		ctx,
		"POST",
		fmt.Sprintf("/items/%d/toggle", fixture.secondarySummaryItemID),
		secondarySummaryRowTarget,
		secondarySummarySelectedID,
	)
	waitForJS(t, ctx, hasClassExpression(secondarySummaryRowSelector, "is-read"), "secondary summary row marked read")

	waitForJS(
		t,
		ctx,
		elementHiddenExpression(secondaryFeedSelector),
		"secondary feed hidden after individual toggles reach zero unread",
	)

	runActions(t, ctx, chromedp.Focus("#item-list", chromedp.ByQuery))
	pressKey(t, ctx, "h")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression(tertiaryFeedSelector),
		"hidden selected feed resolves to next visible feed after individual toggles",
	)
	waitForJS(t, ctx, hasClassExpression(tertiaryFeedSelector, "active"), "tertiary feed active after toggle fallback")
	waitForJS(t, ctx, elementPresentExpression(tertiaryListSelector), "tertiary items auto-load after returning to feed panel")

	pressKey(t, ctx, "l")
	waitForJS(t, ctx, elementPresentExpression(tertiaryListSelector), "tertiary items loaded before mark-all")
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "items panel focused on tertiary feed")

	requestHTMX(
		t,
		ctx,
		"POST",
		fmt.Sprintf("/feeds/%d/items/read", fixture.tertiaryFeedID),
		tertiaryListTarget,
		"",
	)
	waitForJS(
		t,
		ctx,
		elementHiddenExpression(tertiaryFeedSelector),
		"tertiary feed hidden after mark-all-read reaches zero unread",
	)

	runActions(t, ctx, chromedp.Focus("#item-list", chromedp.ByQuery))
	pressKey(t, ctx, "h")
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression(quaternaryFeedSelector),
		"hidden selected feed resolves to next visible feed after mark-all-read",
	)
	waitForJS(
		t,
		ctx,
		hasClassExpression(quaternaryFeedSelector, "active"),
		"quaternary feed active after mark-all fallback",
	)
	waitForJS(t, ctx, elementPresentExpression(quaternaryListSelector), "quaternary items auto-load after mark-all fallback")
}

func runMobileFilteredHistoryFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	selectedFeedID := fixture.secondaryFeedID
	filteredStreamURI := fmt.Sprintf("/mobile/stream?selected_feed_id=%d", selectedFeedID)
	readerURI := fmt.Sprintf("/mobile/items/%d/reader?selected_feed_id=%d", fixture.secondaryFirstItemID, selectedFeedID)
	readerSelector := fmt.Sprintf(
		`.mobile-card-open[hx-get="/mobile/items/%d/reader?selected_feed_id=%d"]`,
		fixture.secondaryFirstItemID,
		selectedFeedID,
	)

	selectMobileFeedFilter(t, ctx, selectedFeedID)
	waitForJS(t, ctx, requestURIExpression(filteredStreamURI), "filtered mobile stream URL")
	waitForJS(t, ctx, mobileFilterValueExpression(selectedFeedID), "secondary feed selected in filter")
	waitForJS(t, ctx, textPresentExpression("Secondary One"), "secondary item present in filtered stream")
	waitForJS(t, ctx, textAbsentExpression("Primary One"), "other feed item hidden from filtered stream")

	runActions(
		t,
		ctx,
		chromedp.WaitVisible(readerSelector, chromedp.ByQuery),
		chromedp.Click(readerSelector, chromedp.ByQuery),
	)
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "filtered reader loaded")
	waitForJS(t, ctx, requestURIExpression(readerURI), "filtered reader URL")
	waitForJS(t, ctx, textPresentExpression("Secondary One"), "filtered reader title present")

	runActions(t, ctx, chromedp.Evaluate(`history.back()`, nil))
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "history back returns to filtered stream")
	waitForJS(t, ctx, requestURIExpression(filteredStreamURI), "filtered stream URL after history back")
	waitForJS(t, ctx, mobileFilterValueExpression(selectedFeedID), "filter stays selected after history back")
	waitForJS(t, ctx, textPresentExpression("Secondary One"), "filtered stream item still present after history back")
	waitForJS(t, ctx, textAbsentExpression("Primary One"), "other feed item remains hidden after history back")
}

func runMobileSummaryOnlyPreviewFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	waitForJS(
		t,
		ctx,
		mobileCardCompactPreviewExpression(
			fixture.secondarySummaryItemID,
			"Summary-only heading Summary-only fallback preview.",
		),
		"summary-only mobile card compact preview",
	)
}

func runMobileFilteredEmptyStateFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	selectedFeedID := fixture.primaryFeedID
	filteredStreamURI := fmt.Sprintf("/mobile/stream?selected_feed_id=%d", selectedFeedID)
	markReadSelector := fmt.Sprintf(
		`.mobile-card-mark-read[hx-post="/mobile/items/%d/read?selected_feed_id=%d"]`,
		fixture.primaryFirstItemID,
		selectedFeedID,
	)

	selectMobileFeedFilter(t, ctx, selectedFeedID)
	waitForJS(t, ctx, requestURIExpression(filteredStreamURI), "single-item filtered stream URL")
	waitForJS(t, ctx, mobileFilterValueExpression(selectedFeedID), "single-item feed selected in filter")
	waitForJS(t, ctx, textPresentExpression("Primary One"), "single-item filtered story present before clear")

	runActions(
		t,
		ctx,
		chromedp.WaitVisible(markReadSelector, chromedp.ByQuery),
		chromedp.Click(markReadSelector, chromedp.ByQuery),
	)
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-empty="true"]`), "filtered empty state after clearing last unread")
	waitForJS(t, ctx, requestURIExpression(filteredStreamURI), "filtered stream URL retained after clearing last unread")
	waitForJS(t, ctx, mobileFilterValueExpression(selectedFeedID), "filter stays selected after clearing last unread")
	waitForJS(t, ctx, textPresentExpression("Primary Feed is caught up."), "feed-specific empty-state heading shown")
	waitForJS(
		t,
		ctx,
		textPresentExpression("There is nothing unread in Primary Feed right now."),
		"feed-specific empty-state copy shown",
	)
}

func runMobileReaderTopBarFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	selectedFeedID := fixture.secondaryFeedID
	filteredStreamURI := fmt.Sprintf("/mobile/stream?selected_feed_id=%d", selectedFeedID)
	readerURI := fmt.Sprintf("/mobile/items/%d/reader?selected_feed_id=%d", fixture.secondaryFirstItemID, selectedFeedID)
	readerSelector := fmt.Sprintf(
		`.mobile-card-open[hx-get="/mobile/items/%d/reader?selected_feed_id=%d"]`,
		fixture.secondaryFirstItemID,
		selectedFeedID,
	)
	primaryStreamURI := fmt.Sprintf("/mobile/stream?selected_feed_id=%d", fixture.primaryFeedID)

	selectMobileFeedFilter(t, ctx, selectedFeedID)
	waitForJS(t, ctx, requestURIExpression(filteredStreamURI), "reader topbar filtered stream URL")

	runActions(
		t,
		ctx,
		chromedp.WaitVisible(readerSelector, chromedp.ByQuery),
		chromedp.Click(readerSelector, chromedp.ByQuery),
	)
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "reader topbar reader loaded")
	waitForJS(t, ctx, requestURIExpression(readerURI), "reader topbar reader URL")
	waitForJS(t, ctx, mobileFilterValueExpression(selectedFeedID), "reader topbar filter visible in reader")

	clickElement(t, ctx, "#topbar-brand-button", "mobile topbar brand pulse")
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "brand pulse returns to stream")
	waitForJS(t, ctx, requestURIExpression(filteredStreamURI), "brand pulse preserves filtered stream URL")
	waitForJS(t, ctx, mobileFilterValueExpression(selectedFeedID), "brand pulse preserves filtered selection")

	runActions(
		t,
		ctx,
		chromedp.WaitVisible(readerSelector, chromedp.ByQuery),
		chromedp.Click(readerSelector, chromedp.ByQuery),
	)
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-reader="true"]`), "reader reopened before selector change")
	waitForJS(t, ctx, mobileFilterValueExpression(selectedFeedID), "reader reopened with selected feed")

	selectMobileFeedFilter(t, ctx, fixture.primaryFeedID)
	waitForJS(t, ctx, elementPresentExpression(`[data-mobile-stream="true"]`), "reader selector change returns to stream")
	waitForJS(t, ctx, requestURIExpression(primaryStreamURI), "reader selector change updates stream URL")
	waitForJS(t, ctx, mobileFilterValueExpression(fixture.primaryFeedID), "reader selector change updates selection")
	waitForJS(t, ctx, textPresentExpression("Primary One"), "reader selector change shows primary item")
	waitForJS(t, ctx, textAbsentExpression("Secondary One"), "reader selector change hides previous feed items")
}

func runExpandCollapseFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	rowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)
	itemID := fixture.secondaryFirstItemID
	selectedItemID := fmt.Sprintf("item-%d", itemID)
	itemTarget := fmt.Sprintf("#item-%d", itemID)

	requestHTMX(
		t,
		ctx,
		"GET",
		fmt.Sprintf("/items/%d", itemID),
		itemTarget,
		selectedItemID,
	)

	waitForJS(t, ctx, hasClassExpression(rowSelector, "is-expanded"), "expanded row")
	waitForJS(t, ctx, hasClassExpression("#content-panel", "is-open"), "opened content panel")
	waitForJS(t, ctx, contentPanelItemExpression(fixture.secondaryFirstItemID), "expanded panel item")

	requestHTMX(
		t,
		ctx,
		"GET",
		fmt.Sprintf("/items/%d/compact", itemID),
		itemTarget,
		selectedItemID,
	)
	waitForJS(t, ctx, missingClassExpression(rowSelector, "is-expanded"), "collapsed row")
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "closed content panel")
}

func runSummaryOnlyDesktopReaderFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	rowSelector := fmt.Sprintf(`#item-%d`, fixture.secondarySummaryItemID)
	itemID := fixture.secondarySummaryItemID
	itemTarget := fmt.Sprintf("#item-%d", itemID)
	selectedItemID := fmt.Sprintf("item-%d", itemID)

	waitForJS(t, ctx, elementPresentExpression(rowSelector), "summary-only row present in desktop list")
	waitForJS(t, ctx, textPresentExpression("Summary-only heading Summary-only fallback preview."), "summary-only compact preview text")
	waitForJS(t, ctx, elementAbsentExpression(rowSelector+" .item-summary h2"), "summary-only compact row heading markup removed")
	waitForJS(t, ctx, elementAbsentExpression(rowSelector+" .item-summary img"), "summary-only compact row image removed")

	requestHTMX(
		t,
		ctx,
		"GET",
		fmt.Sprintf("/items/%d", itemID),
		itemTarget,
		selectedItemID,
	)
	waitForJS(t, ctx, hasClassExpression(rowSelector, "is-expanded"), "summary-only row expanded")
	waitForJS(t, ctx, hasClassExpression("#content-panel", "is-open"), "summary-only content panel opened")
	waitForJS(t, ctx, contentPanelItemExpression(itemID), "summary-only content panel item")
}

func runFeedToItemsOutlineEntryFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	firstRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)

	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-active"), "first row active before feed-to-items step")
	waitForJS(
		t,
		ctx,
		missingClassExpression("#item-list", "is-keyboard-nav"),
		"keyboard nav marker absent before feed focus",
	)

	runActions(t, ctx, chromedp.Focus("#feed-list .feed-link.active", chromedp.ByQuery))
	waitForJS(
		t,
		ctx,
		activeElementMatchesExpression("#feed-list .feed-link.active"),
		"feed panel focus before right arrow",
	)

	pressKey(t, ctx, "l")
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "items panel focus after right arrow from feed")
	waitForJS(t, ctx, itemOutlineVisibleExpression(firstRowSelector), "outline visible after right arrow from feed")
}

func runContentToItemsOutlineEntryFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	firstRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)
	firstRowTitleSelector := fmt.Sprintf(`#item-%d .item-title`, fixture.secondaryFirstItemID)

	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-active"), "first row active before content-to-items step")
	runActions(t, ctx, chromedp.Click(firstRowTitleSelector, chromedp.ByQuery))
	waitForJS(
		t,
		ctx,
		hasClassExpression(firstRowSelector, "is-expanded"),
		"title opens first row before content entry",
	)
	waitForJS(
		t,
		ctx,
		hasClassExpression("#content-panel", "is-open"),
		"title opens content panel before content entry",
	)
	waitForJS(
		t,
		ctx,
		missingClassExpression("#item-list", "is-keyboard-nav"),
		"keyboard nav marker absent before items-to-content step",
	)

	runActions(t, ctx, chromedp.Focus("#item-list", chromedp.ByQuery))
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "items panel focus before content entry")

	pressKey(t, ctx, "l")
	waitForJS(
		t,
		ctx,
		hasClassExpression(firstRowSelector, "is-expanded"),
		"expanded first row before content-to-items step",
	)
	waitForJS(t, ctx, hasClassExpression("#content-panel", "is-open"), "opened content panel before content-to-items step")
	waitForJS(t, ctx, activeElementMatchesExpression("#content-panel"), "content panel focus before left arrow")

	pressKey(t, ctx, "h")
	waitForJS(
		t,
		ctx,
		missingClassExpression(firstRowSelector, "is-expanded"),
		"collapsed first row after left arrow from content",
	)
	waitForJS(
		t,
		ctx,
		missingClassExpression("#content-panel", "is-open"),
		"closed content panel after left arrow from content",
	)
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "items panel focus after left arrow from content")
	waitForJS(t, ctx, itemOutlineVisibleExpression(firstRowSelector), "outline visible after left arrow from content")
}

func runToggleReadFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	rowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)
	toggleSelector := fmt.Sprintf(`#item-%d .item-read-toggle`, fixture.secondaryFirstItemID)

	clickPointerElement(t, ctx, toggleSelector, "mark row read with pointer")
	waitForJS(t, ctx, hasClassExpression(rowSelector, "is-read"), "row marked read")
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "mouse read toggle keeps reader closed")

	clickPointerElement(t, ctx, toggleSelector, "mark row unread with pointer")
	waitForJS(t, ctx, missingClassExpression(rowSelector, "is-read"), "row marked unread")
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "mouse unread toggle keeps reader closed")
}

func runKeyboardFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	firstRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)
	secondRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondarySecondItemID)
	firstRowToggleSelector := fmt.Sprintf(`#item-%d .item-read-toggle`, fixture.secondaryFirstItemID)

	runActions(
		t,
		ctx,
		chromedp.Focus("#item-list", chromedp.ByQuery),
	)
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "item panel focus for keyboard flow")
	pressKey(t, ctx, "k")
	pressKey(t, ctx, "k")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-active"), "first row active")

	pressKey(t, ctx, "j")
	waitForJS(t, ctx, hasClassExpression(secondRowSelector, "is-active"), "second row active")
	waitForJS(t, ctx, itemOutlineVisibleExpression(secondRowSelector), "outline visible after keyboard navigation")

	pressKey(t, ctx, "k")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-active"), "first row active after up")

	clickPointerElement(t, ctx, firstRowToggleSelector, "mark active row read with pointer")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-read"), "row marked read after pointer interaction")
	waitForJS(
		t,
		ctx,
		missingClassExpression("#item-list", "is-keyboard-nav"),
		"pointer click clears keyboard outline state",
	)
	waitForJS(t, ctx, itemOutlineAbsentExpression(firstRowSelector), "outline hidden after pointer interaction")

	runActions(t, ctx, chromedp.Focus("#item-list", chromedp.ByQuery))
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "item panel focus after pointer interaction")

	pressKey(t, ctx, "k")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-active"), "first row active after keyboard restore")
	waitForJS(t, ctx, hasClassExpression("#item-list", "is-keyboard-nav"), "keyboard outline state restored")
	waitForJS(t, ctx, itemOutlineVisibleExpression(firstRowSelector), "outline visible after keyboard restore")

	pressKey(t, ctx, "h")
	waitForJS(t, ctx, activeElementMatchesExpression("#feed-list .feed-link.active"), "feed panel focus")
	waitForJS(
		t,
		ctx,
		hasClassExpression("#item-list", "is-keyboard-nav"),
		"keyboard outline state retained while feed panel focused",
	)
	waitForJS(t, ctx, itemOutlineAbsentExpression(firstRowSelector), "outline hidden while feed panel focused")

	pressKey(t, ctx, "l")
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "item panel focus")
	waitForJS(t, ctx, itemOutlineVisibleExpression(firstRowSelector), "outline visible when returning to items panel")
	pressKey(t, ctx, "k")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-active"), "first row active before content panel open")

	pressKey(t, ctx, "l")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-expanded"), "expanded first row via keyboard")
	waitForJS(t, ctx, hasClassExpression("#content-panel", "is-open"), "opened content panel via keyboard")
	waitForJS(t, ctx, contentPanelItemExpression(fixture.secondaryFirstItemID), "content panel item via keyboard")
	waitForJS(t, ctx, activeElementMatchesExpression("#content-panel"), "content panel focus via keyboard")
	waitForJS(t, ctx, itemOutlineAbsentExpression(firstRowSelector), "outline hidden while content panel focused")

	pressKey(t, ctx, "h")
	waitForJS(t, ctx, missingClassExpression(firstRowSelector, "is-expanded"), "collapsed first row via keyboard")
	waitForJS(t, ctx, missingClassExpression("#content-panel", "is-open"), "closed content panel via keyboard")
	waitForJS(t, ctx, activeElementMatchesExpression("#item-list"), "item panel focus after keyboard collapse")
	pressKey(t, ctx, "j")
	waitForJS(t, ctx, hasClassExpression(secondRowSelector, "is-active"), "second row active after returning to items")
	waitForJS(t, ctx, itemOutlineVisibleExpression(secondRowSelector), "outline visible after return and keyboard nav")
}

func runContentPanelMarkReadButtonFlow(t *testing.T, ctx context.Context, fixture smokeFixture) {
	t.Helper()

	firstRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryFirstItemID)
	secondRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondarySecondItemID)
	noReaderRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondaryNoReaderID)
	summaryRowSelector := fmt.Sprintf(`#item-%d`, fixture.secondarySummaryItemID)
	markReadSelector := `#content-panel button[data-content-panel-mark-read="true"]`

	clickElement(t, ctx, firstRowSelector, "open first article before panel mark-read flow")
	waitForJS(t, ctx, hasClassExpression("#content-panel", "is-open"), "content panel open before mark-read")
	waitForJS(t, ctx, contentPanelItemExpression(fixture.secondaryFirstItemID), "first article open in content panel")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-read"), "first article starts read before mark-read")
	waitForJS(
		t,
		ctx,
		feedUnreadCountExpression(fixture.secondaryFeedID, "3"),
		"secondary feed unread count before mark-read",
	)

	clickElement(t, ctx, markReadSelector, "advance from read first article in content panel")
	waitForJS(t, ctx, hasClassExpression(firstRowSelector, "is-read"), "first article stays read after panel advance")
	waitForJS(
		t,
		ctx,
		contentPanelItemExpression(fixture.secondarySecondItemID),
		"second article opens after read first article",
	)
	waitForJS(t, ctx, hasClassExpression(secondRowSelector, "is-expanded"), "second article row expanded")
	waitForJS(
		t,
		ctx,
		feedUnreadCountExpression(fixture.secondaryFeedID, "3"),
		"secondary feed unread count unchanged after read article advance",
	)

	clickElement(t, ctx, markReadSelector, "mark second article read from content panel")
	waitForJS(t, ctx, hasClassExpression(secondRowSelector, "is-read"), "second article marked read from content panel")
	waitForJS(
		t,
		ctx,
		missingClassExpression(noReaderRowSelector, "is-expanded"),
		"no-reader row skipped by panel mark-read",
	)
	waitForJS(
		t,
		ctx,
		contentPanelItemExpression(fixture.secondarySummaryItemID),
		"summary article opens after skipping no-reader row",
	)
	waitForJS(t, ctx, hasClassExpression(summaryRowSelector, "is-expanded"), "summary row expanded after no-reader skip")
	waitForJS(
		t,
		ctx,
		feedUnreadCountExpression(fixture.secondaryFeedID, "2"),
		"secondary feed unread count after second mark-read",
	)
}

func runActions(t *testing.T, ctx context.Context, actions ...chromedp.Action) {
	t.Helper()

	if err := chromedp.Run(ctx, actions...); err != nil {
		t.Fatalf("chromedp run: %v", err)
	}
}

func clickElement(t *testing.T, ctx context.Context, selector, label string) {
	t.Helper()

	clickElementWithDetail(t, ctx, selector, label, 0)
}

func clickPointerElement(t *testing.T, ctx context.Context, selector, label string) {
	t.Helper()

	clickElementWithDetail(t, ctx, selector, label, 1)
}

func clickElementWithDetail(t *testing.T, ctx context.Context, selector, label string, detail int) {
	t.Helper()

	expression := fmt.Sprintf(
		`(() => {
			const el = document.querySelector(%q);
			if (!el || typeof el.click !== "function") {
				return false;
			}
			if (document.querySelector(".htmx-request, .htmx-swapping, .htmx-settling")) {
				return false;
			}
			const htmxSelector = [
				"[hx-get]", "[hx-post]", "[hx-put]", "[hx-delete]", "[hx-patch]",
				"[data-hx-get]", "[data-hx-post]", "[data-hx-put]", "[data-hx-delete]", "[data-hx-patch]",
			].join(",");
			if (el.matches(htmxSelector)) {
				const data = el["htmx-internal-data"];
				if (!data || !Array.isArray(data.listenerInfos) || data.listenerInfos.length === 0) {
					return false;
				}
			}
			if (%d > 0) {
				el.dispatchEvent(new MouseEvent("click", {
					bubbles: true,
					cancelable: true,
					composed: true,
					detail: %d,
					view: window,
				}));
			} else {
				el.click();
			}
			return true;
		})()`,
		selector,
		detail,
		detail,
	)

	waitForJS(t, ctx, expression, label+" on settled HTMX element")
	waitForJS(t, ctx, htmxSettledExpression(), label+" HTMX settle")
}

func selectMobileFeedFilter(t *testing.T, ctx context.Context, feedID int64) {
	t.Helper()

	expression := fmt.Sprintf(
		`(() => {
			const select = document.querySelector("#mobile-stream-feed-filter");
			if (!select) {
				return false;
			}
			select.value = %q;
			select.dispatchEvent(new Event("change", { bubbles: true }));
			return select.value === %q;
		})()`,
		fmt.Sprintf("%d", feedID),
		fmt.Sprintf("%d", feedID),
	)

	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &ok)); err != nil {
		t.Fatalf("select mobile feed %d: %v", feedID, err)
	}
	if !ok {
		t.Fatalf("select mobile feed %d: mobile filter not available", feedID)
	}
}

func requestHTMX(t *testing.T, ctx context.Context, method, path, target, selectedItemID string) {
	t.Helper()

	expression := fmt.Sprintf(
		`(() => {
			if (document.querySelector(".htmx-request, .htmx-swapping, .htmx-settling")) {
				return false;
			}
			if (!window.htmx || typeof window.htmx.ajax !== "function") {
				return false;
			}
			window.htmx.ajax(%q, %q, {
				source: document.getElementById("main-content"),
				target: %q,
				swap: "outerHTML",
				values: { selected_item_id: %q },
			});
			return true;
		})()`,
		method,
		path,
		target,
		selectedItemID,
	)

	waitForJS(t, ctx, expression, fmt.Sprintf("start htmx %s %s from settled state", method, path))
	waitForJS(t, ctx, htmxSettledExpression(), fmt.Sprintf("htmx %s %s settle", method, path))
}

func pressKey(t *testing.T, ctx context.Context, key string) {
	t.Helper()

	expression := fmt.Sprintf(
		`(() => {
			if (document.querySelector(".htmx-request, .htmx-swapping, .htmx-settling")) {
				return false;
			}
			const target = document.activeElement || document.body;
			target.dispatchEvent(new KeyboardEvent("keydown", {key: %q, bubbles: true, cancelable: true}));
			target.dispatchEvent(new KeyboardEvent("keyup", {key: %q, bubbles: true, cancelable: true}));
			return true;
		})()`,
		key,
		key,
	)

	waitForJS(t, ctx, expression, fmt.Sprintf("press key %q after HTMX settle", key))
}

func waitForJS(t *testing.T, ctx context.Context, expression, label string) {
	t.Helper()

	deadline := time.Now().Add(smokeWaitTimeout)
	for time.Now().Before(deadline) {
		var matches bool
		err := chromedp.Run(ctx, chromedp.Evaluate(expression, &matches))
		if err == nil && matches {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", label)
}

func hasClassExpression(selector, className string) string {
	return fmt.Sprintf(
		`(() => { const el = document.querySelector(%q); return !!el && el.classList.contains(%q); })()`,
		selector,
		className,
	)
}

func inactiveReaderBoundaryExpression() string {
	return `(() => {
		const root = document.querySelector('[data-reader-content="true"]');
		if (!root || !root.hasAttribute("hx-disable")) {
			return false;
		}
		const activeSelector = [
			"[hx-get]", "[hx-post]", "[hx-put]", "[hx-delete]", "[hx-patch]", "[hx-trigger]",
			"[data-hx-get]", "[data-hx-post]", "[data-hx-put]", "[data-hx-delete]", "[data-hx-patch]",
			"[data-hx-trigger]", "form", "iframe", "script", "style", "svg", "math",
		].join(",");
		return root.textContent.includes("Inactive boundary smoke content") && !root.querySelector(activeSelector);
	})()`
}

func pathnameExpression(path string) string {
	return fmt.Sprintf(`(() => window.location.pathname === %q)()`, path)
}

func requestURIExpression(path string) string {
	return fmt.Sprintf(`(() => (window.location.pathname + window.location.search) === %q)()`, path)
}

func mobileFilterValueExpression(feedID int64) string {
	return fmt.Sprintf(
		`(() => {
			const select = document.querySelector("#mobile-stream-feed-filter");
			return !!select && select.value === %q;
		})()`,
		fmt.Sprintf("%d", feedID),
	)
}

func elementPresentExpression(selector string) string {
	return fmt.Sprintf(
		`(() => !!document.querySelector(%q))()`,
		selector,
	)
}

func elementAbsentExpression(selector string) string {
	return fmt.Sprintf(
		`(() => !document.querySelector(%q))()`,
		selector,
	)
}

func elementHiddenExpression(selector string) string {
	return fmt.Sprintf(
		`(() => {
			const el = document.querySelector(%q);
			return !!el && el.getClientRects().length === 0;
		})()`,
		selector,
	)
}

func elementVisibleExpression(selector string) string {
	return fmt.Sprintf(
		`(() => {
			const el = document.querySelector(%q);
			return !!el && el.getClientRects().length > 0;
		})()`,
		selector,
	)
}

func generalMenuOpenExpression() string {
	return `(() => {
		const button = document.querySelector("#topbar-shortcuts-button");
		const panel = document.querySelector("#topbar-shortcuts-panel");
		return !!button && !!panel && button.getAttribute("aria-expanded") === "true" &&
			!panel.hidden && panel.getClientRects().length > 0;
	})()`
}

func generalMenuClosedExpression() string {
	return `(() => {
		const button = document.querySelector("#topbar-shortcuts-button");
		const panel = document.querySelector("#topbar-shortcuts-panel");
		return !!button && !!panel && button.getAttribute("aria-expanded") === "false" &&
			panel.hidden && panel.getClientRects().length === 0;
	})()`
}

func generalMenuCompactExpression() string {
	return `(() => {
		const panel = document.querySelector("#topbar-shortcuts-panel");
		if (!panel || panel.hidden) return false;
		const rect = panel.getBoundingClientRect();
		return getComputedStyle(panel).overflowY === "auto" && panel.scrollHeight > panel.clientHeight &&
			rect.top >= 0 && rect.bottom <= innerHeight + 1;
	})()`
}

func generalMenuArrowScrollExpression() string {
	return `(() => {
		const panel = document.querySelector("#topbar-shortcuts-panel");
		const active = document.querySelector("#item-list .item-entry.is-active");
		return !!panel && !!active && document.activeElement === panel && panel.scrollTop > 0 &&
			active.id === window.__menuReaderActiveID;
	})()`
}

func generalMenuEndVisibleExpression() string {
	return `(() => {
		const panel = document.querySelector("#topbar-shortcuts-panel");
		const lastRow = panel && panel.querySelector('[data-menu-section="shortcuts"] .topbar-shortcuts-row:last-child');
		if (!panel || !lastRow) return false;
		const panelRect = panel.getBoundingClientRect();
		const rowRect = lastRow.getBoundingClientRect();
		return panel.scrollTop > 0 && rowRect.top >= panelRect.top && rowRect.bottom <= panelRect.bottom + 1;
	})()`
}

func focusOutlineVisibleExpression(selector string) string {
	return fmt.Sprintf(
		`(() => {
			const el = document.querySelector(%q);
			if (!el || document.activeElement !== el) return false;
			const style = getComputedStyle(el);
			return style.outlineStyle !== "none" && parseFloat(style.outlineWidth) >= 2;
		})()`,
		selector,
	)
}

func itemActionHierarchyExpression(primarySelector, secondarySelector string) string {
	return fmt.Sprintf(
		`(() => {
			const primary = document.querySelector(%q);
			const secondary = document.querySelector(%q);
			if (!primary || !secondary) return false;
			const primaryStyle = getComputedStyle(primary);
			const secondaryStyle = getComputedStyle(secondary);
			return parseInt(primaryStyle.fontWeight, 10) >= 600 &&
				parseFloat(primaryStyle.fontSize) > parseFloat(secondaryStyle.fontSize);
		})()`,
		primarySelector,
		secondarySelector,
	)
}

func itemReadToggleStateExpression(selector, state, action string, checked bool) string {
	return fmt.Sprintf(
		`(() => {
			const button = document.querySelector(%q);
			if (!button) return false;
			const hasCheck = !!button.querySelector(".item-read-toggle-check");
			return button.dataset.readState === %q &&
				button.getAttribute("aria-label") === %q &&
				button.title === %q && hasCheck === %t;
		})()`,
		selector,
		state,
		action,
		action,
		checked,
	)
}

func itemReadToggleThemeExpression(selector, theme string) string {
	return fmt.Sprintf(
		`(() => {
			document.documentElement.setAttribute("data-theme", %q);
			const button = document.querySelector(%q);
			if (!button) return false;
			const rect = button.getBoundingClientRect();
			const style = getComputedStyle(button);
			return rect.width >= 32 && rect.height >= 32 &&
				style.display.endsWith("flex") && style.borderStyle === "solid" &&
				parseFloat(style.borderWidth) >= 1 && style.color !== "rgba(0, 0, 0, 0)";
		})()`,
		theme,
		selector,
	)
}

func armSourceLinkCapture(t *testing.T, ctx context.Context) {
	t.Helper()

	runActions(t, ctx, chromedp.Evaluate(`(() => {
		window.__pulseCapturedSourceHref = "";
		if (window.__pulseSourceCaptureBound) return true;
		window.__pulseSourceCaptureBound = true;
		document.addEventListener("click", (event) => {
			const target = event.target;
			const link = target && target.closest
				? target.closest("a[data-item-source-link='true']")
				: null;
			if (!link) return;
			event.preventDefault();
			window.__pulseCapturedSourceHref = link.href;
		}, true);
		return true;
	})()`, nil))
}

func sourceLinkCapturedExpression(selector string) string {
	return fmt.Sprintf(
		`(() => {
			const link = document.querySelector(%q);
			return !!link && window.__pulseCapturedSourceHref === link.href;
		})()`,
		selector,
	)
}

func missingClassExpression(selector, className string) string {
	return fmt.Sprintf(
		`(() => { const el = document.querySelector(%q); return !!el && !el.classList.contains(%q); })()`,
		selector,
		className,
	)
}

func activeElementMatchesExpression(selector string) string {
	return fmt.Sprintf(
		`(() => { const el = document.querySelector(%q); return !!el && document.activeElement === el; })()`,
		selector,
	)
}

func feedSelectionSettledExpression(feedID int64, feedTitle string) string {
	return fmt.Sprintf(
		`(() => {
			const feedID = %q;
			const active = document.querySelector("#feed-list .feed-link.active");
			const input = document.querySelector("#selected-feed-id");
			const list = document.querySelector("#item-list");
			const title = document.querySelector(".items-title");
			return !!active && !!input && !!list && !!title &&
				active.dataset.feedId === feedID &&
				input.value === feedID &&
				list.dataset.feedId === feedID &&
				title.textContent.trim() === %q;
		})()`,
		fmt.Sprintf("%d", feedID),
		feedTitle,
	)
}

func feedMoreExpandedExpression() string {
	return `(() => {
		const button = document.querySelector("#feed-list .feed-more-button");
		const zeroList = document.querySelector("#feed-list #feed-zero-list");
		if (!button || !zeroList) {
			return false;
		}
		return button.getAttribute("aria-expanded") === "true" &&
			zeroList.hidden === false &&
			window.getComputedStyle(zeroList).display !== "none";
	})()`
}

func feedMoreCollapsedExpression() string {
	return `(() => {
		const button = document.querySelector("#feed-list .feed-more-button");
		const zeroList = document.querySelector("#feed-list #feed-zero-list");
		if (!button || !zeroList) {
			return false;
		}
		return button.getAttribute("aria-expanded") === "false" &&
			zeroList.hidden === true &&
			window.getComputedStyle(zeroList).display === "none";
	})()`
}

func itemOutlineVisibleExpression(rowSelector string) string {
	return fmt.Sprintf(
		`(() => {
			const list = document.querySelector("#item-list");
			const row = document.querySelector(%q);
			if (!list || !row) {
				return false;
			}
			const boxShadow = window.getComputedStyle(row).boxShadow || "";
			return list.classList.contains("is-keyboard-nav") &&
				list.matches(":focus-within") &&
				boxShadow.includes("rgb(37, 99, 235)");
		})()`,
		rowSelector,
	)
}

func itemOutlineAbsentExpression(rowSelector string) string {
	return fmt.Sprintf(
		`(() => {
			const row = document.querySelector(%q);
			if (!row) {
				return false;
			}
			const boxShadow = window.getComputedStyle(row).boxShadow || "";
			return !boxShadow.includes("rgb(37, 99, 235)");
		})()`,
		rowSelector,
	)
}

func desktopLayoutExpression() string {
	return `(() => !window.matchMedia("(max-width: 960px)").matches)()`
}

func htmxReadyExpression() string {
	return `(() => !!window.htmx && typeof window.htmx.ajax === "function")()`
}

func htmxElementReadyExpression(selector string) string {
	return fmt.Sprintf(
		`(() => {
			if (document.querySelector(".htmx-request, .htmx-swapping, .htmx-settling")) {
				return false;
			}
			const el = document.querySelector(%q);
			if (!el) {
				return false;
			}
			if (!el.matches("[hx-get], [hx-post], [hx-put], [hx-delete], [hx-patch]")) {
				return true;
			}
			const data = el["htmx-internal-data"];
			return !!data && Array.isArray(data.listenerInfos) && data.listenerInfos.length > 0;
		})()`,
		selector,
	)
}

func htmxSettledExpression() string {
	return `(() => !document.querySelector(".htmx-request, .htmx-swapping, .htmx-settling"))()`
}

func mobileLayoutExpression() string {
	return `(() => window.matchMedia("(max-width: 960px)").matches)()`
}

func mobileAggregateSectionOrderExpression(feedIDs ...int64) string {
	parts := make([]string, 0, len(feedIDs))

	for _, feedID := range feedIDs {
		parts = append(parts, strconv.FormatInt(feedID, 10))
	}
	expected := strings.Join(parts, ",")

	return fmt.Sprintf(
		`(() => Array.from(document.querySelectorAll("[data-mobile-feed-section][data-feed-id]"))
			.map((section) => section.dataset.feedId).join(",") === %q)()`,
		expected,
	)
}

func mobileAggregateBoundedExpression(maxSections, maxItemsPerSection int) string {
	return fmt.Sprintf(
		`(() => {
			const sections = Array.from(document.querySelectorAll("[data-mobile-feed-section]"));
			return sections.length <= %d && sections.every((section) =>
				section.querySelectorAll("[data-mobile-item-id]").length <= %d
			);
		})()`,
		maxSections,
		maxItemsPerSection,
	)
}

func responsiveMobileLayoutExpression(feedID int64) string {
	return fmt.Sprintf(
		`(() => {
			const content = document.querySelectorAll(
				'#main-content [data-mobile-stream="true"], #main-content [data-mobile-reader="true"]',
			);
			const filters = document.querySelectorAll("#mobile-stream-feed-filter");
			const filter = filters[0];
			return window.matchMedia("(max-width: 960px)").matches &&
				content.length === 1 && filters.length === 1 &&
				filter.getClientRects().length > 0 && !filter.disabled &&
				filter.options.length > 0 && filter.value === %q;
		})()`,
		fmt.Sprintf("%d", feedID),
	)
}

func responsiveDesktopLayoutExpression(feedID int64) string {
	return fmt.Sprintf(
		`(() => {
			const list = document.querySelector("#item-list");
			const feedPanel = document.querySelector(".feed-panel");
			const mainPanel = document.querySelector(".main-panel");
			const contentPanel = document.querySelector("#content-panel");
			const brand = document.querySelector('#topbar-brand-button[hx-post="/feeds/pulse"]');
			const mobileSlot = document.querySelector("#topbar-mobile-slot");
			return !window.matchMedia("(max-width: 960px)").matches &&
				!!list && list.dataset.feedId === %q &&
				feedPanel.getClientRects().length > 0 && mainPanel.getClientRects().length > 0 &&
				!!contentPanel && !!brand && !!mobileSlot && !mobileSlot.classList.contains("is-active") &&
				!document.querySelector("#main-content [data-mobile-stream], #main-content [data-mobile-reader]") &&
				!document.querySelector("#mobile-stream-feed-filter");
		})()`,
		fmt.Sprintf("%d", feedID),
	)
}

func textPresentExpression(text string) string {
	return fmt.Sprintf(
		`(() => document.body && document.body.textContent && document.body.textContent.includes(%q))()`,
		text,
	)
}

func textAbsentExpression(text string) string {
	return fmt.Sprintf(
		`(() => !document.body || !document.body.textContent || !document.body.textContent.includes(%q))()`,
		text,
	)
}

func contentPanelItemExpression(itemID int64) string {
	return fmt.Sprintf(
		`(() => {
			const panel = document.querySelector("#content-panel.is-open");
			if (!panel) {
				return false;
			}
			const article = panel.querySelector(".content-panel-article[data-item-id]");
			return !!article && article.getAttribute("data-item-id") === %q;
		})()`,
		fmt.Sprintf("%d", itemID),
	)
}

func feedUnreadCountExpression(feedID int64, want string) string {
	return fmt.Sprintf(
		`(() => {
			const feed = document.querySelector(%q);
			if (!feed) {
				return false;
			}
			const count = feed.querySelector(".feed-count");
			return !!count && count.textContent.trim() === %q;
		})()`,
		fmt.Sprintf(`#feed-list .feed-link[data-feed-id="%d"]`, feedID),
		want,
	)
}

func desktopPulseIndicatorsExpression(fixture smokeFixture) string {
	return fmt.Sprintf(
		`(() => {
			const checks = [
				[%q, "fresh", "Fresh"],
				[%q, "pending", "Refreshing"],
				[%q, "error", "Refresh failed"],
			];
			if (document.documentElement.scrollWidth > window.innerWidth + 1) {
				return false;
			}
			return checks.every(([feedID, className, label]) => {
				const feed = document.querySelector(
					'#feed-list .feed-link[data-feed-id="' + feedID + '"]'
				);
				const indicator = feed && feed.querySelector(".feed-pulse-indicator." + className);
				if (!feed || !indicator || indicator.getAttribute("role") !== "img" ||
					indicator.getAttribute("aria-label") !== label) {
					return false;
				}
				const style = window.getComputedStyle(feed);
				const columns = style.gridTemplateColumns.split(" ");
				return columns.length === 3 && Math.round(parseFloat(columns[0])) === 10;
			});
		})()`,
		fmt.Sprintf("%d", fixture.primaryFeedID),
		fmt.Sprintf("%d", fixture.secondaryFeedID),
		fmt.Sprintf("%d", fixture.tertiaryFeedID),
	)
}

func mobileFlatStreamLayoutExpression(fixture smokeFixture) string {
	return fmt.Sprintf(
		`(() => {
			const panel = document.querySelector(".mobile-feed-status-panel");
			const refreshRow = document.querySelector(".mobile-stream-refresh-row");
			const refreshButton = document.querySelector(".mobile-stream-refresh-button");
			const brandButton = document.querySelector("#topbar-brand-button");
			const list = document.querySelector(".mobile-stream-list");
			const card = document.querySelector(".mobile-card");
			const titleRow = card && card.querySelector(".mobile-card-title-row");
			const openButton = card && card.querySelector(".mobile-card-open");
			const markRead = card && card.querySelector(".mobile-card-mark-read");
			if (panel || refreshRow || refreshButton || !brandButton || !list || !card ||
				!titleRow || !openButton || !markRead || document.documentElement.scrollWidth > window.innerWidth + 1) {
				return false;
			}
			const hxPost = brandButton.getAttribute("hx-post") || "";
			if (!hxPost.includes(%q) || !hxPost.includes(%q)) {
				return false;
			}
			if (brandButton.getAttribute("aria-label") !== "Refresh feed Secondary Feed") {
				return false;
			}
			if (brandButton.getAttribute("hx-indicator") !== "#topbar-brand-button") {
				return false;
			}
			const pending = brandButton.querySelector(".brand-subtitle-pending");
			if (!pending || pending.textContent.trim() !== "Refreshing Secondary Feed") {
				return false;
			}
			const listStyle = window.getComputedStyle(list);
			const cardStyle = window.getComputedStyle(card);
			if (listStyle.borderTopStyle === "none" || cardStyle.borderBottomStyle === "none" ||
				cardStyle.borderRadius !== "0px" || cardStyle.boxShadow !== "none") {
				return false;
			}
			const titleRect = openButton.getBoundingClientRect();
			const markRect = markRead.getBoundingClientRect();
			if (markRect.left <= titleRect.right || markRect.width < 34 || markRect.height < 34) {
				return false;
			}
			return Boolean(markRead.querySelector("svg.icon")) && markRect.right <= window.innerWidth + 1;
		})()`,
		fmt.Sprintf("/mobile/feeds/%d/refresh", fixture.secondaryFeedID),
		fmt.Sprintf("%d", fixture.secondaryFeedID),
	)
}

func mobileCardCompactPreviewExpression(itemID int64, previewText string) string {
	return fmt.Sprintf(
		`(() => {
			const button = document.querySelector(%q);
			if (!button) {
				return false;
			}
			const card = button.closest(".mobile-card");
			if (!card) {
				return false;
			}
			const summary = card.querySelector(".mobile-card-summary");
			if (!summary) {
				return false;
			}
			const text = (summary.textContent || "").replace(/\s+/g, " ").trim();
			return text === %q && !summary.querySelector("h1, h2, h3, img");
		})()`,
		fmt.Sprintf(`.mobile-card-open[hx-get^="/mobile/items/%d/reader"]`, itemID),
		previewText,
	)
}
