package server

import "rss/internal/view"

type pageData struct {
	ItemList       *view.ItemListData
	CSRFToken      string
	Feeds          []view.FeedView
	SelectedFeedID int64
	FeedEditMode   bool
}

type subscribeResponseData struct {
	ItemList       *view.ItemListData
	Message        string
	MessageClass   string
	Feeds          []view.FeedView
	SelectedFeedID int64
	Update         bool
	FeedEditMode   bool
}

type newItemsResponseData struct {
	Items    []view.ItemView
	NewestID int64
	Banner   view.NewItemsData
}

type pollResponseData struct {
	RefreshDisplay string
	Feeds          []view.FeedView
	Banner         view.NewItemsData
	SelectedFeedID int64
	FeedEditMode   bool
}

type itemListResponseData struct {
	ItemList       *view.ItemListData
	Feeds          []view.FeedView
	SelectedFeedID int64
	FeedEditMode   bool
}

type toggleReadResponseData struct {
	View           string
	Feeds          []view.FeedView
	Item           view.ItemView
	SelectedFeedID int64
	FeedEditMode   bool
}

type authLoginPageData struct {
	Message string
}

type authSetupPageData struct {
	Message               string
	RegistrationURL       string
	SetupUnlocked         bool
	HasCredentials        bool
	SetupTokenSet         bool
	AutoStartRegistration bool
}

type authSecurityPageData struct {
	CSRFToken          string
	RecoveryCode       string
	RegistrationURL    string
	RecoveryEnabledURL string
	Message            string
	PasskeyCount       int
	HasRecoveryCode    bool
}

type authRecoveryPageData struct {
	Message string
}
