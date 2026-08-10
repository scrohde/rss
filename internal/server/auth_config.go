package server

import (
	"crypto/sha256"
	"fmt"
	"net"
	"strings"
	"time"

	"rss/internal/auth"
)

// AuthConfig controls optional passkey authentication features.
type AuthConfig struct {
	RPID              string
	RPOrigin          string
	RPName            string
	SetupToken        string
	CookieName        string
	TrustedProxyCIDRs []string
	SessionTTL        time.Duration
	ChallengeTTL      time.Duration
	Enabled           bool
	CookieSecure      bool
}

// SetAuthConfig enables passkey authentication middleware and routes when configured.
func (a *App) SetAuthConfig(cfg *AuthConfig) error {
	if cfg == nil || !cfg.Enabled {
		a.authEnabled = false

		return nil
	}

	cookieName := strings.TrimSpace(cfg.CookieName)
	if cookieName == "" {
		cookieName = defaultAuthCookieName
	}

	setupToken := strings.TrimSpace(cfg.SetupToken)
	if setupToken == "" {
		return errAuthSetupTokenRequired
	}

	manager, err := auth.NewManager(a.db, &auth.Config{
		RPID:         strings.TrimSpace(cfg.RPID),
		RPOrigin:     strings.TrimSpace(cfg.RPOrigin),
		RPName:       strings.TrimSpace(cfg.RPName),
		SessionTTL:   cfg.SessionTTL,
		ChallengeTTL: cfg.ChallengeTTL,
		CookieName:   cookieName,
		CookieSecure: cfg.CookieSecure,
	})
	if err != nil {
		return fmt.Errorf("initialize auth manager: %w", err)
	}

	hash := sha256.Sum256([]byte(setupToken))

	trustedProxies, err := parseTrustedProxyCIDRs(cfg.TrustedProxyCIDRs)
	if err != nil {
		return err
	}

	a.authEnabled = true
	a.authManager = manager
	a.authCookieName = cookieName
	a.authCookieSecure = cfg.CookieSecure
	a.authSetupToken = setupToken
	a.authSetupCookieName = defaultSetupCookieName
	a.authSetupSignerKey = hash[:]
	a.authRateLimiter = newAuthRateLimiter()
	a.authTrustedProxies = trustedProxies

	return nil
}

func parseTrustedProxyCIDRs(rawCIDRs []string) ([]*net.IPNet, error) {
	trustedProxies := make([]*net.IPNet, 0, len(rawCIDRs))
	for _, raw := range rawCIDRs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("parse trusted proxy CIDR %q: %w", raw, err)
		}

		trustedProxies = append(trustedProxies, network)
	}

	return trustedProxies, nil
}
