package server

import (
	"rss/internal/store"
	"rss/internal/view"
)

type fullPageData struct {
	CSRFToken          string
	AppearanceTheme    string
	ThemeReturnPath    string
	ReadingPreferences store.ReadingPreferences
}

type pageData struct {
	fullPageData

	Today             *todayViewData
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
	ItemList           *view.ItemListData
	FeedPulseStatuses  map[int64]*pulseFeedStatusView
	Message            string
	MessageClass       string
	Feeds              []view.FeedView
	ReadingPreferences store.ReadingPreferences
	SelectedFeedID     int64
	Update             bool
	FeedEditMode       bool
}

type newItemsResponseData struct {
	Items    []view.ItemView
	NewestID int64
	Banner   view.NewItemsData
}

type pollResponseData struct {
	ReadingPreferences store.ReadingPreferences
	FeedPulseStatuses  map[int64]*pulseFeedStatusView
	RefreshDisplay     string
	LastError          string
	Feeds              []view.FeedView
	Continuation       view.FeedContinuationData
	Banner             view.NewItemsData
	SelectedFeedID     int64
	FeedEditMode       bool
}

type itemListResponseData struct {
	ReadingPreferences store.ReadingPreferences
	ItemList           *view.ItemListData
	FeedPulseStatuses  map[int64]*pulseFeedStatusView
	Feeds              []view.FeedView
	Continuation       view.FeedContinuationData
	SelectedFeedID     int64
	FeedEditMode       bool
}

type toggleReadResponseData struct {
	ReadingPreferences store.ReadingPreferences
	FeedPulseStatuses  map[int64]*pulseFeedStatusView
	View               string
	Feeds              []view.FeedView
	Item               view.ItemView
	Continuation       view.FeedContinuationData
	SelectedFeedID     int64
	FeedEditMode       bool
	UpdatePanel        bool
	TodayMode          bool
}

type pulseStatusResponseData struct {
	ReadingPreferences store.ReadingPreferences
	FeedPulseStatuses  map[int64]*pulseFeedStatusView
	Message            string
	MessageClass       string
	Feeds              []view.FeedView
	Continuation       view.FeedContinuationData
	SelectedFeedID     int64
	FeedEditMode       bool
	Running            bool
	Initial            bool
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
	ShowExactUnreadCounts    bool
	ShowCaughtUpSelectedFeed bool
	TodayMode                bool
	AllFeedsMode             bool
	StreamPath               string
}

type mobileStreamResponseData struct {
	ReadingPreferences store.ReadingPreferences
	Aggregate          *mobileAggregateResponseData
	Items              []view.ItemView
	TopBar             mobileTopBarData
	TodayMode          bool
	TodayBatchIDs      string
	TodaySettings      *todayViewData
	AllFeedsMode       bool
	TodayCards         []mobileTodayCardData
}

type mobileTodayCardData struct {
	BatchIDsText string
	Item         view.ItemView
}

type todayViewData struct {
	FeedPulseStatuses  map[int64]*pulseFeedStatusView
	SelectedFeeds      map[int64]bool
	CSRFToken          string
	BatchIDsText       string
	Items              []view.ItemView
	Feeds              []view.FeedView
	BatchIDs           []int64
	ReadingPreferences store.ReadingPreferences
}

type mobileReaderResponseData struct {
	ReadingPreferences store.ReadingPreferences
	BackPath           string
	MarkReadPath       string
	TopBar             mobileTopBarData
	Item               view.ItemView
}

type mobileAggregateResponseData struct {
	NextSectionsPath  string
	ResetSectionsPath string
	Sections          []mobileFeedSectionData
	HasNext           bool
	HasPrevious       bool
}

type mobileStreamSectionsResponseData struct {
	ReadingPreferences store.ReadingPreferences
	Aggregate          *mobileAggregateResponseData
	TopBar             mobileTopBarData
}

type mobileFeedSectionData struct {
	FeedTitle       string
	MarkReadQuery   string
	NewestItemsPath string
	OlderItemsPath  string
	ReaderQuery     string
	Items           []view.ItemView
	FeedID          int64
	HasOlder        bool
	IsOlderPage     bool
}

type mobileFeedSectionResponseData struct {
	ReadingPreferences store.ReadingPreferences
	Section            mobileFeedSectionData
	TopBar             mobileTopBarData
}

type readingPreferencesResponseData struct {
	ReadingPreferences store.ReadingPreferences
	FeedPulseStatuses  map[int64]*pulseFeedStatusView
	Feeds              []view.FeedView
	MobileTopBar       *mobileTopBarData
	SelectedFeedID     int64
	FeedEditMode       bool
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
