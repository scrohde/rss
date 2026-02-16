package content

import (
	"net"
	"net/url"
	"strings"
)

func ProxyImageURL(rawURL string, base *url.URL) (string, bool) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return rawURL, false
	}
	if strings.HasPrefix(trimmed, ImageProxyPath+"?") {
		return rawURL, false
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "data:") {
		return rawURL, false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return rawURL, false
	}
	if parsed.Host == "" {
		if base == nil {
			return rawURL, false
		}
		parsed = base.ResolveReference(parsed)
	} else if parsed.Scheme == "" && base != nil {
		parsed.Scheme = base.Scheme
	}
	if parsed.Host == "" {
		return rawURL, false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return rawURL, false
	}
	if !IsAllowedProxyURL(parsed) {
		return rawURL, false
	}
	return ImageProxyPath + "?url=" + url.QueryEscape(parsed.String()), true
}

func IsAllowedProxyURL(target *url.URL) bool {
	if target == nil {
		return false
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return false
	}
	if target.Hostname() == "" {
		return false
	}
	return !isDisallowedHost(target.Hostname())
}

func isDisallowedHost(host string) bool {
	hostname := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if hostname == "" || hostname == "localhost" {
		return true
	}
	if ip := net.ParseIP(hostname); ip != nil {
		// Block direct IPs that point to local/internal ranges.
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return true
		}
	}
	return false
}
