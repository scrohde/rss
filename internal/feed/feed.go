package feed

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"rss/internal/store"
)

const (
	RefreshInterval     = 20 * time.Minute
	RefreshLoopInterval = 30 * time.Second
	RefreshBatchSize    = 5
	refreshBackoffMax   = 12 * time.Hour
	refreshJitterMin    = 0.10
	refreshJitterMax    = 0.20
	feedFetchTimeout    = 15 * time.Second
	maxErrorLength      = 300
)

type FetchResult struct {
	Feed         *gofeed.Feed
	ETag         string
	LastModified string
	NotModified  bool
	StatusCode   int
}

type CacheMeta struct {
	ETag           string
	LastModified   string
	UnchangedCount int
}

type RefreshMeta struct {
	ETag           string
	LastModified   string
	LastCheckedAt  time.Time
	LastError      string
	UnchangedCount int
	NextRefreshAt  time.Time
}

func NormalizeURL(raw string) (string, error) {
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

func Fetch(feedURL, etag, lastModified string) (*FetchResult, error) {
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

	result := &FetchResult{
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

func Refresh(db *sql.DB, feedID int64) (int64, error) {
	feedURL, err := store.GetFeedURL(db, feedID)
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
	result, err := Fetch(feedURL, cache.ETag, cache.LastModified)
	duration := time.Since(start).Milliseconds()
	checkedAt := time.Now().UTC()

	meta := RefreshMeta{
		LastCheckedAt: checkedAt,
	}

	if err != nil {
		meta.LastError = truncateString(err.Error(), maxErrorLength)
		meta.UnchangedCount = 0
		meta.NextRefreshAt = NextRefreshAt(checkedAt, meta.UnchangedCount)
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
		meta.NextRefreshAt = NextRefreshAt(checkedAt, meta.UnchangedCount)
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
		meta.NextRefreshAt = NextRefreshAt(checkedAt, meta.UnchangedCount)
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

	updatedID, err := store.UpsertFeed(db, feedURL, feedTitle)
	if err != nil {
		meta.LastError = truncateString(err.Error(), maxErrorLength)
		_ = updateFeedRefreshMeta(db, feedID, meta)
		slog.Error("refresh upsert feed failed", "feed_id", feedID, "feed_url", feedURL, "err", err)
		return 0, err
	}

	inserted, err := store.UpsertItems(db, updatedID, result.Feed.Items)
	if err != nil {
		meta.LastError = truncateString(err.Error(), maxErrorLength)
		meta.UnchangedCount = 0
		meta.NextRefreshAt = NextRefreshAt(checkedAt, meta.UnchangedCount)
		_ = updateFeedRefreshMeta(db, feedID, meta)
		slog.Error("refresh upsert items failed", "feed_id", feedID, "feed_url", feedURL, "err", err)
		return 0, err
	}

	if err := store.EnforceItemLimit(db, updatedID); err != nil {
		meta.LastError = truncateString(err.Error(), maxErrorLength)
		meta.UnchangedCount = 0
		meta.NextRefreshAt = NextRefreshAt(checkedAt, meta.UnchangedCount)
		_ = updateFeedRefreshMeta(db, feedID, meta)
		slog.Error("refresh enforce item limit failed", "feed_id", feedID, "feed_url", feedURL, "err", err)
		return 0, err
	}

	if inserted == 0 {
		meta.UnchangedCount = cache.UnchangedCount + 1
	} else {
		meta.UnchangedCount = 0
	}
	meta.NextRefreshAt = NextRefreshAt(checkedAt, meta.UnchangedCount)
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

func SaveRefreshMeta(db *sql.DB, feedID int64, meta RefreshMeta) error {
	return updateFeedRefreshMeta(db, feedID, meta)
}

func NextRefreshAt(checkedAt time.Time, unchangedCount int) time.Time {
	interval := ComputeBackoffInterval(unchangedCount)
	interval = ApplyJitter(interval)
	if interval > refreshBackoffMax {
		interval = refreshBackoffMax
	}
	return checkedAt.Add(interval)
}

func ComputeBackoffInterval(unchangedCount int) time.Duration {
	if unchangedCount < 0 {
		unchangedCount = 0
	}
	interval := RefreshInterval
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

func ApplyJitter(base time.Duration) time.Duration {
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

func getFeedCacheMeta(db *sql.DB, feedID int64) (CacheMeta, error) {
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
		return CacheMeta{}, err
	}
	return CacheMeta{
		ETag:           strings.TrimSpace(etag.String),
		LastModified:   strings.TrimSpace(lastModified.String),
		UnchangedCount: int(unchangedCount.Int64),
	}, nil
}

func updateFeedRefreshMeta(db *sql.DB, feedID int64, meta RefreshMeta) error {
	if meta.LastCheckedAt.IsZero() {
		meta.LastCheckedAt = time.Now().UTC()
	}
	if meta.UnchangedCount < 0 {
		meta.UnchangedCount = 0
	}
	if meta.NextRefreshAt.IsZero() {
		meta.NextRefreshAt = NextRefreshAt(meta.LastCheckedAt, meta.UnchangedCount)
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

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
