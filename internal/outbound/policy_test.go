//nolint:testpackage // Policy tests intentionally exercise package-internal helpers.
package outbound

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"testing"
)

type netIPResolverFunc func(context.Context, string, string) ([]netip.Addr, error)

func (f netIPResolverFunc) LookupNetIP(
	ctx context.Context,
	network string,
	host string,
) ([]netip.Addr, error) {
	return f(ctx, network, host)
}

func TestValidateURL(t *testing.T) {
	t.Parallel()

	allowed := []string{
		"http://example.com/feed.xml",
		"https://example.com./feed.xml",
		"http://8.8.8.8/",
		"https://[2606:4700:4700::1111]/",
		"http://example.com:80/",
		"https://example.com:443/",
	}
	blocked := []string{
		"ftp://example.com/feed.xml",
		"https://user@example.com/feed.xml",
		"https://localhost/feed.xml",
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://168.63.129.16/",
		"http://192.0.2.1/",
		"http://[::1]/",
		"http://[::ffff:127.0.0.1]/",
		"http://[::ffff:8.8.8.8]/",
		"http://[2001:db8::1]/",
		"http://[fe80::1%25eth0]/",
		"http://example.com:8080/",
		"https://example.com:80/",
		"https://example.com:0443/",
		"https://example..com/",
	}

	runAllowedURLTests(t, allowed)
	runBlockedURLTests(t, blocked)
}

func runAllowedURLTests(t *testing.T, values []string) {
	t.Helper()

	for _, raw := range values {
		t.Run("allows_"+url.PathEscape(raw), func(t *testing.T) {
			t.Parallel()

			err := ValidateURL(mustParseURL(t, raw))
			if err != nil {
				t.Fatalf("ValidateURL(%q): %v", raw, err)
			}
		})
	}
}

func runBlockedURLTests(t *testing.T, values []string) {
	t.Helper()

	for _, raw := range values {
		t.Run("blocks_"+url.PathEscape(raw), func(t *testing.T) {
			t.Parallel()

			err := ValidateURL(mustParseURL(t, raw))
			if err == nil {
				t.Fatalf("ValidateURL(%q) unexpectedly succeeded", raw)
			}
		})
	}
}

func TestValidateResolvedURLRejectsAnyNonPublicAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       string
		addresses []net.IPAddr
	}{
		{name: "private ipv4", raw: "https://private.example/", addresses: ipAddrs("10.0.0.1")},
		{name: "loopback ipv6", raw: "https://loopback.example/", addresses: ipAddrs("::1")},
		{
			name:      "mixed answers",
			raw:       "https://mixed.example/",
			addresses: ipAddrs("93.184.216.34", "127.0.0.1"),
		},
		{name: "mapped ipv6", raw: "https://mapped.example/", addresses: ipAddrs("::ffff:127.0.0.1")},
		{name: "alternate ipv4", raw: "http://127.1/", addresses: ipAddrs("127.0.0.1")},
		{name: "trailing dot", raw: "https://private.example./", addresses: ipAddrs("192.168.1.1")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolver := LookupIPAddrFunc(func(_ context.Context, _ string) ([]net.IPAddr, error) {
				return tc.addresses, nil
			})

			err := ValidateResolvedURL(context.Background(), mustParseURL(t, tc.raw), resolver)
			if !errors.Is(err, ErrDestinationNotPublic) {
				t.Fatalf("expected non-public destination error, got %v", err)
			}
		})
	}
}

func TestValidateResolvedURLAllowsPublicIPv4AndIPv6(t *testing.T) {
	t.Parallel()

	var lookedUpHost string

	resolver := LookupIPAddrFunc(func(_ context.Context, host string) ([]net.IPAddr, error) {
		lookedUpHost = host

		return ipAddrs("93.184.216.34", "2606:4700:4700::1111"), nil
	})

	err := ValidateResolvedURL(context.Background(), mustParseURL(t, "https://Example.COM./feed"), resolver)
	if err != nil {
		t.Fatalf("validate public destination: %v", err)
	}

	if lookedUpHost != "example.com" {
		t.Fatalf("expected canonical lookup host, got %q", lookedUpHost)
	}
}

func TestValidateResolvedURLRejectsIPv4MappedIPv6Answer(t *testing.T) {
	t.Parallel()

	resolver := netIPResolverFunc(
		func(_ context.Context, _, _ string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("::ffff:8.8.8.8")}, nil
		},
	)

	err := ValidateResolvedURL(context.Background(), mustParseURL(t, "https://mapped.example/"), resolver)
	if !errors.Is(err, ErrDestinationNotPublic) {
		t.Fatalf("expected mapped address to be blocked, got %v", err)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL %q: %v", raw, err)
	}

	return parsed
}

func ipAddrs(values ...string) []net.IPAddr {
	result := make([]net.IPAddr, 0, len(values))
	for _, value := range values {
		var addr net.IPAddr

		addr.IP = net.ParseIP(value)
		result = append(result, addr)
	}

	return result
}
