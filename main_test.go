package main

import (
	"log/slog"
	"testing"
	"time"
)

func TestResolveAuthConfigDefaultsToSecureAuthSettings(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "true")
	t.Setenv("AUTH_RP_ID", "example.com")
	t.Setenv("AUTH_RP_ORIGIN", "https://example.com")
	t.Setenv("AUTH_SETUP_TOKEN", "setup-token")
	t.Setenv("AUTH_RP_NAME", "")
	t.Setenv("AUTH_SESSION_TTL", "")
	t.Setenv("AUTH_CHALLENGE_TTL", "")
	t.Setenv("AUTH_COOKIE_SECURE", "")

	cfg, err := resolveAuthConfig()
	if err != nil {
		t.Fatalf("resolveAuthConfig: %v", err)
	}

	if cfg.SessionTTL != 24*time.Hour {
		t.Fatalf("expected default session ttl 24h, got %s", cfg.SessionTTL)
	}

	if cfg.ChallengeTTL != 5*time.Minute {
		t.Fatalf("expected default challenge ttl 5m, got %s", cfg.ChallengeTTL)
	}

	if !cfg.CookieSecure {
		t.Fatal("expected auth cookie secure by default when auth is enabled")
	}
}

func TestResolveAuthConfigAllowsExplicitInsecureCookieOverride(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "true")
	t.Setenv("AUTH_RP_ID", "example.com")
	t.Setenv("AUTH_RP_ORIGIN", "https://example.com")
	t.Setenv("AUTH_SETUP_TOKEN", "setup-token")
	t.Setenv("AUTH_COOKIE_SECURE", "false")

	cfg, err := resolveAuthConfig()
	if err != nil {
		t.Fatalf("resolveAuthConfig: %v", err)
	}

	if cfg.CookieSecure {
		t.Fatal("expected explicit AUTH_COOKIE_SECURE=false override")
	}
}

func TestResolveLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")

	if got := resolveLogLevel(); got != slog.LevelInfo {
		t.Fatalf("expected default info level, got %s", got)
	}

	testCases := []struct {
		name  string
		value string
		want  slog.Level
	}{
		{name: "debug", value: "debug", want: slog.LevelDebug},
		{name: "warn", value: "warn", want: slog.LevelWarn},
		{name: "warning alias", value: "warning", want: slog.LevelWarn},
		{name: "error", value: "ERROR", want: slog.LevelError},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tc.value)

			if got := resolveLogLevel(); got != tc.want {
				t.Fatalf("LOG_LEVEL=%q: expected %s, got %s", tc.value, tc.want, got)
			}
		})
	}
}

func TestResolveLogLevelInvalidValueFallsBackToInfo(t *testing.T) {
	t.Setenv("LOG_LEVEL", "verbose")

	if got := resolveLogLevel(); got != slog.LevelInfo {
		t.Fatalf("expected fallback info level for invalid LOG_LEVEL, got %s", got)
	}
}
