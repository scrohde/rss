package main

import (
	"bufio"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	_ "modernc.org/sqlite"
)

const (
	maxItemsPerFeed         = 200
	readRetention           = 30 * time.Minute
	refreshInterval         = 20 * time.Minute
	refreshLoopInterval     = 30 * time.Second
	refreshBatchSize        = 5
	refreshBackoffMax       = 12 * time.Hour
	refreshJitterMin        = 0.10
	refreshJitterMax        = 0.20
	feedFetchTimeout        = 15 * time.Second
	imageProxyPath          = "/image-proxy"
	maxImageProxyURLLength  = 4096
	imageProxyTimeout       = 15 * time.Second
	imageProxyCacheFallback = "public, max-age=86400"
	maxErrorLength          = 300
	skipDeleteWarningCookie = "pulse_rss_skip_delete_warning"
)

var imageProxyClient = &http.Client{
	Timeout: imageProxyTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if !isAllowedProxyURL(req.URL) {
			return errors.New("redirect blocked")
		}
		return nil
	},
}

type App struct {
	db        *sql.DB
	tmpl      *template.Template
	refreshMu sync.Mutex
}

type FeedView struct {
	ID                 int64
	Title              string
	URL                string
	ItemCount          int
	UnreadCount        int
	LastRefreshDisplay string
	LastError          string
}

type ItemView struct {
	ID               int64
	Title            string
	Link             string
	SummaryHTML      template.HTML
	PublishedDisplay string
	PublishedCompact string
	IsRead           bool
	IsActive         bool
}

type ItemListData struct {
	Feed     FeedView
	Items    []ItemView
	NewestID int64
	NewItems NewItemsData
}

type PageData struct {
	Feeds             []FeedView
	SelectedFeedID    int64
	ItemList          *ItemListData
	SkipDeleteWarning bool
}

type SubscribeResponseData struct {
	Message           string
	MessageClass      string
	Feeds             []FeedView
	SelectedFeedID    int64
	ItemList          *ItemListData
	Update            bool
	SkipDeleteWarning bool
}

type NewItemsData struct {
	FeedID  int64
	Count   int
	SwapOOB bool
}

type NewItemsResponseData struct {
	Items    []ItemView
	NewestID int64
	Banner   NewItemsData
}

type PollResponseData struct {
	Banner            NewItemsData
	Feeds             []FeedView
	SelectedFeedID    int64
	SkipDeleteWarning bool
}

type ItemListResponseData struct {
	ItemList          *ItemListData
	Feeds             []FeedView
	SelectedFeedID    int64
	SkipDeleteWarning bool
}

type ToggleReadResponseData struct {
	Item              ItemView
	Feeds             []FeedView
	SelectedFeedID    int64
	View              string
	SkipDeleteWarning bool
}

type DeleteFeedConfirmData struct {
	Feed FeedView
	Show bool
}

type RenameFeedFormData struct {
	Feed FeedView
	Show bool
}

type RenameFeedResponseData struct {
	FeedID            int64
	ItemList          *ItemListData
	Feeds             []FeedView
	SelectedFeedID    int64
	SkipDeleteWarning bool
}

func main() {
	setupLogging()
	db, err := openDB("rss.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := initDB(db); err != nil {
		log.Fatal(err)
	}

	tmpl := template.Must(template.ParseGlob("templates/*.html"))
	tmpl = template.Must(tmpl.ParseGlob("templates/partials/*.html"))

	app := &App{db: db, tmpl: tmpl}

	go app.cleanupLoop()
	go app.refreshLoop()

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.HandleFunc("/", app.route)

	server := &http.Server{
		Addr:         resolveAddr(),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	slog.Info("rss reader running", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func setupLogging() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(handler))
}

func resolveAddr() string {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		return ":8080"
	}
	if strings.HasPrefix(port, ":") {
		return port
	}
	if _, err := strconv.Atoi(port); err != nil {
		return ":8080"
	}
	return ":" + port
}

func openDB(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite behaves best with a single connection for this workload.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, err
	}
	return db, nil
}

