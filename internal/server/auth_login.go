package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

type passkeyVerifyRequest struct {
	ChallengeID string          `json:"challenge_id"` //nolint:tagliatelle // Frontend contract uses snake_case.
	Next        string          `json:"next"`
	Credential  json.RawMessage `json:"credential"`
}

type passkeyOptionsRequest struct {
	Mediation string `json:"mediation"`
}

type passkeyOptionsResponse struct {
	Options   any    `json:"options"`
	Mediation string `json:"mediation"`
	//nolint:tagliatelle // Frontend contract uses snake_case payload keys.
	ChallengeID string `json:"challenge_id"`
}

type passkeyVerifyResponse struct {
	Redirect string `json:"redirect"`
	OK       bool   `json:"ok"`
}

func (*App) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	_, err := w.Write([]byte("ok"))
	if err != nil {
		slog.Warn("write healthz response failed")
	}
}

func (a *App) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	credentials, err := a.authManager.CredentialCount(r.Context())
	if err != nil {
		http.Error(w, "failed to load login state", http.StatusInternalServerError)

		return
	}

	message := strings.TrimSpace(r.URL.Query().Get("message"))
	next := safeAuthRedirect(r.URL.Query().Get("next"))
	a.renderTemplate(w, "auth_login", authLoginPageData{
		Message: message,
		Next:    next,
		SessionExpired: r.URL.Query().Get("reason") == "session_expired" &&
			strings.TrimSpace(r.URL.Query().Get("next")) != "",
		ShowSetupLink: credentials == 0,
	})
}

func (a *App) handleAuthLoginOptions(w http.ResponseWriter, r *http.Request) {
	request := passkeyOptionsRequest{Mediation: ""}

	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPasskeyJSONBytes)).Decode(&request)
	if err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "invalid passkey options request", http.StatusBadRequest)

		return
	}

	mediation := "required"
	if request.Mediation == "conditional" {
		mediation = request.Mediation
	}

	result, err := a.authManager.BeginDiscoverableLogin(r.Context(), a.realIPFromRequest(r))
	if err != nil {
		a.recordAuthFailure(r)
		http.Error(w, authFailureMessage, http.StatusUnauthorized)

		return
	}

	writeJSON(w, passkeyOptionsResponse{
		ChallengeID: result.ChallengeID,
		Options:     result.Assertion,
		Mediation:   mediation,
	})
}

func (a *App) handleAuthLoginVerify(w http.ResponseWriter, r *http.Request) {
	request, body, err := decodePasskeyVerifyRequest(w, r)
	if err != nil {
		if isRequestBodyTooLarge(err) {
			http.Error(w, "passkey payload too large", http.StatusRequestEntityTooLarge)

			return
		}

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

	writeJSON(w, newPasskeyVerifyResponse(request.Next))
}

func newPasskeyVerifyResponse(next string) passkeyVerifyResponse {
	return passkeyVerifyResponse{Redirect: safeAuthRedirect(next), OK: true}
}

func safeAuthRedirect(raw string) string {
	raw = strings.TrimSpace(raw)
	if invalidAuthRedirectRaw(raw) {
		return "/"
	}

	parsed, err := url.ParseRequestURI(raw)
	if err != nil || invalidAuthRedirectURL(parsed) {
		return "/"
	}

	return parsed.RequestURI()
}

func invalidAuthRedirectRaw(raw string) bool {
	return raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") ||
		strings.ContainsAny(raw, "#\r\n")
}

func invalidAuthRedirectURL(parsed *url.URL) bool {
	return parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" || parsed.Path == "/auth" ||
		strings.HasPrefix(parsed.Path, "/auth/")
}
