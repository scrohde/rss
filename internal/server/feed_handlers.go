package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"rss/internal/feed"
	"rss/internal/store"
	"rss/internal/view"
)

var errFeedReturnedNoContent = errors.New("feed returned no content")

func (a *App) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if !parseFormOrBadRequest(w, r, "invalid form") {
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

	robotsErr := feed.CheckRobotsAllowed(ctx, feedURL)
	if robotsErr != nil {
		slog.Warn("subscribe blocked by robots policy", "feed_url", feedURL, "err", robotsErr)

		return 0, fmt.Errorf("check robots policy: %w", robotsErr)
	}

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

func (a *App) handleEnterFeedEditMode(w http.ResponseWriter, r *http.Request) {
	setFeedEditModeCookie(w)

	feeds, ok := a.listFeedsOrError(w, r)
	if !ok {
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

	feeds, ok := a.listFeedsOrError(w, r)
	if !ok {
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
	if !parseFormOrBadRequest(w, r, "invalid form") {
		return
	}

	selectedFeedID := parseSelectedFeedID(r)

	feeds, ok := a.listFeedsOrError(w, r)
	if !ok {
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
	feeds, ok := a.listFeedsOrError(w, r)
	if !ok {
		return
	}

	selection, ok := a.feedEditRenderStateOrError(
		w,
		r,
		selectedFeedID,
		deletedFeedID,
		feeds,
		"failed to load items",
	)
	if !ok {
		return
	}

	var data itemListResponseData

	data.ItemList = selection.itemList
	data.Feeds = feeds
	data.SelectedFeedID = selection.selectedFeedID
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
	lastError := ""

	for _, listedFeed := range feeds {
		if listedFeed.ID == feedID {
			refreshDisplay = listedFeed.LastRefreshDisplay
			lastError = listedFeed.LastError

			break
		}
	}

	var data pollResponseData

	data.Banner = view.NewItemsData{FeedID: feedID, Count: count, SwapOOB: false}
	data.Feeds = feeds
	data.RefreshDisplay = refreshDisplay
	data.LastError = lastError
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
	for idx := range items {
		if items[idx].ID > newestID {
			newestID = items[idx].ID
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
	item.IsExpanded = true

	var collapseItem *view.ItemView

	collapseID := parseOptionalItemID(r, "collapse_item_id")
	if collapseID != 0 && collapseID != item.ID {
		collapsedItem, collapsedErr := store.GetItem(r.Context(), a.db, collapseID)
		if collapsedErr == nil {
			collapsedItem.IsActive = false
			collapsedItem.IsExpanded = false
			collapseItem = &collapsedItem
		}
	}

	a.renderTemplate(w, "item_expanded_response", itemExpandedResponseData{
		Item:         item,
		CollapseItem: collapseItem,
	})
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
	item.IsExpanded = false
	a.renderTemplate(w, "item_compact_response", item)
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
	item.IsExpanded = currentView == "expanded"

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
		UpdatePanel:    true,
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

func (a *App) handlePulseFeeds(w http.ResponseWriter, r *http.Request) {
	cutoff := time.Now().UTC().Add(-pulseRecentRefreshWindow)

	feedIDs, err := store.ListPulseFeedIDs(r.Context(), a.db, cutoff)
	if err != nil {
		a.renderPulseMessage(w, "pulse failed", "error")

		return
	}

	if len(feedIDs) == 0 {
		a.renderPulseMessage(w, "No feeds to pulse.", "")

		return
	}

	if !a.startPulse(r.Context(), feedIDs) {
		a.renderPulseMessage(w, "Pulse already running.", "")

		return
	}

	a.renderPulseMessage(w, "", "")
}

func (a *App) startPulse(ctx context.Context, feedIDs []int64) bool {
	a.pulseMu.Lock()
	if a.pulseRunning {
		a.pulseMu.Unlock()

		return false
	}

	a.pulseRunning = true
	a.pulseMu.Unlock()

	feedIDsCopy := append([]int64(nil), feedIDs...)
	pulseCtx := context.WithoutCancel(ctx)

	go a.runPulse(pulseCtx, feedIDsCopy)

	return true
}

func (a *App) runPulse(ctx context.Context, feedIDs []int64) {
	defer a.finishPulse()

	slog.Info("pulse refresh started", "feeds", len(feedIDs))

	failed := 0

	for _, feedID := range feedIDs {
		a.refreshMu.Lock()
		_, err := feed.Refresh(ctx, a.db, feedID)
		a.refreshMu.Unlock()

		if err != nil {
			failed++

			slog.Warn("pulse refresh failed", "feed_id", feedID, "err", err)
		}
	}

	slog.Info(
		"pulse refresh finished",
		"feeds",
		len(feedIDs),
		"failed",
		failed,
	)
}

func (a *App) finishPulse() {
	a.pulseMu.Lock()
	a.pulseRunning = false
	a.pulseMu.Unlock()
}

func (a *App) isPulseRunning() bool {
	a.pulseMu.Lock()
	defer a.pulseMu.Unlock()

	return a.pulseRunning
}

//nolint:gosec // Delete logs include request-derived feed IDs for operational visibility.
func (a *App) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)

		return
	}

	if !parseFormOrBadRequest(w, r, "invalid form") {
		return
	}

	selectedFeedID := parseSelectedFeedID(r)

	deleteErr := store.DeleteFeed(r.Context(), a.db, feedID)
	if deleteErr != nil {
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
