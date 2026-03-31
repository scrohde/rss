// Package feed handles feed fetching and refresh scheduling.
package feed

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"rss/internal/store"
)

const (
	// RefreshInterval is the base interval between refresh attempts.
	RefreshInterval = 30 * time.Minute
	// RefreshLoopInterval controls how often the refresh loop runs.
	RefreshLoopInterval = 30 * time.Second
	// RefreshBatchSize is the max number of feeds processed per loop.
	RefreshBatchSize             = 5
	refreshBackoffMax            = 12 * time.Hour
	refreshJitterMin             = 0.01
	refreshJitterMax             = 0.20
	feedFetchTimeout             = 15 * time.Second
	maxErrorLength               = 300
	randomFallback               = 0.5
	countReset                   = 0
	countStep                    = 1
	backoffMultiplier            = 2
	jitterNeutral                = 1
	byteIndexFirst               = 0
	randomBitMask          uint8 = 1
	randomBitFallback            = randomBitMask
	zeroFeedID             int64 = 0
	feedUserAgent                = "PulseRSS/1.0"
	logFieldFeedID               = "feed_id"
	logFieldFeedURL              = "feed_url"
	logFieldErr                  = "err"
	logFieldSkipReason           = "skip_reason"
	skipReasonRobotsPolicy       = "robots_policy"
)

var (
	errFeedURLRequired       = errors.New("feed URL is required")
	errFeedURLInvalid        = errors.New("feed URL looks invalid")
	errFeedReturnedNoContent = errors.New("feed returned no content")
	errUnexpectedFeedStatus  = errors.New("unexpected status from feed")
	errFeedBlockedByRobots   = errors.New("feed blocked by robots policy")
	errRefreshMetaNil        = errors.New("refresh meta is nil")
)

// FetchResult contains parsed feed data and fetch/cache metadata.
type FetchResult struct {
	Feed         *gofeed.Feed
	ETag         string
	LastModified string
	NotModified  bool
	StatusCode   int
}

// FetchStatusError indicates a non-2xx, non-304 HTTP response from a feed URL.
type FetchStatusError struct {
	StatusCode int
	RetryAfter time.Duration
}

// Error returns the unexpected status message and response status code.
func (e *FetchStatusError) Error() string {
	return fmt.Sprintf("%v: %d", errUnexpectedFeedStatus, e.StatusCode)
}

// Unwrap enables errors.Is checks against errUnexpectedFeedStatus.
func (*FetchStatusError) Unwrap() error {
	return errUnexpectedFeedStatus
}

// CacheMeta stores cached response validators and unchanged counter.
type CacheMeta struct {
	ETag           string
	LastModified   string
	UnchangedCount int
}

// RefreshMeta stores the refresh bookkeeping persisted for each feed.
type RefreshMeta struct {
	LastCheckedAt  time.Time
	NextRefreshAt  time.Time
	ETag           string
	LastModified   string
	LastError      string
	UnchangedCount int
}

// NormalizeURL validates and normalizes a feed URL.
func NormalizeURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errFeedURLRequired
	}

	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}

	u, err := url.ParseRequestURI(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errFeedURLInvalid
	}

	return u.String(), nil
}

// Fetch retrieves and parses a feed URL with conditional request headers.
//
//nolint:gosec // Validated URL fetch path and branchy flow.
func Fetch(ctx context.Context, feedURL, etag, lastModified string) (*FetchResult, error) {
	normalizedURL, err := NormalizeURL(feedURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalizedURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("User-Agent", feedUserAgent)
	setConditionalHeaders(req, etag, lastModified)

	client := newFeedHTTPClient()

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch feed: %w", err)
	}

	defer func() {
		closeErr := resp.Body.Close()
		if closeErr != nil {
			slog.Warn("feed response close failed", logFieldFeedURL, normalizedURL, logFieldErr, closeErr)
		}
	}()

	result, parseErr := parseFetchResponse(resp)
	if parseErr != nil {
		return nil, parseErr
	}

	return result, nil
}

