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

type authSessionFixture struct {
	cookie    *http.Cookie
	csrfToken string
}

func issueAuthCookie(t *testing.T, app *App) *http.Cookie {
	t.Helper()

	session := issueAuthSession(t, app)

	return session.cookie
}

func issueAuthSession(t *testing.T, app *App) authSessionFixture {
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

	return authSessionFixture{
		cookie:    cookie,
		csrfToken: issue.CSRFToken,
	}
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, pathIndex, http.NoBody)
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, pathIndex, http.NoBody)
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/login", http.NoBody)
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

	if strings.Contains(body, `data-passkey-autostart="true"`) {
		t.Fatal("did not expect passkey auto-start without a registered credential")
	}

	if !strings.Contains(body, `data-auth-message`) {
		t.Fatal("expected auth message placeholder")
	}

	if !strings.Contains(body, `<a href="/auth/setup">Initial setup</a>`) {
		t.Fatal("expected setup link before any passkey is registered")
	}
}

func TestAuthLoginPageAutoStartsWhenCredentialExists(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)
	seedAuthCredential(t, app)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/login", http.NoBody)
	rr := httptest.NewRecorder()

	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected login page status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, `data-passkey-autostart="true"`) {
		t.Fatal("expected passkey auto-start when a credential exists")
	}

	if !strings.Contains(body, `data-auth-login-pending`) {
		t.Fatal("expected pending login section")
	}

	if strings.Contains(body, `<a href="/auth/setup">Initial setup</a>`) {
		t.Fatal("did not expect setup link after initial setup is complete")
	}
}

func TestAuthenticatedIndexShowsLogoutInMenu(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)
	seedAuthCredential(t, app)
	cookie := issueAuthCookie(t, app)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, pathIndex, http.NoBody)
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected authenticated index status 200, got %d", rr.Code)
	}

	body := rr.Body.String()

	requiredSnippets := []string{
		`action="/auth/logout"`,
		`href="/auth/security"`,
		`action="/auth/theme"`,
		`data-theme-form="true"`,
		`data-theme-status="true"`,
		`name="return_to" value="/"`,
		`name="csrf_token"`,
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(body, snippet) {
			t.Fatalf("expected menu to include %q", snippet)
		}
	}

	if strings.Contains(body, "Save appearance") {
		t.Fatal("did not expect explicit save appearance button in menu")
	}
}

func TestAuthenticatedIndexAppliesStoredThemeToRootMarkup(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)
	seedAuthCredential(t, app)

	err := store.UpdateAuthOwnerAppearanceTheme(context.Background(), app.db, store.AuthAppearanceThemeDark)
	if err != nil {
		t.Fatalf("UpdateAuthOwnerAppearanceTheme: %v", err)
	}

	cookie := issueAuthCookie(t, app)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, pathIndex, http.NoBody)
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected authenticated index status 200, got %d", rr.Code)
	}

	if !strings.Contains(rr.Body.String(), `<html lang="en" data-theme="dark">`) {
		t.Fatal("expected dark theme marker on authenticated index page")
	}

	if !strings.Contains(rr.Body.String(), `value="dark" checked`) {
		t.Fatal("expected saved dark appearance option to be selected in menu")
	}
}

func TestAuthenticatedIndexAppliesLightThemeToRootMarkup(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)
	seedAuthCredential(t, app)

	err := store.UpdateAuthOwnerAppearanceTheme(context.Background(), app.db, store.AuthAppearanceThemeLight)
	if err != nil {
		t.Fatalf("UpdateAuthOwnerAppearanceTheme: %v", err)
	}

	cookie := issueAuthCookie(t, app)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, pathIndex, http.NoBody)
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected authenticated index status 200, got %d", rr.Code)
	}

	if !strings.Contains(rr.Body.String(), `<html lang="en" data-theme="light">`) {
		t.Fatal("expected light theme marker on authenticated index page")
	}
}

func TestAuthLoginPageDoesNotEmitStoredThemeMarker(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)

	_, err := app.authManager.EnsureOwner(context.Background())
	if err != nil {
		t.Fatalf("EnsureOwner: %v", err)
	}

	err = store.UpdateAuthOwnerAppearanceTheme(context.Background(), app.db, store.AuthAppearanceThemeDark)
	if err != nil {
		t.Fatalf("UpdateAuthOwnerAppearanceTheme: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/login", http.NoBody)
	rr := httptest.NewRecorder()

	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected login page status 200, got %d", rr.Code)
	}

	if strings.Contains(rr.Body.String(), `data-theme=`) {
		t.Fatal("did not expect stored theme marker on login page")
	}
}

func TestAuthDisabledIndexDoesNotExposeAppearanceControlsOrThemeMarker(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, pathIndex, http.NoBody)
	rr := httptest.NewRecorder()

	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected index status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if strings.Contains(body, `data-theme=`) {
		t.Fatal("did not expect theme marker when auth is disabled")
	}

	if strings.Contains(body, `href="/auth/security"`) {
		t.Fatal("did not expect security settings link when auth is disabled")
	}

	if strings.Contains(body, `action="/auth/theme"`) {
		t.Fatal("did not expect appearance form when auth is disabled")
	}
}

func TestAuthSecurityPageNoLongerShowsAppearanceControls(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)
	seedAuthCredential(t, app)

	err := store.UpdateAuthOwnerAppearanceTheme(context.Background(), app.db, store.AuthAppearanceThemeDark)
	if err != nil {
		t.Fatalf("UpdateAuthOwnerAppearanceTheme: %v", err)
	}

	cookie := issueAuthCookie(t, app)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/security", http.NoBody)
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected security page status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, `<html lang="en" data-theme="dark">`) {
		t.Fatal("expected dark theme marker on security page")
	}

	if strings.Contains(body, `action="/auth/theme"`) {
		t.Fatal("did not expect appearance form action on security page")
	}
}

