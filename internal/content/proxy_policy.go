package content

import (
	"context"
	"net/url"
	"strings"

	"rss/internal/outbound"
)

// LookupIPAddrFunc resolves a host name to one or more IP addresses.
type LookupIPAddrFunc = outbound.LookupIPAddrFunc

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
	return outbound.ValidateURL(target) == nil
}

// IsAllowedResolvedProxyURL checks URL safety and resolved host addresses.
func IsAllowedResolvedProxyURL(ctx context.Context, target *url.URL, lookup LookupIPAddrFunc) bool {
	if lookup == nil {
		return false
	}

	return outbound.ValidateResolvedURL(ctx, target, lookup) == nil
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