func initDB(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS feeds (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	url TEXT NOT NULL UNIQUE,
	title TEXT NOT NULL,
	custom_title TEXT,
	created_at DATETIME NOT NULL,
	etag TEXT,
	last_modified TEXT,
	last_refreshed_at DATETIME,
	last_error TEXT,
	unchanged_count INTEGER NOT NULL DEFAULT 0,
	next_refresh_at DATETIME
);

CREATE TABLE IF NOT EXISTS items (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	feed_id INTEGER NOT NULL,
	guid TEXT NOT NULL,
	title TEXT NOT NULL,
	link TEXT NOT NULL,
	summary TEXT,
	content TEXT,
	published_at DATETIME,
	read_at DATETIME,
	created_at DATETIME NOT NULL,
	UNIQUE(feed_id, guid),
	FOREIGN KEY(feed_id) REFERENCES feeds(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS tombstones (
	feed_id INTEGER NOT NULL,
	guid TEXT NOT NULL,
	deleted_at DATETIME NOT NULL,
	PRIMARY KEY (feed_id, guid),
	FOREIGN KEY(feed_id) REFERENCES feeds(id) ON DELETE CASCADE
);

CREATE TRIGGER IF NOT EXISTS tombstones_prune
AFTER INSERT ON tombstones
BEGIN
	DELETE FROM tombstones
	WHERE datetime(deleted_at) <= datetime('now', '-30 days');
END;
`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	return nil
}

func (a *App) route(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/static/") {
		http.NotFound(w, r)
		return
	}

	if r.URL.Path == "/" && r.Method == http.MethodGet {
		a.handleIndex(w, r)
		return
	}

	if r.URL.Path == imageProxyPath {
		a.handleImageProxy(w, r)
		return
	}

	parts := pathParts(r.URL.Path)
	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}

	switch parts[0] {
	case "feeds":
		if r.Method == http.MethodPost && len(parts) == 1 {
			a.handleSubscribe(w, r)
			return
		}
		if r.Method == http.MethodGet && len(parts) == 4 && parts[2] == "delete" && parts[3] == "confirm" {
			feedID, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			a.handleDeleteFeedConfirm(w, r, feedID)
			return
		}
		if r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "delete" {
			feedID, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			a.handleDeleteFeed(w, r, feedID)
			return
		}
		if r.Method == http.MethodGet && len(parts) == 3 && parts[2] == "rename" {
			feedID, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			a.handleRenameFeedForm(w, r, feedID)
			return
		}
		if r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "rename" {
			feedID, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			a.handleRenameFeed(w, r, feedID)
			return
		}
		if len(parts) >= 3 && parts[2] == "items" {
			feedID, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			switch {
			case r.Method == http.MethodGet && len(parts) == 3:
				a.handleFeedItems(w, r, feedID)
				return
			case r.Method == http.MethodGet && len(parts) == 4 && parts[3] == "new":
				a.handleFeedItemsNew(w, r, feedID)
				return
			case r.Method == http.MethodGet && len(parts) == 4 && parts[3] == "poll":
				a.handleFeedItemsPoll(w, r, feedID)
				return
			case r.Method == http.MethodPost && len(parts) == 4 && parts[3] == "read":
				a.handleMarkAllRead(w, r, feedID)
				return
			}
		}
	case "items":
		if len(parts) >= 2 {
			itemID, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			switch {
			case r.Method == http.MethodGet && len(parts) == 2:
				a.handleItemExpanded(w, r, itemID)
				return
			case r.Method == http.MethodGet && len(parts) == 3 && parts[2] == "compact":
				a.handleItemCompact(w, r, itemID)
				return
			case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "toggle":
				a.handleToggleRead(w, r, itemID)
				return
			}
		}
	}

	http.NotFound(w, r)
}

func pathParts(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
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

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	feeds, err := listFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	data := PageData{
		Feeds:             feeds,
		SkipDeleteWarning: deleteWarningSkipped(r),
	}
	a.renderTemplate(w, "index", data)
}

func (a *App) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	rawURL := r.FormValue("url")
	feedURL, err := normalizeFeedURL(rawURL)
	if err != nil {
		a.renderSubscribeError(w, err)
		return
	}

	start := time.Now()
	slog.Info("subscribe feed", "feed_url", feedURL)
	result, err := fetchFeed(feedURL, "", "")
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

	feedID, err := upsertFeed(a.db, feedURL, feedTitle)
	if err != nil {
		slog.Error("subscribe upsert feed failed", "feed_url", feedURL, "err", err)
		a.renderSubscribeError(w, err)
		return
	}

	inserted, err := upsertItems(a.db, feedID, result.Feed.Items)
	if err != nil {
		slog.Error("subscribe upsert items failed", "feed_id", feedID, "feed_url", feedURL, "err", err)
		a.renderSubscribeError(w, err)
		return
	}

	if err := enforceItemLimit(a.db, feedID); err != nil {
		slog.Error("subscribe enforce item limit failed", "feed_id", feedID, "feed_url", feedURL, "err", err)
		a.renderSubscribeError(w, err)
		return
	}

	duration := time.Since(start).Milliseconds()
	checkedAt := time.Now().UTC()
	if err := updateFeedRefreshMeta(a.db, feedID, FeedRefreshMeta{
		ETag:           chooseHeader(result.ETag, ""),
		LastModified:   chooseHeader(result.LastModified, ""),
		LastCheckedAt:  checkedAt,
		LastError:      "",
		UnchangedCount: 0,
		NextRefreshAt:  nextRefreshAt(checkedAt, 0),
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

	feeds, err := listFeeds(a.db)
	if err != nil {
		a.renderSubscribeError(w, err)
		return
	}

	itemList, err := loadItemList(a.db, feedID)
	if err != nil {
		a.renderSubscribeError(w, err)
		return
	}

	data := SubscribeResponseData{
		Feeds:             feeds,
		SelectedFeedID:    feedID,
		ItemList:          itemList,
		Update:            true,
		SkipDeleteWarning: deleteWarningSkipped(r),
	}

	a.renderTemplate(w, "subscribe_response", data)
}

func (a *App) renderSubscribeError(w http.ResponseWriter, err error) {
	data := SubscribeResponseData{
		Message:      err.Error(),
		MessageClass: "error",
		Update:       false,
	}
	a.renderTemplate(w, "subscribe_response", data)
}

func (a *App) handleFeedItems(w http.ResponseWriter, r *http.Request, feedID int64) {
	itemList, err := loadItemList(a.db, feedID)
	if err != nil {
		http.Error(w, "failed to load items", http.StatusInternalServerError)
		return
	}
	a.renderTemplate(w, "item_list", itemList)
}

func (a *App) handleFeedItemsPoll(w http.ResponseWriter, r *http.Request, feedID int64) {
	afterID := parseAfterID(r)

	count, err := countItemsAfter(a.db, feedID, afterID)
	if err != nil {
		http.Error(w, "failed to check new items", http.StatusInternalServerError)
		return
	}

	feeds, err := listFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	data := PollResponseData{
		Banner:            NewItemsData{FeedID: feedID, Count: count},
		Feeds:             feeds,
		SelectedFeedID:    feedID,
		SkipDeleteWarning: deleteWarningSkipped(r),
	}
	a.renderTemplate(w, "poll_response", data)
}

func (a *App) handleFeedItemsNew(w http.ResponseWriter, r *http.Request, feedID int64) {
	afterID := parseAfterID(r)

	items, err := listItemsAfter(a.db, feedID, afterID)
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

	data := NewItemsResponseData{
		Items:    items,
		NewestID: newestID,
		Banner:   NewItemsData{FeedID: feedID, Count: 0, SwapOOB: true},
	}
	a.renderTemplate(w, "item_new_response", data)
}

func (a *App) handleItemExpanded(w http.ResponseWriter, r *http.Request, itemID int64) {
	item, err := getItemView(a.db, itemID)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}
	item.IsActive = parseSelectedItemID(r) == item.ID
	a.renderTemplate(w, "item_expanded", item)
}

func (a *App) handleItemCompact(w http.ResponseWriter, r *http.Request, itemID int64) {
	item, err := getItemView(a.db, itemID)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}
	item.IsActive = parseSelectedItemID(r) == item.ID
	a.renderTemplate(w, "item_compact", item)
}

func (a *App) handleToggleRead(w http.ResponseWriter, r *http.Request, itemID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	view := r.FormValue("view")
	if err := toggleRead(a.db, itemID); err != nil {
		http.Error(w, "failed to update item", http.StatusInternalServerError)
		return
	}
	slog.Info("item read toggled", "item_id", itemID, "view", view)

	feedID, err := getFeedIDByItem(a.db, itemID)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}

	item, err := getItemView(a.db, itemID)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}
	item.IsActive = parseSelectedItemID(r) == item.ID

	feeds, err := listFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	data := ToggleReadResponseData{
		Item:              item,
		Feeds:             feeds,
		SelectedFeedID:    feedID,
		View:              view,
		SkipDeleteWarning: deleteWarningSkipped(r),
	}
	a.renderTemplate(w, "item_toggle_response", data)
}

func (a *App) handleMarkAllRead(w http.ResponseWriter, r *http.Request, feedID int64) {
	if err := markAllRead(a.db, feedID); err != nil {
		http.Error(w, "failed to update items", http.StatusInternalServerError)
		return
	}
	slog.Info("feed items marked read", "feed_id", feedID)

	itemList, err := loadItemList(a.db, feedID)
	if err != nil {
		http.Error(w, "failed to load items", http.StatusInternalServerError)
		return
	}

	feeds, err := listFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	data := ItemListResponseData{
		ItemList:          itemList,
		Feeds:             feeds,
		SelectedFeedID:    feedID,
		SkipDeleteWarning: deleteWarningSkipped(r),
	}
	a.renderTemplate(w, "item_list_response", data)
}

func (a *App) handleDeleteFeedConfirm(w http.ResponseWriter, r *http.Request, feedID int64) {
	if deleteWarningSkipped(r) || r.URL.Query().Get("cancel") == "1" {
		data := DeleteFeedConfirmData{Feed: FeedView{ID: feedID}, Show: false}
		a.renderTemplate(w, "feed_remove_confirm", data)
		return
	}

	feed, err := getFeed(a.db, feedID)
	if err != nil {
		http.Error(w, "feed not found", http.StatusNotFound)
		return
	}

	data := DeleteFeedConfirmData{Feed: feed, Show: true}
	a.renderTemplate(w, "feed_remove_confirm", data)
}

func (a *App) handleDeleteFeed(w http.ResponseWriter, r *http.Request, feedID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	var selectedFeedID int64
	if selected := r.FormValue("selected_feed_id"); selected != "" {
		if parsed, err := strconv.ParseInt(selected, 10, 64); err == nil {
			selectedFeedID = parsed
		}
	}

	if err := deleteFeed(a.db, feedID); err != nil {
		http.Error(w, "failed to delete feed", http.StatusInternalServerError)
		return
	}
	slog.Info("feed deleted", "feed_id", feedID)

	skipDeleteWarning := deleteWarningSkipped(r)
	if r.FormValue("skip_delete_warning") != "" {
		setSkipDeleteWarningCookie(w)
		skipDeleteWarning = true
	}

	feeds, err := listFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	selectedFeedID = selectRemainingFeed(selectedFeedID, feedID, feeds)

	var itemList *ItemListData
	if selectedFeedID != 0 {
		itemList, err = loadItemList(a.db, selectedFeedID)
		if err != nil {
			http.Error(w, "failed to load items", http.StatusInternalServerError)
			return
		}
	}

	data := ItemListResponseData{
		ItemList:          itemList,
		Feeds:             feeds,
		SelectedFeedID:    selectedFeedID,
		SkipDeleteWarning: skipDeleteWarning,
	}
	a.renderTemplate(w, "delete_feed_response", data)
}

func (a *App) handleRenameFeedForm(w http.ResponseWriter, r *http.Request, feedID int64) {
	if r.URL.Query().Get("cancel") == "1" {
		data := RenameFeedFormData{Feed: FeedView{ID: feedID}, Show: false}
		a.renderTemplate(w, "feed_rename_form", data)
		return
	}

	feed, err := getFeed(a.db, feedID)
	if err != nil {
		http.Error(w, "feed not found", http.StatusNotFound)
		return
	}

	data := RenameFeedFormData{Feed: feed, Show: true}
	a.renderTemplate(w, "feed_rename_form", data)
}

func (a *App) handleRenameFeed(w http.ResponseWriter, r *http.Request, feedID int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))

	if err := updateFeedTitle(a.db, feedID, title); err != nil {
		http.Error(w, "failed to rename feed", http.StatusInternalServerError)
		return
	}
	slog.Info("feed renamed", "feed_id", feedID, "title", title)

	var selectedFeedID int64
	if selected := r.FormValue("selected_feed_id"); selected != "" {
		if parsed, err := strconv.ParseInt(selected, 10, 64); err == nil {
			selectedFeedID = parsed
		}
	}

	feeds, err := listFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	var itemList *ItemListData
	if selectedFeedID == feedID && selectedFeedID != 0 {
		itemList, err = loadItemList(a.db, selectedFeedID)
		if err != nil {
			http.Error(w, "failed to load items", http.StatusInternalServerError)
			return
		}
	}

	data := RenameFeedResponseData{
		FeedID:            feedID,
		ItemList:          itemList,
		Feeds:             feeds,
		SelectedFeedID:    selectedFeedID,
		SkipDeleteWarning: deleteWarningSkipped(r),
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
	if len(raw) > maxImageProxyURLLength {
		http.Error(w, "url too long", http.StatusRequestURITooLong)
		return
	}

	target, err := url.Parse(raw)
	if err != nil || !isAllowedProxyURL(target) {
		http.Error(w, "invalid url", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	req.Header.Set("User-Agent", "PulseRSS/1.0")
	req.Header.Set("Accept", "image/*,*/*;q=0.8")
	req.Header.Set("Referer", fmt.Sprintf("%s://%s/", target.Scheme, target.Host))

	resp, err := imageProxyClient.Do(req)
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
		w.Header().Set("Cache-Control", imageProxyCacheFallback)
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

func normalizeFeedURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("feed URL is required")
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	u, err := url.ParseRequestURI(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("feed URL looks invalid")
	}
	return u.String(), nil
}

type FeedFetchResult struct {
	Feed         *gofeed.Feed
	ETag         string
	LastModified string
	NotModified  bool
	StatusCode   int
}

func fetchFeed(feedURL, etag, lastModified string) (*FeedFetchResult, error) {
	req, err := http.NewRequest(http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "PulseRSS/1.0")
	if strings.TrimSpace(etag) != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if strings.TrimSpace(lastModified) != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	client := &http.Client{Timeout: feedFetchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch feed: %w", err)
	}
	defer resp.Body.Close()

	result := &FeedFetchResult{
		ETag:         strings.TrimSpace(resp.Header.Get("ETag")),
		LastModified: strings.TrimSpace(resp.Header.Get("Last-Modified")),
		StatusCode:   resp.StatusCode,
	}

	if resp.StatusCode == http.StatusNotModified {
		result.NotModified = true
		return result, nil
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("unexpected status %d from feed", resp.StatusCode)
	}

	parser := gofeed.NewParser()
	feed, err := parser.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse feed: %w", err)
	}
	result.Feed = feed
	return result, nil
}

func refreshFeed(db *sql.DB, feedID int64) (int64, error) {
	feedURL, err := getFeedURL(db, feedID)
	if err != nil {
		slog.Error("refresh feed lookup failed", "feed_id", feedID, "err", err)
		return 0, err
	}

	cache, err := getFeedCacheMeta(db, feedID)
	if err != nil {
		slog.Error("refresh feed cache lookup failed", "feed_id", feedID, "feed_url", feedURL, "err", err)
		return 0, err
	}

	start := time.Now()
	result, err := fetchFeed(feedURL, cache.ETag, cache.LastModified)
	duration := time.Since(start).Milliseconds()
	checkedAt := time.Now().UTC()

	meta := FeedRefreshMeta{
		LastCheckedAt: checkedAt,
	}

	if err != nil {
		meta.LastError = truncateString(err.Error(), maxErrorLength)
		meta.UnchangedCount = 0
		meta.NextRefreshAt = nextRefreshAt(checkedAt, meta.UnchangedCount)
		_ = updateFeedRefreshMeta(db, feedID, meta)
		slog.Error("refresh feed fetch failed",
			"feed_id", feedID,
			"feed_url", feedURL,
			"duration_ms", duration,
			"err", err,
		)
		return 0, err
	}

	meta.LastError = ""
	meta.ETag = chooseHeader(result.ETag, cache.ETag)
	meta.LastModified = chooseHeader(result.LastModified, cache.LastModified)

	if result.NotModified {
		meta.UnchangedCount = cache.UnchangedCount + 1
		meta.NextRefreshAt = nextRefreshAt(checkedAt, meta.UnchangedCount)
		if err := updateFeedRefreshMeta(db, feedID, meta); err != nil {
			return 0, err
		}
		slog.Info("refresh feed cache hit",
			"feed_id", feedID,
			"feed_url", feedURL,
			"status", result.StatusCode,
			"duration_ms", duration,
		)
		return feedID, nil
	}

	if result.Feed == nil {
		meta.LastError = "feed returned no content"
		meta.UnchangedCount = 0
		meta.NextRefreshAt = nextRefreshAt(checkedAt, meta.UnchangedCount)
		_ = updateFeedRefreshMeta(db, feedID, meta)
		slog.Warn("refresh feed returned no content",
			"feed_id", feedID,
			"feed_url", feedURL,
			"status", result.StatusCode,
		)
		return 0, errors.New(meta.LastError)
	}

	feedTitle := strings.TrimSpace(result.Feed.Title)
	if feedTitle == "" {
		feedTitle = feedURL
	}

	updatedID, err := upsertFeed(db, feedURL, feedTitle)
	if err != nil {
		meta.LastError = truncateString(err.Error(), maxErrorLength)
		_ = updateFeedRefreshMeta(db, feedID, meta)
		slog.Error("refresh upsert feed failed", "feed_id", feedID, "feed_url", feedURL, "err", err)
		return 0, err
	}

	inserted, err := upsertItems(db, updatedID, result.Feed.Items)
	if err != nil {
		meta.LastError = truncateString(err.Error(), maxErrorLength)
		meta.UnchangedCount = 0
		meta.NextRefreshAt = nextRefreshAt(checkedAt, meta.UnchangedCount)
		_ = updateFeedRefreshMeta(db, feedID, meta)
		slog.Error("refresh upsert items failed", "feed_id", feedID, "feed_url", feedURL, "err", err)
		return 0, err
	}

	if err := enforceItemLimit(db, updatedID); err != nil {
		meta.LastError = truncateString(err.Error(), maxErrorLength)
		meta.UnchangedCount = 0
		meta.NextRefreshAt = nextRefreshAt(checkedAt, meta.UnchangedCount)
		_ = updateFeedRefreshMeta(db, feedID, meta)
		slog.Error("refresh enforce item limit failed", "feed_id", feedID, "feed_url", feedURL, "err", err)
		return 0, err
	}

	if inserted == 0 {
		meta.UnchangedCount = cache.UnchangedCount + 1
	} else {
		meta.UnchangedCount = 0
	}
	meta.NextRefreshAt = nextRefreshAt(checkedAt, meta.UnchangedCount)
	if err := updateFeedRefreshMeta(db, updatedID, meta); err != nil {
		return 0, err
	}

	slog.Info("refresh feed updated",
		"feed_id", updatedID,
		"feed_url", feedURL,
		"title", feedTitle,
		"status", result.StatusCode,
		"items_in_feed", len(result.Feed.Items),
		"items_new", inserted,
		"duration_ms", duration,
	)
	return updatedID, nil
}

type FeedCacheMeta struct {
	ETag           string
	LastModified   string
	UnchangedCount int
}

type FeedRefreshMeta struct {
	ETag           string
	LastModified   string
	LastCheckedAt  time.Time
	LastError      string
	UnchangedCount int
	NextRefreshAt  time.Time
}

func getFeedCacheMeta(db *sql.DB, feedID int64) (FeedCacheMeta, error) {
	var (
		etag           sql.NullString
		lastModified   sql.NullString
		unchangedCount sql.NullInt64
	)
	if err := db.QueryRow(`
SELECT etag, last_modified, unchanged_count
FROM feeds
WHERE id = ?
`, feedID).Scan(&etag, &lastModified, &unchangedCount); err != nil {
		return FeedCacheMeta{}, err
	}
	return FeedCacheMeta{
		ETag:           strings.TrimSpace(etag.String),
		LastModified:   strings.TrimSpace(lastModified.String),
		UnchangedCount: int(unchangedCount.Int64),
	}, nil
}

func updateFeedRefreshMeta(db *sql.DB, feedID int64, meta FeedRefreshMeta) error {
	if meta.LastCheckedAt.IsZero() {
		meta.LastCheckedAt = time.Now().UTC()
	}
	if meta.UnchangedCount < 0 {
		meta.UnchangedCount = 0
	}
	if meta.NextRefreshAt.IsZero() {
		meta.NextRefreshAt = nextRefreshAt(meta.LastCheckedAt, meta.UnchangedCount)
	}
	_, err := db.Exec(`
UPDATE feeds
SET etag = COALESCE(?, etag),
    last_modified = COALESCE(?, last_modified),
    last_refreshed_at = ?,
    last_error = ?,
    unchanged_count = ?,
    next_refresh_at = ?
WHERE id = ?
`,
		nullString(meta.ETag),
		nullString(meta.LastModified),
		meta.LastCheckedAt,
		nullString(meta.LastError),
		meta.UnchangedCount,
		meta.NextRefreshAt,
		feedID,
	)
	return err
}

func upsertFeed(db *sql.DB, feedURL, title string) (int64, error) {
	now := time.Now().UTC()
	_, err := db.Exec(`
INSERT INTO feeds (url, title, created_at)
VALUES (?, ?, ?)
ON CONFLICT(url) DO UPDATE SET title = excluded.title
`, feedURL, title, now)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := db.QueryRow("SELECT id FROM feeds WHERE url = ?", feedURL).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func updateFeedTitle(db *sql.DB, feedID int64, title string) error {
	_, err := db.Exec("UPDATE feeds SET custom_title = ? WHERE id = ?", nullString(title), feedID)
	return err
}

func deleteFeed(db *sql.DB, feedID int64) error {
	_, err := db.Exec("DELETE FROM feeds WHERE id = ?", feedID)
	return err
}

func upsertItems(db *sql.DB, feedID int64, items []*gofeed.Item) (int, error) {
	now := time.Now().UTC()
	stmt, err := db.Prepare(`
INSERT OR IGNORE INTO items
(feed_id, guid, title, link, summary, content, published_at, created_at)
SELECT ?, ?, ?, ?, ?, ?, ?, ?
WHERE NOT EXISTS (
	SELECT 1 FROM tombstones WHERE feed_id = ? AND guid = ?
)
`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	inserted := 0
	for idx, item := range items {
		guid := strings.TrimSpace(item.GUID)
		if guid == "" {
			guid = strings.TrimSpace(item.Link)
		}
		if guid == "" {
			guid = strings.TrimSpace(item.Title)
		}
		if guid == "" && item.PublishedParsed != nil {
			guid = item.PublishedParsed.UTC().Format(time.RFC3339Nano)
		}
		if guid == "" {
			guid = fmt.Sprintf("feed-%d-item-%d", feedID, idx)
		}

		publishedAt := sql.NullTime{}
		if item.PublishedParsed != nil {
			publishedAt = sql.NullTime{Time: item.PublishedParsed.UTC(), Valid: true}
		} else if item.UpdatedParsed != nil {
			publishedAt = sql.NullTime{Time: item.UpdatedParsed.UTC(), Valid: true}
		}

		summary := strings.TrimSpace(item.Description)
		content := strings.TrimSpace(item.Content)
		res, err := stmt.Exec(
			feedID,
			guid,
			fallbackString(item.Title, "(untitled)"),
			fallbackString(item.Link, "#"),
			summary,
			content,
			nullTimeToValue(publishedAt),
			now,
			feedID,
			guid,
		)
		if err != nil {
			return inserted, err
		}
		if affected, err := res.RowsAffected(); err == nil && affected > 0 {
			inserted += int(affected)
		}
	}

	return inserted, nil
}

func enforceItemLimit(db *sql.DB, feedID int64) error {
	now := time.Now().UTC()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(`
INSERT OR IGNORE INTO tombstones (feed_id, guid, deleted_at)
SELECT feed_id, guid, ?
FROM items
WHERE feed_id = ?
  AND id NOT IN (
	SELECT id FROM items
	WHERE feed_id = ?
	ORDER BY COALESCE(published_at, created_at) DESC, id DESC
	LIMIT ?
  )
`, now, feedID, feedID, maxItemsPerFeed); err != nil {
		return err
	}
	if _, err = tx.Exec(`
DELETE FROM items
WHERE feed_id = ?
  AND id NOT IN (
	SELECT id FROM items
	WHERE feed_id = ?
	ORDER BY COALESCE(published_at, created_at) DESC, id DESC
	LIMIT ?
  )
`, feedID, feedID, maxItemsPerFeed); err != nil {
		return err
	}
	return tx.Commit()
}

func listFeeds(db *sql.DB) ([]FeedView, error) {
	rows, err := db.Query(`
SELECT f.id, COALESCE(f.custom_title, f.title) AS display_title, f.url,
       (SELECT COUNT(*) FROM items i WHERE i.feed_id = f.id) AS item_count,
       (SELECT COUNT(*) FROM items i WHERE i.feed_id = f.id AND i.read_at IS NULL) AS unread_count,
       f.last_refreshed_at,
       f.last_error
FROM feeds f
ORDER BY display_title COLLATE NOCASE
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var feeds []FeedView
	for rows.Next() {
		var (
			id          int64
			title       string
			url         string
			itemCount   int
			unreadCount int
			lastChecked sql.NullTime
			lastError   sql.NullString
		)
		if err := rows.Scan(&id, &title, &url, &itemCount, &unreadCount, &lastChecked, &lastError); err != nil {
			return nil, err
		}
		feeds = append(feeds, buildFeedView(id, title, url, itemCount, unreadCount, lastChecked, lastError))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slog.Info("db list feeds", "count", len(feeds))
	return feeds, nil
}

func selectRemainingFeed(selectedID, deletedID int64, feeds []FeedView) int64 {
	if len(feeds) == 0 {
		return 0
	}
	if selectedID != 0 && selectedID != deletedID {
		for _, feed := range feeds {
			if feed.ID == selectedID {
				return selectedID
			}
		}
	}
	return feeds[0].ID
}

func loadItemList(db *sql.DB, feedID int64) (*ItemListData, error) {
	feed, err := getFeed(db, feedID)
	if err != nil {
		return nil, err
	}
	items, err := listItems(db, feedID)
	if err != nil {
		return nil, err
	}
	newestID := maxItemID(items)
	return &ItemListData{
		Feed:     feed,
		Items:    items,
		NewestID: newestID,
		NewItems: NewItemsData{FeedID: feed.ID, Count: 0},
	}, nil
}

func getFeed(db *sql.DB, feedID int64) (FeedView, error) {
	row := db.QueryRow(`
SELECT f.id, COALESCE(f.custom_title, f.title) AS display_title, f.url,
       (SELECT COUNT(*) FROM items i WHERE i.feed_id = f.id) AS item_count,
       (SELECT COUNT(*) FROM items i WHERE i.feed_id = f.id AND i.read_at IS NULL) AS unread_count,
       f.last_refreshed_at,
       f.last_error
FROM feeds f
WHERE f.id = ?
`, feedID)
	var (
		id          int64
		title       string
		url         string
		itemCount   int
		unreadCount int
		lastChecked sql.NullTime
		lastError   sql.NullString
	)
	if err := row.Scan(&id, &title, &url, &itemCount, &unreadCount, &lastChecked, &lastError); err != nil {
		return FeedView{}, err
	}
	slog.Info("db get feed", "feed_id", feedID)
	return buildFeedView(id, title, url, itemCount, unreadCount, lastChecked, lastError), nil
}

func getFeedURL(db *sql.DB, feedID int64) (string, error) {
	var u string
	if err := db.QueryRow("SELECT url FROM feeds WHERE id = ?", feedID).Scan(&u); err != nil {
		return "", err
	}
	return u, nil
}

func listDueFeeds(db *sql.DB, now time.Time, limit int) ([]int64, error) {
	rows, err := db.Query(`
	SELECT id
	FROM feeds
	WHERE next_refresh_at IS NULL OR next_refresh_at <= ?
	ORDER BY COALESCE(next_refresh_at, created_at)
	LIMIT ?
	`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func listItems(db *sql.DB, feedID int64) ([]ItemView, error) {
	rows, err := db.Query(`
SELECT id, title, link, summary, content, published_at, read_at
FROM items
WHERE feed_id = ?
ORDER BY COALESCE(published_at, created_at) DESC, id DESC
`, feedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ItemView
	for rows.Next() {
		item, err := scanItemView(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slog.Info("db list items", "feed_id", feedID, "count", len(items))
	return items, nil
}

func listItemsAfter(db *sql.DB, feedID, afterID int64) ([]ItemView, error) {
	rows, err := db.Query(`
SELECT id, title, link, summary, content, published_at, read_at
FROM items
WHERE feed_id = ? AND id > ?
ORDER BY COALESCE(published_at, created_at) DESC, id DESC
`, feedID, afterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ItemView
	for rows.Next() {
		item, err := scanItemView(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slog.Info("db list items after", "feed_id", feedID, "after_id", afterID, "count", len(items))
	return items, nil
}

func countItemsAfter(db *sql.DB, feedID, afterID int64) (int, error) {
	var count int
	if err := db.QueryRow(`
SELECT COUNT(*)
FROM items
WHERE feed_id = ? AND id > ?
`, feedID, afterID).Scan(&count); err != nil {
		return 0, err
	}
	slog.Info("db count items after", "feed_id", feedID, "after_id", afterID, "count", count)
	return count, nil
}

func maxItemID(items []ItemView) int64 {
	var maxID int64
	for _, item := range items {
		if item.ID > maxID {
			maxID = item.ID
		}
	}
	return maxID
}

func buildFeedView(id int64, title, url string, itemCount, unreadCount int, lastChecked sql.NullTime, lastError sql.NullString) FeedView {
	refreshDisplay := "Never"
	if lastChecked.Valid {
		refreshDisplay = formatTime(lastChecked.Time)
	}
	errText := ""
	if lastError.Valid {
		errText = lastError.String
	}
	return FeedView{
		ID:                 id,
		Title:              title,
		URL:                url,
		ItemCount:          itemCount,
		UnreadCount:        unreadCount,
		LastRefreshDisplay: refreshDisplay,
		LastError:          errText,
	}
}

func getItemView(db *sql.DB, itemID int64) (ItemView, error) {
	row := db.QueryRow(`
SELECT id, title, link, summary, content, published_at, read_at
FROM items
WHERE id = ?
`, itemID)

	var (
		id        int64
		title     string
		link      string
		summary   sql.NullString
		content   sql.NullString
		published sql.NullTime
		readAt    sql.NullTime
	)
	if err := row.Scan(&id, &title, &link, &summary, &content, &published, &readAt); err != nil {
		return ItemView{}, err
	}
	slog.Info("db get item", "item_id", itemID)
	return buildItemView(id, title, link, summary, content, published, readAt), nil
}

func getFeedIDByItem(db *sql.DB, itemID int64) (int64, error) {
	var feedID int64
	if err := db.QueryRow("SELECT feed_id FROM items WHERE id = ?", itemID).Scan(&feedID); err != nil {
		return 0, err
	}
	return feedID, nil
}

func scanItemView(rows *sql.Rows) (ItemView, error) {
	var (
		id        int64
		title     string
		link      string
		summary   sql.NullString
		content   sql.NullString
		published sql.NullTime
		readAt    sql.NullTime
	)
	if err := rows.Scan(&id, &title, &link, &summary, &content, &published, &readAt); err != nil {
		return ItemView{}, err
	}
	return buildItemView(id, title, link, summary, content, published, readAt), nil
}

func buildItemView(id int64, title, link string, summary, content sql.NullString, published, readAt sql.NullTime) ItemView {
	summaryHTML := pickSummaryHTML(summary, content)
	publishedDisplay := "Unpublished"
	publishedCompact := "na"
	if published.Valid {
		publishedDisplay = formatTime(published.Time)
		publishedCompact = formatRelativeShort(published.Time, time.Now())
	}
	return ItemView{
		ID:               id,
		Title:            title,
		Link:             link,
		SummaryHTML:      summaryHTML,
		PublishedDisplay: publishedDisplay,
		PublishedCompact: publishedCompact,
		IsRead:           readAt.Valid,
	}
}

func pickSummaryHTML(summary, content sql.NullString) template.HTML {
	text := ""
	if content.Valid && strings.TrimSpace(content.String) != "" {
		text = content.String
	} else if summary.Valid && strings.TrimSpace(summary.String) != "" {
		text = summary.String
	}
	if text == "" {
		text = "<p>No summary available.</p>"
	}
	text = rewriteSummaryImages(text)
	return template.HTML(text)
}

func rewriteSummaryImages(text string) string {
	if !strings.Contains(text, "<img") && !strings.Contains(text, "<source") {
		return text
	}
	root := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	nodes, err := html.ParseFragment(strings.NewReader(text), root)
	if err != nil {
		return text
	}
	changed := false
	for _, node := range nodes {
		if rewriteImageNode(node) {
			changed = true
		}
	}
	if !changed {
		return text
	}
	var b strings.Builder
	for _, node := range nodes {
		_ = html.Render(&b, node)
	}
	return b.String()
}

func rewriteImageNode(node *html.Node) bool {
	changed := false
	if node.Type == html.ElementNode {
		switch node.Data {
		case "img":
			if rewriteAttr(node, "src", proxyImageURL) {
				changed = true
			}
			if rewriteAttr(node, "srcset", rewriteSrcset) {
				changed = true
			}
		case "source":
			if rewriteAttr(node, "srcset", rewriteSrcset) {
				changed = true
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if rewriteImageNode(child) {
			changed = true
		}
	}
	return changed
}

func rewriteAttr(node *html.Node, key string, rewrite func(string) (string, bool)) bool {
	for i, attr := range node.Attr {
		if attr.Key != key {
			continue
		}
		if updated, ok := rewrite(attr.Val); ok {
			node.Attr[i].Val = updated
			return true
		}
		return false
	}
	return false
}

func rewriteSrcset(value string) (string, bool) {
	parts := strings.Split(value, ",")
	changed := false
	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		if updated, ok := proxyImageURL(fields[0]); ok {
			fields[0] = updated
			changed = true
		}
		parts[i] = strings.Join(fields, " ")
	}
	if !changed {
		return value, false
	}
	return strings.Join(parts, ", "), true
}

func proxyImageURL(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return raw, false
	}
	if strings.HasPrefix(trimmed, imageProxyPath+"?") {
		return raw, false
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "data:") {
		return raw, false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return raw, false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return raw, false
	}
	if !isAllowedProxyURL(parsed) {
		return raw, false
	}
	return imageProxyPath + "?url=" + url.QueryEscape(parsed.String()), true
}

func isAllowedProxyURL(target *url.URL) bool {
	if target == nil {
		return false
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return false
	}
	if target.Hostname() == "" {
		return false
	}
	return !isDisallowedHost(target.Hostname())
}

func isDisallowedHost(host string) bool {
	hostname := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if hostname == "" || hostname == "localhost" {
		return true
	}
	if ip := net.ParseIP(hostname); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return true
		}
	}
	return false
}

func formatTime(t time.Time) string {
	return t.Local().Format("Jan 2, 2006 - 3:04 PM")
}

func formatRelativeShort(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "na"
	}
	age := now.Sub(t)
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Minute:
		return fmt.Sprintf("%ds", int(age.Seconds()))
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh", int(age.Hours()))
	case age < 365*24*time.Hour:
		return fmt.Sprintf("%dd", int(age.Hours()/24))
	default:
		return fmt.Sprintf("%dy", int(age.Hours()/(24*365)))
	}
}

func toggleRead(db *sql.DB, itemID int64) error {
	var readAt sql.NullTime
	if err := db.QueryRow("SELECT read_at FROM items WHERE id = ?", itemID).Scan(&readAt); err != nil {
		return err
	}
	if readAt.Valid {
		_, err := db.Exec("UPDATE items SET read_at = NULL WHERE id = ?", itemID)
		return err
	}
	_, err := db.Exec("UPDATE items SET read_at = ? WHERE id = ?", time.Now().UTC(), itemID)
	return err
}

func markAllRead(db *sql.DB, feedID int64) error {
	_, err := db.Exec(`
UPDATE items
SET read_at = ?
WHERE feed_id = ? AND read_at IS NULL
`, time.Now().UTC(), feedID)
	return err
}

func (a *App) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		if err := cleanupReadItems(a.db); err != nil {
			slog.Error("cleanup error", "err", err)
		}
		<-ticker.C
	}
}

func (a *App) refreshLoop() {
	ticker := time.NewTicker(refreshLoopInterval)
	defer ticker.Stop()
	for {
		if err := a.refreshDueFeeds(); err != nil {
			slog.Error("refresh loop error", "err", err)
		}
		<-ticker.C
	}
}

func (a *App) refreshDueFeeds() error {
	ids, err := listDueFeeds(a.db, time.Now().UTC(), refreshBatchSize)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		slog.Info("refresh due feeds", "count", len(ids))
	}
	for _, id := range ids {
		a.refreshMu.Lock()
		_, err := refreshFeed(a.db, id)
		a.refreshMu.Unlock()
		if err != nil {
			slog.Error("refresh feed error", "feed_id", id, "err", err)
		}
	}
	return nil
}

func cleanupReadItems(db *sql.DB) error {
	cutoff := time.Now().UTC().Add(-readRetention)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(`
INSERT OR IGNORE INTO tombstones (feed_id, guid, deleted_at)
SELECT feed_id, guid, ?
FROM items
WHERE read_at IS NOT NULL AND read_at <= ?
`, time.Now().UTC(), cutoff); err != nil {
		return err
	}
	deleteResult, err := tx.Exec("DELETE FROM items WHERE read_at IS NOT NULL AND read_at <= ?", cutoff)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if deleted, err := deleteResult.RowsAffected(); err == nil && deleted > 0 {
		slog.Info("cleanup read items", "deleted", deleted)
	}
	return nil
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func nullTimeToValue(value sql.NullTime) any {
	if value.Valid {
		return value.Time
	}
	return nil
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func chooseHeader(preferred, fallback string) string {
	if strings.TrimSpace(preferred) != "" {
		return preferred
	}
	return fallback
}

func truncateString(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max]
}

func nextRefreshAt(checkedAt time.Time, unchangedCount int) time.Time {
	interval := computeBackoffInterval(unchangedCount)
	interval = applyJitter(interval)
	if interval > refreshBackoffMax {
		interval = refreshBackoffMax
	}
	return checkedAt.Add(interval)
}

func computeBackoffInterval(unchangedCount int) time.Duration {
	if unchangedCount < 0 {
		unchangedCount = 0
	}
	interval := refreshInterval
	for i := 0; i < unchangedCount; i++ {
		interval *= 2
		if interval >= refreshBackoffMax {
			return refreshBackoffMax
		}
	}
	if interval > refreshBackoffMax {
		return refreshBackoffMax
	}
	return interval
}

func applyJitter(base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}
	magnitude := refreshJitterMin + rand.Float64()*(refreshJitterMax-refreshJitterMin)
	if rand.Intn(2) == 0 {
		magnitude = -magnitude
	}
	adjusted := float64(base) * (1 + magnitude)
	return time.Duration(adjusted)
}
