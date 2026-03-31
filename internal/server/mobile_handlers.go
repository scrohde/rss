package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"rss/internal/feed"
	"rss/internal/store"
	"rss/internal/view"
)

func (a *App) handleMobileStream(w http.ResponseWriter, r *http.Request) {
	a.renderMobileStream(w, r, "")
}

func (a *App) handleMobileReader(w http.ResponseWriter, r *http.Request) {
	itemID, ok := parsePathInt64(r, "itemID")
	if !ok {
		http.NotFound(w, r)

		return
	}

	item, err := store.GetUnreadStreamItem(r.Context(), a.db, itemID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			a.renderMobileStream(w, r, "That story is no longer in your unread stream.")

			return
		}

		http.Error(w, "failed to load item", http.StatusInternalServerError)

		return
	}

	topBar, err := a.mobileTopBarData(r)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return
	}

	data := mobileReaderResponseData{
		Item:   item,
		TopBar: topBar,
	}
	a.renderMobileReader(w, r, &data)
}

func (a *App) handleMobileMarkRead(w http.ResponseWriter, r *http.Request) {
	itemID, ok := parsePathInt64(r, "itemID")
	if !ok {
		http.NotFound(w, r)

		return
	}

	err := store.MarkItemRead(r.Context(), a.db, itemID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			a.renderMobileStream(w, r, "That story was already cleared.")

			return
		}

		http.Error(w, "failed to mark item as read", http.StatusInternalServerError)

		return
	}

	a.renderMobileStream(w, r, "Saved for now.")
}

func (a *App) handleMobilePulse(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), mobilePulseTimeout)
	defer cancel()

	feedIDs, err := a.mobilePulseFeedIDs(ctx)
	if err != nil {
		http.Error(w, "failed to pulse feeds", http.StatusInternalServerError)

		return
	}

	if len(feedIDs) == 0 {
		a.renderMobileStream(w, r, "Already fresh enough.")

		return
	}

	updated, pulseErr := a.runMobilePulseRefresh(ctx, feedIDs)
	status := mobilePulseStatusMessage(updated, pulseErr)

	a.renderMobileStream(w, r, status)
}

func (a *App) mobilePulseFeedIDs(ctx context.Context) ([]int64, error) {
	cutoff := time.Now().UTC().Add(-pulseRecentRefreshWindow)

	feedIDs, err := store.ListPulseFeedIDs(ctx, a.db, cutoff)
	if err != nil {
		return nil, fmt.Errorf("list pulse feed IDs: %w", err)
	}

	return feedIDs, nil
}

func (a *App) runMobilePulseRefresh(ctx context.Context, feedIDs []int64) (int, error) {
	updated := 0

	for _, feedID := range feedIDs {
		if shouldStopMobilePulse(ctx) {
			break
		}

		updated += a.mobilePulseRefreshDelta(ctx, feedID)
	}

	ctxErr := wrapMobilePulseContextErr(ctx)
	if ctxErr != nil {
		return updated, ctxErr
	}

	return updated, nil
}

func shouldStopMobilePulse(ctx context.Context) bool {
	return ctx.Err() != nil
}

func (a *App) mobilePulseRefreshDelta(ctx context.Context, feedID int64) int {
	refreshed, refreshErr := a.refreshFeedForMobilePulse(ctx, feedID)
	if refreshErr != nil {
		return 0
	}

	if refreshed {
		return 1
	}

	return 0
}

func (a *App) refreshFeedForMobilePulse(ctx context.Context, feedID int64) (bool, error) {
	a.refreshMu.Lock()
	refreshedID, refreshErr := feed.Refresh(ctx, a.db, feedID)
	a.refreshMu.Unlock()

	if refreshErr != nil {
		// Do not include upstream error text in user-adjacent logs.
		slog.Warn("mobile pulse feed refresh failed", "feed_id", feedID)

		return false, fmt.Errorf("refresh feed %d: %w", feedID, refreshErr)
	}

	return refreshedID != 0, nil
}

func wrapMobilePulseContextErr(ctx context.Context) error {
	ctxErr := ctx.Err()
	if ctxErr == nil {
		return nil
	}

	return fmt.Errorf("mobile pulse context: %w", ctxErr)
}

func mobilePulseStatusMessage(updated int, pulseErr error) string {
	if errors.Is(pulseErr, context.DeadlineExceeded) {
		return fmt.Sprintf("Updated %d feeds before timeout.", updated)
	}

	return fmt.Sprintf("Updated %d feeds.", updated)
}

