package server

import "rss/internal/view"

type fullPageData struct {
	CSRFToken       string
	AppearanceTheme string
	ThemeReturnPath string
}

type pageData struct {
	fullPageData

	ItemList       *view.ItemListData
	MobileStream   *mobileStreamResponseData
	MobileReader   *mobileReaderResponseData
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
	LastError      string
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
	UpdatePanel    bool
}

type itemExpandedResponseData struct {
	CollapseItem *view.ItemView
	Item         view.ItemView
}

type mobileStreamResponseData struct {
	StatusMessage string
	Items         []view.ItemView
}

type mobileReaderResponseData struct {
	Item view.ItemView
}

type authLoginPageData struct {
	Message        string
	AutoStartLogin bool
	ShowSetupLink  bool
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
	fullPageData

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
