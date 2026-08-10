package server

import (
	"net"
	"net/http"
	"strings"
)

func (a *App) realIPFromRequest(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}

	peer := net.ParseIP(host)
	if peer == nil || !a.isTrustedProxy(peer) {
		return host
	}

	return a.forwardedClientIP(r.Header.Get("X-Forwarded-For"), host)
}

func (a *App) forwardedClientIP(forwarded, fallback string) string {
	parts := strings.Split(forwarded, ",")
	for index := len(parts) - 1; index >= 0; index-- {
		candidate := net.ParseIP(strings.TrimSpace(parts[index]))
		if candidate == nil {
			return fallback
		}

		if !a.isTrustedProxy(candidate) {
			return candidate.String()
		}
	}

	return fallback
}

func (a *App) isTrustedProxy(ip net.IP) bool {
	for _, network := range a.authTrustedProxies {
		if network.Contains(ip) {
			return true
		}
	}

	return false
}

func requestRealIP(r *http.Request) string {
	raw := r.Context().Value(authRealIPContextKey)
	if raw == nil {
		return ""
	}

	ip, ok := raw.(string)
	if !ok {
		return ""
	}

	return ip
}

func requestIDFromRequest(r *http.Request) string {
	requestID, ok := r.Context().Value(authRequestIDContextKey).(string)
	if !ok {
		return ""
	}

	return requestID
}
