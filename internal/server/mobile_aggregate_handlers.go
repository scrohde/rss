package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"rss/internal/store"
	"rss/internal/view"
)

func (a *App) handleMobileStreamSections(w http.ResponseWriter, r *http.Request) {
	state := parseMobileAggregateState(r)
	state.FeedID = 0
	state.ItemCursor = nil

	aggregate, err := a.mobileAggregateData(r.Context(), state)
	if err != nil {
		http.Error(w, "failed to load unread items", http.StatusInternalServerError)

		return
	}

	topBar, ok := a.mobileTopBarOrError(w, r, "failed to load feeds")
	if !ok {
		return
	}

	w.Header().Set("Hx-Push-Url", mobileStreamStatePath(0, state))
	a.renderTemplate(w, "mobile_stream_sections_response", mobileStreamSectionsResponseData{
		Aggregate: aggregate,
		TopBar:    topBar,
	})
}

func (a *App) handleMobileFeedItems(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parsePathInt64(r, "feedID")
	if !ok {
		http.NotFound(w, r)

		return
	}

	state := parseMobileAggregateState(r)
	if state.ItemCursor == nil {
		state.FeedID = 0
	} else {
		state.FeedID = feedID
	}

	a.renderMobileAggregateItemPageBatch(w, r, state, feedID)
}

func (a *App) mobileAggregateOrError(
	w http.ResponseWriter,
	r *http.Request,
	state mobileAggregateState,
	message string,
) (*mobileAggregateResponseData, bool) {
	aggregate, err := a.mobileAggregateData(r.Context(), state)
	if err != nil {
		http.Error(w, message, http.StatusInternalServerError)

		return nil, false
	}

	return aggregate, true
}

func (a *App) mobileAggregateData(
	ctx context.Context,
	state mobileAggregateState,
) (*mobileAggregateResponseData, error) {
	page, err := store.ListUnreadFeedSections(
		ctx,
		a.db,
		state.FeedCursor,
		mobileAggregateFeedPageLimit,
		mobileAggregateItemPageLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("list unread feed sections: %w", err)
	}

	sections := make([]mobileFeedSectionData, 0, len(page.Sections))
	for _, storedSection := range page.Sections {
		section, sectionErr := a.mobileAggregateSectionData(ctx, storedSection, state)
		if sectionErr != nil {
			return nil, sectionErr
		}

		sections = append(sections, section)
	}

	return &mobileAggregateResponseData{
		NextSectionsPath:  mobileFeedSectionsPagePath(page.Next),
		ResetSectionsPath: mobileFeedSectionsPagePath(nil),
		Sections:          sections,
		HasNext:           page.Next != nil,
		HasPrevious:       state.FeedCursor != nil,
	}, nil
}

func (a *App) mobileAggregateSectionData(
	ctx context.Context,
	storedSection store.UnreadFeedSection,
	state mobileAggregateState,
) (mobileFeedSectionData, error) {
	if state.FeedID != storedSection.FeedID || state.ItemCursor == nil {
		return buildMobileFeedSectionData(
			storedSection.FeedID,
			storedSection.FeedTitle,
			storedSection.Items,
			state,
			nil,
			storedSection.Next,
		), nil
	}

	itemPage, err := store.ListUnreadItemsByFeedPage(
		ctx,
		a.db,
		storedSection.FeedID,
		state.ItemCursor,
		mobileAggregateItemPageLimit,
	)
	if err != nil {
		return mobileFeedSectionData{}, fmt.Errorf(
			"list unread item page for feed %d: %w",
			storedSection.FeedID,
			err,
		)
	}

	return buildMobileFeedSectionData(
		storedSection.FeedID,
		storedSection.FeedTitle,
		itemPage.Items,
		state,
		state.ItemCursor,
		itemPage.Next,
	), nil
}

