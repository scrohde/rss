package server

import (
	"net/http"

	"rss/internal/store"
	"rss/internal/view"
)

func parseFormOrBadRequest(w http.ResponseWriter, r *http.Request, message string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)

	err := r.ParseForm()
	if err != nil {
		http.Error(w, message, http.StatusBadRequest)

		return false
	}

	return true
}

func (a *App) listFeedsOrError(w http.ResponseWriter, r *http.Request) ([]view.FeedView, bool) {
	feeds, err := store.ListFeeds(r.Context(), a.db)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return nil, false
	}

	return feeds, true
}

func (a *App) itemListOrError(
	w http.ResponseWriter,
	r *http.Request,
	feedID int64,
	feeds []view.FeedView,
) (*view.ItemListData, bool) {
	itemList, err := store.LoadItemList(r.Context(), a.db, feedID)
	if err != nil {
		http.Error(w, "failed to load items", http.StatusInternalServerError)

		return nil, false
	}

	itemList = a.attachMarkAllReadUndo(itemList)

	return attachFeedContinuation(itemList, feeds), true
}

func attachFeedContinuation(itemList *view.ItemListData, feeds []view.FeedView) *view.ItemListData {
	if itemList == nil {
		return nil
	}

	itemList.Continuation = view.BuildFeedContinuation(itemList.Feed.ID, feeds)

	return itemList
}

func feedContinuationOOB(currentFeedID int64, feeds []view.FeedView) view.FeedContinuationData {
	continuation := view.BuildFeedContinuation(currentFeedID, feeds)
	continuation.SwapOOB = true

	return continuation
}

func (a *App) pageDataOrError(
	w http.ResponseWriter,
	r *http.Request,
	message string,
) (pageData, bool) {
	data, err := a.newPageData(r)
	if err != nil {
		http.Error(w, message, http.StatusInternalServerError)

		var zero pageData

		return zero, false
	}

	return data, true
}

func (a *App) mobilePageDataOrError(
	w http.ResponseWriter,
	r *http.Request,
	message string,
) (pageData, bool) {
	data, err := a.mobilePageData(r)
	if err != nil {
		http.Error(w, message, http.StatusInternalServerError)

		var zero pageData

		return zero, false
	}

	return data, true
}

func (a *App) mobileTopBarOrError(
	w http.ResponseWriter,
	r *http.Request,
	message string,
) (mobileTopBarData, bool) {
	topBar, err := a.mobileTopBarData(r)
	if err != nil {
		http.Error(w, message, http.StatusInternalServerError)

		var zero mobileTopBarData

		return zero, false
	}

	return topBar, true
}

func (a *App) mobileStreamItemsOrError(
	w http.ResponseWriter,
	r *http.Request,
	selectedFeedID int64,
	message string,
) ([]view.ItemView, bool) {
	items, err := a.mobileStreamItems(r, selectedFeedID)
	if err != nil {
		http.Error(w, message, http.StatusInternalServerError)

		return nil, false
	}

	return items, true
}

type feedEditRenderState struct {
	itemList       *view.ItemListData
	selectedFeedID int64
}

func (a *App) feedEditRenderStateOrError(
	w http.ResponseWriter,
	r *http.Request,
	selectedFeedID int64,
	deletedFeedID int64,
	feeds []view.FeedView,
	message string,
) (feedEditRenderState, bool) {
	nextFeedID, itemList, err := a.feedEditSelection(r.Context(), selectedFeedID, deletedFeedID, feeds)
	if err != nil {
		http.Error(w, message, http.StatusInternalServerError)

		var zero feedEditRenderState

		return zero, false
	}

	return feedEditRenderState{
		selectedFeedID: nextFeedID,
		itemList:       itemList,
	}, true
}
