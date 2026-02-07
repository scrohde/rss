package testutil

import (
	"database/sql"
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

type FeedServer struct {
	mu      sync.RWMutex
	feedXML string
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func NewFeedServer(t *testing.T, feedXML string) (*FeedServer, string) {
	t.Helper()
	fs := &FeedServer{feedXML: feedXML}
	feedURL := "https://feed.test/" + url.PathEscape(t.Name())
	prevTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != feedURL {
			return nil, fmt.Errorf("unexpected feed url: %s", req.URL.String())
		}
		fs.mu.RLock()
		defer fs.mu.RUnlock()
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/rss+xml"}},
			Body:       io.NopCloser(strings.NewReader(fs.feedXML)),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	return fs, feedURL
}

func (f *FeedServer) SetFeedXML(xml string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feedXML = xml
}

type RSSItem struct {
	Title       string
	Link        string
	GUID        string
	PubDate     string
	Description string
}

func RSSXML(title string, items []RSSItem) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString("<rss version=\"2.0\"><channel>")
	b.WriteString(fmt.Sprintf("<title>%s</title>", title))
	b.WriteString("<link>http://example.com</link>")
	b.WriteString("<description>Test feed</description>")
	for _, item := range items {
		b.WriteString("<item>")
		b.WriteString(fmt.Sprintf("<title>%s</title>", item.Title))
		b.WriteString(fmt.Sprintf("<link>%s</link>", item.Link))
		b.WriteString(fmt.Sprintf("<guid>%s</guid>", item.GUID))
		b.WriteString(fmt.Sprintf("<pubDate>%s</pubDate>", item.PubDate))
		b.WriteString(fmt.Sprintf("<description><![CDATA[%s]]></description>", item.Description))
		b.WriteString("</item>")
	}
	b.WriteString("</channel></rss>")
	return b.String()
}

func OpenTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := store.Init(db); err != nil {
		_ = db.Close()
		t.Fatalf("store.Init: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TimePtr(tw time.Time) *time.Time {
	return &tw
}
