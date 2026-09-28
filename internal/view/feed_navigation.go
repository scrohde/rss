package view

import (
	"html/template"
	"strconv"
)

// TodayFeedID identifies the built-in feed without occupying a subscription row.
const TodayFeedID int64 = -1

// TodayFeed returns the built-in, time-filtered feed.
func TodayFeed() FeedView {
	var feed FeedView

	feed.ID = TodayFeedID
	feed.Title = "Today"
	feed.SortOrder = -1

	return feed
}

// IsToday reports whether this feed uses the all-source 24-hour query.
//
//nolint:gocritic // Value receivers are required for non-addressable template values.
func (feed FeedView) IsToday() bool { return feed.ID == TodayFeedID }

// ItemsPath is the navigation address for this feed.
//
//nolint:gocritic // Value receivers are required for non-addressable template values.
func (feed FeedView) ItemsPath() string {
	if feed.IsToday() {
		return "/today"
	}

	return "/feeds/" + strconv.FormatInt(feed.ID, 10) + "/items"
}

type feedLinkData struct {
	Status     any
	Feed       FeedView
	Selected   bool
	ShowCounts bool
}

// TemplateFuncs supplies shared feed navigation models to all template renderers.
func TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"navigationFeeds": func(feeds []FeedView) []FeedView {
			return append([]FeedView{TodayFeed()}, feeds...)
		},
		"feedLink": func(feed FeedView, selected int64, counts bool, status any) feedLinkData {
			return feedLinkData{Feed: feed, Status: status, Selected: feed.ID == selected, ShowCounts: counts}
		},
	}
}
