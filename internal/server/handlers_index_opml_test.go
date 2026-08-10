//nolint:testpackage // Handler integration tests intentionally exercise unexported helpers.
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rss/internal/opml"
	"rss/internal/outbound"
	"rss/internal/store"
)

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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, pathIndex, http.NoBody)
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

	target := fmt.Sprintf("/feeds/%d/delete/confirm", feedID)
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, target, http.NoBody,
	)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("confirm endpoint status: %d", rec.Code)
	}
}

func TestIndexIncludesSubscriptionAndOPMLControls(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, pathIndex, http.NoBody)
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(errIndexStatusFmt, rec.Code)
	}

	body := rec.Body.String()

	requiredSnippets := []string{
		`aria-labelledby="topbar-menu-title"`,
		`id="topbar-menu-title">Menu</h2>`,
		`for="topbar-subscribe-url"`,
		`id="topbar-subscribe-url"`,
		`hx-post="/feeds"`,
		`href="/opml/export"`,
		`hx-post="/opml/import"`,
		`tabindex="-1"`,
		`data-import-file-input="true"`,
	}
	for _, snippet := range requiredSnippets {
		assertContains(t, body, snippet, "expected general feed-management menu control")
	}

	assertMenuSectionOrder(t, body, "feeds", "shortcuts")
	assertNotContains(t, body, `data-menu-section="account"`, "expected no anonymous account menu section")

	if strings.Contains(body, `aria-label="Keyboard shortcuts"`) {
		t.Fatal("expected the whole panel to use a general accessible name")
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/opml/export", http.NoBody)
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/opml/import", body)
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

func TestImportOPMLSkipsNonPublicDestinations(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	app.outboundResolver = outbound.LookupIPAddrFunc(opmlDestinationLookup)
	subscriptions := []opml.Subscription{
		{Title: "Public", URL: "https://public.example/feed.xml"},
		{Title: "Direct loopback", URL: "http://127.0.0.1/feed.xml"},
		{Title: "Private DNS", URL: "https://internal.example/feed.xml"},
		{Title: "Mixed DNS", URL: "https://mixed.example/feed.xml"},
	}

	counts := app.importOPMLSubscriptions(context.Background(), subscriptions)
	if counts.imported != 1 || counts.skipped != 3 {
		t.Fatalf("import counts = %+v, want 1 imported and 3 skipped", counts)
	}

	feeds, err := store.ListFeeds(context.Background(), app.db)
	if err != nil {
		t.Fatalf(errStoreListFeeds, err)
	}

	if len(feeds) != 1 || feeds[0].URL != "https://public.example/feed.xml" {
		t.Fatalf("persisted feeds = %+v, want only public feed", feeds)
	}
}

func opmlDestinationLookup(_ context.Context, host string) ([]net.IPAddr, error) {
	switch host {
	case "public.example":
		return []net.IPAddr{testIPAddr(examplePublicIP)}, nil
	case "mixed.example":
		return []net.IPAddr{testIPAddr(examplePublicIP), testIPAddr("127.0.0.1")}, nil
	default:
		return []net.IPAddr{testIPAddr("192.168.1.10")}, nil
	}
}