func parseFetchResponse(resp *http.Response) (*FetchResult, error) {
	result := new(FetchResult)
	result.ETag = strings.TrimSpace(resp.Header.Get("ETag"))
	result.LastModified = strings.TrimSpace(resp.Header.Get("Last-Modified"))
	result.StatusCode = resp.StatusCode

	if resp.StatusCode == http.StatusNotModified {
		result.NotModified = true

		return result, nil
	}

	if resp.StatusCode < http.StatusOK ||
		resp.StatusCode >= http.StatusMultipleChoices {
		return nil, buildFetchStatusError(resp)
	}

	parser := gofeed.NewParser()

	feed, err := parser.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse feed: %w", err)
	}

	result.Feed = feed

	return result, nil
}

func buildFetchStatusError(resp *http.Response) error {
	retryAfter := parseRetryAfterHeader(resp.Header.Get("Retry-After"), time.Now().UTC())

	return &FetchStatusError{
		StatusCode: resp.StatusCode,
		RetryAfter: retryAfter,
	}
}

func parseRetryAfterHeader(value string, now time.Time) time.Duration {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}

	seconds, err := strconv.Atoi(trimmed)
	if err == nil {
		if seconds <= 0 {
			return 0
		}

		return time.Duration(seconds) * time.Second
	}

	retryAt, err := http.ParseTime(trimmed)
	if err != nil || !retryAt.After(now) {
		return 0
	}

	return retryAt.Sub(now)
}

func nextRefreshAtForFetchError(
	checkedAt time.Time,
	unchangedCount int,
	fetchErr error,
) time.Time {
	nextRefreshAt := NextRefreshAt(checkedAt, unchangedCount)

	statusErr := new(FetchStatusError)
	if !errors.As(fetchErr, &statusErr) {
		return nextRefreshAt
	}

	retryAfterAt := checkedAt.Add(statusErr.RetryAfter)
	if retryAfterAt.After(nextRefreshAt) {
		return retryAfterAt
	}

	return nextRefreshAt
}

// Refresh fetches a feed, updates stored items, and persists refresh metadata.
func Refresh(ctx context.Context, db *sql.DB, feedID int64) (int64, error) {
	state, err := loadRefreshContext(ctx, db, feedID)
	if err != nil {
		return zeroFeedID, err
	}

	robotsErr := enforceRefreshRobotsPolicy(ctx, db, state, time.Now().UTC())
	if robotsErr != nil {
		return zeroFeedID, robotsErr
	}

	phase, err := fetchRefreshPhase(ctx, db, state)
	if err != nil {
		return zeroFeedID, err
	}

	return persistRefreshOutcome(ctx, db, state, &phase)
}

type refreshContext struct {
	feedURL string
	cache   CacheMeta
	feedID  int64
}

type refreshFetchPhase struct {
	result     *FetchResult
	meta       RefreshMeta
	durationMS int64
}

type refreshUpsertResult struct {
	feedTitle string
	updatedID int64
}

func loadRefreshContext(ctx context.Context, db *sql.DB, feedID int64) (refreshContext, error) {
	feedURL, err := store.GetFeedURL(ctx, db, feedID)
	if err != nil {
		slog.Error("refresh feed lookup failed", logFieldFeedID, feedID, logFieldErr, err)

		return refreshContext{}, fmt.Errorf("get feed URL: %w", err)
	}

	cache, err := getFeedCacheMeta(ctx, db, feedID)
	if err != nil {
		slog.Error(
			"refresh feed cache lookup failed",
			logFieldFeedID,
			feedID,
			logFieldFeedURL,
			feedURL,
			logFieldErr,
			err,
		)

		return refreshContext{}, err
	}

	return refreshContext{
		feedID:  feedID,
		feedURL: feedURL,
		cache:   cache,
	}, nil
}

