package server

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"rss/internal/auth"
	"rss/internal/store"
)

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

func (a *App) handleAuthTheme(w http.ResponseWriter, r *http.Request) {
	_, ok := currentPrincipal(r)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)

		return
	}

	if !parseFormOrBadRequest(w, r, "invalid form") {
		return
	}

	theme := strings.TrimSpace(r.PostForm.Get("theme"))

	err := store.UpdateAuthOwnerAppearanceTheme(r.Context(), a.db, theme)
	if errors.Is(err, store.ErrInvalidAppearanceTheme) {
		http.Error(w, "invalid appearance theme", http.StatusBadRequest)

		return
	}

	if err != nil {
		http.Error(w, "failed to update appearance theme", http.StatusInternalServerError)

		return
	}

	redirectTarget := authThemeRedirectTarget(r.PostForm.Get("return_to"))
	if redirectTarget == "" {
		redirectTarget = "/auth/security?message=" + url.QueryEscape("Appearance updated.")
	}

	http.Redirect(w, r, redirectTarget, http.StatusSeeOther)
}

func authThemeRedirectTarget(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return ""
	}

	target, err := url.Parse(raw)
	if err != nil || target.IsAbs() || target.Host != "" || !strings.HasPrefix(target.Path, "/") {
		return ""
	}

	return target.RequestURI()
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
	_ auth.SessionPrincipal,
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

	page, err := a.newFullPageData(r)
	if err != nil {
		http.Error(w, "failed to load security state", http.StatusInternalServerError)

		return
	}

	data := authSecurityPageData{
		fullPageData:       page,
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
	if !parseFormOrBadRequest(w, r, "invalid request") {
		return
	}

	code := strings.TrimSpace(r.PostForm.Get("recovery_code"))

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
