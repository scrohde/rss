package server

import "rss/internal/view"

type fullPageData struct {
	CSRFToken       string
	AppearanceTheme string
	ThemeReturnPath string
}

type pageData struct {
	fullPageData

	ItemList          *view.ItemListData
	MobileStream      *mobileStreamResponseData
	MobileReader      *mobileReaderResponseData
	MobileTopBar      *mobileTopBarData
	FeedPulseStatuses map[int64]*pulseFeedStatusView
	Feeds             []view.FeedView
	SelectedFeedID    int64
	FeedEditMode      bool
}

type subscribeResponseData struct {
	ItemList          *view.ItemListData
	FeedPulseStatuses map[int64]*pulseFeedStatusView
	Message           string
	MessageClass      string
	Feeds             []view.FeedView
	SelectedFeedID    int64
	Update            bool
	FeedEditMode      bool
}

type newItemsResponseData struct {
	Items    []view.ItemView
	NewestID int64
	Banner   view.NewItemsData
}

type pollResponseData struct {
	FeedPulseStatuses map[int64]*pulseFeedStatusView
	RefreshDisplay    string
	LastError         string
	Feeds             []view.FeedView
	Continuation      view.FeedContinuationData
	Banner            view.NewItemsData
	SelectedFeedID    int64
	FeedEditMode      bool
}

type itemListResponseData struct {
	ItemList          *view.ItemListData
	FeedPulseStatuses map[int64]*pulseFeedStatusView
	Feeds             []view.FeedView
	Continuation      view.FeedContinuationData
	SelectedFeedID    int64
	FeedEditMode      bool
}

type toggleReadResponseData struct {
	FeedPulseStatuses map[int64]*pulseFeedStatusView
	View              string
	Feeds             []view.FeedView
	Item              view.ItemView
	Continuation      view.FeedContinuationData
	SelectedFeedID    int64
	FeedEditMode      bool
	UpdatePanel       bool
}

type pulseStatusResponseData struct {
	FeedPulseStatuses map[int64]*pulseFeedStatusView
	Message           string
	MessageClass      string
	Feeds             []view.FeedView
	Continuation      view.FeedContinuationData
	SelectedFeedID    int64
	FeedEditMode      bool
	Running           bool
	Initial           bool
}

type itemExpandedResponseData struct {
	CollapseItem *view.ItemView
	Item         view.ItemView
}

type mobileTopBarData struct {
	SelectedFeedTitle        string
	PulseLabel               string
	PulsePendingLabel        string
	PulsePath                string
	FeedOptions              []view.FeedView
	SelectedFeedID           int64
	ShowCaughtUpSelectedFeed bool
}

type mobileStreamResponseData struct {
	Items  []view.ItemView
	TopBar mobileTopBarData
}

type mobileReaderResponseData struct {
	TopBar mobileTopBarData
	Item   view.ItemView
}

type authLoginPageData struct {
	Message        string
	Next           string
	SessionExpired bool
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