func enforceRefreshRobotsPolicy(
	ctx context.Context,
	db *sql.DB,
	state refreshContext,
	checkedAt time.Time,
) error {
	robotsResult, robotsErr := checkRobotsPolicy(ctx, state.feedURL)
	if robotsErr != nil {
		slog.Warn(
			"robots policy check failed",
			logFieldFeedID,
			state.feedID,
			logFieldFeedURL,
			state.feedURL,
			logFieldErr,
			robotsErr,
		)
	}

	if robotsResult.BlockedErr == nil {
		return nil
	}

	meta := RefreshMeta{
		LastCheckedAt:  checkedAt,
		NextRefreshAt:  time.Time{},
		ETag:           "",
		LastModified:   "",
		LastError:      truncateString(robotsResult.BlockedErr.Error()),
		UnchangedCount: state.cache.UnchangedCount + countStep,
	}
	meta.NextRefreshAt = NextRefreshAt(checkedAt, meta.UnchangedCount)
	saveRefreshMetaBestEffort(ctx, db, state.feedID, &meta)
	slog.Warn(
		"refresh skipped by robots policy",
		logFieldFeedID,
		state.feedID,
		logFieldFeedURL,
		state.feedURL,
		logFieldSkipReason,
		skipReasonRobotsPolicy,
		"robots_url",
		robotsResult.BlockedErr.RobotsURL,
		"disallow_rule",
		robotsResult.BlockedErr.DisallowRule,
	)

	return robotsResult.BlockedErr
}

func fetchRefreshPhase(
	ctx context.Context,
	db *sql.DB,
	state refreshContext,
) (refreshFetchPhase, error) {
	start := time.Now()
	result, err := Fetch(ctx, state.feedURL, state.cache.ETag, state.cache.LastModified)
	duration := time.Since(start).Milliseconds()
	checkedAt := time.Now().UTC()
	phase := refreshFetchPhase{
		result:     result,
		durationMS: duration,
		meta: RefreshMeta{
			LastCheckedAt:  checkedAt,
			NextRefreshAt:  time.Time{},
			ETag:           "",
			LastModified:   "",
			LastError:      "",
			UnchangedCount: countReset,
		},
	}

	if err != nil {
		phase.meta.LastError = truncateString(err.Error())
		phase.meta.UnchangedCount = state.cache.UnchangedCount + countStep
		phase.meta.NextRefreshAt = nextRefreshAtForFetchError(
			checkedAt,
			phase.meta.UnchangedCount,
			err,
		)
		saveRefreshMetaBestEffort(ctx, db, state.feedID, &phase.meta)
		slog.Error("refresh feed fetch failed",
			logFieldFeedID, state.feedID,
			logFieldFeedURL, state.feedURL,
			"duration_ms", duration,
			logFieldErr, err,
		)

		return refreshFetchPhase{}, err
	}

	phase.meta.ETag = chooseHeader(result.ETag, state.cache.ETag)
	phase.meta.LastModified = chooseHeader(result.LastModified, state.cache.LastModified)

	return phase, nil
}

func persistRefreshOutcome(
	ctx context.Context,
	db *sql.DB,
	state refreshContext,
	phase *refreshFetchPhase,
) (int64, error) {
	meta := phase.meta
	result := phase.result

	if result.NotModified {
		meta.UnchangedCount = state.cache.UnchangedCount + countStep
		meta.NextRefreshAt = NextRefreshAt(meta.LastCheckedAt, meta.UnchangedCount)

		updateErr := updateFeedRefreshMeta(ctx, db, state.feedID, &meta)
		if updateErr != nil {
			return zeroFeedID, updateErr
		}

		slog.Info("refresh feed cache hit",
			logFieldFeedID, state.feedID,
			logFieldFeedURL, state.feedURL,
			"status", result.StatusCode,
			"duration_ms", phase.durationMS,
		)

		return state.feedID, nil
	}

	if result.Feed == nil {
		meta.LastError = "feed returned no content"
		meta.UnchangedCount = countReset
		meta.NextRefreshAt = NextRefreshAt(meta.LastCheckedAt, meta.UnchangedCount)
		saveRefreshMetaBestEffort(ctx, db, state.feedID, &meta)
		slog.Warn("refresh feed returned no content",
			logFieldFeedID, state.feedID,
			logFieldFeedURL, state.feedURL,
			"status", result.StatusCode,
		)

		return zeroFeedID, errFeedReturnedNoContent
	}

	return persistRefreshFeed(ctx, db, state, phase)
}

