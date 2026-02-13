package server

import (
	"bufio"
	"database/sql"
	"errors"
	"html/template"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"rss/internal/content"
	"rss/internal/feed"
	"rss/internal/opml"
	"rss/internal/store"
	"rss/internal/view"
)

const skipDeleteWarningCookie = "pulse_rss_skip_delete_warning"
const feedEditModeCookie = "pulse_rss_feed_edit_mode"

const maxOPMLUploadBytes int64 = 2 << 20

type App struct {
	db               *sql.DB
	tmpl             *template.Template
	refreshMu        sync.Mutex
	imageProxyClient *http.Client
}

func New(db *sql.DB, tmpl *template.Template) *App {
	return &App{
		db:               db,
		tmpl:             tmpl,
		imageProxyClient: content.NewHTTPClient(),
	}
}

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.HandleFunc("GET /{$}", a.handleIndex)
	mux.HandleFunc("POST /feeds", a.handleSubscribe)
	mux.HandleFunc("POST /feeds/edit-mode", a.handleEnterFeedEditMode)
	mux.HandleFunc("POST /feeds/edit-mode/save", a.handleSaveFeedEditMode)
	mux.HandleFunc("POST /feeds/edit-mode/cancel", a.handleCancelFeedEditMode)
	mux.HandleFunc("GET /opml/export", a.handleExportOPML)
	mux.HandleFunc("POST /opml/import", a.handleImportOPML)
	mux.HandleFunc("GET "+content.ImageProxyPath, a.handleImageProxy)
	mux.HandleFunc("GET /feeds/{feedID}/delete/confirm", a.handleDeleteFeedConfirm)
	mux.HandleFunc("POST /feeds/{feedID}/delete", a.handleDeleteFeed)
	mux.HandleFunc("GET /feeds/{feedID}/rename", a.handleRenameFeedForm)
	mux.HandleFunc("POST /feeds/{feedID}/rename", a.handleRenameFeed)
	mux.HandleFunc("POST /feeds/{feedID}/refresh", a.handleRefreshFeed)
	mux.HandleFunc("GET /feeds/{feedID}/items", a.handleFeedItems)
	mux.HandleFunc("GET /feeds/{feedID}/items/new", a.handleFeedItemsNew)
	mux.HandleFunc("GET /feeds/{feedID}/items/poll", a.handleFeedItemsPoll)
	mux.HandleFunc("POST /feeds/{feedID}/items/read", a.handleMarkAllRead)
	mux.HandleFunc("POST /feeds/{feedID}/items/sweep", a.handleSweepRead)
	mux.HandleFunc("GET /items/{itemID}", a.handleItemExpanded)
	mux.HandleFunc("GET /items/{itemID}/compact", a.handleItemCompact)
	mux.HandleFunc("POST /items/{itemID}/toggle", a.handleToggleRead)
	return mux
}

func (a *App) StartBackgroundLoops() {
	go a.cleanupLoop()
	go a.refreshLoop()
}

func deleteWarningSkipped(r *http.Request) bool {
	cookie, err := r.Cookie(skipDeleteWarningCookie)
	if err != nil {
		return false
	}
	return cookie.Value == "1"
}

func setSkipDeleteWarningCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     skipDeleteWarningCookie,
		Value:    "1",
		Path:     "/",
		MaxAge:   60 * 60 * 24 * 365,
		Expires:  time.Now().Add(365 * 24 * time.Hour),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func feedEditModeEnabled(r *http.Request) bool {
	cookie, err := r.Cookie(feedEditModeCookie)
	if err != nil {
		return false
	}
	return cookie.Value == "1"
}

func setFeedEditModeCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     feedEditModeCookie,
		Value:    "1",
		Path:     "/",
		MaxAge:   60 * 60 * 24 * 365,
		Expires:  time.Now().Add(365 * 24 * time.Hour),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearFeedEditModeCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     feedEditModeCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	feeds, err := store.ListFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	data := view.PageData{
		Feeds:             feeds,
		SkipDeleteWarning: deleteWarningSkipped(r),
		FeedEditMode:      feedEditModeEnabled(r),
	}
	a.renderTemplate(w, "index", data)
}

