package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"rss/internal/auth"
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
)

var (
	errAuthSetupTokenRequired = errors.New("AUTH_SETUP_TOKEN is required when auth is enabled")
	errMissingChallengeOrCred = errors.New("missing challenge or credential")
)

type authContextKey string

const (
	authPrincipalContextKey authContextKey = "authPrincipal"
	authRealIPContextKey    authContextKey = "realIP"
	authRequestIDContextKey authContextKey = "requestID"
)

// AuthConfig controls optional passkey authentication features.
type AuthConfig struct {
	RPID         string
	RPOrigin     string
	RPName       string
	SetupToken   string
	CookieName   string
	SessionTTL   time.Duration
	ChallengeTTL time.Duration
	Enabled      bool
	CookieSecure bool
}

type authRateLimiter struct {
	entries map[string]*authRateLimitEntry
	mu      sync.Mutex
}

type authRateLimitEntry struct {
	lastSeen    time.Time
	lockedUntil time.Time
	tokens      float64
	failedCount int
}

type passkeyVerifyRequest struct {
	//nolint:tagliatelle // Frontend contract uses snake_case payload keys.
	ChallengeID string          `json:"challenge_id"`
	Credential  json.RawMessage `json:"credential"`
}

type passkeyOptionsResponse struct {
	Options any `json:"options"`
	//nolint:tagliatelle // Frontend contract uses snake_case payload keys.
	ChallengeID string `json:"challenge_id"`
}

func newAuthRateLimiter() *authRateLimiter {
	return &authRateLimiter{
		entries: make(map[string]*authRateLimitEntry),
		mu:      sync.Mutex{},
	}
}

func (l *authRateLimiter) allow(ip string, now time.Time) bool {
	if strings.TrimSpace(ip) == "" {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.ensureEntry(ip, now)
	if !entry.lockedUntil.IsZero() && entry.lockedUntil.After(now) {
		return false
	}

	if entry.tokens < 1 {
		return false
	}

	entry.tokens--
	entry.lastSeen = now

	return true
}

func (l *authRateLimiter) ensureEntry(ip string, now time.Time) *authRateLimitEntry {
	entry, ok := l.entries[ip]
	if !ok {
		entry = &authRateLimitEntry{
			lastSeen:    now,
			lockedUntil: time.Time{},
			tokens:      authRateMaxTokens,
			failedCount: 0,
		}
		l.entries[ip] = entry

		return entry
	}

	elapsed := now.Sub(entry.lastSeen).Seconds()
	if elapsed > 0 {
		entry.tokens += elapsed * authRateRefillPerSec
		if entry.tokens > authRateMaxTokens {
			entry.tokens = authRateMaxTokens
		}
	}

	entry.lastSeen = now

	return entry
}

func (l *authRateLimiter) recordFailure(ip string) {
	if strings.TrimSpace(ip) == "" {
		return
	}

	now := time.Now().UTC()

	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.ensureEntry(ip, now)

	entry.failedCount++
	if entry.failedCount >= authFailureThreshold {
		entry.failedCount = 0
		entry.lockedUntil = now.Add(authLockDuration)
	}
}

func (l *authRateLimiter) recordSuccess(ip string) {
	if strings.TrimSpace(ip) == "" {
		return
	}

	now := time.Now().UTC()

	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.ensureEntry(ip, now)
	entry.failedCount = 0
	entry.lockedUntil = time.Time{}
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

	a.authEnabled = true
	a.authManager = manager
	a.authCookieName = cookieName
	a.authCookieSecure = cfg.CookieSecure
	a.authSetupToken = setupToken
	a.authSetupCookieName = defaultSetupCookieName
	a.authSetupSignerKey = hash[:]
	a.authRateLimiter = newAuthRateLimiter()

	return nil
}

func (*App) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, err := randomToken(requestIDTokenBytes)
		if err != nil {
			requestID = strconv.FormatInt(time.Now().UnixNano(), 10)
		}

		ctx := context.WithValue(r.Context(), authRequestIDContextKey, requestID)
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (*App) withRealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := realIPFromRequest(r)
		ctx := context.WithValue(r.Context(), authRealIPContextKey, ip)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (*App) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set(
			"Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; "+
				"img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; "+
				"frame-ancestors 'none'; form-action 'self'",
		)

		next.ServeHTTP(w, r)
	})
}

