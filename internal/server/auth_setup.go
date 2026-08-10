package server

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

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

	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)

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

	result, err := a.authManager.BeginRegistration(r.Context(), userID, a.realIPFromRequest(r))
	if err != nil {
		http.Error(w, "failed to start registration", http.StatusBadRequest)

		return
	}

	writeJSON(w, passkeyOptionsResponse{ChallengeID: result.ChallengeID, Options: result.Creation, Mediation: ""})
}

func (a *App) handleAuthRegisterVerify(w http.ResponseWriter, r *http.Request) {
	_, ok := a.registrationUserID(r)
	if !ok {
		http.Error(w, "setup is required", http.StatusUnauthorized)

		return
	}

	request, body, err := decodePasskeyVerifyRequest(w, r)
	if err != nil {
		if isRequestBodyTooLarge(err) {
			http.Error(w, "passkey payload too large", http.StatusRequestEntityTooLarge)

			return
		}

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
