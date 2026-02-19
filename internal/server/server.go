package server

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"rss/internal/auth"
	"rss/internal/content"
	"rss/internal/feed"
	"rss/internal/opml"
	"rss/internal/store"
	"rss/internal/view"
)

const (
	feedEditModeCookie             = "pulse_rss_feed_edit_mode"
	maxOPMLUploadBytes       int64 = 2 << 20
	imageProxySniffBytes           = 512
	cleanupInterval                = 10 * time.Minute
	feedEditModeCookieMaxAge       = 60 * 60 * 24 * 365
)

var errFeedReturnedNoContent = errors.New("feed returned no content")

// App wires handlers, dependencies, and background loops for the HTTP server.
type App struct {
	staticHandler       http.Handler
	authManager         *auth.Manager
	db                  *sql.DB
	tmpl                *template.Template
	imageProxyClient    *http.Client
	imageProxyLookup    content.LookupIPAddrFunc
	authRateLimiter     *authRateLimiter
	authCookieName      string
	authSetupToken      string
	authSetupCookieName string
	authSetupSignerKey  []byte
	refreshMu           sync.Mutex
	authEnabled         bool
	authCookieSecure    bool
}

// New constructs an App with default static file and image proxy dependencies.
func New(db *sql.DB, tmpl *template.Template) *App {
	app := new(App)
	app.db = db
	app.tmpl = tmpl
	app.staticHandler = http.FileServer(http.Dir("static"))
	app.imageProxyClient = content.NewHTTPClient()
	app.imageProxyLookup = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return net.DefaultResolver.LookupIPAddr(ctx, host)
	}
	app.authManager = nil
	app.authRateLimiter = nil
	app.authCookieName = ""
	app.authSetupToken = ""
	app.authSetupCookieName = ""
	app.authSetupSignerKey = nil
	app.refreshMu = sync.Mutex{}
	app.authEnabled = false
	app.authCookieSecure = false

	return app
}

// SetStaticFS replaces the static file system used for `/static/*` routes.
func (a *App) SetStaticFS(fsys fs.FS) {
	a.staticHandler = http.FileServer(http.FS(fsys))
}

// Routes returns the fully configured application HTTP handler.
func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	a.registerCoreRoutes(mux)
	a.registerFeedRoutes(mux)

	if a.authEnabled {
		a.registerAuthRoutes(mux)
	}

	var handler http.Handler = mux

	return a.wrapRoutes(handler)
}

// StartBackgroundLoops starts cleanup and feed refresh goroutines.
func (a *App) StartBackgroundLoops() {
	go a.cleanupLoop()
	go a.refreshLoop()
}

func (a *App) registerCoreRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", a.handleHealthz)
	mux.Handle("GET /static/", http.StripPrefix("/static/", a.staticHandler))
	mux.HandleFunc("GET /{$}", a.handleIndex)
	mux.HandleFunc("GET /opml/export", a.handleExportOPML)
	mux.HandleFunc("POST /opml/import", a.handleImportOPML)
	mux.HandleFunc("GET "+content.ImageProxyPath, a.handleImageProxy)
}

func (a *App) registerFeedRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /feeds", a.handleSubscribe)
	mux.HandleFunc("POST /feeds/edit-mode", a.handleEnterFeedEditMode)
	mux.HandleFunc("POST /feeds/edit-mode/save", a.handleSaveFeedEditMode)
	mux.HandleFunc("POST /feeds/edit-mode/cancel", a.handleCancelFeedEditMode)
	mux.HandleFunc("POST /feeds/{feedID}/delete", a.handleDeleteFeed)
	mux.HandleFunc("POST /feeds/{feedID}/refresh", a.handleRefreshFeed)
	mux.HandleFunc("GET /feeds/{feedID}/items", a.handleFeedItems)
	mux.HandleFunc("GET /feeds/{feedID}/items/new", a.handleFeedItemsNew)
	mux.HandleFunc("GET /feeds/{feedID}/items/poll", a.handleFeedItemsPoll)
	mux.HandleFunc("POST /feeds/{feedID}/items/read", a.handleMarkAllRead)
	mux.HandleFunc("POST /feeds/{feedID}/items/sweep", a.handleSweepRead)
	mux.HandleFunc("GET /items/{itemID}", a.handleItemExpanded)
	mux.HandleFunc("GET /items/{itemID}/compact", a.handleItemCompact)
	mux.HandleFunc("POST /items/{itemID}/toggle", a.handleToggleRead)
}

