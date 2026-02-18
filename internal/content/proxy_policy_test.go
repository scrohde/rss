//nolint:testpackage // Content tests exercise package-internal helpers directly.
package content

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"
)

var errLookupFailed = errors.New("lookup failed")

const exampleImageURL = "https://example.com/image.png"

func TestIsAllowedProxyURL(t *testing.T) {
	t.Parallel()

	allowedCases := []struct {
		name string
		raw  string
	}{
		{name: "public host", raw: exampleImageURL},
		{name: "public ipv4", raw: "https://8.8.8.8/image.png"},
	}

	blockedCases := []struct {
		name string
		raw  string
	}{
		{name: "localhost", raw: "https://localhost/image.png"},
		{
			name: "loopback ipv4",
			raw:  "https://127.0.0.1/image.png",
		},
		{name: "private ipv4", raw: "https://10.0.0.2/image.png"},
		{name: "loopback ipv6", raw: "https://[::1]/image.png"},
		{
			name: "invalid scheme",
			raw:  "ftp://example.com/image.png",
		},
		{name: "relative", raw: "/image.png"},
		{
			name: "credentials",
			raw:  "https://user@example.com/image.png",
		},
	}

	for _, tc := range allowedCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertProxyURLAllowed(t, tc.raw)
		})
	}

	for _, tc := range blockedCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertProxyURLBlocked(t, tc.raw)
		})
	}
}

func TestIsAllowedResolvedProxyURL(t *testing.T) {
	t.Parallel()

	public := func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "example.com" {
			t.Fatalf("unexpected host %q", host)
		}

		return []net.IPAddr{ipAddr("93.184.216.34")}, nil
	}
	private := func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{ipAddr("127.0.0.1")}, nil
	}
	failing := func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return nil, errLookupFailed
	}

	target, err := url.Parse(exampleImageURL)
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}

	if !IsAllowedResolvedProxyURL(context.Background(), target, public) {
		t.Fatal("expected public resolved address to be allowed")
	}

	if IsAllowedResolvedProxyURL(context.Background(), target, private) {
		t.Fatal("expected private resolved address to be blocked")
	}

	if IsAllowedResolvedProxyURL(context.Background(), target, failing) {
		t.Fatal("expected lookup failure to be blocked")
	}
}

func TestProxyImageURL(t *testing.T) {
	t.Parallel()

	base, err := url.Parse("https://example.com/posts/123")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}

	got, ok := ProxyImageURL("images/a.jpg", base)
	if !ok {
		t.Fatal("expected rewrite to succeed")
	}

	want := "/image-proxy?url=https%3A%2F%2Fexample.com%2Fposts%2Fimages" +
		"%2Fa.jpg"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	withUser, err := url.Parse(exampleImageURL)
	if err != nil {
		t.Fatalf("parse credentials url: %v", err)
	}

	withUser.User = url.User("user")

	rawWithCredentials := withUser.String()
	if rewritten, rewrittenOK := ProxyImageURL(
		rawWithCredentials,
		nil,
	); rewrittenOK {
		t.Fatalf("expected credentials URL to be rejected, got %q", rewritten)
	}
}

func ipAddr(raw string) net.IPAddr {
	var addr net.IPAddr

	addr.IP = net.ParseIP(raw)

	return addr
}

func assertProxyURLAllowed(t *testing.T, raw string) {
	t.Helper()

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	got := IsAllowedProxyURL(parsed)
	if !got {
		t.Fatalf(
			"IsAllowedProxyURL(%q) = %v, want true",
			raw,
			got,
		)
	}
}

func assertProxyURLBlocked(t *testing.T, raw string) {
	t.Helper()

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	got := IsAllowedProxyURL(parsed)
	if got {
		t.Fatalf(
			"IsAllowedProxyURL(%q) = %v, want false",
			raw,
			got,
		)
	}
}