func (a *App) withAuthRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.authEnabled || !strings.HasPrefix(r.URL.Path, "/auth/") {
			next.ServeHTTP(w, r)

			return
		}

		now := time.Now().UTC()
		ip := requestRealIP(r)

		if !a.authRateLimiter.allow(ip, now) {
			http.Error(w, "too many auth requests", http.StatusTooManyRequests)

			return
		}

		next.ServeHTTP(w, r)
	})
}

func (a *App) withAuthSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.authEnabled {
			next.ServeHTTP(w, r)

			return
		}

		r = a.requestWithPrincipal(r)
		if a.redirectIfAlreadyAuthenticated(w, r) {
			return
		}

		if a.rejectIfAuthRequiredAndMissing(w, r) {
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (a *App) requestWithPrincipal(r *http.Request) *http.Request {
	principal, hasPrincipal := a.loadPrincipalFromRequest(r)
	if !hasPrincipal {
		return r
	}

	ctx := context.WithValue(r.Context(), authPrincipalContextKey, principal)

	return r.WithContext(ctx)
}

func (*App) redirectIfAlreadyAuthenticated(w http.ResponseWriter, r *http.Request) bool {
	if !shouldRedirectAuthenticatedFromPath(r.URL.Path) {
		return false
	}

	if _, ok := currentPrincipal(r); !ok {
		return false
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)

	return true
}

func (a *App) rejectIfAuthRequiredAndMissing(w http.ResponseWriter, r *http.Request) bool {
	if !pathRequiresAuth(r.URL.Path) {
		return false
	}

	if _, ok := currentPrincipal(r); ok {
		return false
	}

	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		redirectPath := "/auth/login"

		credentials, err := a.authManager.CredentialCount(r.Context())
		if err == nil && credentials == 0 && !a.setupUnlocked(r) {
			redirectPath = "/auth/setup"
		}

		http.Redirect(w, r, redirectPath, http.StatusSeeOther)
	} else {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}

	return true
}

func (a *App) withCSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, shouldValidate := a.csrfPrincipalForRequest(r)
		if !shouldValidate {
			next.ServeHTTP(w, r)

			return
		}

		valid, err := csrfTokenMatches(r, principal.CSRFToken)
		if err != nil {
			http.Error(w, "invalid csrf payload", http.StatusBadRequest)

			return
		}

		if !valid {
			http.Error(w, "invalid csrf token", http.StatusForbidden)

			return
		}

		next.ServeHTTP(w, r)
	})
}

func (a *App) csrfPrincipalForRequest(r *http.Request) (auth.SessionPrincipal, bool) {
	if !a.authEnabled || isSafeMethod(r.Method) {
		return emptySessionPrincipal(), false
	}

	principal, ok := currentPrincipal(r)
	if !ok {
		return emptySessionPrincipal(), false
	}

	return principal, true
}

func csrfTokenMatches(r *http.Request, expected string) (bool, error) {
	token := strings.TrimSpace(r.Header.Get("X-Csrf-Token"))
	if token == "" {
		err := r.ParseForm()
		if err != nil {
			return false, fmt.Errorf("parse csrf form: %w", err)
		}

		token = strings.TrimSpace(r.FormValue("csrf_token"))
	}

	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1, nil
}

func pathRequiresAuth(path string) bool {
	if path == "/healthz" || strings.HasPrefix(path, "/static/") {
		return false
	}

	switch path {
	case "/auth/login",
		"/auth/setup",
		"/auth/setup/unlock",
		"/auth/recovery",
		"/auth/recovery/use",
		"/auth/webauthn/login/options",
		"/auth/webauthn/login/verify",
		"/auth/webauthn/register/options",
		"/auth/webauthn/register/verify":
		return false
	default:
		return true
	}
}

