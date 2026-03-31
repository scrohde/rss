package server

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rss/internal/store"
	"rss/internal/view"
)

func feedEditModeEnabled(r *http.Request) bool {
	cookie, err := r.Cookie(feedEditModeCookie)
	if err != nil {
		return false
	}

	return cookie.Value == "1"
}

func setFeedEditModeCookie(w http.ResponseWriter) {
	cookie := new(http.Cookie)
	cookie.Name = feedEditModeCookie
	cookie.Value = "1"
	cookie.Path = "/"
	cookie.MaxAge = feedEditModeCookieMaxAge
	cookie.Expires = time.Now().Add(365 * 24 * time.Hour)
	cookie.HttpOnly = true
	cookie.SameSite = http.SameSiteLaxMode
	http.SetCookie(w, cookie)
}

func clearFeedEditModeCookie(w http.ResponseWriter) {
	cookie := new(http.Cookie)
	cookie.Name = feedEditModeCookie
	cookie.Value = ""
	cookie.Path = "/"
	cookie.MaxAge = -1
	cookie.Expires = time.Unix(1, 0)
	cookie.HttpOnly = true
	cookie.SameSite = http.SameSiteLaxMode
	http.SetCookie(w, cookie)
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	feeds, ok := a.listFeedsOrError(w, r)
	if !ok {
		return
	}

	data, ok := a.pageDataOrError(w, r, "failed to load page state")
	if !ok {
		return
	}

	data.Feeds = feeds
	data.FeedEditMode = feedEditModeEnabled(r)
	a.renderTemplate(w, "index", data)
}

func (a *App) renderPulseMessage(w http.ResponseWriter, message, className string) {
	data := subscribeResponseData{
		ItemList:       nil,
		Message:        message,
		MessageClass:   className,
		Feeds:          nil,
		SelectedFeedID: 0,
		Update:         false,
		FeedEditMode:   false,
	}
	a.renderTemplate(w, "subscribe_response", data)
}

func (a *App) renderItemListResponse(w http.ResponseWriter, r *http.Request, feedID int64) {
	itemList, ok := a.itemListOrError(w, r, feedID)
	if !ok {
		return
	}

	feeds, ok := a.listFeedsOrError(w, r)
	if !ok {
		return
	}

	data := itemListResponseData{
		ItemList:       itemList,
		Feeds:          feeds,
		SelectedFeedID: feedID,
		FeedEditMode:   feedEditModeEnabled(r),
	}
	a.renderTemplate(w, "item_list_response", data)
}

func (a *App) renderTemplate(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	err := a.tmpl.ExecuteTemplate(w, name, data)
	if err != nil {
		log.Printf("template execute failed: %v", err)
		http.Error(w, "template error", http.StatusInternalServerError)

		return
	}
}

func (a *App) newPageData(r *http.Request) (pageData, error) {
	base, err := a.newFullPageData(r)
	if err != nil {
		return pageData{}, err
	}

	return pageData{
		fullPageData:   base,
		ItemList:       nil,
		MobileStream:   nil,
		MobileReader:   nil,
		MobileTopBar:   nil,
		Feeds:          nil,
		SelectedFeedID: 0,
		FeedEditMode:   false,
	}, nil
}

func (a *App) newFullPageData(r *http.Request) (fullPageData, error) {
	theme, err := a.requestAppearanceTheme(r)
	if err != nil {
		return fullPageData{}, err
	}

	return fullPageData{
		CSRFToken:       a.csrfTokenForRequest(r),
		AppearanceTheme: theme,
		ThemeReturnPath: r.URL.RequestURI(),
	}, nil
}

func (a *App) requestAppearanceTheme(r *http.Request) (string, error) {
	principal, ok := currentPrincipal(r)
	if !ok {
		return "", nil
	}

	user, err := store.GetAuthUserByID(r.Context(), a.db, principal.UserID)
	if err != nil {
		return "", fmt.Errorf("load auth user appearance theme: %w", err)
	}

	return user.AppearanceTheme, nil
}

func parsePathInt64(r *http.Request, key string) (int64, bool) {
	raw := strings.TrimSpace(r.PathValue(key))
	if raw == "" {
		return 0, false
	}

	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}

	return parsed, true
}

func isHTMXRequest(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Hx-Request")), "true")
}

func isMobileStreamSelectorTrigger(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Hx-Trigger")), "mobile-stream-feed-filter")
}

func parseAfterID(r *http.Request) int64 {
	err := r.ParseForm()
	if err != nil {
		return 0
	}

	raw := strings.TrimSpace(r.FormValue("after_id"))
	if raw == "" {
		return 0
	}

	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}

	return parsed
}

func parseSelectedFeedID(r *http.Request) int64 {
	err := r.ParseForm()
	if err != nil {
		return 0
	}

	raw := strings.TrimSpace(r.FormValue("selected_feed_id"))
	if raw == "" {
		return 0
	}

	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed <= 0 {
		return 0
	}

	return parsed
}

func parseSelectedItemID(r *http.Request) int64 {
	return parseOptionalItemID(r, "selected_item_id")
}

func parseOptionalItemID(r *http.Request, field string) int64 {
	err := r.ParseForm()
	if err != nil {
		return 0
	}

	raw := strings.TrimSpace(r.FormValue(field))
	if raw == "" {
		return 0
	}

	if after, ok := strings.CutPrefix(raw, "item-"); ok {
		raw = after
	}

	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}

	return parsed
}

func unreadFeedOptions(feeds []view.FeedView) []view.FeedView {
	options := make([]view.FeedView, 0, len(feeds))
	for _, feedView := range feeds {
		if feedView.UnreadCount > 0 {
			options = append(options, feedView)
		}
	}

	return options
}

func feedOptionsContainID(options []view.FeedView, feedID int64) bool {
	for _, feedView := range options {
		if feedView.ID == feedID {
			return true
		}
	}

	return false
}

func feedTitleByID(feedID int64, feeds []view.FeedView) string {
	if feedID <= 0 {
		return ""
	}

	for _, feedView := range feeds {
		if feedView.ID == feedID {
			return feedView.Title
		}
	}

	return ""
}

func normalizeSelectedFeedID(selectedFeedID int64, feeds []view.FeedView) int64 {
	if selectedFeedID <= 0 {
		return 0
	}

	for _, feedView := range feeds {
		if feedView.ID == selectedFeedID {
			return selectedFeedID
		}
	}

	return 0
}

func mobileStreamPath(selectedFeedID int64) string {
	if selectedFeedID <= 0 {
		return "/mobile/stream"
	}

	return fmt.Sprintf("/mobile/stream?selected_feed_id=%d", selectedFeedID)
}

func mobilePulsePath(selectedFeedID int64) string {
	if selectedFeedID <= 0 {
		return "/mobile/pulse"
	}

	return fmt.Sprintf("/mobile/pulse?selected_feed_id=%d", selectedFeedID)
}
