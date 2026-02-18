// Package testutil provides shared helpers for integration-style tests.
package testutil

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"rss/internal/store"
)

var errUnexpectedFeedURL = errors.New("unexpected feed url")

// FeedServer serves mutable feed XML for HTTP-based tests.
type FeedServer struct {
	feedXML string
	mu      sync.RWMutex
}

var (
	//nolint:gochecknoglobals // Tests need one process-wide transport install.
	feedTransportOnce sync.Once
	//nolint:gochecknoglobals // Stores original default transport for passthrough.
	feedTransportBase http.RoundTripper

	//nolint:gochecknoglobals // Shared registry maps synthetic feed URLs to test servers.
	feedRegistryMu sync.RWMutex
	//nolint:gochecknoglobals // Shared registry maps synthetic feed URLs to test servers.
	feedRegistry = make(map[string]*FeedServer)
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// NewFeedServer returns an in-memory feed server and its synthetic feed URL.
//
//nolint:nonamedreturns // gocritic prefers named result tuple here for test helper clarity.
func NewFeedServer(t *testing.T, feedXML string) (server *FeedServer, feedURL string) {
	t.Helper()

	installFeedTransport()

	server = new(FeedServer)
	server.feedXML = feedXML
	feedURL = "https://feed.test/" + url.PathEscape(t.Name())

	feedRegistryMu.Lock()
	feedRegistry[feedURL] = server
	feedRegistryMu.Unlock()

	t.Cleanup(func() {
		feedRegistryMu.Lock()
		delete(feedRegistry, feedURL)
		feedRegistryMu.Unlock()
	})

	return server, feedURL
}

// SetFeedXML replaces the XML body served by this test feed server.
func (f *FeedServer) SetFeedXML(xml string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.feedXML = xml
}

func installFeedTransport() {
	feedTransportOnce.Do(func() {
		feedTransportBase = http.DefaultTransport
		http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			feedRegistryMu.RLock()

			server, ok := feedRegistry[req.URL.String()]

			feedRegistryMu.RUnlock()

			if ok {
				server.mu.RLock()
				defer server.mu.RUnlock()

				resp := new(http.Response)
				resp.StatusCode = http.StatusOK
				resp.Status = "200 OK"
				resp.Header = http.Header{
					"Content-Type": []string{"application/rss+xml"},
				}
				resp.Body = io.NopCloser(strings.NewReader(server.feedXML))
				resp.Request = req

				return resp, nil
			}

			if strings.EqualFold(req.URL.Hostname(), "feed.test") {
				return nil, fmt.Errorf(
					"%w: %s",
					errUnexpectedFeedURL,
					req.URL.String(),
				)
			}

			return feedTransportBase.RoundTrip(req)
		})
	})
}

// RSSItem represents one item used by RSSXML test feed generation.
type RSSItem struct {
	Title       string
	Link        string
	GUID        string
	PubDate     string
	Description string
}

// RSSXML builds a minimal RSS document string with the provided title and items.
func RSSXML(title string, items []RSSItem) string {
	xml := `<?xml version="1.0" encoding="UTF-8"?>`
	xml += "<rss version=\"2.0\"><channel>"
	xml += fmt.Sprintf("<title>%s</title>", title)
	xml += "<link>http://example.com</link>"
	xml += "<description>Test feed</description>"

	var xmlSb84 strings.Builder

	appendXML := func(fragment string) {
		_, writeErr := xmlSb84.WriteString(fragment)
		if writeErr != nil {
			panic(writeErr)
		}
	}

	for _, item := range items {
		appendXML("<item>")
		appendXML(fmt.Sprintf("<title>%s</title>", item.Title))
		appendXML(fmt.Sprintf("<link>%s</link>", item.Link))
		appendXML(fmt.Sprintf("<guid>%s</guid>", item.GUID))
		appendXML(fmt.Sprintf("<pubDate>%s</pubDate>", item.PubDate))
		appendXML(fmt.Sprintf("<description><![CDATA[%s]]></description>", item.Description))
		appendXML("</item>")
	}

	xml += xmlSb84.String()

	xml += "</channel></rss>"

	return xml
}

// OpenTestDB opens and initializes a temporary SQLite database for tests.
func OpenTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	initErr := store.Init(db)
	if initErr != nil {
		closeErr := db.Close()
		if closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}

		t.Fatalf("store.Init: %v", initErr)
	}

	t.Cleanup(func() {
		closeErr := db.Close()
		if closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}
	})

	return db
}

// TimePtr returns a pointer to the provided time value.
func TimePtr(tw time.Time) *time.Time {
	return new(tw)
}