func (a *App) registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", a.handleAuthLogin)
	mux.HandleFunc("POST /auth/webauthn/login/options", a.handleAuthLoginOptions)
	mux.HandleFunc("POST /auth/webauthn/login/verify", a.handleAuthLoginVerify)
	mux.HandleFunc("GET /auth/setup", a.handleAuthSetup)
	mux.HandleFunc("POST /auth/setup/unlock", a.handleAuthSetupUnlock)
	mux.HandleFunc("POST /auth/webauthn/register/options", a.handleAuthRegisterOptions)
	mux.HandleFunc("POST /auth/webauthn/register/verify", a.handleAuthRegisterVerify)
	mux.HandleFunc("POST /auth/logout", a.handleAuthLogout)
	mux.HandleFunc("GET /auth/security", a.handleAuthSecurity)
	mux.HandleFunc("GET /auth/recovery", a.handleAuthRecovery)
	mux.HandleFunc("POST /auth/recovery/use", a.handleAuthRecoveryUse)
	mux.HandleFunc("POST /auth/recovery/generate", a.handleAuthRecoveryGenerate)
}

func (a *App) wrapRoutes(handler http.Handler) http.Handler {
	handler = a.withRequestID(handler)
	handler = a.withRealIP(handler)
	handler = a.withSecurityHeaders(handler)

	if a.authEnabled {
		handler = a.withAuthRateLimit(handler)
		handler = a.withCSRFMiddleware(handler)
		handler = a.withAuthSession(handler)
	}

	return handler
}

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
	feeds, err := store.ListFeeds(r.Context(), a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return
	}

	var data pageData

	data.Feeds = feeds
	data.FeedEditMode = feedEditModeEnabled(r)
	data.CSRFToken = a.csrfTokenForRequest(r)
	a.renderTemplate(w, "index", data)
}

func (a *App) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)

		return
	}

	feedID, err := a.subscribeAndStoreFeed(r.Context(), r.FormValue("url"))
	if err != nil {
		a.renderSubscribeError(w, err)

		return
	}

	data, err := a.buildSubscribeResponseData(r.Context(), r, feedID)
	if err != nil {
		a.renderSubscribeError(w, err)

		return
	}

	a.renderTemplate(w, "subscribe_response", data)
}

func (a *App) subscribeAndStoreFeed(ctx context.Context, rawURL string) (int64, error) {
	feedURL, err := feed.NormalizeURL(rawURL)
	if err != nil {
		return 0, fmt.Errorf("normalize feed URL: %w", err)
	}

	start := time.Now()

	slog.Info("subscribe feed")

	result, err := feed.Fetch(ctx, feedURL, "", "")
	if err != nil {
		slog.Error("subscribe fetch failed", "err", err)

		return 0, fmt.Errorf("fetch feed: %w", err)
	}

	if result.NotModified || result.Feed == nil {
		slog.Warn("subscribe feed returned no content")

		return 0, errFeedReturnedNoContent
	}

	feedID, err := a.persistSubscribedFeed(ctx, feedURL, result)
	if err != nil {
		return 0, err
	}

	a.saveSubscribeRefreshMeta(ctx, feedID, result)

	slog.Info("subscribe feed stored",
		"duration_ms", time.Since(start).Milliseconds(),
	)

	return feedID, nil
}