func (a *App) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	rawURL := r.FormValue("url")
	feedURL, err := feed.NormalizeURL(rawURL)
	if err != nil {
		a.renderSubscribeError(w, err)
		return
	}

	start := time.Now()
	slog.Info("subscribe feed", "feed_url", feedURL)
	result, err := feed.Fetch(feedURL, "", "")
	if err != nil {
		slog.Error("subscribe fetch failed", "feed_url", feedURL, "err", err)
		a.renderSubscribeError(w, err)
		return
	}
	if result.NotModified || result.Feed == nil {
		slog.Warn("subscribe feed returned no content", "feed_url", feedURL, "status", result.StatusCode)
		a.renderSubscribeError(w, errors.New("feed returned no content"))
		return
	}

	feedTitle := strings.TrimSpace(result.Feed.Title)
	if feedTitle == "" {
		feedTitle = feedURL
	}

	feedID, err := store.UpsertFeed(a.db, feedURL, feedTitle)
	if err != nil {
		slog.Error("subscribe upsert feed failed", "feed_url", feedURL, "err", err)
		a.renderSubscribeError(w, err)
		return
	}

	inserted, err := store.UpsertItems(a.db, feedID, result.Feed.Items)
	if err != nil {
		slog.Error("subscribe upsert items failed", "feed_id", feedID, "feed_url", feedURL, "err", err)
		a.renderSubscribeError(w, err)
		return
	}

	if err := store.EnforceItemLimit(a.db, feedID); err != nil {
		slog.Error("subscribe enforce item limit failed", "feed_id", feedID, "feed_url", feedURL, "err", err)
		a.renderSubscribeError(w, err)
		return
	}

	duration := time.Since(start).Milliseconds()
	checkedAt := time.Now().UTC()
	if err := feed.SaveRefreshMeta(a.db, feedID, feed.RefreshMeta{
		ETag:           result.ETag,
		LastModified:   result.LastModified,
		LastCheckedAt:  checkedAt,
		LastError:      "",
		UnchangedCount: 0,
		NextRefreshAt:  feed.NextRefreshAt(checkedAt, 0),
	}); err != nil {
		log.Printf("refresh meta update failed: %v", err)
	}
	slog.Info("subscribe feed stored",
		"feed_id", feedID,
		"title", feedTitle,
		"items_in_feed", len(result.Feed.Items),
		"items_new", inserted,
		"duration_ms", duration,
	)

	feeds, err := store.ListFeeds(a.db)
	if err != nil {
		a.renderSubscribeError(w, err)
		return
	}

	itemList, err := store.LoadItemList(a.db, feedID)
	if err != nil {
		a.renderSubscribeError(w, err)
		return
	}

	data := view.SubscribeResponseData{
		Feeds:             feeds,
		SelectedFeedID:    feedID,
		ItemList:          itemList,
		Update:            true,
		SkipDeleteWarning: deleteWarningSkipped(r),
		FeedEditMode:      feedEditModeEnabled(r),
	}

	a.renderTemplate(w, "subscribe_response", data)
}

func (a *App) renderSubscribeError(w http.ResponseWriter, err error) {
	data := view.SubscribeResponseData{
		Message:      err.Error(),
		MessageClass: "error",
		Update:       false,
	}
	a.renderTemplate(w, "subscribe_response", data)
}

func (a *App) handleExportOPML(w http.ResponseWriter, r *http.Request) {
	feeds, err := store.ListFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	subscriptions := make([]opml.Subscription, 0, len(feeds))
	for _, listedFeed := range feeds {
		subscriptions = append(subscriptions, opml.Subscription{
			Title: listedFeed.Title,
			URL:   listedFeed.URL,
		})
	}

	filename := "pulse-rss-subscriptions-" + time.Now().UTC().Format("20060102") + ".opml"
	w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	if err := opml.Write(w, "Pulse RSS Subscriptions", subscriptions); err != nil {
		http.Error(w, "failed to export opml", http.StatusInternalServerError)
		return
	}
}

