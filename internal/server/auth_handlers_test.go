//nolint:testpackage // Handler integration tests intentionally exercise unexported helpers.
package server

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"rss/internal/store"
)

func newAuthEnabledTestApp(t *testing.T) *App {
	t.Helper()

	app := newTestApp(t)

	err := app.SetAuthConfig(&AuthConfig{
		Enabled:      true,
		RPID:         "example.com",
		RPOrigin:     "https://example.com",
		RPName:       "Pulse RSS",
		SetupToken:   "setup-token",
		CookieName:   "",
		SessionTTL:   24 * time.Hour,
		ChallengeTTL: 5 * time.Minute,
		CookieSecure: false,
	})
	if err != nil {
		t.Fatalf("SetAuthConfig: %v", err)
	}

	return app
}

func issueAuthCookie(t *testing.T, app *App) *http.Cookie {
	t.Helper()

	owner, err := app.authManager.EnsureOwner(context.Background())
	if err != nil {
		t.Fatalf("EnsureOwner: %v", err)
	}

	issue, err := app.authManager.CreateSessionForUser(context.Background(), owner.ID)
	if err != nil {
		t.Fatalf("CreateSessionForUser: %v", err)
	}

	cookie := new(http.Cookie)
	cookie.Name = app.authCookieName
	cookie.Value = issue.CookieValue

	return cookie
}

func seedAuthCredential(t *testing.T, app *App) {
	t.Helper()

	owner, err := app.authManager.EnsureOwner(context.Background())
	if err != nil {
		t.Fatalf("EnsureOwner: %v", err)
	}

	err = store.UpsertAuthCredential(context.Background(), app.db, &store.AuthCredentialRecord{
		CreatedAt:      time.Now().UTC(),
		LastUsedAt:     sql.NullTime{Time: time.Time{}, Valid: false},
		Transports:     "internal",
		CredentialID:   []byte("cred-1"),
		PublicKey:      []byte("pk"),
		AAGUID:         []byte("aaguid"),
		ID:             0,
		UserID:         owner.ID,
		SignCount:      1,
		BackupEligible: sql.NullBool{Bool: false, Valid: false},
		BackupState:    sql.NullBool{Bool: false, Valid: false},
	})
	if err != nil {
		t.Fatalf("UpsertAuthCredential: %v", err)
	}
}

func TestAuthRedirectsUnauthenticatedRequestsToSetupBeforeInitialCode(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)

	req := httptest.NewRequest(http.MethodGet, pathIndex, http.NoBody)
	rr := httptest.NewRecorder()

	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect status, got %d", rr.Code)
	}

	if rr.Header().Get("Location") != "/auth/setup" {
		t.Fatalf("expected /auth/setup redirect, got %q", rr.Header().Get("Location"))
	}
}

func TestAuthRedirectsUnauthenticatedRequestsToLoginAfterInitialCode(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)

	unlockResp := httptest.NewRecorder()

	err := app.setSetupUnlockCookie(unlockResp)
	if err != nil {
		t.Fatalf("setSetupUnlockCookie: %v", err)
	}

	cookies := unlockResp.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected setup unlock cookie")
	}

	req := httptest.NewRequest(http.MethodGet, pathIndex, http.NoBody)
	req.AddCookie(cookies[0])

	rr := httptest.NewRecorder()

	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect status, got %d", rr.Code)
	}

	if rr.Header().Get("Location") != "/auth/login" {
		t.Fatalf("expected /auth/login redirect, got %q", rr.Header().Get("Location"))
	}
}

func TestAuthSecurityHeadersOnLoginPage(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)

	req := httptest.NewRequest(http.MethodGet, "/auth/login", http.NoBody)
	rr := httptest.NewRecorder()

	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected login page status 200, got %d", rr.Code)
	}

	if rr.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("expected Content-Security-Policy header")
	}

	if rr.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("expected X-Frame-Options DENY, got %q", rr.Header().Get("X-Frame-Options"))
	}

	body := rr.Body.String()
	if !strings.Contains(body, `data-passkey-login="true"`) {
		t.Fatal("expected passkey login button")
	}

	if !strings.Contains(body, `data-auth-message`) {
		t.Fatal("expected auth message placeholder")
	}
}

func TestAuthCSRFRequiredForUnsafeRequests(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)
	cookie := issueAuthCookie(t, app)

	form := url.Values{"url": {exampleRSSURL}}
	req := httptest.NewRequest(http.MethodPost, "/feeds", strings.NewReader(form.Encode()))
	req.Header.Set(headerContentType, formURLEncoded)
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected csrf forbidden status, got %d", rr.Code)
	}
}