func (a *App) persistSubscribedFeed(ctx context.Context, feedURL string, result *feed.FetchResult) (int64, error) {
	feedTitle := subscribeFeedTitle(result.Feed.Title, feedURL)

	feedID, err := store.UpsertFeed(ctx, a.db, feedURL, feedTitle)
	if err != nil {
		slog.Error("subscribe upsert feed failed", "err", err)

		return 0, fmt.Errorf("upsert feed: %w", err)
	}

	_, err = store.UpsertItems(ctx, a.db, feedID, result.Feed.Items)
	if err != nil {
		slog.Error("subscribe upsert items failed")

		return 0, fmt.Errorf("upsert feed items: %w", err)
	}

	enforceErr := store.EnforceItemLimit(ctx, a.db, feedID)
	if enforceErr != nil {
		slog.Error("subscribe enforce item limit failed")

		return 0, fmt.Errorf("enforce item limit: %w", enforceErr)
	}

	return feedID, nil
}

func subscribeFeedTitle(rawTitle, feedURL string) string {
	title := strings.TrimSpace(rawTitle)
	if title == "" {
		return feedURL
	}

	return title
}

func (a *App) saveSubscribeRefreshMeta(ctx context.Context, feedID int64, result *feed.FetchResult) {
	checkedAt := time.Now().UTC()
	meta := new(feed.RefreshMeta)
	meta.ETag = result.ETag
	meta.LastModified = result.LastModified
	meta.LastCheckedAt = checkedAt
	meta.LastError = ""
	meta.UnchangedCount = 0
	meta.NextRefreshAt = feed.NextRefreshAt(checkedAt, 0)

	err := feed.SaveRefreshMeta(ctx, a.db, feedID, meta)
	if err != nil {
		log.Printf("refresh meta update failed: %v", err)
	}
}

func (a *App) buildSubscribeResponseData(
	ctx context.Context,
	r *http.Request,
	feedID int64,
) (subscribeResponseData, error) {
	feeds, err := store.ListFeeds(ctx, a.db)
	if err != nil {
		return subscribeResponseData{}, fmt.Errorf("list feeds: %w", err)
	}

	itemList, err := store.LoadItemList(ctx, a.db, feedID)
	if err != nil {
		return subscribeResponseData{}, fmt.Errorf("load feed items: %w", err)
	}

	return subscribeResponseData{
		Message:        "",
		MessageClass:   "",
		Feeds:          feeds,
		SelectedFeedID: feedID,
		ItemList:       itemList,
		Update:         true,
		FeedEditMode:   feedEditModeEnabled(r),
	}, nil
}

func (a *App) renderSubscribeError(w http.ResponseWriter, err error) {
	var data subscribeResponseData

	data.Message = err.Error()
	data.MessageClass = "error"
	data.Update = false
	a.renderTemplate(w, "subscribe_response", data)
}

func (a *App) handleExportOPML(w http.ResponseWriter, r *http.Request) {
	feeds, err := store.ListFeeds(r.Context(), a.db)
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

	err = opml.Write(w, "Pulse RSS Subscriptions", subscriptions)
	if err != nil {
		http.Error(w, "failed to export opml", http.StatusInternalServerError)

		return
	}
}

type opmlImportCounts struct {
	imported int
	skipped  int
}

func (a *App) handleImportOPML(w http.ResponseWriter, r *http.Request) {
	subscriptions, message := parseOPMLUpload(w, r)
	if message != "" {
		a.renderOPMLImportResponse(w, r, 0, 0, "error", message)

		return
	}

	counts := a.importOPMLSubscriptions(r.Context(), subscriptions)

	if counts.imported == 0 {
		a.renderOPMLImportResponse(
			w,
			r,
			counts.imported,
			counts.skipped,
			"error",
			"no valid feeds found in OPML",
		)

		return
	}

	a.renderOPMLImportResponse(w, r, counts.imported, counts.skipped, "success", "")
}

//nolint:gocritic // Tuple return keeps upload parsing call sites simple.
func parseOPMLUpload(w http.ResponseWriter, r *http.Request) ([]opml.Subscription, string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOPMLUploadBytes)

	parseErr := r.ParseMultipartForm(maxOPMLUploadBytes)
	if parseErr != nil {
		return nil, "invalid OPML upload"
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		return nil, "missing OPML file"
	}

	defer func() {
		closeErr := file.Close()
		if closeErr != nil {
			log.Printf("opml upload close: %v", closeErr)
		}
	}()

	subscriptions, err := opml.Parse(file)
	if err != nil {
		return nil, "invalid OPML file"
	}

	return subscriptions, ""
}

