package content

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"
)

func TestIsAllowedProxyURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "public host", raw: "https://example.com/image.png", want: true},
		{name: "public ipv4", raw: "https://8.8.8.8/image.png", want: true},
		{name: "localhost", raw: "https://localhost/image.png", want: false},
		{name: "loopback ipv4", raw: "https://127.0.0.1/image.png", want: false},
		{name: "private ipv4", raw: "https://10.0.0.2/image.png", want: false},
		{name: "loopback ipv6", raw: "https://[::1]/image.png", want: false},
		{name: "invalid scheme", raw: "ftp://example.com/image.png", want: false},
		{name: "relative", raw: "/image.png", want: false},
		{name: "credentials", raw: "https://user:pass@example.com/image.png", want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := url.Parse(tc.raw)
			if err != nil {
				t.Fatalf("parse url: %v", err)
			}
			if got := IsAllowedProxyURL(parsed); got != tc.want {
				t.Fatalf("IsAllowedProxyURL(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestIsAllowedResolvedProxyURL(t *testing.T) {
	public := func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "example.com" {
			t.Fatalf("unexpected host %q", host)
		}
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	private := func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	failing := func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return nil, errors.New("lookup failed")
	}

	target, err := url.Parse("https://example.com/image.png")
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	if !IsAllowedResolvedProxyURL(context.Background(), target, public) {
		t.Fatalf("expected public resolved address to be allowed")
	}
	if IsAllowedResolvedProxyURL(context.Background(), target, private) {
		t.Fatalf("expected private resolved address to be blocked")
	}
	if IsAllowedResolvedProxyURL(context.Background(), target, failing) {
		t.Fatalf("expected lookup failure to be blocked")
	}
}

func TestProxyImageURL(t *testing.T) {
	base, err := url.Parse("https://example.com/posts/123")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}

	got, ok := ProxyImageURL("images/a.jpg", base)
	if !ok {
		t.Fatalf("expected rewrite to succeed")
	}
	want := "/image-proxy?url=https%3A%2F%2Fexample.com%2Fposts%2Fimages%2Fa.jpg"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	rawWithCredentials := "https://user:pass@example.com/image.png"
	if got, ok := ProxyImageURL(rawWithCredentials, nil); ok {
		t.Fatalf("expected credentials URL to be rejected, got %q", got)
	}
}