func TestAuthCSRFRequiredForUnsafeRequests(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)
	cookie := issueAuthCookie(t, app)

	form := url.Values{"url": {exampleRSSURL}}
	ctx := context.Background()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/feeds", strings.NewReader(form.Encode()))
	req.Header.Set(headerContentType, formURLEncoded)
	req.AddCookie(cookie)

	rr := httptest.NewRecorder()
	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected csrf forbidden status, got %d", rr.Code)
	}
}

func TestAuthThemeUpdatePersistsAndRedirects(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)
	seedAuthCredential(t, app)
	session := issueAuthSession(t, app)

	form := url.Values{
		"return_to":  {"/"},
		"theme":      {store.AuthAppearanceThemeDark},
		"csrf_token": {session.csrfToken},
	}
	ctx := context.Background()
	body := strings.NewReader(form.Encode())
	req := httptest.NewRequestWithContext(
		ctx, http.MethodPost, "/auth/theme", body,
	)
	req.Header.Set(headerContentType, formURLEncoded)
	req.AddCookie(session.cookie)

	rr := httptest.NewRecorder()
	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect status, got %d", rr.Code)
	}

	if rr.Header().Get("Location") != "/" {
		t.Fatalf("expected return redirect, got %q", rr.Header().Get("Location"))
	}

	theme, err := store.GetAuthOwnerAppearanceTheme(context.Background(), app.db)
	if err != nil {
		t.Fatalf("GetAuthOwnerAppearanceTheme: %v", err)
	}

	if theme != store.AuthAppearanceThemeDark {
		t.Fatalf("unexpected stored theme: got %q want %q", theme, store.AuthAppearanceThemeDark)
	}
}

func TestAuthThemeUpdateRejectsExternalReturnTarget(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)
	seedAuthCredential(t, app)
	session := issueAuthSession(t, app)

	form := url.Values{
		"return_to":  {"https://example.com/phish"},
		"theme":      {store.AuthAppearanceThemeDark},
		"csrf_token": {session.csrfToken},
	}
	ctx := context.Background()
	body := strings.NewReader(form.Encode())
	req := httptest.NewRequestWithContext(
		ctx, http.MethodPost, "/auth/theme", body,
	)
	req.Header.Set(headerContentType, formURLEncoded)
	req.AddCookie(session.cookie)

	rr := httptest.NewRecorder()
	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect status, got %d", rr.Code)
	}

	if rr.Header().Get("Location") != "/auth/security?message=Appearance+updated." {
		t.Fatalf("expected security fallback redirect, got %q", rr.Header().Get("Location"))
	}
}

func TestAuthThemeUpdateRejectsInvalidValue(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)
	seedAuthCredential(t, app)
	session := issueAuthSession(t, app)

	form := url.Values{
		"theme":      {"violet"},
		"csrf_token": {session.csrfToken},
	}
	ctx := context.Background()
	body := strings.NewReader(form.Encode())
	req := httptest.NewRequestWithContext(
		ctx, http.MethodPost, "/auth/theme", body,
	)
	req.Header.Set(headerContentType, formURLEncoded)
	req.AddCookie(session.cookie)

	rr := httptest.NewRecorder()
	app.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request status, got %d", rr.Code)
	}

	theme, err := store.GetAuthOwnerAppearanceTheme(context.Background(), app.db)
	if err != nil {
		t.Fatalf("GetAuthOwnerAppearanceTheme: %v", err)
	}

	if theme != store.AuthAppearanceThemeSystem {
		t.Fatalf(
			"unexpected stored theme after invalid submission: got %q want %q",
			theme,
			store.AuthAppearanceThemeSystem,
		)
	}
}

func TestAuthLoginVerifyRejectsInvalidChallenge(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)

	payload := `{"challenge_id":"missing","credential":` +
		`{"id":"x","rawId":"eA","type":"public-key","response":` +
		`{"clientDataJSON":"e30","authenticatorData":"e30","signature":"e30"}}}`
	ctx := context.Background()
	req := httptest.NewRequestWithContext(
		ctx, http.MethodPost, "/auth/webauthn/login/verify",
		strings.NewReader(payload),
	)
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

	ctx := context.Background()
	req := httptest.NewRequestWithContext(
		ctx, http.MethodPost, "/auth/webauthn/register/options",
		strings.NewReader(`{}`),
	)
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

	ctx := context.Background()

	wrong := url.Values{"setup_token": {"wrong-token"}}
	wrongBody := strings.NewReader(wrong.Encode())
	wrongReq := httptest.NewRequestWithContext(
		ctx, http.MethodPost, "/auth/setup/unlock", wrongBody,
	)
	wrongReq.Header.Set(headerContentType, formURLEncoded)

	wrongResp := httptest.NewRecorder()
	app.Routes().ServeHTTP(wrongResp, wrongReq)

	if wrongResp.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized for wrong setup token, got %d", wrongResp.Code)
	}

	valid := url.Values{"setup_token": {"setup-token"}}
	validBody := strings.NewReader(valid.Encode())
	validReq := httptest.NewRequestWithContext(
		ctx, http.MethodPost, "/auth/setup/unlock", validBody,
	)
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/setup", http.NoBody)
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

	ctx := context.Background()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/auth/setup?autoregister=1", http.NoBody)
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

	ctx := context.Background()
	valid := url.Values{"setup_token": {"setup-token"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/auth/setup/unlock", strings.NewReader(valid.Encode()))
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, pathIndex, http.NoBody)
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