func buildMobileFeedSectionData(
	feedID int64,
	feedTitle string,
	items []view.ItemView,
	aggregateState mobileAggregateState,
	currentItemCursor *store.UnreadItemCursor,
	nextItemCursor *store.UnreadItemCursor,
) mobileFeedSectionData {
	newestState := aggregateState
	newestState.FeedID = feedID
	newestState.ItemCursor = nil

	olderState := aggregateState
	olderState.FeedID = feedID
	olderState.ItemCursor = nextItemCursor

	return mobileFeedSectionData{
		FeedTitle:       feedTitle,
		MarkReadQuery:   mobileAggregateSectionQuery(aggregateState, feedID),
		NewestItemsPath: mobileFeedItemsPagePath(feedID, newestState),
		OlderItemsPath:  mobileFeedItemsPagePath(feedID, olderState),
		ReaderQuery:     mobileAggregateQuery(aggregateState),
		Items:           items,
		FeedID:          feedID,
		HasOlder:        nextItemCursor != nil,
		IsOlderPage:     currentItemCursor != nil,
	}
}

func (a *App) renderMobileFeedSectionResponse(
	w http.ResponseWriter,
	r *http.Request,
	feedID int64,
	state mobileAggregateState,
) {
	section, remove, err := a.mobileFeedSectionPageData(r.Context(), feedID, state)
	if err != nil {
		http.Error(w, "failed to load unread items", http.StatusInternalServerError)

		return
	}

	if remove {
		a.renderMobileAggregateRepairedBatch(w, r, state, feedID)

		return
	}

	topBar, err := a.mobileTopBarDataForState(r, state)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return
	}

	data := mobileFeedSectionResponseData{
		Section: section,
		TopBar:  topBar,
	}
	a.renderTemplate(w, "mobile_feed_section_response", data)
}

func (a *App) mobileFeedSectionPageData(
	ctx context.Context,
	feedID int64,
	state mobileAggregateState,
) (mobileFeedSectionData, bool, error) {
	feedView, err := store.GetFeed(ctx, a.db, feedID)
	if errors.Is(err, sql.ErrNoRows) {
		var zero mobileFeedSectionData

		return zero, true, nil
	}

	if err != nil {
		var zero mobileFeedSectionData

		return zero, false, fmt.Errorf("get feed %d: %w", feedID, err)
	}

	if feedView.UnreadCount == 0 {
		var zero mobileFeedSectionData

		return zero, true, nil
	}

	currentItemCursor := state.ItemCursor
	if state.FeedID != feedID {
		currentItemCursor = nil
	}

	page, err := store.ListUnreadItemsByFeedPage(
		ctx,
		a.db,
		feedID,
		currentItemCursor,
		mobileAggregateItemPageLimit,
	)
	if err != nil {
		var zero mobileFeedSectionData

		return zero, false, fmt.Errorf("list unread items for feed %d: %w", feedID, err)
	}

	section := buildMobileFeedSectionData(
		feedID,
		feedView.Title,
		page.Items,
		state,
		currentItemCursor,
		page.Next,
	)

	return section, false, nil
}

func (a *App) renderMobileAggregateItemPageBatch(
	w http.ResponseWriter,
	r *http.Request,
	state mobileAggregateState,
	feedID int64,
) {
	reswap := fmt.Sprintf("outerHTML show:#mobile-feed-section-%d:top", feedID)
	a.renderMobileAggregateBatch(w, r, state, reswap)
}

func (a *App) renderMobileAggregateBatch(
	w http.ResponseWriter,
	r *http.Request,
	state mobileAggregateState,
	reswap string,
) {
	aggregate, err := a.mobileAggregateData(r.Context(), state)
	if err != nil {
		http.Error(w, "failed to load unread items", http.StatusInternalServerError)

		return
	}

	topBar, err := a.mobileTopBarDataForState(r, state)
	if err != nil {
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Hx-Replace-Url", mobileStreamStatePath(0, state))
	w.Header().Set("Hx-Retarget", "#mobile-stream-sections")
	w.Header().Set("Hx-Reswap", reswap)
	a.renderTemplate(w, "mobile_stream_sections_response", mobileStreamSectionsResponseData{
		Aggregate: aggregate,
		TopBar:    topBar,
	})
}

func (a *App) renderMobileAggregateRepairedBatch(
	w http.ResponseWriter,
	r *http.Request,
	state mobileAggregateState,
	removedFeedID int64,
) {
	if state.FeedID == removedFeedID {
		state.FeedID = 0
		state.ItemCursor = nil
	}

	a.renderMobileAggregateBatch(w, r, state, "outerHTML show:top")
}
