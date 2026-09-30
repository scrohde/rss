package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rss/internal/store"
	"rss/internal/view"
)

var errInvalidTodayBatchIDs = errors.New("invalid Today batch IDs")

//nolint:cyclop,funlen,gocognit,revive // The Today route coordinates redirects, loading, and desktop/HTMX responses.
func (a *App) handleToday(w http.ResponseWriter, r *http.Request) {
	a.clearMarkAllReadUndoExcept(view.TodayFeedID)

	if isTodayMobileLayoutRequest(r) {
		if !isHTMXRequest(r) && (r.URL.Query().Has("batch_ids") || r.URL.Query().Has("transition")) {
			http.Redirect(w, r, "/today?layout=mobile", http.StatusSeeOther)

			return
		}

		a.renderMobileToday(w, r)

		return
	}

	if !isHTMXRequest(r) && (r.URL.Query().Has("batch_ids") || r.URL.Query().Has("transition")) {
		http.Redirect(w, r, "/today", http.StatusSeeOther)

		return
	}

	page, err := a.newPageData(r)
	if err != nil {
		http.Error(w, "failed to load page state", http.StatusInternalServerError)

		return
	}

	feeds, err := store.ListFeeds(r.Context(), a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return
	}

	items, err := a.todayItemsForRequest(r)
	if err != nil {
		http.Error(w, "failed to load Today stories", http.StatusBadRequest)

		return
	}

	itemList, ok := a.itemListOrError(w, r, view.TodayFeedID, feeds)
	if !ok {
		return
	}

	itemList.Items = items
	itemList.BatchIDsText = todayBatchIDsText(itemsToIDs(items))

	if isHTMXRequest(r) {
		if r.URL.Query().Get("transition") == "1" || r.URL.Query().Get("refresh") == "1" {
			w.Header().Set("Hx-Replace-Url", todayPath(itemsToIDs(items), false))
		}

		data := new(itemListResponseData)
		data.ItemList = itemList
		data.Feeds = feeds
		data.FeedPulseStatuses = a.pulseStatusViews()
		data.SelectedFeedID = view.TodayFeedID
		a.renderTemplateWithReadingPreferences(w, r, "item_list_response", data)

		return
	}

	page.ItemList = itemList
	page.Feeds = feeds
	page.FeedPulseStatuses = a.pulseStatusViews()
	page.SelectedFeedID = view.TodayFeedID
	page.FeedEditMode = false
	page.ThemeReturnPath = "/today"
	page.MobileTopBar = nil
	a.renderTemplate(w, "index", page)
}

//nolint:cyclop,funlen,gocognit,revive // Mobile Today coordinates batches, navigation headers, and response modes.
func (a *App) renderMobileToday(w http.ResponseWriter, r *http.Request) {
	page, err := a.newPageData(r)
	if err != nil {
		http.Error(w, "failed to load page state", http.StatusInternalServerError)

		return
	}

	feeds, err := store.ListFeeds(r.Context(), a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return
	}

	items, err := a.todayItemsForRequest(r)
	if err != nil {
		http.Error(w, "failed to load Today stories", http.StatusBadRequest)

		return
	}

	topBar, err := a.mobileTopBarData(r)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return
	}

	topBar.TodayMode = true
	topBar.PulseLabel = "Refresh Today stories"
	topBar.PulsePendingLabel = "Refreshing Today stories"
	topBar.PulsePath = mobileTodayPulsePath()

	data := mobileStreamResponseData{
		ReadingPreferences: page.ReadingPreferences,
		Aggregate:          nil,
		Items:              items,
		TopBar:             topBar,
		TodayMode:          true,
		TodayBatchIDs:      todayBatchIDsText(itemsToIDs(items)),
		TodaySettings:      newTodayViewData(&page, feeds, items),
		AllFeedsMode:       false,
		TodayCards:         nil,
	}

	data.TodayCards = make([]mobileTodayCardData, 0, len(items))
	for index := range items {
		data.TodayCards = append(data.TodayCards, mobileTodayCardData{
			Item:         items[index],
			BatchIDsText: data.TodayBatchIDs,
		})
	}

	a.clearMarkAllReadUndoExcept(0)

	if r.URL.Query().Get("transition") == "1" ||
		isTodayMobileReaderRequest(r) || r.URL.Path == "/mobile/pulse" {
		w.Header().Set("Hx-Replace-Url", todayPath(itemsToIDs(items), true))
	} else if isHTMXRequest(r) && !isHTMXHistoryRestoreRequest(r) {
		w.Header().Set("Hx-Push-Url", todayPath(itemsToIDs(items), true))
	}

	if isHTMXRequest(r) && !isHTMXHistoryRestoreRequest(r) {
		a.renderTemplateWithReadingPreferences(w, r, "mobile_stream_response", &data)

		return
	}

	page.MobileTopBar = &data.TopBar
	page.MobileStream = &data
	page.Feeds = feeds
	page.ThemeReturnPath = "/today"
	a.renderTemplate(w, "index", page)
}