func (a *App) handleImportOPML(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOPMLUploadBytes)
	if err := r.ParseMultipartForm(maxOPMLUploadBytes); err != nil {
		a.renderOPMLImportResponse(w, r, 0, 0, false, "invalid OPML upload")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		a.renderOPMLImportResponse(w, r, 0, 0, false, "missing OPML file")
		return
	}
	defer file.Close()

	subscriptions, err := opml.Parse(file)
	if err != nil {
		a.renderOPMLImportResponse(w, r, 0, 0, false, "invalid OPML file")
		return
	}

	imported := 0
	skipped := 0
	for _, subscription := range subscriptions {
		feedURL, normalizeErr := feed.NormalizeURL(subscription.URL)
		if normalizeErr != nil {
			skipped++
			continue
		}

		feedTitle := strings.TrimSpace(subscription.Title)
		if feedTitle == "" {
			feedTitle = feedURL
		}

		if _, upsertErr := store.UpsertFeed(a.db, feedURL, feedTitle); upsertErr != nil {
			skipped++
			continue
		}
		imported++
	}

	if imported == 0 {
		a.renderOPMLImportResponse(w, r, imported, skipped, false, "no valid feeds found in OPML")
		return
	}

	a.renderOPMLImportResponse(w, r, imported, skipped, true, "")
}

func (a *App) renderOPMLImportResponse(w http.ResponseWriter, r *http.Request, imported, skipped int, update bool, fallbackMessage string) {
	feeds, err := store.ListFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	messageClass := "success"
	message := fallbackMessage
	if message == "" {
		message = "Imported " + strconv.Itoa(imported) + " feed"
		if imported != 1 {
			message += "s"
		}
	}
	if skipped > 0 {
		message += " (" + strconv.Itoa(skipped) + " skipped)"
	}
	if !update {
		messageClass = "error"
	}

	data := view.SubscribeResponseData{
		Message:           message,
		MessageClass:      messageClass,
		Feeds:             feeds,
		Update:            update,
		SkipDeleteWarning: deleteWarningSkipped(r),
		FeedEditMode:      feedEditModeEnabled(r),
	}
	a.renderTemplate(w, "opml_import_response", data)
}

func (a *App) handleEnterFeedEditMode(w http.ResponseWriter, r *http.Request) {
	setFeedEditModeCookie(w)

	feeds, err := store.ListFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	data := view.ItemListResponseData{
		Feeds:             feeds,
		SelectedFeedID:    parseSelectedFeedID(r),
		SkipDeleteWarning: deleteWarningSkipped(r),
		FeedEditMode:      true,
	}
	a.renderTemplate(w, "feed_list", data)
}

func (a *App) handleCancelFeedEditMode(w http.ResponseWriter, r *http.Request) {
	clearFeedEditModeCookie(w)

	feeds, err := store.ListFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	data := view.ItemListResponseData{
		Feeds:             feeds,
		SelectedFeedID:    parseSelectedFeedID(r),
		SkipDeleteWarning: deleteWarningSkipped(r),
		FeedEditMode:      false,
	}
	a.renderTemplate(w, "feed_list", data)
}

func (a *App) handleSaveFeedEditMode(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	feeds, err := store.ListFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	currentTitles := make(map[int64]string, len(feeds))
	originalTitles := make(map[int64]string, len(feeds))
	for _, listedFeed := range feeds {
		currentTitles[listedFeed.ID] = strings.TrimSpace(listedFeed.Title)
		originalTitles[listedFeed.ID] = strings.TrimSpace(listedFeed.OriginalTitle)
	}

	updates := parseFeedTitleUpdates(r.PostForm)
	for _, feedID := range updates.FeedIDs {
		title := updates.TitlesByID[feedID]
		if title == originalTitles[feedID] {
			if err := store.UpdateFeedTitle(a.db, feedID, ""); err != nil {
				http.Error(w, "failed to rename feed", http.StatusInternalServerError)
				return
			}
			continue
		}
		if title == currentTitles[feedID] {
			continue
		}
		if err := store.UpdateFeedTitle(a.db, feedID, title); err != nil {
			http.Error(w, "failed to rename feed", http.StatusInternalServerError)
			return
		}
	}

	clearFeedEditModeCookie(w)

	feeds, err = store.ListFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	data := view.ItemListResponseData{
		Feeds:             feeds,
		SelectedFeedID:    parseSelectedFeedID(r),
		SkipDeleteWarning: deleteWarningSkipped(r),
		FeedEditMode:      false,
	}
	a.renderTemplate(w, "feed_list", data)
}

func (a *App) handleFeedItems(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)
		return
	}

	a.renderItemListResponse(w, r, feedID)
}

