package server

import (
	"errors"
	"time"
)

const (
	defaultAuthCookieName  = "pulse_rss_session"
	defaultSetupCookieName = "pulse_rss_setup"
	setupUnlockTTL         = 10 * time.Minute
	setupNonceTokenBytes   = 18
	authFailureMessage     = "authentication failed"
	requestIDTokenBytes    = 16
	authRateRefillPerSec   = 5.0
	authRateMaxTokens      = 20.0
	authFailureThreshold   = 5
	authLockDuration       = 10 * time.Minute
	authRateEntryTTL       = 30 * time.Minute
	authRateCleanupPeriod  = time.Minute
	authRateMaxEntries     = 4096
)

var (
	errAuthSetupTokenRequired = errors.New("AUTH_SETUP_TOKEN is required when auth is enabled")
	errMissingChallengeOrCred = errors.New("missing challenge or credential")
)