func (a *App) importOPMLSubscriptions(ctx context.Context, subscriptions []opml.Subscription) opmlImportCounts {
	var counts opmlImportCounts

	for _, subscription := range subscriptions {
		feedURL, err := feed.NormalizeURL(subscription.URL)
		if err != nil {
			counts.skipped++

			continue
		}

		feedTitle := subscribeFeedTitle(subscription.Title, feedURL)

		_, upsertErr := store.UpsertFeed(ctx, a.db, feedURL, feedTitle)
		if upsertErr != nil {
			counts.skipped++

			continue
		}

		counts.imported++
	}

	return counts
}

func (a *App) renderOPMLImportResponse(
	w http.ResponseWriter,
	r *http.Request,
	imported,
	skipped int,
	messageClass,
	fallbackMessage string,
) {
	feeds, err := store.ListFeeds(r.Context(), a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return
	}

	message := opmlImportMessage(imported, skipped, fallbackMessage)
	update := messageClass == "success"

	var data subscribeResponseData

	data.Message = message
	data.MessageClass = messageClass
	data.Feeds = feeds
	data.Update = update
	data.FeedEditMode = feedEditModeEnabled(r)
	a.renderTemplate(w, "opml_import_response", data)
}

func opmlImportMessage(imported, skipped int, fallbackMessage string) string {
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

	return message
}

func (a *App) handleEnterFeedEditMode(w http.ResponseWriter, r *http.Request) {
	setFeedEditModeCookie(w)

	feeds, err := store.ListFeeds(r.Context(), a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return
	}

	var data itemListResponseData

	data.ItemList = nil
	data.Feeds = feeds
	data.SelectedFeedID = parseSelectedFeedID(r)
	data.FeedEditMode = true
	a.renderTemplate(w, "feed_list", data)
}

func (a *App) handleCancelFeedEditMode(w http.ResponseWriter, r *http.Request) {
	clearFeedEditModeCookie(w)

	feeds, err := store.ListFeeds(r.Context(), a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return
	}

	var data itemListResponseData

	data.ItemList = nil
	data.Feeds = feeds
	data.SelectedFeedID = parseSelectedFeedID(r)
	data.FeedEditMode = false
	a.renderTemplate(w, "feed_list", data)
}

func (a *App) handleSaveFeedEditMode(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)

		return
	}

	selectedFeedID := parseSelectedFeedID(r)

	feeds, err := store.ListFeeds(r.Context(), a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return
	}

	titles := feedTitleMaps(feeds)

	deleteUpdates := parseFeedDeleteUpdates(r.PostForm)
	deleteByID := existingDeleteSet(deleteUpdates, titles.current)
	orderUpdates := parseFeedOrderUpdates(r.PostForm)

	updates := parseFeedTitleUpdates(r.PostForm)

	titleErr := a.applyFeedTitleUpdates(r.Context(), updates, deleteByID, titles)
	if titleErr != nil {
		http.Error(w, "failed to rename feed", http.StatusInternalServerError)

		return
	}

	selectedFeedDeleted, err := a.applyFeedDeletes(r.Context(), deleteUpdates, deleteByID, selectedFeedID)
	if err != nil {
		http.Error(w, "failed to delete feed", http.StatusInternalServerError)

		return
	}

	reorderErr := a.applyFeedReorder(r.Context(), orderUpdates, deleteByID)
	if reorderErr != nil {
		http.Error(w, "failed to reorder feeds", http.StatusInternalServerError)

		return
	}

	clearFeedEditModeCookie(w)

	deletedFeedID := int64(0)
	if selectedFeedDeleted {
		deletedFeedID = selectedFeedID
	}

	a.renderFeedEditSaveResponse(w, r, selectedFeedID, deletedFeedID)
}