func (a *App) handleFeedItemsPoll(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)
		return
	}

	afterID := parseAfterID(r)

	count, err := store.CountItemsAfter(a.db, feedID, afterID)
	if err != nil {
		http.Error(w, "failed to check new items", http.StatusInternalServerError)
		return
	}

	feeds, err := store.ListFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	refreshDisplay := "Never"
	for _, listedFeed := range feeds {
		if listedFeed.ID == feedID {
			refreshDisplay = listedFeed.LastRefreshDisplay
			break
		}
	}

	data := view.PollResponseData{
		Banner:            view.NewItemsData{FeedID: feedID, Count: count},
		Feeds:             feeds,
		RefreshDisplay:    refreshDisplay,
		SelectedFeedID:    feedID,
		SkipDeleteWarning: deleteWarningSkipped(r),
		FeedEditMode:      feedEditModeEnabled(r),
	}
	a.renderTemplate(w, "poll_response", data)
}

func (a *App) handleFeedItemsNew(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)
		return
	}

	afterID := parseAfterID(r)

	items, err := store.ListItemsAfter(a.db, feedID, afterID)
	if err != nil {
		http.Error(w, "failed to load new items", http.StatusInternalServerError)
		return
	}

	newestID := afterID
	for _, item := range items {
		if item.ID > newestID {
			newestID = item.ID
		}
	}

	data := view.NewItemsResponseData{
		Items:    items,
		NewestID: newestID,
		Banner:   view.NewItemsData{FeedID: feedID, Count: 0, SwapOOB: true},
	}
	a.renderTemplate(w, "item_new_response", data)
}

func (a *App) handleItemExpanded(w http.ResponseWriter, r *http.Request) {
	itemID, ok := parsePathInt64(r, "itemID")
	if !ok {
		http.NotFound(w, r)
		return
	}

	item, err := store.GetItem(a.db, itemID)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}
	item.IsActive = parseSelectedItemID(r) == item.ID
	a.renderTemplate(w, "item_expanded", item)
}

func (a *App) handleItemCompact(w http.ResponseWriter, r *http.Request) {
	itemID, ok := parsePathInt64(r, "itemID")
	if !ok {
		http.NotFound(w, r)
		return
	}

	item, err := store.GetItem(a.db, itemID)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}
	item.IsActive = parseSelectedItemID(r) == item.ID
	a.renderTemplate(w, "item_compact", item)
}

func (a *App) handleToggleRead(w http.ResponseWriter, r *http.Request) {
	itemID, ok := parsePathInt64(r, "itemID")
	if !ok {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	currentView := r.FormValue("view")
	if err := store.ToggleRead(a.db, itemID); err != nil {
		http.Error(w, "failed to update item", http.StatusInternalServerError)
		return
	}
	slog.Info("item read toggled", "item_id", itemID, "view", currentView)

	feedID, err := store.GetFeedIDByItem(a.db, itemID)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}

	item, err := store.GetItem(a.db, itemID)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}
	item.IsActive = parseSelectedItemID(r) == item.ID

	feeds, err := store.ListFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	data := view.ToggleReadResponseData{
		Item:              item,
		Feeds:             feeds,
		SelectedFeedID:    feedID,
		View:              currentView,
		SkipDeleteWarning: deleteWarningSkipped(r),
		FeedEditMode:      feedEditModeEnabled(r),
	}
	a.renderTemplate(w, "item_toggle_response", data)
}

func (a *App) handleMarkAllRead(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)
		return
	}

	if err := store.MarkAllRead(a.db, feedID); err != nil {
		http.Error(w, "failed to update items", http.StatusInternalServerError)
		return
	}
	slog.Info("feed items marked read", "feed_id", feedID)

	a.renderItemListResponse(w, r, feedID)
}

func (a *App) handleSweepRead(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)
		return
	}

	deleted, err := store.SweepReadItems(a.db, feedID)
	if err != nil {
		http.Error(w, "failed to remove read items", http.StatusInternalServerError)
		return
	}
	slog.Info("feed read items swept", "feed_id", feedID, "deleted", deleted)

	a.renderItemListResponse(w, r, feedID)
}

func (a *App) handleRefreshFeed(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)
		return
	}

	a.refreshMu.Lock()
	_, err := feed.Refresh(a.db, feedID)
	a.refreshMu.Unlock()
	if err != nil {
		slog.Warn("manual refresh failed", "feed_id", feedID, "err", err)
	}

	a.renderItemListResponse(w, r, feedID)
}