func persistRefreshFeed(
	ctx context.Context,
	db *sql.DB,
	state refreshContext,
	phase *refreshFetchPhase,
) (int64, error) {
	meta := phase.meta
	result := phase.result

	upserted, err := upsertRefreshFeedRecord(ctx, db, state, &meta, result)
	if err != nil {
		return zeroFeedID, err
	}

	inserted, err := upsertRefreshItems(ctx, db, state, &meta, upserted.updatedID, result.Feed.Items)
	if err != nil {
		return zeroFeedID, err
	}

	if inserted == countReset {
		meta.UnchangedCount = state.cache.UnchangedCount + countStep
	} else {
		meta.UnchangedCount = countReset
	}

	meta.NextRefreshAt = NextRefreshAt(meta.LastCheckedAt, meta.UnchangedCount)

	updateErr := updateFeedRefreshMeta(ctx, db, upserted.updatedID, &meta)
	if updateErr != nil {
		return zeroFeedID, updateErr
	}

	slog.Info("refresh feed updated",
		"feed_id", upserted.updatedID,
		"feed_url", state.feedURL,
		"title", upserted.feedTitle,
		"status", result.StatusCode,
		"items_in_feed", len(result.Feed.Items),
		"items_new", inserted,
		"duration_ms", phase.durationMS,
	)

	return upserted.updatedID, nil
}

func upsertRefreshFeedRecord(
	ctx context.Context,
	db *sql.DB,
	state refreshContext,
	meta *RefreshMeta,
	result *FetchResult,
) (refreshUpsertResult, error) {
	feedTitle := strings.TrimSpace(result.Feed.Title)
	if feedTitle == "" {
		feedTitle = state.feedURL
	}

	updatedID, err := store.UpsertFeed(ctx, db, state.feedURL, feedTitle)
	if err != nil {
		meta.LastError = truncateString(err.Error())
		saveRefreshMetaBestEffort(ctx, db, state.feedID, meta)
		slog.Error(
			"refresh upsert feed failed",
			logFieldFeedID, state.feedID,
			logFieldFeedURL, state.feedURL,
			logFieldErr, err,
		)

		return refreshUpsertResult{}, fmt.Errorf("upsert feed: %w", err)
	}

	return refreshUpsertResult{
		updatedID: updatedID,
		feedTitle: feedTitle,
	}, nil
}

func upsertRefreshItems(
	ctx context.Context,
	db *sql.DB,
	state refreshContext,
	meta *RefreshMeta,
	updatedID int64,
	items []*gofeed.Item,
) (int, error) {
	inserted, err := store.UpsertItems(ctx, db, updatedID, items)
	if err != nil {
		meta.LastError = truncateString(err.Error())
		meta.UnchangedCount = countReset
		meta.NextRefreshAt = NextRefreshAt(meta.LastCheckedAt, meta.UnchangedCount)
		saveRefreshMetaBestEffort(ctx, db, state.feedID, meta)
		slog.Error(
			"refresh upsert items failed",
			logFieldFeedID, state.feedID,
			logFieldFeedURL, state.feedURL,
			logFieldErr, err,
		)

		return countReset, fmt.Errorf("upsert items: %w", err)
	}

	enforceErr := store.EnforceItemLimit(ctx, db, updatedID)
	if enforceErr != nil {
		meta.LastError = truncateString(enforceErr.Error())
		meta.UnchangedCount = countReset
		meta.NextRefreshAt = NextRefreshAt(meta.LastCheckedAt, meta.UnchangedCount)
		saveRefreshMetaBestEffort(ctx, db, state.feedID, meta)
		slog.Error(
			"refresh enforce item limit failed",
			logFieldFeedID, state.feedID,
			logFieldFeedURL, state.feedURL,
			logFieldErr, enforceErr,
		)

		return countReset, fmt.Errorf("enforce item limit: %w", enforceErr)
	}

	return inserted, nil
}

func setConditionalHeaders(req *http.Request, etag, lastModified string) {
	if strings.TrimSpace(etag) != "" {
		req.Header.Set("If-None-Match", etag)
	}

	if strings.TrimSpace(lastModified) != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}
}

// SaveRefreshMeta persists refresh metadata for a feed.
func SaveRefreshMeta(ctx context.Context, db *sql.DB, feedID int64, meta *RefreshMeta) error {
	return updateFeedRefreshMeta(ctx, db, feedID, meta)
}

