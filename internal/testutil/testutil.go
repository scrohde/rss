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
	"sync/atomic"
	"testing"
	"time"

	"rss/internal/store"
)

var errUnexpectedFeedURL = errors.New("unexpected feed url")

// FeedServer serves mutable feed XML for HTTP-based tests.
type FeedServer struct {
	headers        http.Header
	feedXML        string
	robotsTxt      string
	statusCode     int
	feedRequests   int
	robotsRequests int
	mu             sync.RWMutex
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
	//nolint:gochecknoglobals // Shared registry maps synthetic hosts to test servers for robots lookups.
	feedHostRegistry = make(map[string]*FeedServer)
	//nolint:gochecknoglobals // Monotonic counter ensures synthetic hosts stay unique in parallel tests.
	feedServerCounter uint64
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type feedLookup struct {
	server        *FeedServer
	robotsRequest bool
	found         bool
}

// NewFeedServer returns an in-memory feed server and its synthetic feed URL.
//
//nolint:nonamedreturns // gocritic prefers named result tuple here for test helper clarity.
func NewFeedServer(t *testing.T, feedXML string) (server *FeedServer, feedURL string) {
	t.Helper()

	installFeedTransport()

	server = new(FeedServer)
	server.feedXML = feedXML
	server.robotsTxt = "User-agent: *\nAllow: /\n"
	server.statusCode = http.StatusOK
	server.headers = http.Header{
		"Content-Type": []string{"application/rss+xml"},
	}
	host := fmt.Sprintf("feed-%d.feed.test", atomic.AddUint64(&feedServerCounter, 1))
	feedURL = "https://" + host + "/" + url.PathEscape(t.Name())

	feedRegistryMu.Lock()
	feedRegistry[feedURL] = server
	feedHostRegistry[host] = server
	feedRegistryMu.Unlock()

	t.Cleanup(func() {
		feedRegistryMu.Lock()
		delete(feedRegistry, feedURL)
		delete(feedHostRegistry, host)
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

// SetRobotsTxt configures robots.txt content for this synthetic feed host.
func (f *FeedServer) SetRobotsTxt(robotsTxt string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.robotsTxt = robotsTxt
}

// SetHTTPResponse configures the response status and headers for this feed URL.
func (f *FeedServer) SetHTTPResponse(statusCode int, headers http.Header) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if statusCode <= 0 {
		statusCode = http.StatusOK
	}

	f.statusCode = statusCode

	f.headers = cloneHeaders(headers)
	if f.headers == nil {
		f.headers = make(http.Header)
	}

	if strings.TrimSpace(f.headers.Get("Content-Type")) == "" {
		f.headers.Set("Content-Type", "application/rss+xml")
	}
}

// FeedRequestCount returns the number of feed URL requests served.
func (f *FeedServer) FeedRequestCount() int {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return f.feedRequests
}

// RobotsRequestCount returns the number of robots.txt requests served.
func (f *FeedServer) RobotsRequestCount() int {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return f.robotsRequests
}

func installFeedTransport() {
	feedTransportOnce.Do(func() {
		feedTransportBase = http.DefaultTransport
		http.DefaultTransport = roundTripFunc(feedTransportRoundTrip)
	})
}

func feedTransportRoundTrip(req *http.Request) (*http.Response, error) {
	resp, handled := syntheticFeedResponse(req)
	if handled {
		return resp, nil
	}

	if isSyntheticFeedHost(req.URL.Hostname()) {
		return nil, fmt.Errorf(
			"%w: %s",
			errUnexpectedFeedURL,
			req.URL.String(),
		)
	}

	resp, err := feedTransportBase.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("base transport round trip: %w", err)
	}

	return resp, nil
}

func syntheticFeedResponse(req *http.Request) (*http.Response, bool) {
	lookup := lookupFeedServer(req.URL)
	if !lookup.found {
		return nil, false
	}

	if lookup.robotsRequest {
		return buildRobotsResponseForServer(req, lookup.server), true
	}

	return buildFeedResponse(req, lookup.server), true
}

func lookupFeedServer(reqURL *url.URL) feedLookup {
	feedRegistryMu.RLock()
	defer feedRegistryMu.RUnlock()

	server, ok := feedRegistry[reqURL.String()]
	if ok {
		return feedLookup{
			server:        server,
			robotsRequest: false,
			found:         true,
		}
	}

	if reqURL.Path == "/robots.txt" {
		host := strings.ToLower(reqURL.Hostname())

		server, ok = feedHostRegistry[host]
		if ok {
			return feedLookup{
				server:        server,
				robotsRequest: true,
				found:         true,
			}
		}
	}

	return feedLookup{
		server:        nil,
		robotsRequest: false,
		found:         false,
	}
}

func buildFeedResponse(req *http.Request, server *FeedServer) *http.Response {
	server.mu.Lock()
	server.feedRequests++
	feedXML := server.feedXML
	statusCode := server.statusCode
	headers := cloneHeaders(server.headers)
	server.mu.Unlock()

	statusCode, headers = normalizeFeedResponse(statusCode, headers)

	resp := new(http.Response)
	resp.StatusCode = statusCode
	resp.Status = fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode))
	resp.Header = headers
	resp.Body = io.NopCloser(strings.NewReader(feedXML))
	resp.Request = req

	return resp
}

func buildRobotsResponseForServer(req *http.Request, server *FeedServer) *http.Response {
	server.mu.Lock()
	server.robotsRequests++
	robotsTxt := server.robotsTxt
	server.mu.Unlock()

	return buildRobotsResponse(req, robotsTxt)
}

func buildRobotsResponse(req *http.Request, robotsTxt string) *http.Response {
	header := make(http.Header)
	header.Set("Content-Type", "text/plain")

	resp := new(http.Response)
	resp.StatusCode = http.StatusOK
	resp.Status = fmt.Sprintf("%d %s", http.StatusOK, http.StatusText(http.StatusOK))
	resp.Header = header
	resp.Body = io.NopCloser(strings.NewReader(robotsTxt))
	resp.Request = req

	return resp
}

func isSyntheticFeedHost(host string) bool {
	normalized := strings.ToLower(strings.TrimSpace(host))
	if normalized == "feed.test" {
		return true
	}

	return strings.HasSuffix(normalized, ".feed.test")
}

func normalizeFeedResponse(statusCode int, headers http.Header) (int, http.Header) {
	if statusCode <= 0 {
		statusCode = http.StatusOK
	}

	if headers == nil {
		headers = make(http.Header)
	}

	if strings.TrimSpace(headers.Get("Content-Type")) == "" {
		headers.Set("Content-Type", "application/rss+xml")
	}

	return statusCode, headers
}

func cloneHeaders(src http.Header) http.Header {
	if src == nil {
		return nil
	}

	dst := make(http.Header, len(src))
	for key, values := range src {
		copied := append([]string(nil), values...)
		dst[key] = copied
	}

	return dst
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