func shouldRedirectAuthenticatedFromPath(path string) bool {
	switch path {
	case "/auth/login", "/auth/setup", "/auth/recovery":
		return true
	default:
		return false
	}
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func currentPrincipal(r *http.Request) (auth.SessionPrincipal, bool) {
	raw := r.Context().Value(authPrincipalContextKey)
	if raw == nil {
		return emptySessionPrincipal(), false
	}

	principal, ok := raw.(auth.SessionPrincipal)
	if !ok {
		return emptySessionPrincipal(), false
	}

	return principal, true
}

func (*App) csrfTokenForRequest(r *http.Request) string {
	principal, ok := currentPrincipal(r)
	if !ok {
		return ""
	}

	return principal.CSRFToken
}

func realIPFromRequest(r *http.Request) string {
	forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}

	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}

	return strings.TrimSpace(r.RemoteAddr)
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

func (a *App) recordAuthFailure(r *http.Request) {
	if a.authRateLimiter == nil {
		return
	}

	a.authRateLimiter.recordFailure(requestRealIP(r))
}

func (a *App) recordAuthSuccess(r *http.Request) {
	if a.authRateLimiter == nil {
		return
	}

	a.authRateLimiter.recordSuccess(requestRealIP(r))
}

func (a *App) loadPrincipalFromRequest(r *http.Request) (auth.SessionPrincipal, bool) {
	cookie, err := r.Cookie(a.authCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return emptySessionPrincipal(), false
	}

	principal, err := a.authManager.ValidateSessionCookie(r.Context(), cookie.Value)
	if err != nil {
		return emptySessionPrincipal(), false
	}

	return principal, true
}

func emptySessionPrincipal() auth.SessionPrincipal {
	var principal auth.SessionPrincipal

	return principal
}

func (a *App) setAuthSessionCookie(w http.ResponseWriter, value string) {
	cookie := new(http.Cookie)
	cookie.Name = a.authCookieName
	cookie.Value = value
	cookie.Path = "/"
	cookie.HttpOnly = true
	cookie.Secure = a.authCookieSecure
	cookie.SameSite = http.SameSiteStrictMode

	http.SetCookie(w, cookie)
}

func (a *App) clearAuthSessionCookie(w http.ResponseWriter) {
	cookie := new(http.Cookie)
	cookie.Name = a.authCookieName
	cookie.Value = ""
	cookie.Path = "/"
	cookie.MaxAge = -1
	cookie.Expires = time.Unix(1, 0)
	cookie.HttpOnly = true
	cookie.Secure = a.authCookieSecure
	cookie.SameSite = http.SameSiteStrictMode

	http.SetCookie(w, cookie)
}

func (a *App) setSetupUnlockCookie(w http.ResponseWriter) error {
	nonce, err := randomToken(setupNonceTokenBytes)
	if err != nil {
		return err
	}

	expiresAt := time.Now().UTC().Add(setupUnlockTTL).Unix()
	payload := strconv.FormatInt(expiresAt, 10) + ":" + nonce
	signature := signSetupPayload(a.authSetupSignerKey, payload)
	value := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(signature)

	cookie := new(http.Cookie)
	cookie.Name = a.authSetupCookieName
	cookie.Value = value
	cookie.Path = "/"
	cookie.HttpOnly = true
	cookie.Secure = a.authCookieSecure
	cookie.SameSite = http.SameSiteStrictMode
	cookie.MaxAge = int(setupUnlockTTL.Seconds())
	cookie.Expires = time.Now().Add(setupUnlockTTL)

	http.SetCookie(w, cookie)

	return nil
}

func (a *App) clearSetupUnlockCookie(w http.ResponseWriter) {
	cookie := new(http.Cookie)
	cookie.Name = a.authSetupCookieName
	cookie.Value = ""
	cookie.Path = "/"
	cookie.HttpOnly = true
	cookie.Secure = a.authCookieSecure
	cookie.SameSite = http.SameSiteStrictMode
	cookie.MaxAge = -1
	cookie.Expires = time.Unix(1, 0)

	http.SetCookie(w, cookie)
}

