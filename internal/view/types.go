package view

import "html/template"

// FeedView is template data for one feed in the feed list.
type FeedView struct {
	Title              string
	OriginalTitle      string
	URL                string
	LastRefreshDisplay string
	LastError          string
	ID                 int64
	ItemCount          int
	UnreadCount        int
}

// ItemView is template data for one feed item row.
type ItemView struct {
	Title            string
	Link             string
	SummaryHTML      template.HTML
	PublishedDisplay string
	PublishedCompact string
	ID               int64
	IsRead           bool
	IsActive         bool
}

// NewItemsData is template data for the new-items banner.
type NewItemsData struct {
	FeedID  int64
	Count   int
	SwapOOB bool
}

// ItemListData is template data for a feed and its item list.
type ItemListData struct {
	Items    []ItemView
	Feed     FeedView
	NewItems NewItemsData
	NewestID int64
}
