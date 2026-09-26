//nolint:testpackage,wsl_v5 // Handler tests use package helpers in sequential request/response flows.
package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"rss/internal/store"
)

func requireReadingPreferenceHandlerNoError(t *testing.T, err error, operation string) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", operation, err)
	}
}

func readingPreferenceRequest(method, path string, form url.Values) *http.Request {
	var body io.Reader = http.NoBody
	if form != nil {
		body = strings.NewReader(form.Encode())
	}

	request := httptest.NewRequestWithContext(context.Background(), method, path, body)
	if form != nil {
		request.Header.Set(headerContentType, formURLEncoded)
	}

	return request
}

func requireReadingPreferenceSnippets(t *testing.T, body string, snippets []string) {
	t.Helper()

	for _, snippet := range snippets {
		if !strings.Contains(body, snippet) {
			t.Fatalf("expected response to include %q", snippet)
		}
	}
}

func TestReadingPreferencesSaveWorksWithoutAuthOwnerInLocalDevelopment(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	var owners int
	err := app.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM auth_users").Scan(&owners)
	requireReadingPreferenceHandlerNoError(t, err, "count auth owners before save")

	if owners != 0 {
		t.Fatalf("expected no auth owner in local development, got %d", owners)
	}

	feedID, err := store.UpsertFeed(context.Background(), app.db, "https://example.com/today", "Today")
	requireReadingPreferenceHandlerNoError(t, err, "UpsertFeed")

	countsForm := url.Values{"enabled": {"on"}, "return_to": {"/"}}
	countsRequest := readingPreferenceRequest(http.MethodPost, "/preferences/reading/counts", countsForm)
	countsResponse := httptest.NewRecorder()
	app.Routes().ServeHTTP(countsResponse, countsRequest)

	if countsResponse.Code != http.StatusSeeOther || countsResponse.Header().Get("Location") != "/" {
		t.Fatalf(
			"expected local form redirect, got status %d location %q",
			countsResponse.Code,
			countsResponse.Header().Get("Location"),
		)
	}

	todayForm := url.Values{"feed_id": {strconv.FormatInt(feedID, 10)}, "return_to": {"/"}}
	todayRequest := readingPreferenceRequest(http.MethodPost, "/preferences/reading/today-feeds", todayForm)
	todayResponse := httptest.NewRecorder()
	app.Routes().ServeHTTP(todayResponse, todayRequest)

	if todayResponse.Code != http.StatusSeeOther {
		t.Fatalf("expected Today preference form redirect, got %d", todayResponse.Code)
	}

	prefs, err := store.GetReadingPreferences(context.Background(), app.db)
	requireReadingPreferenceHandlerNoError(t, err, "GetReadingPreferences")
	if !prefs.ShowExactUnreadCounts || !prefs.TodaySetupCompleted || len(prefs.TodayFeedIDs) != 1 ||
		prefs.TodayFeedIDs[0] != feedID {
		t.Fatalf("unexpected saved reading preferences: %+v", prefs)
	}

	err = app.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM auth_users").Scan(&owners)
	requireReadingPreferenceHandlerNoError(t, err, "count auth owners after save")
	if owners != 0 {
		t.Fatalf("reading preference save created an auth owner: %d", owners)
	}
}

func TestReadingPreferenceHTMXSaveReturnsUpdatedState(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	form := url.Values{"enabled": {"true"}}
	request := readingPreferenceRequest(http.MethodPost, "/preferences/reading/counts", form)
	request.Header.Set("Hx-Request", "true")
	response := httptest.NewRecorder()
	app.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected htmx response 200, got %d: %s", response.Code, response.Body.String())
	}

	requireReadingPreferenceSnippets(t, response.Body.String(), []string{
		`id="reading-preferences-state"`,
		`hx-swap-oob="outerHTML"`,
		`data-show-exact-unread-counts="true"`,
		`data-today-setup-completed="false"`,
		`id="feed-list" hx-swap-oob="innerHTML"`,
		`id="reading-preference-status"`,
		"Unread counts are on.",
	})
}