// NextRefreshAt returns the next refresh time with backoff and jitter.
func NextRefreshAt(checkedAt time.Time, unchangedCount int) time.Time {
	interval := ComputeBackoffInterval(unchangedCount)

	interval = min(ApplyJitter(interval), refreshBackoffMax)

	return checkedAt.Add(interval)
}

// ComputeBackoffInterval computes a capped exponential backoff interval.
func ComputeBackoffInterval(unchangedCount int) time.Duration {
	if unchangedCount < countReset {
		unchangedCount = countReset
	}

	interval := RefreshInterval
	for range unchangedCount {
		interval *= backoffMultiplier
		if interval >= refreshBackoffMax {
			return refreshBackoffMax
		}
	}

	if interval > refreshBackoffMax {
		return refreshBackoffMax
	}

	return interval
}

// ApplyJitter applies randomized jitter to a base interval.
func ApplyJitter(base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}

	magnitude := refreshJitterMin + randomFloat64()*
		(refreshJitterMax-refreshJitterMin)
	if randomBit() == 0 {
		magnitude = -magnitude
	}

	adjusted := float64(base) * (jitterNeutral + magnitude)

	return time.Duration(adjusted)
}

func getFeedCacheMeta(
	ctx context.Context,
	db *sql.DB,
	feedID int64,
) (CacheMeta, error) {
	var (
		etag           sql.NullString
		lastModified   sql.NullString
		unchangedCount sql.NullInt64
	)

	err := db.QueryRowContext(ctx, `
SELECT etag, last_modified, unchanged_count
FROM feeds
WHERE id = ?
`, feedID).Scan(&etag, &lastModified, &unchangedCount)
	if err != nil {
		return CacheMeta{}, fmt.Errorf("load feed cache metadata: %w", err)
	}

	return CacheMeta{
		ETag:           strings.TrimSpace(etag.String),
		LastModified:   strings.TrimSpace(lastModified.String),
		UnchangedCount: int(unchangedCount.Int64),
	}, nil
}

func updateFeedRefreshMeta(ctx context.Context, db *sql.DB, feedID int64, meta *RefreshMeta) error {
	if meta == nil {
		return errRefreshMetaNil
	}

	if meta.LastCheckedAt.IsZero() {
		meta.LastCheckedAt = time.Now().UTC()
	}

	if meta.UnchangedCount < countReset {
		meta.UnchangedCount = countReset
	}

	if meta.NextRefreshAt.IsZero() {
		meta.NextRefreshAt = NextRefreshAt(
			meta.LastCheckedAt,
			meta.UnchangedCount,
		)
	}

	_, err := db.ExecContext(ctx, `
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
	if err != nil {
		return fmt.Errorf("update feed refresh metadata: %w", err)
	}

	return nil
}

func saveRefreshMetaBestEffort(ctx context.Context, db *sql.DB, feedID int64, meta *RefreshMeta) {
	err := updateFeedRefreshMeta(ctx, db, feedID, meta)
	if err != nil {
		slog.Error(
			"refresh meta update failed",
			logFieldFeedID,
			feedID,
			logFieldErr,
			err,
		)
	}
}

func newFeedHTTPClient() *http.Client {
	client := new(http.Client)
	client.Timeout = feedFetchTimeout

	return client
}

func randomFloat64() float64 {
	var b [8]byte

	_, err := rand.Read(b[:])
	if err != nil {
		return randomFallback
	}

	const maxUint64 = ^uint64(0)

	return float64(binary.BigEndian.Uint64(b[:])) / float64(maxUint64)
}

func randomBit() uint8 {
	var b [1]byte

	_, err := rand.Read(b[:])
	if err != nil {
		return randomBitFallback
	}

	return b[byteIndexFirst] & randomBitMask
}

func chooseHeader(preferred, fallback string) string {
	if strings.TrimSpace(preferred) != "" {
		return preferred
	}

	return fallback
}

func truncateString(value string) string {
	if len(value) <= maxErrorLength {
		return value
	}

	return value[:maxErrorLength]
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	return value
}
