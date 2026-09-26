package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rss/internal/store"
)

const (
	catchUpCutoffField       = "cutoff"
	catchUpPreviewCountField = "catch_up_count"
	catchUpSurfaceField      = "surface"
	catchUpMobileSurface     = "mobile"
	catchUpResultEvent       = "pulse:catch-up-applied"
)

type catchUpPreviewResponseData struct {
	Cutoff        string
	FeedID        int64
	AffectedCount int
}

type catchUpAppliedEvent struct {
	Cutoff        string `json:"cutoff"`
	UndoToken     string `json:"undoToken"`
	FeedID        int64  `json:"feedId"`
	AffectedCount int    `json:"affectedCount"`
}

func (a *App) handleCatchUpPreview(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)

		return
	}

	cutoff, ok := parseCatchUpCutoff(w, r.URL.Query().Get(catchUpCutoffField))
	if !ok {
		return
	}

	affectedCount, err := store.CountUnreadItemsBefore(r.Context(), a.db, feedID, cutoff)
	if err != nil {
		http.Error(w, "failed to preview catch-up", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	a.renderTemplate(w, "catch_up_preview_response", catchUpPreviewResponseData{
		Cutoff:        cutoff.Format(time.RFC3339Nano),
		FeedID:        feedID,
		AffectedCount: affectedCount,
	})
}

func (a *App) handleCatchUp(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)

		return
	}

	if !parseFormOrBadRequest(w, r, "invalid catch-up form") {
		return
	}

	cutoff, ok := parseCatchUpCutoff(w, r.FormValue(catchUpCutoffField))
	if !ok {
		return
	}
	result, token, err := a.applyCatchUp(r, feedID, cutoff)
	if err != nil {
		slog.Error("catch-up apply failed", "feed_id", feedID, "err", err)
		http.Error(w, "failed to apply catch-up", http.StatusInternalServerError)

		return
	}

	cutoffValue := cutoff.Format(time.RFC3339Nano)
	slogCatchUpApplied(feedID, result.AffectedCount, cutoffValue)
	a.respondCatchUp(w, r, feedID, cutoff, cutoffValue, result.AffectedCount, token)
}

func (a *App) applyCatchUp(r *http.Request, feedID int64, cutoff time.Time) (store.CatchUpResult, string, error) {
	token, err := a.generateUndoToken()
	if err != nil {
		return store.CatchUpResult{}, "", fmt.Errorf("prepare undo: %w", err)
	}

	result, err := store.MarkUnreadItemsBefore(r.Context(), a.db, feedID, cutoff)
	if err != nil {
		return store.CatchUpResult{}, "", fmt.Errorf("update items: %w", err)
	}

	err = a.storeMarkAllReadUndo(feedID, result.ChangedItemIDs, token)
	if err != nil {
		return store.CatchUpResult{}, "", fmt.Errorf("store undo: %w", err)
	}

	return result, token, nil
}

func (a *App) respondCatchUp(
	w http.ResponseWriter,
	r *http.Request,
	feedID int64,
	cutoff time.Time,
	cutoffValue string,
	affectedCount int,
	undoToken string,
) {
	mobileSurface := isMobileCatchUpSurface(r)

	if isHTMXRequest(r) {
		setCatchUpAppliedHeaders(w, feedID, affectedCount, cutoffValue, undoToken)
		if mobileSurface {
			a.renderMobileStreamPreservingUndoFeed(w, r, feedID)

			return
		}
		a.renderItemListResponse(w, r, feedID)

		return
	}

	if mobileSurface {
		selectedFeedID := parseSelectedFeedID(r)
		state := parseMobileAggregateState(r)

		http.Redirect( //nolint:gosec // Destination comes from the internal mobile stream URL helper.
			w,
			r,
			mobileCatchUpResultURL(selectedFeedID, state, affectedCount, cutoff),
			http.StatusSeeOther,
		)

		return
	}

	http.Redirect(w, r, catchUpResultURL(feedID, affectedCount, cutoff), http.StatusSeeOther)
}

func parseCatchUpCutoff(w http.ResponseWriter, raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		http.Error(w, "invalid cutoff", http.StatusBadRequest)

		return time.Time{}, false
	}

	cutoff, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || cutoff.IsZero() {
		http.Error(w, "invalid cutoff", http.StatusBadRequest)

		return time.Time{}, false
	}

	return cutoff.UTC(), true
}

func setCatchUpAppliedHeaders(w http.ResponseWriter, feedID int64, affectedCount int, cutoff, undoToken string) {
	w.Header().Set("X-Catch-Up-Affected-Count", strconv.Itoa(affectedCount))

	event := map[string]catchUpAppliedEvent{
		catchUpResultEvent: {
			FeedID:        feedID,
			Cutoff:        cutoff,
			AffectedCount: affectedCount,
			UndoToken:     undoToken,
		},
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return
	}

	w.Header().Set("Hx-Trigger", string(encoded))
}

func slogCatchUpApplied(feedID int64, affectedCount int, cutoff string) {
	slog.Info("feed items caught up", "feed_id", feedID, "items", affectedCount, "cutoff", cutoff)
}

func catchUpResultURL(feedID int64, affectedCount int, cutoff time.Time) string {
	query := url.Values{}
	query.Set("catch_up_feed_id", strconv.FormatInt(feedID, 10))
	query.Set(catchUpPreviewCountField, strconv.Itoa(affectedCount))
	query.Set("catch_up_cutoff", cutoff.Format(time.RFC3339Nano))

	return "/?" + query.Encode()
}

func parseCatchUpResultFeedID(r *http.Request) int64 {
	query := r.URL.Query()
	countValue := strings.TrimSpace(query.Get(catchUpPreviewCountField))
	count, err := strconv.Atoi(countValue)
	if err != nil || count < 0 {
		return 0
	}

	cutoffValue := strings.TrimSpace(query.Get("catch_up_cutoff"))
	cutoff, err := time.Parse(time.RFC3339Nano, cutoffValue)
	if err != nil || cutoff.IsZero() {
		return 0
	}

	feedID, err := strconv.ParseInt(strings.TrimSpace(query.Get("catch_up_feed_id")), 10, 64)
	if err != nil || feedID <= 0 {
		return 0
	}

	return feedID
}

func mobileCatchUpResultURL(
	selectedFeedID int64,
	state mobileAggregateState,
	affectedCount int,
	cutoff time.Time,
) string {
	path := mobileStreamStatePath(selectedFeedID, state)
	parsedPath, err := url.Parse(path)
	if err != nil {
		return "/mobile/stream"
	}
	query := parsedPath.Query()
	query.Set(catchUpPreviewCountField, strconv.Itoa(affectedCount))
	query.Set("catch_up_cutoff", cutoff.Format(time.RFC3339Nano))

	return parsedPath.Path + "?" + query.Encode()
}

func isMobileCatchUpSurface(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.FormValue(catchUpSurfaceField)), catchUpMobileSurface)
}

func (a *App) renderBulkReadUndoResponse(w http.ResponseWriter, r *http.Request, feedID int64) {
	if isMobileCatchUpSurface(r) {
		a.renderMobileStream(w, r)

		return
	}

	a.renderItemListResponse(w, r, feedID)
}