func (a *App) renderMobileStream(w http.ResponseWriter, r *http.Request, statusMessage string) {
	topBar, ok := a.mobileTopBarOrError(w, r, "failed to load feeds")
	if !ok {
		return
	}

	items, ok := a.mobileStreamItemsOrError(w, r, topBar.SelectedFeedID, "failed to load unread items")
	if !ok {
		return
	}

	data := mobileStreamResponseData{
		Items:         items,
		StatusMessage: statusMessage,
		TopBar:        topBar,
	}

	if isHTMXRequest(r) {
		w.Header().Set("Hx-Replace-Url", mobileStreamPath(topBar.SelectedFeedID))

		if isMobileStreamSelectorTrigger(r) {
			a.renderTemplate(w, "mobile_stream_selector_response", data)
		} else {
			a.renderTemplate(w, "mobile_stream_response", data)
		}

		return
	}

	page, ok := a.mobilePageDataOrError(w, r, "failed to load feeds")
	if !ok {
		return
	}

	page.MobileTopBar = &data.TopBar
	page.MobileStream = &data
	a.renderTemplate(w, "index", page)
}

func (a *App) renderMobileReader(w http.ResponseWriter, r *http.Request, data *mobileReaderResponseData) {
	if isHTMXRequest(r) {
		w.Header().Set("Hx-Push-Url", r.URL.RequestURI())
		a.renderTemplate(w, "mobile_reader_response", data)

		return
	}

	page, ok := a.mobilePageDataOrError(w, r, "failed to load feeds")
	if !ok {
		return
	}

	page.MobileTopBar = &data.TopBar
	page.MobileReader = data
	a.renderTemplate(w, "index", page)
}

type mobileStreamSelection struct {
	FeedTitle string
	Options   []view.FeedView
	FeedID    int64
}

func (a *App) mobileStreamFeedOptions(r *http.Request) (mobileStreamSelection, error) {
	feeds, err := store.ListFeeds(r.Context(), a.db)
	if err != nil {
		return mobileStreamSelection{}, fmt.Errorf("list feeds: %w", err)
	}

	feedOptions := unreadFeedOptions(feeds)
	selectedFeedID := normalizeSelectedFeedID(parseSelectedFeedID(r), feeds)
	selectedFeedTitle := feedTitleByID(selectedFeedID, feeds)

	return mobileStreamSelection{
		FeedID:    selectedFeedID,
		FeedTitle: selectedFeedTitle,
		Options:   feedOptions,
	}, nil
}

func (a *App) mobileTopBarData(r *http.Request) (mobileTopBarData, error) {
	selection, err := a.mobileStreamFeedOptions(r)
	if err != nil {
		return mobileTopBarData{}, err
	}

	return mobileTopBarData{
		FeedOptions:              selection.Options,
		SelectedFeedTitle:        selection.FeedTitle,
		SelectedFeedID:           selection.FeedID,
		ShowCaughtUpSelectedFeed: shouldShowCaughtUpSelectedFeed(selection),
		PulsePath:                mobilePulsePath(selection.FeedID),
	}, nil
}

func shouldShowCaughtUpSelectedFeed(selection mobileStreamSelection) bool {
	if selection.FeedID <= 0 || selection.FeedTitle == "" {
		return false
	}

	return !feedOptionsContainID(selection.Options, selection.FeedID)
}

func (a *App) mobileStreamItems(r *http.Request, selectedFeedID int64) ([]view.ItemView, error) {
	if selectedFeedID > 0 {
		items, err := store.ListUnreadItemsByFeed(r.Context(), a.db, selectedFeedID, mobileStreamLimit)
		if err != nil {
			return nil, fmt.Errorf("list unread items for feed %d: %w", selectedFeedID, err)
		}

		return items, nil
	}

	items, err := store.ListUnreadItemsAllFeeds(r.Context(), a.db, mobileStreamLimit)
	if err != nil {
		return nil, fmt.Errorf("list unread items across feeds: %w", err)
	}

	return items, nil
}

func (a *App) mobilePageData(r *http.Request) (pageData, error) {
	feeds, err := store.ListFeeds(r.Context(), a.db)
	if err != nil {
		return pageData{}, fmt.Errorf("list feeds: %w", err)
	}

	data, err := a.newPageData(r)
	if err != nil {
		return pageData{}, err
	}

	data.Feeds = feeds
	data.SelectedFeedID = 0
	data.FeedEditMode = feedEditModeEnabled(r)

	return data, nil
}
