package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rss/internal/auth"
)

type authPrincipalLoad struct {
	Principal      auth.SessionPrincipal
	HasPrincipal   bool
	InvalidSession bool
}

func (a *App) loadPrincipalFromRequest(r *http.Request) authPrincipalLoad {
	cookie, err := r.Cookie(a.authCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return authPrincipalLoad{Principal: emptySessionPrincipal(), HasPrincipal: false, InvalidSession: false}
	}

	principal, err := a.authManager.ValidateSessionCookie(r.Context(), cookie.Value)
	if err != nil {
		return authPrincipalLoad{Principal: emptySessionPrincipal(), HasPrincipal: false, InvalidSession: true}
	}

	return authPrincipalLoad{Principal: principal, HasPrincipal: true, InvalidSession: false}
}

func invalidSessionFromRequest(r *http.Request) bool {
	raw := r.Context().Value(authInvalidSessionKey)
	invalid, ok := raw.(bool)

	return ok && invalid
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

	expiresAtSec := time.Now().UTC().Add(setupUnlockTTL).Unix()
	payload := strconv.FormatInt(expiresAtSec, 10) + ":" + nonce
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

	expiresAtSec, err := strconv.ParseInt(expRaw, 10, 64)
	if err != nil {
		return false
	}

	return time.Now().UTC().Before(time.Unix(expiresAtSec, 0).UTC())
}

func signSetupPayload(key []byte, payload string) []byte {
	mac := hmac.New(sha256.New, key)

	_, err := mac.Write([]byte(payload))
	if err != nil {
		return nil
	}

	return mac.Sum(nil)
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
