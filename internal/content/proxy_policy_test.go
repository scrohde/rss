package content

import (
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
}
