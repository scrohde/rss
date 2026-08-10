//nolint:testpackage // Handler integration tests intentionally exercise unexported helpers.
package server

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rss/internal/content"
)

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
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, proxyURL, http.NoBody)
	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}

	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("expected no redirect location, got %q", got)
	}

	body := logs.String()
	if !strings.Contains(body, "image proxy upstream non-2xx") {
		t.Fatalf("expected debug log for non-2xx upstream response, got %q", body)
	}

	if !strings.Contains(body, "status=403") {
		t.Fatalf("expected status in log entry, got %q", body)
	}
}

func TestImageProxyFetchFailureDoesNotRedirect(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	app.imageProxyLookup = func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{testIPAddr(examplePublicIP)}, nil
	}
	app.imageProxyClient = newTestHTTPClient(roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, http.ErrServerClosed
	}))

	targetImageURL := "https://example.com/image.png"
	proxyURL := content.ImageProxyPath + imageProxyURLQuery + url.QueryEscape(targetImageURL)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, proxyURL, http.NoBody)
	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}

	if location := rec.Header().Get("Location"); location != "" {
		t.Fatalf("expected no redirect location, got %q", location)
	}

	if cacheControl := rec.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cacheControl)
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
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, proxyURL, http.NoBody)
	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}

	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("expected no redirect location, got %q", got)
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
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, proxyURL, http.NoBody)
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
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, proxyURL, http.NoBody)
	rec := httptest.NewRecorder()

	app.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}

	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("expected no redirect location, got %q", got)
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
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, proxyURL, http.NoBody)
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
