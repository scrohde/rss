package main

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	_ "modernc.org/sqlite"
)

const (
	maxItemsPerFeed = 200
	readRetention   = 2 * time.Hour
)

type App struct {
	db   *sql.DB
	tmpl *template.Template
}

type FeedView struct {
	ID        int64
	Title     string
	URL       string
	ItemCount int
}

type ItemView struct {
	ID               int64
	Title            string
	Link             string
	SummaryHTML      template.HTML
	PublishedDisplay string
	IsRead           bool
}

type ItemListData struct {
	Feed     FeedView
	Items    []ItemView
	NewestID int64
	NewItems NewItemsData
}

type PageData struct {
	Feeds          []FeedView
	SelectedFeedID int64
	ItemList       *ItemListData
}

type SubscribeResponseData struct {
	Message        string
	MessageClass   string
	Feeds          []FeedView
	SelectedFeedID int64
	ItemList       *ItemListData
	Update         bool
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

func main() {
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

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.HandleFunc("/", app.route)

	server := &http.Server{
		Addr:         ":8080",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Println("RSS reader running on http://localhost:8080")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
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
	created_at DATETIME NOT NULL
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
`
	_, err := db.Exec(schema)
	return err
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
		if r.Method == http.MethodGet && len(parts) >= 3 && parts[2] == "items" {
			feedID, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			switch {
			case len(parts) == 3:
				a.handleFeedItems(w, r, feedID)
				return
			case len(parts) == 4 && parts[3] == "new":
				a.handleFeedItemsNew(w, r, feedID)
				return
			case len(parts) == 4 && parts[3] == "poll":
				a.handleFeedItemsPoll(w, r, feedID)
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

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	feeds, err := listFeeds(a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	data := PageData{Feeds: feeds}
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

	feed, err := fetchFeed(feedURL)
	if err != nil {
		a.renderSubscribeError(w, err)
		return
	}

	feedTitle := strings.TrimSpace(feed.Title)
	if feedTitle == "" {
		feedTitle = feedURL
	}

	feedID, err := upsertFeed(a.db, feedURL, feedTitle)
	if err != nil {
		a.renderSubscribeError(w, err)
		return
	}

	if err := upsertItems(a.db, feedID, feed.Items); err != nil {
		a.renderSubscribeError(w, err)
		return
	}

	if err := enforceItemLimit(a.db, feedID); err != nil {
		a.renderSubscribeError(w, err)
		return
	}

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
		Message:        fmt.Sprintf("Subscribed to %s", feedTitle),
		MessageClass:   "success",
		Feeds:          feeds,
		SelectedFeedID: feedID,
		ItemList:       itemList,
		Update:         true,
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

	if _, err := refreshFeed(a.db, feedID); err != nil {
		http.Error(w, "failed to refresh feed", http.StatusInternalServerError)
		return
	}

	count, err := countItemsAfter(a.db, feedID, afterID)
	if err != nil {
		http.Error(w, "failed to check new items", http.StatusInternalServerError)
		return
	}

	data := NewItemsData{FeedID: feedID, Count: count}
	a.renderTemplate(w, "new_items_banner", data)
}

func (a *App) handleFeedItemsNew(w http.ResponseWriter, r *http.Request, feedID int64) {
	afterID := parseAfterID(r)

	if _, err := refreshFeed(a.db, feedID); err != nil {
		http.Error(w, "failed to refresh feed", http.StatusInternalServerError)
		return
	}

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
	a.renderTemplate(w, "item_expanded", item)
}

func (a *App) handleItemCompact(w http.ResponseWriter, r *http.Request, itemID int64) {
	item, err := getItemView(a.db, itemID)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}
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

	item, err := getItemView(a.db, itemID)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}

	templateName := "item_compact"
	if view == "expanded" {
		templateName = "item_expanded"
	}
	a.renderTemplate(w, templateName, item)
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

func fetchFeed(feedURL string) (*gofeed.Feed, error) {
	parser := gofeed.NewParser()
	parser.Client = &http.Client{Timeout: 15 * time.Second}
	parser.UserAgent = "PulseRSS/1.0"
	feed, err := parser.ParseURL(feedURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch feed: %w", err)
	}
	return feed, nil
}

func refreshFeed(db *sql.DB, feedID int64) (int64, error) {
	feedURL, err := getFeedURL(db, feedID)
	if err != nil {
		return 0, err
	}

	feed, err := fetchFeed(feedURL)
	if err != nil {
		return 0, err
	}

	feedTitle := strings.TrimSpace(feed.Title)
	if feedTitle == "" {
		feedTitle = feedURL
	}

	updatedID, err := upsertFeed(db, feedURL, feedTitle)
	if err != nil {
		return 0, err
	}

	if err := upsertItems(db, updatedID, feed.Items); err != nil {
		return 0, err
	}

	if err := enforceItemLimit(db, updatedID); err != nil {
		return 0, err
	}

	return updatedID, nil
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

func upsertItems(db *sql.DB, feedID int64, items []*gofeed.Item) error {
	now := time.Now().UTC()
	stmt, err := db.Prepare(`
INSERT OR IGNORE INTO items
(feed_id, guid, title, link, summary, content, published_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
`)
	if err != nil {
		return err
	}
	defer stmt.Close()

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
		if _, err := stmt.Exec(
			feedID,
			guid,
			fallbackString(item.Title, "(untitled)"),
			fallbackString(item.Link, "#"),
			summary,
			content,
			nullTimeToValue(publishedAt),
			now,
		); err != nil {
			return err
		}
	}

	return nil
}

func enforceItemLimit(db *sql.DB, feedID int64) error {
	_, err := db.Exec(`
DELETE FROM items
WHERE feed_id = ?
  AND id NOT IN (
	SELECT id FROM items
	WHERE feed_id = ?
	ORDER BY COALESCE(published_at, created_at) DESC, id DESC
	LIMIT ?
  )
`, feedID, feedID, maxItemsPerFeed)
	return err
}

func listFeeds(db *sql.DB) ([]FeedView, error) {
	rows, err := db.Query(`
SELECT f.id, f.title, f.url,
       (SELECT COUNT(*) FROM items i WHERE i.feed_id = f.id) AS item_count
FROM feeds f
ORDER BY f.title COLLATE NOCASE
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var feeds []FeedView
	for rows.Next() {
		var feed FeedView
		if err := rows.Scan(&feed.ID, &feed.Title, &feed.URL, &feed.ItemCount); err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	return feeds, rows.Err()
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
	var feed FeedView
	row := db.QueryRow(`
SELECT f.id, f.title, f.url,
       (SELECT COUNT(*) FROM items i WHERE i.feed_id = f.id) AS item_count
FROM feeds f
WHERE f.id = ?
`, feedID)
	if err := row.Scan(&feed.ID, &feed.Title, &feed.URL, &feed.ItemCount); err != nil {
		return FeedView{}, err
	}
	return feed, nil
}

func getFeedURL(db *sql.DB, feedID int64) (string, error) {
	var url string
	if err := db.QueryRow("SELECT url FROM feeds WHERE id = ?", feedID).Scan(&url); err != nil {
		return "", err
	}
	return url, nil
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
	return items, rows.Err()
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
	return items, rows.Err()
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
	return buildItemView(id, title, link, summary, content, published, readAt), nil
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
	if published.Valid {
		publishedDisplay = formatTime(published.Time)
	}
	return ItemView{
		ID:               id,
		Title:            title,
		Link:             link,
		SummaryHTML:      summaryHTML,
		PublishedDisplay: publishedDisplay,
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
	return template.HTML(text)
}

func formatTime(t time.Time) string {
	return t.Local().Format("Jan 2, 2006 - 3:04 PM")
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

func (a *App) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		if err := cleanupReadItems(a.db); err != nil {
			log.Printf("cleanup error: %v", err)
		}
		<-ticker.C
	}
}

func cleanupReadItems(db *sql.DB) error {
	cutoff := time.Now().UTC().Add(-readRetention)
	_, err := db.Exec("DELETE FROM items WHERE read_at IS NOT NULL AND read_at <= ?", cutoff)
	return err
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