func TestAuthenticatedReadingPreferencesRequireCSRFAndSynchronizeAcrossSessions(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)
	seedAuthCredential(t, app)
	firstSession := issueAuthSession(t, app)
	secondSession := issueAuthSession(t, app)
	feedID, err := store.UpsertFeed(context.Background(), app.db, "https://example.com/shared", "Shared")
	requireReadingPreferenceHandlerNoError(t, err, "UpsertFeed")
	feedIDText := strconv.FormatInt(feedID, 10)

	withoutCSRF := url.Values{"enabled": {"on"}}
	request := readingPreferenceRequest(http.MethodPost, "/preferences/reading/counts", withoutCSRF)
	request.AddCookie(firstSession.cookie)
	response := httptest.NewRecorder()
	app.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected missing CSRF token to be rejected, got %d", response.Code)
	}

	todayForm := url.Values{
		"feed_id":    {feedIDText},
		"return_to":  {"/"},
		"csrf_token": {firstSession.csrfToken},
	}
	todayRequest := readingPreferenceRequest(http.MethodPost, "/preferences/reading/today-feeds", todayForm)
	todayRequest.AddCookie(firstSession.cookie)
	todayResponse := httptest.NewRecorder()
	app.Routes().ServeHTTP(todayResponse, todayRequest)

	if todayResponse.Code != http.StatusSeeOther {
		t.Fatalf(
			"expected authenticated Today save redirect, got %d: %s",
			todayResponse.Code,
			todayResponse.Body.String(),
		)
	}

	countsForm := url.Values{
		"enabled":    {"on"},
		"return_to":  {"/"},
		"csrf_token": {firstSession.csrfToken},
	}
	countsRequest := readingPreferenceRequest(http.MethodPost, "/preferences/reading/counts", countsForm)
	countsRequest.AddCookie(firstSession.cookie)
	countsResponse := httptest.NewRecorder()
	app.Routes().ServeHTTP(countsResponse, countsRequest)

	if countsResponse.Code != http.StatusSeeOther {
		t.Fatalf("expected authenticated count save redirect, got %d", countsResponse.Code)
	}

	requireReadingPreferencesVisibleToSession(t, app, secondSession.cookie, feedIDText)
}

func requireReadingPreferencesVisibleToSession(t *testing.T, app *App, cookie *http.Cookie, feedIDText string) {
	t.Helper()

	indexRequest := readingPreferenceRequest(http.MethodGet, pathIndex, nil)
	indexRequest.AddCookie(cookie)
	indexResponse := httptest.NewRecorder()
	app.Routes().ServeHTTP(indexResponse, indexRequest)
	if indexResponse.Code != http.StatusOK {
		t.Fatalf("expected second session index status 200, got %d", indexResponse.Code)
	}
	requireReadingPreferenceSnippets(t, indexResponse.Body.String(), []string{
		`id="reading-preferences-state"`,
		`data-show-exact-unread-counts="true"`,
		`data-today-setup-completed="true"`,
		`data-today-feed-id="` + feedIDText + `"`,
	})

	partialPath := "/feeds/" + feedIDText + "/items"
	partialRequest := readingPreferenceRequest(http.MethodGet, partialPath, nil)
	partialRequest.Header.Set("Hx-Request", "true")
	partialRequest.AddCookie(cookie)
	partialResponse := httptest.NewRecorder()
	app.Routes().ServeHTTP(partialResponse, partialRequest)
	if partialResponse.Code != http.StatusOK {
		t.Fatalf("expected htmx partial status 200, got %d", partialResponse.Code)
	}
	requireReadingPreferenceSnippets(t, partialResponse.Body.String(), []string{
		`hx-swap-oob="outerHTML"`,
		`data-show-exact-unread-counts="true"`,
		`data-today-feed-id="` + feedIDText + `"`,
	})
}

func TestAuthenticatedReadingPreferenceRouteRejectsMissingSession(t *testing.T) {
	t.Parallel()

	app := newAuthEnabledTestApp(t)
	form := url.Values{"enabled": {"true"}}
	request := readingPreferenceRequest(http.MethodPost, "/preferences/reading/counts", form)
	response := httptest.NewRecorder()
	app.Routes().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated preference save to be rejected, got %d", response.Code)
	}
}
