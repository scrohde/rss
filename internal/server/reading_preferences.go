//nolint:wsl_v5 // Sequential form validation stays together within each handler.
package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"rss/internal/store"
)

type readingPreferencesCarrier interface {
	setReadingPreferences(preferences store.ReadingPreferences)
}

func emptyReadingPreferences() store.ReadingPreferences {
	return store.ReadingPreferences{
		TodayFeedIDs:          make([]int64, 0),
		ShowExactUnreadCounts: false,
		TodaySetupCompleted:   false,
	}
}

func (data *fullPageData) setReadingPreferences(preferences store.ReadingPreferences) {
	data.ReadingPreferences = preferences
}

func (data *pageData) setReadingPreferences(preferences store.ReadingPreferences) {
	data.ReadingPreferences = preferences
}

func (data *subscribeResponseData) setReadingPreferences(preferences store.ReadingPreferences) {
	data.ReadingPreferences = preferences
}

func (data *pollResponseData) setReadingPreferences(preferences store.ReadingPreferences) {
	data.ReadingPreferences = preferences
}

func (data *itemListResponseData) setReadingPreferences(preferences store.ReadingPreferences) {
	data.ReadingPreferences = preferences
}

func (data *toggleReadResponseData) setReadingPreferences(preferences store.ReadingPreferences) {
	data.ReadingPreferences = preferences
}

func (data *pulseStatusResponseData) setReadingPreferences(preferences store.ReadingPreferences) {
	data.ReadingPreferences = preferences
}

func (data *mobileStreamResponseData) setReadingPreferences(preferences store.ReadingPreferences) {
	data.ReadingPreferences = preferences
}

func (data *mobileReaderResponseData) setReadingPreferences(preferences store.ReadingPreferences) {
	data.ReadingPreferences = preferences
}

func (data *mobileStreamSectionsResponseData) setReadingPreferences(preferences store.ReadingPreferences) {
	data.ReadingPreferences = preferences
}

func (data *mobileFeedSectionResponseData) setReadingPreferences(preferences store.ReadingPreferences) {
	data.ReadingPreferences = preferences
}

func (data *readingPreferencesResponseData) setReadingPreferences(preferences store.ReadingPreferences) {
	data.ReadingPreferences = preferences
}

func (a *App) renderTemplateWithReadingPreferences(
	w http.ResponseWriter,
	r *http.Request,
	name string,
	data readingPreferencesCarrier,
) {
	preferences, err := store.GetReadingPreferences(r.Context(), a.db)
	if err != nil {
		http.Error(w, "failed to load reading preferences", http.StatusInternalServerError)

		return
	}

	data.setReadingPreferences(preferences)
	a.renderTemplate(w, name, data)
}

func (a *App) handleSetShowExactUnreadCounts(w http.ResponseWriter, r *http.Request) {
	if !a.requireReadingPreferenceAuthentication(w, r) {
		return
	}
	if !parseFormOrBadRequest(w, r, "invalid form") {
		return
	}

	parsedPreference := parseReadingPreferenceBool(r.PostForm["enabled"])
	if !parsedPreference.valid {
		http.Error(w, "invalid exact unread-count preference", http.StatusBadRequest)

		return
	}

	enabled := parsedPreference.enabled
	err := store.SetShowExactUnreadCounts(r.Context(), a.db, enabled)
	if err != nil {
		http.Error(w, "failed to update reading preferences", http.StatusInternalServerError)

		return
	}

	a.finishReadingPreferenceSave(w, r)
}

func (a *App) handleSetTodayFeedIDs(w http.ResponseWriter, r *http.Request) {
	if !a.requireReadingPreferenceAuthentication(w, r) {
		return
	}
	if !parseFormOrBadRequest(w, r, "invalid form") {
		return
	}

	feedIDs, ok := parseTodayFeedIDs(r.PostForm["feed_id"])
	if !ok {
		http.Error(w, "invalid Today feed selection", http.StatusBadRequest)

		return
	}

	err := store.SetTodayFeedIDs(r.Context(), a.db, feedIDs)
	if errors.Is(err, store.ErrInvalidTodayFeedID) {
		http.Error(w, "invalid Today feed selection", http.StatusBadRequest)

		return
	}
	if err != nil {
		http.Error(w, "failed to update reading preferences", http.StatusInternalServerError)

		return
	}

	a.finishReadingPreferenceSave(w, r)
}

func (a *App) requireReadingPreferenceAuthentication(w http.ResponseWriter, r *http.Request) bool {
	if !a.authEnabled {
		return true
	}
	if _, ok := currentPrincipal(r); ok {
		return true
	}

	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)

	return false
}

type parsedReadingPreferenceBool struct {
	enabled bool
	valid   bool
}

func parseReadingPreferenceBool(values []string) parsedReadingPreferenceBool {
	if len(values) == 0 {
		return parsedReadingPreferenceBool{enabled: false, valid: true}
	}

	raw := strings.ToLower(strings.TrimSpace(values[len(values)-1]))
	switch raw {
	case "1", "true", "on":
		return parsedReadingPreferenceBool{enabled: true, valid: true}
	case "0", "false", "off":
		return parsedReadingPreferenceBool{enabled: false, valid: true}
	default:
		return parsedReadingPreferenceBool{enabled: false, valid: false}
	}
}

func parseTodayFeedIDs(values []string) ([]int64, bool) {
	feedIDs := make([]int64, 0, len(values))
	for _, raw := range values {
		feedID, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || feedID <= 0 {
			return nil, false
		}
		feedIDs = append(feedIDs, feedID)
	}

	return feedIDs, true
}

func (a *App) finishReadingPreferenceSave(w http.ResponseWriter, r *http.Request) {
	if isHTMXRequest(r) {
		data := readingPreferencesResponseData{ReadingPreferences: emptyReadingPreferences()}
		a.renderTemplateWithReadingPreferences(w, r, "reading_preferences_response", &data)

		return
	}

	redirectTarget := readingPreferencesRedirectTarget(r.PostForm.Get("return_to"))
	if redirectTarget == "" {
		redirectTarget = "/"
	}
	//nolint:gosec // Redirect targets are validated as same-origin absolute paths below.
	http.Redirect(w, r, redirectTarget, http.StatusSeeOther)
}

func readingPreferencesRedirectTarget(raw string) string {
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