func (a *App) setupUnlocked(r *http.Request) bool {
	cookie, err := r.Cookie(a.authSetupCookieName)
	if err != nil {
		return false
	}

	encodedPayload, encodedSignature, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return false
	}

	payload, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return false
	}

	signature, err := base64.RawURLEncoding.DecodeString(encodedSignature)
	if err != nil {
		return false
	}

	expected := signSetupPayload(a.authSetupSignerKey, string(payload))
	if subtle.ConstantTimeCompare(signature, expected) != 1 {
		return false
	}

	expRaw, _, ok := strings.Cut(string(payload), ":")
	if !ok {
		return false
	}

	expiresAt, err := strconv.ParseInt(expRaw, 10, 64)
	if err != nil {
		return false
	}

	return time.Now().UTC().Before(time.Unix(expiresAt, 0).UTC())
}

func signSetupPayload(key []byte, payload string) []byte {
	mac := hmac.New(sha256.New, key)

	_, err := mac.Write([]byte(payload))
	if err != nil {
		return nil
	}

	return mac.Sum(nil)
}

func (*App) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	_, err := w.Write([]byte("ok"))
	if err != nil {
		slog.Warn("write healthz response failed")
	}
}

func (a *App) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	message := strings.TrimSpace(r.URL.Query().Get("message"))
	a.renderTemplate(w, "auth_login", authLoginPageData{Message: message})
}

func (a *App) handleAuthLoginOptions(w http.ResponseWriter, r *http.Request) {
	result, err := a.authManager.BeginDiscoverableLogin(r.Context())
	if err != nil {
		a.recordAuthFailure(r)
		http.Error(w, authFailureMessage, http.StatusUnauthorized)

		return
	}

	writeJSON(w, passkeyOptionsResponse{ChallengeID: result.ChallengeID, Options: result.Assertion})
}

func (a *App) handleAuthLoginVerify(w http.ResponseWriter, r *http.Request) {
	request, body, err := decodePasskeyVerifyRequest(r)
	if err != nil {
		slog.Warn("decode passkey login verify request failed")
		a.recordAuthFailure(r)
		http.Error(w, authFailureMessage, http.StatusUnauthorized)

		return
	}

	authRequest := requestWithJSONBody(r, body)

	issue, err := a.authManager.FinishDiscoverableLogin(r.Context(), request.ChallengeID, authRequest)
	if err != nil {
		slog.Warn("passkey login verify failed")
		a.recordAuthFailure(r)
		http.Error(w, authFailureMessage, http.StatusUnauthorized)

		return
	}

	a.recordAuthSuccess(r)
	a.setAuthSessionCookie(w, issue.CookieValue)

	writeJSON(w, map[string]any{"ok": true, "redirect": "/"})
}

func (a *App) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	credentials, err := a.authManager.CredentialCount(r.Context())
	if err != nil {
		http.Error(w, "failed to load setup state", http.StatusInternalServerError)

		return
	}

	message := strings.TrimSpace(r.URL.Query().Get("message"))
	if message == "" && r.URL.Query().Get("recovery") == "1" {
		message = "Recovery accepted. Register a new passkey now."
	}

	data := authSetupPageData{
		Message:               message,
		RegistrationURL:       "/auth/webauthn/register/options",
		SetupUnlocked:         a.setupUnlocked(r),
		HasCredentials:        credentials > 0,
		SetupTokenSet:         strings.TrimSpace(a.authSetupToken) != "",
		AutoStartRegistration: false,
	}
	if data.SetupUnlocked && !data.HasCredentials && r.URL.Query().Get("autoregister") == "1" {
		data.AutoStartRegistration = true
	}

	a.renderTemplate(w, "auth_setup", data)
}