func (a *App) renderItemListResponse(w http.ResponseWriter, r *http.Request, feedID int64) {
	itemList, err := store.LoadItemList(a.db, feedID)
	if err != nil {
		http.Error(w, "failed to load items", http.StatusInternalServerError)
		return
	}

	feeds, err := store.ListFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	data := view.ItemListResponseData{
		ItemList:          itemList,
		Feeds:             feeds,
		SelectedFeedID:    feedID,
		SkipDeleteWarning: deleteWarningSkipped(r),
		FeedEditMode:      feedEditModeEnabled(r),
	}
	a.renderTemplate(w, "item_list_response", data)
}

func (a *App) handleDeleteFeedConfirm(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)
		return
	}

	if deleteWarningSkipped(r) || r.URL.Query().Get("cancel") == "1" {
		data := view.DeleteFeedConfirmData{Feed: view.FeedView{ID: feedID}, Show: false}
		a.renderTemplate(w, "feed_remove_confirm", data)
		return
	}

	currentFeed, err := store.GetFeed(a.db, feedID)
	if err != nil {
		http.Error(w, "feed not found", http.StatusNotFound)
		return
	}

	data := view.DeleteFeedConfirmData{Feed: currentFeed, Show: true}
	a.renderTemplate(w, "feed_remove_confirm", data)
}

func (a *App) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	selectedFeedID := parseSelectedFeedID(r)

	if err := store.DeleteFeed(a.db, feedID); err != nil {
		http.Error(w, "failed to delete feed", http.StatusInternalServerError)
		return
	}
	slog.Info("feed deleted", "feed_id", feedID)

	skipDeleteWarning := deleteWarningSkipped(r)
	if r.FormValue("skip_delete_warning") != "" {
		setSkipDeleteWarningCookie(w)
		skipDeleteWarning = true
	}

	feeds, err := store.ListFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	selectedFeedID = store.SelectRemainingFeed(selectedFeedID, feedID, feeds)

	var itemList *view.ItemListData
	if selectedFeedID != 0 {
		itemList, err = store.LoadItemList(a.db, selectedFeedID)
		if err != nil {
			http.Error(w, "failed to load items", http.StatusInternalServerError)
			return
		}
	}

	data := view.ItemListResponseData{
		ItemList:          itemList,
		Feeds:             feeds,
		SelectedFeedID:    selectedFeedID,
		SkipDeleteWarning: skipDeleteWarning,
		FeedEditMode:      feedEditModeEnabled(r),
	}
	a.renderTemplate(w, "delete_feed_response", data)
}

func (a *App) handleRenameFeedForm(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)
		return
	}

	if r.URL.Query().Get("cancel") == "1" {
		clearFeedEditModeCookie(w)

		feeds, err := store.ListFeeds(a.db)
		if err != nil {
			http.Error(w, "failed to load feeds", http.StatusInternalServerError)
			return
		}

		data := view.RenameFeedResponseData{
			FeedID:            feedID,
			Feeds:             feeds,
			SelectedFeedID:    parseSelectedFeedID(r),
			SkipDeleteWarning: deleteWarningSkipped(r),
			FeedEditMode:      false,
		}
		a.renderTemplate(w, "feed_rename_response", data)
		return
	}

	currentFeed, err := store.GetFeed(a.db, feedID)
	if err != nil {
		http.Error(w, "feed not found", http.StatusNotFound)
		return
	}

	data := view.RenameFeedFormData{Feed: currentFeed, Show: true}
	a.renderTemplate(w, "feed_rename_form", data)
}

func (a *App) handleRenameFeed(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))

	if err := store.UpdateFeedTitle(a.db, feedID, title); err != nil {
		http.Error(w, "failed to rename feed", http.StatusInternalServerError)
		return
	}
	slog.Info("feed renamed", "feed_id", feedID, "title", title)

	selectedFeedID := parseSelectedFeedID(r)

	feeds, err := store.ListFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	var itemList *view.ItemListData
	if selectedFeedID == feedID && selectedFeedID != 0 {
		itemList, err = store.LoadItemList(a.db, selectedFeedID)
		if err != nil {
			http.Error(w, "failed to load items", http.StatusInternalServerError)
			return
		}
	}

	clearFeedEditModeCookie(w)

	data := view.RenameFeedResponseData{
		FeedID:            feedID,
		ItemList:          itemList,
		Feeds:             feeds,
		SelectedFeedID:    selectedFeedID,
		SkipDeleteWarning: deleteWarningSkipped(r),
		FeedEditMode:      false,
	}
	a.renderTemplate(w, "feed_rename_response", data)
}

