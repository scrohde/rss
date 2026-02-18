package content

import (
	"context"
	"net"
	"net/url"
	"strings"
)

// LookupIPAddrFunc resolves a host name to one or more IP addresses.
type LookupIPAddrFunc func(context.Context, string) ([]net.IPAddr, error)

// ProxyImageURL rewrites a URL to the local image-proxy endpoint when allowed.
func ProxyImageURL(rawURL string, base *url.URL) (string, bool) {
	parsed, ok := parseProxyURL(rawURL, base)
	if !ok {
		return rawURL, false
	}

	if !hasAllowedProxyScheme(parsed.Scheme) {
		return rawURL, false
	}

	if !IsAllowedProxyURL(parsed) {
		return rawURL, false
	}

	return ImageProxyPath + "?url=" + url.QueryEscape(parsed.String()), true
}

// IsAllowedProxyURL reports whether a URL is safe for image proxying.
func IsAllowedProxyURL(target *url.URL) bool {
	if target == nil {
		return false
	}

	if target.Scheme != "http" && target.Scheme != "https" {
		return false
	}

	if target.User != nil {
		return false
	}

	if target.Hostname() == "" {
		return false
	}

	return !isDisallowedHost(target.Hostname())
}

// IsAllowedResolvedProxyURL checks URL safety and resolved host addresses.
func IsAllowedResolvedProxyURL(ctx context.Context, target *url.URL, lookup LookupIPAddrFunc) bool {
	if !IsAllowedProxyURL(target) {
		return false
	}

	if parsedIP := net.ParseIP(target.Hostname()); parsedIP != nil {
		return !isDisallowedIP(parsedIP)
	}

	if lookup == nil {
		return true
	}

	return hasAllowedResolvedAddrs(ctx, target.Hostname(), lookup)
}

//nolint:revive // Explicit branch checks keep proxy URL validation auditable.
func parseProxyURL(rawURL string, base *url.URL) (*url.URL, bool) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil, false
	}

	if strings.HasPrefix(trimmed, ImageProxyPath+"?") {
		return nil, false
	}

	if strings.HasPrefix(strings.ToLower(trimmed), "data:") {
		return nil, false
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, false
	}

	if parsed.Host == "" {
		if base == nil {
			return nil, false
		}

		parsed = base.ResolveReference(parsed)
	} else if parsed.Scheme == "" && base != nil {
		parsed.Scheme = base.Scheme
	}

	if parsed.Host == "" {
		return nil, false
	}

	return parsed, true
}

func hasAllowedProxyScheme(scheme string) bool {
	return scheme == "http" || scheme == "https"
}

func hasAllowedResolvedAddrs(ctx context.Context, host string, lookup LookupIPAddrFunc) bool {
	addrs, err := lookup(ctx, host)
	if err != nil || len(addrs) < 1 {
		return false
	}

	for _, addr := range addrs {
		if addr.IP == nil || isDisallowedIP(addr.IP) {
			return false
		}
	}

	return true
}

func isDisallowedHost(host string) bool {
	trimmed := strings.TrimSpace(host)
	lower := strings.ToLower(trimmed)
	hostname := strings.TrimSuffix(lower, ".")

	if hostname == "" || hostname == "localhost" {
		return true
	}

	if ip := net.ParseIP(hostname); ip != nil {
		return isDisallowedIP(ip)
	}

	return false
}

func isDisallowedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// Block direct IPs that point to local/internal ranges.
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}