func (a *App) handleAuthSetupUnlock(w http.ResponseWriter, r *http.Request) {
	credentials, err := a.authManager.CredentialCount(r.Context())
	if err != nil {
		http.Error(w, "failed to load setup state", http.StatusInternalServerError)

		return
	}

	if credentials > 0 {
		http.Error(w, "setup is closed", http.StatusForbidden)

		return
	}

	if strings.TrimSpace(a.authSetupToken) == "" {
		http.Error(w, "setup token is not configured", http.StatusInternalServerError)

		return
	}

	err = r.ParseForm()
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)

		return
	}

	provided := strings.TrimSpace(r.FormValue("setup_token"))
	if subtle.ConstantTimeCompare([]byte(provided), []byte(a.authSetupToken)) != 1 {
		a.recordAuthFailure(r)
		http.Error(w, authFailureMessage, http.StatusUnauthorized)

		return
	}

	err = a.setSetupUnlockCookie(w)
	if err != nil {
		http.Error(w, "failed to set setup session", http.StatusInternalServerError)

		return
	}

	a.recordAuthSuccess(r)
	http.Redirect(w, r, "/auth/setup?autoregister=1", http.StatusSeeOther)
}

func (a *App) handleAuthRegisterOptions(w http.ResponseWriter, r *http.Request) {
	userID, ok := a.registrationUserID(r)
	if !ok {
		http.Error(w, "setup is required", http.StatusUnauthorized)

		return
	}

	result, err := a.authManager.BeginRegistration(r.Context(), userID)
	if err != nil {
		http.Error(w, "failed to start registration", http.StatusBadRequest)

		return
	}

	writeJSON(w, passkeyOptionsResponse{ChallengeID: result.ChallengeID, Options: result.Creation})
}

func (a *App) handleAuthRegisterVerify(w http.ResponseWriter, r *http.Request) {
	_, ok := a.registrationUserID(r)
	if !ok {
		http.Error(w, "setup is required", http.StatusUnauthorized)

		return
	}

	request, body, err := decodePasskeyVerifyRequest(r)
	if err != nil {
		http.Error(w, "invalid registration payload", http.StatusBadRequest)

		return
	}

	authRequest := requestWithJSONBody(r, body)

	userID, err := a.authManager.FinishRegistration(r.Context(), request.ChallengeID, authRequest)
	if err != nil {
		http.Error(w, "registration failed", http.StatusBadRequest)

		return
	}

	issue, err := a.issueOrRotateSession(r, userID)
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)

		return
	}

	a.setAuthSessionCookie(w, issue.CookieValue)
	a.clearSetupUnlockCookie(w)
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) issueOrRotateSession(r *http.Request, userID int64) (auth.SessionIssue, error) {
	cookie, err := r.Cookie(a.authCookieName)
	if err != nil {
		issue, createErr := a.authManager.CreateSessionForUser(r.Context(), userID)
		if createErr != nil {
			return auth.SessionIssue{}, fmt.Errorf("create auth session: %w", createErr)
		}

		return issue, nil
	}

	issue, rotateErr := a.authManager.RotateSession(r.Context(), cookie.Value, userID)
	if rotateErr != nil {
		return auth.SessionIssue{}, fmt.Errorf("rotate auth session: %w", rotateErr)
	}

	return issue, nil
}

func (a *App) registrationUserID(r *http.Request) (int64, bool) {
	principal, ok := currentPrincipal(r)
	if ok {
		return principal.UserID, true
	}

	credentials, err := a.authManager.CredentialCount(r.Context())
	if err != nil || credentials > 0 {
		return 0, false
	}

	if !a.setupUnlocked(r) {
		return 0, false
	}

	owner, err := a.authManager.EnsureOwner(r.Context())
	if err != nil {
		return 0, false
	}

	return owner.ID, true
}

func (a *App) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(a.authCookieName)
	if err == nil && strings.TrimSpace(cookie.Value) != "" {
		revokeErr := a.authManager.RevokeSessionCookie(r.Context(), cookie.Value)
		if revokeErr != nil {
			slog.Warn("revoke auth session failed", "err", revokeErr)
		}
	}

	a.clearAuthSessionCookie(w)
	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
}

func (a *App) handleAuthSecurity(w http.ResponseWriter, r *http.Request) {
	principal, ok := currentPrincipal(r)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)

		return
	}

	message := strings.TrimSpace(r.URL.Query().Get("message"))
	a.renderSecurityPage(w, r, principal, message, "")
}