func (a *App) handleImageProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	raw := r.URL.Query().Get("url")
	if raw == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}
	if len(raw) > content.MaxImageProxyURLLength {
		http.Error(w, "url too long", http.StatusRequestURITooLong)
		return
	}

	target, err := url.Parse(raw)
	if err != nil || !content.IsAllowedProxyURL(target) {
		http.Error(w, "invalid url", http.StatusBadRequest)
		return
	}

	req, err := content.BuildImageProxyRequest(target)
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	resp, err := a.imageProxyClient.Do(req)
	if err != nil {
		http.Error(w, "upstream fetch failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}

	reader := bufio.NewReader(resp.Body)
	sniff, _ := reader.Peek(512)
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" || !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		detected := http.DetectContentType(sniff)
		if !strings.HasPrefix(detected, "image/") {
			http.Error(w, "upstream did not return image content", http.StatusUnsupportedMediaType)
			return
		}
		contentType = detected
	}

	w.Header().Set("Content-Type", contentType)
	if cacheControl := resp.Header.Get("Cache-Control"); cacheControl != "" {
		w.Header().Set("Cache-Control", cacheControl)
	} else {
		w.Header().Set("Cache-Control", content.ImageProxyCacheFallback)
	}
	if etag := resp.Header.Get("ETag"); etag != "" {
		w.Header().Set("ETag", etag)
	}
	if modified := resp.Header.Get("Last-Modified"); modified != "" {
		w.Header().Set("Last-Modified", modified)
	}
	if length := resp.Header.Get("Content-Length"); length != "" {
		w.Header().Set("Content-Length", length)
	}

	if _, err := io.Copy(w, reader); err != nil {
		log.Printf("image proxy copy: %v", err)
	}
}

func (a *App) renderTemplate(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
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

func parseAfterID(r *http.Request) int64 {
	if err := r.ParseForm(); err != nil {
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
	if err := r.ParseForm(); err != nil {
		return 0
	}
	raw := strings.TrimSpace(r.FormValue("selected_feed_id"))
	if raw == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func parseSelectedItemID(r *http.Request) int64 {
	if err := r.ParseForm(); err != nil {
		return 0
	}
	raw := strings.TrimSpace(r.FormValue("selected_item_id"))
	if raw == "" {
		return 0
	}
	if strings.HasPrefix(raw, "item-") {
		raw = strings.TrimPrefix(raw, "item-")
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

type feedTitleUpdates struct {
	FeedIDs    []int64
	TitlesByID map[int64]string
}

func parseFeedTitleUpdates(values url.Values) feedTitleUpdates {
	result := feedTitleUpdates{
		FeedIDs:    make([]int64, 0),
		TitlesByID: make(map[int64]string),
	}

	for key, titles := range values {
		if !strings.HasPrefix(key, "feed_title_") {
			continue
		}
		rawID := strings.TrimPrefix(key, "feed_title_")
		feedID, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || feedID <= 0 {
			continue
		}

		title := ""
		if len(titles) > 0 {
			title = strings.TrimSpace(titles[0])
		}

		if _, exists := result.TitlesByID[feedID]; !exists {
			result.FeedIDs = append(result.FeedIDs, feedID)
		}
		result.TitlesByID[feedID] = title
	}

	sort.Slice(result.FeedIDs, func(i, j int) bool {
		return result.FeedIDs[i] < result.FeedIDs[j]
	})
	return result
}

func (a *App) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		if err := store.CleanupReadItems(a.db); err != nil {
			slog.Error("cleanup error", "err", err)
		}
		<-ticker.C
	}
}

func (a *App) refreshLoop() {
	ticker := time.NewTicker(feed.RefreshLoopInterval)
	defer ticker.Stop()
	for {
		if err := a.refreshDueFeeds(); err != nil {
			slog.Error("refresh loop error", "err", err)
		}
		<-ticker.C
	}
}

func (a *App) refreshDueFeeds() error {
	ids, err := store.ListDueFeeds(a.db, time.Now().UTC(), feed.RefreshBatchSize)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		slog.Info("refresh due feeds", "count", len(ids))
	}
	for _, id := range ids {
		a.refreshMu.Lock()
		_, err := feed.Refresh(a.db, id)
		a.refreshMu.Unlock()
		if err != nil {
			slog.Error("refresh feed error", "feed_id", id, "err", err)
		}
	}
	return nil
}