func TestAuthLoginVerifyRejectsInvalidChallenge(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)

	payload := `{"challenge_id":"missing","credential":` +
		`{"id":"x","rawId":"eA","type":"public-key","response":` +
		`{"clientDataJSON":"e30","authenticatorData":"e30","signature":"e30"}}}`
	req := httptest.NewRequest(http.MethodPost, "/auth/webauthn/login/verify", strings.NewReader(payload))
	req.Header.Set(headerContentType, "application/json")

	rr := httptest.NewRecorder()

	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized invalid challenge, got %d", rr.Code)
	}
}

func TestAuthRegisterOptionsRequiresSetupOrSession(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)

	req := httptest.NewRequest(http.MethodPost, "/auth/webauthn/register/options", strings.NewReader(`{}`))
	req.Header.Set(headerContentType, "application/json")

	rr := httptest.NewRecorder()

	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized register options without setup/session, got %d", rr.Code)
	}
}

func TestAuthSetupUnlockRequiresToken(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)

	wrong := url.Values{"setup_token": {"wrong-token"}}
	wrongReq := httptest.NewRequest(http.MethodPost, "/auth/setup/unlock", strings.NewReader(wrong.Encode()))
	wrongReq.Header.Set(headerContentType, formURLEncoded)

	wrongResp := httptest.NewRecorder()
	app.Routes().ServeHTTP(wrongResp, wrongReq)

	if wrongResp.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized for wrong setup token, got %d", wrongResp.Code)
	}

	valid := url.Values{"setup_token": {"setup-token"}}
	validReq := httptest.NewRequest(http.MethodPost, "/auth/setup/unlock", strings.NewReader(valid.Encode()))
	validReq.Header.Set(headerContentType, formURLEncoded)

	validResp := httptest.NewRecorder()
	app.Routes().ServeHTTP(validResp, validReq)

	if validResp.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect for valid setup token, got %d", validResp.Code)
	}

	if validResp.Header().Get("Location") != "/auth/setup?autoregister=1" {
		t.Fatalf("expected setup redirect with auto register flag, got %q", validResp.Header().Get("Location"))
	}

	if !strings.Contains(validResp.Header().Get(headerSetCookie), defaultSetupCookieName+"=") {
		t.Fatalf("expected setup cookie, got %q", validResp.Header().Get(headerSetCookie))
	}
}

func TestAuthSetupPageLockedShowsOnlyCodeEntry(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)

	req := httptest.NewRequest(http.MethodGet, "/auth/setup", http.NoBody)
	rr := httptest.NewRecorder()

	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected setup page status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, `<h2>Enter setup code</h2>`) {
		t.Fatal("expected setup code heading")
	}

	if !strings.Contains(body, `action="/auth/setup/unlock"`) {
		t.Fatal("expected setup unlock form action")
	}

	if strings.Contains(body, `data-passkey-register="true"`) {
		t.Fatal("did not expect passkey registration button while setup is locked")
	}
}

func TestAuthSetupPageAutoStartFlagAfterUnlock(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)

	unlockResp := httptest.NewRecorder()

	err := app.setSetupUnlockCookie(unlockResp)
	if err != nil {
		t.Fatalf("setSetupUnlockCookie: %v", err)
	}

	cookies := unlockResp.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected setup unlock cookie")
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/setup?autoregister=1", http.NoBody)
	req.AddCookie(cookies[0])

	rr := httptest.NewRecorder()
	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected setup page status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, `data-passkey-register="true"`) {
		t.Fatal("expected passkey registration button when setup is unlocked")
	}

	if !strings.Contains(body, `data-passkey-autostart="true"`) {
		t.Fatal("expected passkey auto-start flag after unlock redirect")
	}
}

func TestAuthSetupUnlockBlockedAfterCredentialExists(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)
	seedAuthCredential(t, app)

	valid := url.Values{"setup_token": {"setup-token"}}
	req := httptest.NewRequest(http.MethodPost, "/auth/setup/unlock", strings.NewReader(valid.Encode()))
	req.Header.Set(headerContentType, formURLEncoded)

	rr := httptest.NewRecorder()

	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected setup lockout after credential exists, got %d", rr.Code)
	}
}

func TestAuthSessionExpiryRedirectsToLogin(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	err := app.SetAuthConfig(&AuthConfig{
		Enabled:      true,
		RPID:         "example.com",
		RPOrigin:     "https://example.com",
		RPName:       "Pulse RSS",
		SetupToken:   "setup-token",
		CookieName:   "",
		SessionTTL:   40 * time.Millisecond,
		ChallengeTTL: 5 * time.Minute,
		CookieSecure: false,
	})
	if err != nil {
		t.Fatalf("SetAuthConfig: %v", err)
	}

	seedAuthCredential(t, app)
	cookie := issueAuthCookie(t, app)

	time.Sleep(80 * time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, pathIndex, http.NoBody)
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()

	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected expired session redirect, got %d", rr.Code)
	}

	if rr.Header().Get("Location") != "/auth/login" {
		t.Fatalf("expected redirect to login, got %q", rr.Header().Get("Location"))
	}
}
