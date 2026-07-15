package view

// FeedContinuationData is template data for moving through unread feeds in saved order.
type FeedContinuationData struct {
	NextFeed      FeedView
	CurrentFeedID int64
	HasNext       bool
	SwapOOB       bool
}