func (a *App) renderSecurityPage(
	w http.ResponseWriter,
	r *http.Request,
	principal auth.SessionPrincipal,
	message string,
	recoveryCode string,
) {
	credentials, err := a.authManager.CredentialCount(r.Context())
	if err != nil {
		http.Error(w, "failed to load security state", http.StatusInternalServerError)

		return
	}

	hasRecoveryCode, err := a.authManager.HasRecoveryCode(r.Context())
	if err != nil {
		http.Error(w, "failed to load recovery state", http.StatusInternalServerError)

		return
	}

	data := authSecurityPageData{
		CSRFToken:          principal.CSRFToken,
		PasskeyCount:       credentials,
		HasRecoveryCode:    hasRecoveryCode,
		RecoveryCode:       recoveryCode,
		RegistrationURL:    "/auth/webauthn/register/options",
		RecoveryEnabledURL: "/auth/recovery/generate",
		Message:            message,
	}

	a.renderTemplate(w, "auth_security", data)
}

func (a *App) handleAuthRecoveryGenerate(w http.ResponseWriter, r *http.Request) {
	principal, ok := currentPrincipal(r)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)

		return
	}

	code, err := a.authManager.GenerateRecoveryCode(r.Context())
	if err != nil {
		http.Error(w, "failed to generate recovery code", http.StatusInternalServerError)

		return
	}

	a.renderSecurityPage(
		w,
		r,
		principal,
		"Recovery code generated. Store it offline now; this is the only time it is shown.",
		code,
	)
}

func (a *App) handleAuthRecovery(w http.ResponseWriter, r *http.Request) {
	message := strings.TrimSpace(r.URL.Query().Get("message"))
	a.renderTemplate(w, "auth_recovery", authRecoveryPageData{Message: message})
}

func (a *App) handleAuthRecoveryUse(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)

		return
	}

	code := strings.TrimSpace(r.FormValue("recovery_code"))

	consumed, err := a.authManager.ConsumeRecoveryCode(r.Context(), code)
	if err != nil {
		http.Error(w, "failed to apply recovery code", http.StatusInternalServerError)

		return
	}

	if !consumed {
		a.recordAuthFailure(r)
		http.Error(w, authFailureMessage, http.StatusUnauthorized)

		return
	}

	a.recordAuthSuccess(r)
	a.clearAuthSessionCookie(w)

	setErr := a.setSetupUnlockCookie(w)
	if setErr != nil {
		http.Error(w, "failed to initialize recovery setup session", http.StatusInternalServerError)

		return
	}

	http.Redirect(w, r, "/auth/setup?recovery=1", http.StatusSeeOther)
}

func decodePasskeyVerifyRequest(r *http.Request) (passkeyVerifyRequest, []byte, error) {
	var request passkeyVerifyRequest

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return passkeyVerifyRequest{}, nil, fmt.Errorf("read passkey verify body: %w", err)
	}

	err = json.Unmarshal(body, &request)
	if err != nil {
		return passkeyVerifyRequest{}, nil, fmt.Errorf("decode passkey verify body: %w", err)
	}

	if strings.TrimSpace(request.ChallengeID) == "" || len(request.Credential) == 0 {
		return passkeyVerifyRequest{}, nil, errMissingChallengeOrCred
	}

	return request, request.Credential, nil
}

func requestWithJSONBody(r *http.Request, body []byte) *http.Request {
	clone := r.Clone(r.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.Header = r.Header.Clone()
	clone.Header.Set("Content-Type", "application/json")

	return clone
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")

	encoder := json.NewEncoder(w)

	err := encoder.Encode(value)
	if err != nil {
		http.Error(w, "failed to write json", http.StatusInternalServerError)

		return
	}
}

func randomToken(size int) (string, error) {
	buf := make([]byte, size)

	_, err := rand.Read(buf)
	if err != nil {
		return "", fmt.Errorf("read random token bytes: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}