func (a *App) renderFeedEditSaveResponse(
	w http.ResponseWriter,
	r *http.Request,
	selectedFeedID int64,
	deletedFeedID int64,
) {
	feeds, err := store.ListFeeds(r.Context(), a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return
	}

	selectedFeedID, itemList, err := a.feedEditSelection(r.Context(), selectedFeedID, deletedFeedID, feeds)
	if err != nil {
		http.Error(w, "failed to load items", http.StatusInternalServerError)

		return
	}

	var data itemListResponseData

	data.ItemList = itemList
	data.Feeds = feeds
	data.SelectedFeedID = selectedFeedID
	data.FeedEditMode = false
	a.renderTemplate(w, "feed_edit_save_response", data)
}

type feedTitleState struct {
	current  map[int64]string
	original map[int64]string
}

func feedTitleMaps(feeds []view.FeedView) feedTitleState {
	state := feedTitleState{
		current:  make(map[int64]string, len(feeds)),
		original: make(map[int64]string, len(feeds)),
	}

	for _, listedFeed := range feeds {
		state.current[listedFeed.ID] = strings.TrimSpace(listedFeed.Title)
		state.original[listedFeed.ID] = strings.TrimSpace(listedFeed.OriginalTitle)
	}

	return state
}

func existingDeleteSet(deleteUpdates []int64, currentTitles map[int64]string) map[int64]struct{} {
	deleteByID := make(map[int64]struct{}, len(deleteUpdates))

	for _, feedID := range deleteUpdates {
		if _, exists := currentTitles[feedID]; exists {
			deleteByID[feedID] = struct{}{}
		}
	}

	return deleteByID
}

func (a *App) applyFeedTitleUpdates(
	ctx context.Context,
	updates feedTitleUpdates,
	deleteByID map[int64]struct{},
	titles feedTitleState,
) error {
	for _, feedID := range updates.FeedIDs {
		if _, markedForDelete := deleteByID[feedID]; markedForDelete {
			continue
		}

		nextTitle, shouldUpdate := feedTitleUpdate(
			updates.TitlesByID[feedID],
			titles.current[feedID],
			titles.original[feedID],
		)
		if !shouldUpdate {
			continue
		}

		updateErr := store.UpdateFeedTitle(ctx, a.db, feedID, nextTitle)
		if updateErr != nil {
			return fmt.Errorf("update feed title for %d: %w", feedID, updateErr)
		}
	}

	return nil
}

func feedTitleUpdate(nextTitle, currentTitle, originalTitle string) (string, bool) {
	if nextTitle == currentTitle {
		return "", false
	}

	if nextTitle == originalTitle {
		return "", true
	}

	return nextTitle, true
}

func (a *App) applyFeedDeletes(
	ctx context.Context,
	deleteUpdates []int64,
	deleteByID map[int64]struct{},
	selectedFeedID int64,
) (bool, error) {
	selectedFeedDeleted := false

	for _, feedID := range deleteUpdates {
		if _, markedForDelete := deleteByID[feedID]; !markedForDelete {
			continue
		}

		deleteErr := store.DeleteFeed(ctx, a.db, feedID)
		if deleteErr != nil {
			return false, fmt.Errorf("delete feed %d: %w", feedID, deleteErr)
		}

		if feedID == selectedFeedID {
			selectedFeedDeleted = true
		}
	}

	return selectedFeedDeleted, nil
}

func (a *App) applyFeedReorder(ctx context.Context, orderUpdates []int64, deleteByID map[int64]struct{}) error {
	if len(orderUpdates) == 0 {
		return nil
	}

	finalOrder := make([]int64, 0, len(orderUpdates))
	for _, feedID := range orderUpdates {
		if _, markedForDelete := deleteByID[feedID]; markedForDelete {
			continue
		}

		finalOrder = append(finalOrder, feedID)
	}

	err := store.UpdateFeedOrder(ctx, a.db, finalOrder)
	if err != nil {
		return fmt.Errorf("update feed order: %w", err)
	}

	return nil
}

