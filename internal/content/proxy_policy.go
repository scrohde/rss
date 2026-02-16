package content

import (
	"context"
	"net"
	"net/url"
	"strings"
)

type LookupIPAddrFunc func(context.Context, string) ([]net.IPAddr, error)

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
	if target.User != nil {
		return false
	}
	if target.Hostname() == "" {
		return false
	}
	return !isDisallowedHost(target.Hostname())
}

func IsAllowedResolvedProxyURL(ctx context.Context, target *url.URL, lookup LookupIPAddrFunc) bool {
	if !IsAllowedProxyURL(target) {
		return false
	}
	if ip := net.ParseIP(target.Hostname()); ip != nil {
		return !isDisallowedIP(ip)
	}
	if lookup == nil {
		return true
	}
	addrs, err := lookup(ctx, target.Hostname())
	if err != nil || len(addrs) == 0 {
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
	hostname := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
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
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}
