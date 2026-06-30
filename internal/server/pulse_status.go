package server

import (
	"maps"
	"time"

	"rss/internal/view"
)

type pulseFeedStatus string

const (
	pulseFeedStatusNone    pulseFeedStatus = "none"
	pulseFeedStatusFresh   pulseFeedStatus = "fresh"
	pulseFeedStatusPending pulseFeedStatus = "pending"
	pulseFeedStatusError   pulseFeedStatus = "error"
)

type pulseFeedStatusView struct {
	Class string
	Label string
}

type pulseFeedStatusEntry struct {
	UpdatedAt time.Time
	Status    pulseFeedStatus
}

func feedIDsFromViews(feeds []view.FeedView) []int64 {
	ids := make([]int64, 0, len(feeds))
	for _, feed := range feeds {
		ids = append(ids, feed.ID)
	}

	return ids
}

func buildInitialPulseStatuses(allFeedIDs, pendingFeedIDs []int64) map[int64]pulseFeedStatusEntry {
	return buildInitialPulseStatusesAt(allFeedIDs, pendingFeedIDs, time.Now().UTC())
}

func buildInitialPulseStatusesAt(allFeedIDs, pendingFeedIDs []int64, now time.Time) map[int64]pulseFeedStatusEntry {
	statuses := make(map[int64]pulseFeedStatusEntry, len(allFeedIDs))
	for _, feedID := range allFeedIDs {
		statuses[feedID] = pulseFeedStatusEntry{
			Status:    pulseFeedStatusFresh,
			UpdatedAt: now,
		}
	}

	for _, feedID := range pendingFeedIDs {
		statuses[feedID] = pulseFeedStatusEntry{
			Status:    pulseFeedStatusPending,
			UpdatedAt: now,
		}
	}

	return statuses
}

func (a *App) resetPulseStatuses(allFeedIDs, pendingFeedIDs []int64) {
	a.pulseMu.Lock()
	a.pulseStatuses = buildInitialPulseStatuses(allFeedIDs, pendingFeedIDs)
	a.pulseMu.Unlock()
}

func (a *App) markPulseFeedStatus(feedID int64, status pulseFeedStatus) {
	a.markPulseFeedStatusAt(feedID, status, time.Now().UTC())
}

func (a *App) markPulseFeedStatusAt(feedID int64, status pulseFeedStatus, now time.Time) {
	a.pulseMu.Lock()
	if a.pulseStatuses == nil {
		a.pulseStatuses = make(map[int64]pulseFeedStatusEntry)
	}

	a.pulseStatuses[feedID] = pulseFeedStatusEntry{
		Status:    status,
		UpdatedAt: now,
	}
	a.pulseMu.Unlock()
}

func (a *App) pulseFeedStatuses() map[int64]pulseFeedStatus {
	a.pulseMu.Lock()
	defer a.pulseMu.Unlock()

	statuses := make(map[int64]pulseFeedStatus, len(a.pulseStatuses))
	for feedID, entry := range a.pulseStatuses {
		statuses[feedID] = entry.Status
	}

	return statuses
}

func (a *App) pulseFeedStatus(feedID int64) pulseFeedStatus {
	statuses := a.pulseFeedStatuses()
	if status, ok := statuses[feedID]; ok {
		return status
	}

	return pulseFeedStatusNone
}

func (a *App) pulseStatusViews() map[int64]*pulseFeedStatusView {
	return a.pulseStatusViewsAt(time.Now().UTC())
}

func (a *App) pulseStatusViewsAt(now time.Time) map[int64]*pulseFeedStatusView {
	entries := a.pulseFeedStatusEntries()
	views := make(map[int64]*pulseFeedStatusView, len(entries))

	for feedID, entry := range entries {
		statusView := pulseStatusView(entry, now)
		if statusView == nil {
			continue
		}

		views[feedID] = statusView
	}

	return views
}

func (a *App) pulseFeedStatusEntries() map[int64]pulseFeedStatusEntry {
	a.pulseMu.Lock()
	defer a.pulseMu.Unlock()

	entries := make(map[int64]pulseFeedStatusEntry, len(a.pulseStatuses))
	maps.Copy(entries, a.pulseStatuses)

	return entries
}

func pulseStatusView(entry pulseFeedStatusEntry, now time.Time) *pulseFeedStatusView {
	switch entry.Status {
	case pulseFeedStatusNone:
		return nil
	case pulseFeedStatusFresh:
		if !entry.UpdatedAt.IsZero() && now.Sub(entry.UpdatedAt) >= pulseRecentRefreshWindow {
			return nil
		}

		return &pulseFeedStatusView{Class: "fresh", Label: "Fresh"}
	case pulseFeedStatusPending:
		return &pulseFeedStatusView{Class: "pending", Label: "Refreshing"}
	case pulseFeedStatusError:
		return &pulseFeedStatusView{Class: "error", Label: "Refresh failed"}
	}

	return nil
}