func (a *App) feedEditSelection(
	ctx context.Context,
	selectedFeedID int64,
	deletedFeedID int64,
	feeds []view.FeedView,
) (int64, *view.ItemListData, error) {
	nextFeedID := store.SelectRemainingFeed(selectedFeedID, deletedFeedID, feeds)
	if deletedFeedID == 0 || nextFeedID == 0 {
		return nextFeedID, nil, nil
	}

	itemList, err := store.LoadItemList(ctx, a.db, nextFeedID)
	if err != nil {
		return 0, nil, fmt.Errorf("load item list for feed %d: %w", nextFeedID, err)
	}

	return nextFeedID, itemList, nil
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

	count, err := store.CountItemsAfter(r.Context(), a.db, feedID, afterID)
	if err != nil {
		http.Error(w, "failed to check new items", http.StatusInternalServerError)

		return
	}

	feeds, err := store.ListFeeds(r.Context(), a.db)
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

	var data pollResponseData

	data.Banner = view.NewItemsData{FeedID: feedID, Count: count, SwapOOB: false}
	data.Feeds = feeds
	data.RefreshDisplay = refreshDisplay
	data.SelectedFeedID = feedID
	data.FeedEditMode = feedEditModeEnabled(r)
	a.renderTemplate(w, "poll_response", data)
}

func (a *App) handleFeedItemsNew(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)

		return
	}

	afterID := parseAfterID(r)

	items, err := store.ListItemsAfter(r.Context(), a.db, feedID, afterID)
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

	data := newItemsResponseData{
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

	item, err := store.GetItem(r.Context(), a.db, itemID)
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

	item, err := store.GetItem(r.Context(), a.db, itemID)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)

		return
	}

	item.IsActive = parseSelectedItemID(r) == item.ID
	a.renderTemplate(w, "item_compact", item)
}

//nolint:gosec // Read toggle logs include request-derived view values for debugging.
func (a *App) handleToggleRead(w http.ResponseWriter, r *http.Request) {
	itemID, ok := parsePathInt64(r, "itemID")
	if !ok {
		http.NotFound(w, r)

		return
	}

	err := r.ParseForm()
	if err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)

		return
	}

	currentView := r.FormValue("view")

	err = store.ToggleRead(r.Context(), a.db, itemID)
	if err != nil {
		http.Error(w, "failed to update item", http.StatusInternalServerError)

		return
	}

	slog.Info("item read toggled", "item_id", itemID, "view", currentView)

	feedID, err := store.GetFeedIDByItem(r.Context(), a.db, itemID)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)

		return
	}

	item, err := store.GetItem(r.Context(), a.db, itemID)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)

		return
	}

	item.IsActive = parseSelectedItemID(r) == item.ID

	feeds, err := store.ListFeeds(r.Context(), a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return
	}

	data := toggleReadResponseData{
		Item:           item,
		Feeds:          feeds,
		SelectedFeedID: feedID,
		View:           currentView,
		FeedEditMode:   feedEditModeEnabled(r),
	}
	a.renderTemplate(w, "item_toggle_response", data)
}

//nolint:gosec // Mark-all-read logs include request-derived feed IDs for operational visibility.
func (a *App) handleMarkAllRead(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)

		return
	}

	err := store.MarkAllRead(r.Context(), a.db, feedID)
	if err != nil {
		http.Error(w, "failed to update items", http.StatusInternalServerError)

		return
	}

	slog.Info("feed items marked read", "feed_id", feedID)

	a.renderItemListResponse(w, r, feedID)
}

//nolint:gosec // Sweep logs include request-derived feed IDs for operational visibility.
func (a *App) handleSweepRead(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)

		return
	}

	deleted, err := store.SweepReadItems(r.Context(), a.db, feedID)
	if err != nil {
		http.Error(w, "failed to remove read items", http.StatusInternalServerError)

		return
	}

	slog.Info("feed read items swept", "feed_id", feedID, "deleted", deleted)

	a.renderItemListResponse(w, r, feedID)
}

