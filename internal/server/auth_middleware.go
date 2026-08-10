package server

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rss/internal/auth"
)

type authContextKey string

const (
	authPrincipalContextKey authContextKey = "authPrincipal"
	authRealIPContextKey    authContextKey = "realIP"
	authRequestIDContextKey authContextKey = "requestID"
	authInvalidSessionKey   authContextKey = "invalidSession"
)

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

func (a *App) withRealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := a.realIPFromRequest(r)
		ctx := context.WithValue(r.Context(), authRealIPContextKey, ip)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (*App) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Keep enough referrer for third-party embeds (e.g. YouTube) that require client identification.
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set(
			"Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; "+
				"style-src-elem 'self'; font-src 'self' data:; img-src 'self' data: blob:; media-src 'none'; "+
				"connect-src 'self'; frame-src https://www.youtube.com https://www.youtube-nocookie.com; "+
				"object-src 'none'; base-uri 'self'; "+
				"frame-ancestors 'none'; form-action 'self'",
		)

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
	result := a.loadPrincipalFromRequest(r)
	if !result.HasPrincipal {
		if result.InvalidSession {
			ctx := context.WithValue(r.Context(), authInvalidSessionKey, true)

			return r.WithContext(ctx)
		}

		return r
	}

	ctx := context.WithValue(r.Context(), authPrincipalContextKey, result.Principal)

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
		http.Redirect(w, r, a.missingAuthRedirect(r), http.StatusSeeOther)
	} else {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}

	return true
}

func (a *App) missingAuthRedirect(r *http.Request) string {
	credentials, err := a.authManager.CredentialCount(r.Context())
	if err == nil && credentials == 0 && !a.setupUnlocked(r) {
		return "/auth/setup"
	}

	if !invalidSessionFromRequest(r) {
		return "/auth/login"
	}

	query := url.Values{
		"next":   {r.URL.RequestURI()},
		"reason": {"session_expired"},
	}

	return "/auth/login?" + query.Encode()
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
