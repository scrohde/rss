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

//nolint:govet // Keep embedded page defaults first, as required by embeddedstructfieldcheck.
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
	PulsePath                string
	SelectedFeedTitle        string
	CatchUpFeedTitle         string
	CatchUpSurface           string
	PulseLabel               string
	PulsePendingLabel        string
	MarkAllReadUndoToken     string
	StreamPath               string
	FeedOptions              []view.FeedView
	CatchUpFeedID            int64
	SelectedFeedID           int64
	ShowCaughtUpSelectedFeed bool
	TodayMode                bool
	AllFeedsMode             bool
	ShowExactUnreadCounts    bool
}

type mobileStreamResponseData struct {
	Aggregate          *mobileAggregateResponseData
	TodaySettings      *todayViewData
	TodayBatchIDs      string
	Items              []view.ItemView
	TodayCards         []mobileTodayCardData
	ReadingPreferences store.ReadingPreferences
	TopBar             mobileTopBarData
	TodayMode          bool
	AllFeedsMode       bool
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
	BackPath           string
	MarkReadPath       string
	ReadingPreferences store.ReadingPreferences
	Item               view.ItemView
	TopBar             mobileTopBarData
}

type mobileAggregateResponseData struct {
	NextSectionsPath  string
	ResetSectionsPath string
	Sections          []mobileFeedSectionData
	HasNext           bool
	HasPrevious       bool
}

type mobileStreamSectionsResponseData struct {
	Aggregate          *mobileAggregateResponseData
	ReadingPreferences store.ReadingPreferences
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
	FeedPulseStatuses  map[int64]*pulseFeedStatusView
	MobileTopBar       *mobileTopBarData
	Feeds              []view.FeedView
	ReadingPreferences store.ReadingPreferences
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

//nolint:govet // Keep embedded page defaults first, as required by embeddedstructfieldcheck.
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