//nolint:gosec // Manual refresh logs include request-derived feed IDs for operational visibility.
func (a *App) handleRefreshFeed(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)

		return
	}

	a.refreshMu.Lock()
	_, err := feed.Refresh(r.Context(), a.db, feedID)
	a.refreshMu.Unlock()

	if err != nil {
		slog.Warn("manual refresh failed", "feed_id", feedID, "err", err)
	}

	a.renderItemListResponse(w, r, feedID)
}

func (a *App) renderItemListResponse(w http.ResponseWriter, r *http.Request, feedID int64) {
	itemList, err := store.LoadItemList(r.Context(), a.db, feedID)
	if err != nil {
		http.Error(w, "failed to load items", http.StatusInternalServerError)

		return
	}

	feeds, err := store.ListFeeds(r.Context(), a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

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

//nolint:gosec // Delete logs include request-derived feed IDs for operational visibility.
func (a *App) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)

		return
	}

	err := r.ParseForm()
	if err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)

		return
	}

	selectedFeedID := parseSelectedFeedID(r)

	err = store.DeleteFeed(r.Context(), a.db, feedID)
	if err != nil {
		http.Error(w, "failed to delete feed", http.StatusInternalServerError)

		return
	}

	slog.Info("feed deleted", "feed_id", feedID)

	feeds, err := store.ListFeeds(r.Context(), a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return
	}

	selectedFeedID = store.SelectRemainingFeed(selectedFeedID, feedID, feeds)

	var itemList *view.ItemListData
	if selectedFeedID != 0 {
		itemList, err = store.LoadItemList(r.Context(), a.db, selectedFeedID)
		if err != nil {
			http.Error(w, "failed to load items", http.StatusInternalServerError)

			return
		}
	}

	data := itemListResponseData{
		ItemList:       itemList,
		Feeds:          feeds,
		SelectedFeedID: selectedFeedID,
		FeedEditMode:   feedEditModeEnabled(r),
	}
	a.renderTemplate(w, "delete_feed_response", data)
}

//nolint:cyclop,funlen,gocognit,gosec,revive // Validates proxy request and forwards vetted image responses.
func (a *App) handleImageProxy(w http.ResponseWriter, r *http.Request) {
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
	if err != nil || !content.IsAllowedResolvedProxyURL(r.Context(), target, a.imageProxyLookup) {
		http.Error(w, "invalid url", http.StatusBadRequest)

		return
	}

	req, err := content.BuildImageProxyRequest(r.Context(), target)
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)

		return
	}

	resp, err := a.imageProxyClient.Do(req)
	if err != nil {
		http.Error(w, "upstream fetch failed", http.StatusBadGateway)

		return
	}

	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			log.Printf("image proxy close body: %v", closeErr)
		}
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		slog.Debug(
			"image proxy upstream non-2xx",
			"status", resp.StatusCode,
			"target_host", target.Host,
			"target_path", target.EscapedPath(),
		)

		http.Error(w, "upstream error", http.StatusBadGateway)

		return
	}

	reader := bufio.NewReader(resp.Body)

	sniff, err := reader.Peek(imageProxySniffBytes)
	if err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "upstream read failed", http.StatusBadGateway)

		return
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" || !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		detected := http.DetectContentType(sniff)
		if !strings.HasPrefix(detected, "image/") {
			http.Error(w, "upstream did not return image content", http.StatusUnsupportedMediaType)

			return
		}

		contentType = detected
	}

	body, err := io.ReadAll(io.LimitReader(reader, content.ImageProxyMaxBodyBytes+1))
	if err != nil {
		http.Error(w, "upstream read failed", http.StatusBadGateway)

		return
	}

	if int64(len(body)) > content.ImageProxyMaxBodyBytes {
		http.Error(w, "upstream image too large", http.StatusBadGateway)

		return
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

	w.Header().Set("Content-Length", strconv.Itoa(len(body)))

	_, writeErr := w.Write(body)
	if writeErr != nil {
		log.Printf("image proxy copy: %v", writeErr)
	}
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
	if err != nil {
		return 0
	}

	return parsed
}