func newTodayViewData(page *pageData, feeds []view.FeedView, items []view.ItemView) *todayViewData {
	selectedFeeds := make(map[int64]bool, len(page.ReadingPreferences.TodayFeedIDs))
	for _, feedID := range page.ReadingPreferences.TodayFeedIDs {
		selectedFeeds[feedID] = true
	}

	return &todayViewData{
		ReadingPreferences: page.ReadingPreferences,
		CSRFToken:          page.CSRFToken,
		Items:              items,
		Feeds:              feeds,
		FeedPulseStatuses:  nil,
		SelectedFeeds:      selectedFeeds,
		BatchIDs:           itemsToIDs(items),
		BatchIDsText:       todayBatchIDsText(itemsToIDs(items)),
	}
}

//nolint:cyclop,gocognit,revive // Stable and fresh paths have different validation and loading rules.
func (a *App) todayItemsForRequest(r *http.Request) ([]view.ItemView, error) {
	rawBatchIDs := strings.TrimSpace(r.URL.Query().Get("batch_ids"))
	if rawBatchIDs == "" || !isHTMXRequest(r) || r.URL.Query().Get("refresh") == "1" {
		items, err := store.ListTodayItems(r.Context(), a.db, time.Now().UTC())
		if err != nil {
			return nil, fmt.Errorf("list Today items: %w", err)
		}

		setTodayItemMode(items)

		return items, nil
	}

	batchIDs, valid := parseTodayBatchIDs(rawBatchIDs)
	if !valid {
		return nil, errInvalidTodayBatchIDs
	}

	items := make([]view.ItemView, 0, len(batchIDs))
	for _, itemID := range batchIDs {
		item, err := store.GetItem(r.Context(), a.db, itemID)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("load Today item %d: %w", itemID, err)
		}

		if errors.Is(err, sql.ErrNoRows) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("load Today item %d: %w", itemID, err)
		}

		item.TodayMode = true
		items = append(items, item)
	}

	return items, nil
}

func setTodayItemMode(items []view.ItemView) {
	for index := range items {
		items[index].TodayMode = true
	}
}

func itemsToIDs(items []view.ItemView) []int64 {
	ids := make([]int64, 0, len(items))
	for index := range items {
		ids = append(ids, items[index].ID)
	}

	return ids
}

func todayBatchIDsText(ids []int64) string {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, strconv.FormatInt(id, 10))
	}

	return strings.Join(values, ",")
}

func parseTodayBatchIDs(raw string) ([]int64, bool) {
	parts := strings.Split(raw, ",")
	if len(parts) == 0 {
		return nil, false
	}

	ids := make([]int64, 0, len(parts))

	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			return nil, false
		}

		if _, duplicate := seen[id]; duplicate {
			return nil, false
		}

		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	return ids, true
}

//nolint:revive // The boolean selects the canonical mobile variant at concise call sites.
func todayPath(ids []int64, mobile bool) string {
	values := make(url.Values)
	if mobile {
		values.Set("layout", "mobile")
	}

	if len(ids) > 0 {
		values.Set("batch_ids", todayBatchIDsText(ids))
	}

	return pathWithQuery("/today", values)
}

func isTodayMobileLayoutRequest(r *http.Request) bool {
	return r.URL.Path == "/today" && r.URL.Query().Get("layout") == "mobile"
}

func isTodayMobileReaderRequest(r *http.Request) bool {
	return r.URL.Query().Get("today") == "1"
}

func isMobileTodayRequest(r *http.Request) bool {
	return isTodayMobileLayoutRequest(r) || isTodayMobileReaderRequest(r) ||
		strings.TrimSpace(r.FormValue("selected_feed_id")) == strconv.FormatInt(view.TodayFeedID, 10)
}

func isMobileAllFeedsRequest(r *http.Request) bool {
	return r.URL.Query().Get("view") == "all" || r.FormValue("view") == "all"
}

func mobileTodayPulsePath() string {
	return "/mobile/pulse?today=1"
}
