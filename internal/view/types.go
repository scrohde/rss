package view

import "html/template"

type FeedView struct {
	ID                 int64
	Title              string
	URL                string
	ItemCount          int
	UnreadCount        int
	LastRefreshDisplay string
	LastError          string
}

type ItemView struct {
	ID               int64
	Title            string
	Link             string
	SummaryHTML      template.HTML
	PublishedDisplay string
	PublishedCompact string
	IsRead           bool
	IsActive         bool
}

type ItemListData struct {
	Feed     FeedView
	Items    []ItemView
	NewestID int64
	NewItems NewItemsData
}

type PageData struct {
	Feeds             []FeedView
	SelectedFeedID    int64
	ItemList          *ItemListData
	SkipDeleteWarning bool
	FeedEditMode      bool
}

type SubscribeResponseData struct {
	Message           string
	MessageClass      string
	Feeds             []FeedView
	SelectedFeedID    int64
	ItemList          *ItemListData
	Update            bool
	SkipDeleteWarning bool
	FeedEditMode      bool
}

type NewItemsData struct {
	FeedID  int64
	Count   int
	SwapOOB bool
}

type NewItemsResponseData struct {
	Items    []ItemView
	NewestID int64
	Banner   NewItemsData
}

type PollResponseData struct {
	Banner            NewItemsData
	Feeds             []FeedView
	RefreshDisplay    string
	SelectedFeedID    int64
	SkipDeleteWarning bool
	FeedEditMode      bool
}

type ItemListResponseData struct {
	ItemList          *ItemListData
	Feeds             []FeedView
	SelectedFeedID    int64
	SkipDeleteWarning bool
	FeedEditMode      bool
}

type ToggleReadResponseData struct {
	Item              ItemView
	Feeds             []FeedView
	SelectedFeedID    int64
	View              string
	SkipDeleteWarning bool
	FeedEditMode      bool
}

type DeleteFeedConfirmData struct {
	Feed FeedView
	Show bool
}

type RenameFeedFormData struct {
	Feed FeedView
	Show bool
}

type RenameFeedResponseData struct {
	FeedID            int64
	ItemList          *ItemListData
	Feeds             []FeedView
	SelectedFeedID    int64
	SkipDeleteWarning bool
	FeedEditMode      bool
}