func parseSelectedItemID(r *http.Request) int64 {
	err := r.ParseForm()
	if err != nil {
		return 0
	}

	raw := strings.TrimSpace(r.FormValue("selected_item_id"))
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

type feedTitleUpdates struct {
	TitlesByID map[int64]string
	FeedIDs    []int64
}

func parseFeedDeleteUpdates(values url.Values) []int64 {
	feedIDs := make([]int64, 0)
	seen := make(map[int64]struct{})

	for key, rawValues := range values {
		if !containsTruthyValue(rawValues) {
			continue
		}

		feedID, ok := parseFeedIDFromKey(key, "feed_delete_")
		if !ok {
			continue
		}

		if _, exists := seen[feedID]; exists {
			continue
		}

		seen[feedID] = struct{}{}
		feedIDs = append(feedIDs, feedID)
	}

	slices.Sort(feedIDs)

	return feedIDs
}

func containsTruthyValue(values []string) bool {
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "on":
			return true
		}
	}

	return false
}

func parseFeedTitleUpdates(values url.Values) feedTitleUpdates {
	result := feedTitleUpdates{
		FeedIDs:    make([]int64, 0),
		TitlesByID: make(map[int64]string),
	}

	for key, titles := range values {
		feedID, ok := parseFeedIDFromKey(key, "feed_title_")
		if !ok {
			continue
		}

		if _, exists := result.TitlesByID[feedID]; !exists {
			result.FeedIDs = append(result.FeedIDs, feedID)
		}

		result.TitlesByID[feedID] = firstTrimmedValue(titles)
	}

	slices.Sort(result.FeedIDs)

	return result
}

func parseFeedIDFromKey(key, prefix string) (int64, bool) {
	rawID, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return 0, false
	}

	feedID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || feedID <= 0 {
		return 0, false
	}

	return feedID, true
}

func firstTrimmedValue(values []string) string {
	if len(values) == 0 {
		return ""
	}

	return strings.TrimSpace(values[0])
}

func parseFeedOrderUpdates(values url.Values) []int64 {
	rawIDs := values["feed_order"]
	if len(rawIDs) == 0 {
		return nil
	}

	result := make([]int64, 0, len(rawIDs))
	seen := make(map[int64]struct{})

	for _, rawID := range rawIDs {
		feedID, err := strconv.ParseInt(strings.TrimSpace(rawID), 10, 64)
		if err != nil || feedID <= 0 {
			continue
		}

		if _, exists := seen[feedID]; exists {
			continue
		}

		seen[feedID] = struct{}{}
		result = append(result, feedID)
	}

	return result
}

func (a *App) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		a.runCleanupIteration()

		<-ticker.C
	}
}

func (a *App) runCleanupIteration() {
	err := store.CleanupReadItems(a.db)
	if err != nil {
		slog.Error("cleanup error", "err", err)
	}

	if a.authEnabled && a.authManager != nil {
		authErr := a.authManager.CleanupExpiredAuthData(context.Background())
		if authErr != nil {
			slog.Error("auth cleanup error", "err", authErr)
		}
	}
}

func (a *App) refreshLoop() {
	ticker := time.NewTicker(feed.RefreshLoopInterval)
	defer ticker.Stop()

	for {
		err := a.refreshDueFeeds()
		if err != nil {
			slog.Error("refresh loop error", "err", err)
		}

		<-ticker.C
	}
}

func (a *App) refreshDueFeeds() error {
	ids, err := store.ListDueFeeds(a.db, time.Now().UTC(), feed.RefreshBatchSize)
	if err != nil {
		return fmt.Errorf("list due feeds: %w", err)
	}

	if len(ids) > 0 {
		slog.Info("refresh due feeds", "count", len(ids))
	}

	for _, id := range ids {
		a.refreshMu.Lock()
		_, refreshErr := feed.Refresh(context.Background(), a.db, id)
		a.refreshMu.Unlock()

		if refreshErr != nil {
			slog.Error("refresh feed error", "feed_id", id, "err", refreshErr)
		}
	}

	return nil
}
