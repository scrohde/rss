package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"rss/internal/store"
)

const (
	mobileAfterFeedOrderParam  = "after_feed_order"
	mobileAfterFeedIDParam     = "after_feed_id"
	mobileAggregateFeedIDParam = "aggregate_feed_id"
	mobileBeforeItemSortParam  = "before_item_sort"
	mobileBeforeItemIDParam    = "before_item_id"
	mobileSectionFeedIDParam   = "section_feed_id"
	mobileSectionOnlyParam     = "section_only"
)

// mobileAggregateState keeps one canonical older-item page; transient pages in other sections reset on batch repair.
type mobileAggregateState struct {
	FeedCursor *store.UnreadFeedCursor
	ItemCursor *store.UnreadItemCursor
	FeedID     int64
}

func parseMobileAggregateState(r *http.Request) mobileAggregateState {
	state := mobileAggregateState{
		FeedCursor: nil,
		ItemCursor: nil,
		FeedID:     parsePositiveFormInt64(r, mobileAggregateFeedIDParam),
	}

	afterFeedOrder := parsePositiveFormInt(r, mobileAfterFeedOrderParam)
	afterFeedID := parsePositiveFormInt64(r, mobileAfterFeedIDParam)

	if afterFeedOrder > 0 && afterFeedID > 0 {
		state.FeedCursor = &store.UnreadFeedCursor{SortOrder: afterFeedOrder, FeedID: afterFeedID}
	}

	beforeItemSort := strings.TrimSpace(r.FormValue(mobileBeforeItemSortParam))
	beforeItemID := parsePositiveFormInt64(r, mobileBeforeItemIDParam)

	if beforeItemSort != "" && beforeItemID > 0 {
		state.ItemCursor = &store.UnreadItemCursor{SortKey: beforeItemSort, ItemID: beforeItemID}
	}

	return state
}

func parsePositiveFormInt(r *http.Request, key string) int {
	raw := strings.TrimSpace(r.FormValue(key))
	parsed, err := strconv.Atoi(raw)

	if err != nil || parsed <= 0 {
		return 0
	}

	return parsed
}

func parsePositiveFormInt64(r *http.Request, key string) int64 {
	raw := strings.TrimSpace(r.FormValue(key))
	parsed, err := strconv.ParseInt(raw, 10, 64)

	if err != nil || parsed <= 0 {
		return 0
	}

	return parsed
}

func mobileSectionOnlyRequest(r *http.Request) bool {
	return strings.TrimSpace(r.FormValue(mobileSectionOnlyParam)) == "1"
}

func mobileSectionFeedID(r *http.Request) int64 {
	return parsePositiveFormInt64(r, mobileSectionFeedIDParam)
}

func mobileStreamStatePath(selectedFeedID int64, state mobileAggregateState) string {
	if selectedFeedID > 0 {
		return mobileStreamPath(selectedFeedID)
	}

	return pathWithQuery("/mobile/stream", mobileAggregateStateValues(state))
}

func mobilePulseStatePath(selectedFeedID int64, state mobileAggregateState) string {
	if selectedFeedID > 0 {
		return mobilePulsePath(selectedFeedID)
	}

	return pathWithQuery("/mobile/pulse", mobileAggregateStateValues(state))
}

func mobileReaderItemPath(itemID, selectedFeedID int64, state mobileAggregateState) string {
	values := make(url.Values)
	if selectedFeedID > 0 {
		values.Set("selected_feed_id", strconv.FormatInt(selectedFeedID, 10))
	} else {
		addMobileAggregateStateValues(values, state)
	}

	return pathWithQuery(fmt.Sprintf("/mobile/items/%d/reader", itemID), values)
}

func mobileMarkReadItemPath(itemID, selectedFeedID int64, state mobileAggregateState) string {
	values := make(url.Values)
	if selectedFeedID > 0 {
		values.Set("selected_feed_id", strconv.FormatInt(selectedFeedID, 10))
	} else {
		addMobileAggregateStateValues(values, state)
	}

	return pathWithQuery(fmt.Sprintf("/mobile/items/%d/read", itemID), values)
}

func mobileSectionMarkReadItemPath(itemID, sectionFeedID int64, state mobileAggregateState) string {
	values := mobileAggregateStateValues(state)
	values.Set(mobileSectionFeedIDParam, strconv.FormatInt(sectionFeedID, 10))
	values.Set(mobileSectionOnlyParam, "1")

	return pathWithQuery(fmt.Sprintf("/mobile/items/%d/read", itemID), values)
}

func mobileFeedItemsPagePath(feedID int64, state mobileAggregateState) string {
	return pathWithQuery(
		fmt.Sprintf("/mobile/feeds/%d/items", feedID),
		mobileAggregateStateValues(state),
	)
}

func mobileFeedSectionsPagePath(cursor *store.UnreadFeedCursor) string {
	state := mobileAggregateState{FeedCursor: cursor, ItemCursor: nil, FeedID: 0}

	return pathWithQuery("/mobile/stream/sections", mobileAggregateStateValues(state))
}

func mobileAggregateQuery(state mobileAggregateState) string {
	values := mobileAggregateStateValues(state)

	if len(values) == 0 {
		return ""
	}

	return "?" + values.Encode()
}

func mobileAggregateSectionQuery(state mobileAggregateState, sectionFeedID int64) string {
	values := mobileAggregateStateValues(state)
	values.Set(mobileSectionFeedIDParam, strconv.FormatInt(sectionFeedID, 10))
	values.Set(mobileSectionOnlyParam, "1")

	return "?" + values.Encode()
}

func mobileAggregateStateValues(state mobileAggregateState) url.Values {
	values := make(url.Values)
	addMobileAggregateStateValues(values, state)

	return values
}

func addMobileAggregateStateValues(values url.Values, state mobileAggregateState) {
	if state.FeedCursor != nil {
		values.Set(mobileAfterFeedOrderParam, strconv.Itoa(state.FeedCursor.SortOrder))
		values.Set(mobileAfterFeedIDParam, strconv.FormatInt(state.FeedCursor.FeedID, 10))
	}

	if state.FeedID > 0 {
		values.Set(mobileAggregateFeedIDParam, strconv.FormatInt(state.FeedID, 10))
	}

	if state.ItemCursor != nil {
		values.Set(mobileBeforeItemSortParam, state.ItemCursor.SortKey)
		values.Set(mobileBeforeItemIDParam, strconv.FormatInt(state.ItemCursor.ItemID, 10))
	}
}

func pathWithQuery(path string, values url.Values) string {
	if len(values) == 0 {
		return path
	}

	return path + "?" + values.Encode()
}
