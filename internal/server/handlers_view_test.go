//nolint:testpackage // Handler integration tests intentionally exercise unexported helpers.
package server

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"rss/internal/view"
)

func TestBuildFeedViewLastRefreshDisplay(t *testing.T) {
	t.Parallel()

	var (
		emptyChecked        sql.NullTime
		emptyError, noError sql.NullString
	)

	feed := view.BuildFeedView(
		1,
		0,
		itemLimitFeedTitle,
		itemLimitFeedTitle,
		"https://example.com",
		0,
		0,
		view.FeedStatus{
			LastChecked: emptyChecked,
			LastError:   emptyError,
		},
	)
	if feed.LastRefreshDisplay != "Never" {
		t.Fatalf("expected Never, got %q", feed.LastRefreshDisplay)
	}

	cases := []struct {
		name     string
		wantUnit string
		age      time.Duration
	}{
		{name: "seconds", age: threeUnits * time.Second, wantUnit: "s"},
		{name: "minutes", age: threeUnits * time.Minute, wantUnit: "m"},
		{name: "hours", age: threeUnits * time.Hour, wantUnit: "h"},
		{name: "days", age: hoursInThreeDays * time.Hour, wantUnit: "d"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checked := sql.NullTime{Time: time.Now().Add(-tc.age), Valid: true}
			feedView := view.BuildFeedView(
				1,
				0,
				itemLimitFeedTitle,
				itemLimitFeedTitle,
				"https://example.com",
				0,
				0,
				view.FeedStatus{
					LastChecked: checked,
					LastError:   noError,
				},
			)

			got := feedView.LastRefreshDisplay
			if !strings.HasSuffix(got, tc.wantUnit) {
				t.Fatalf("expected unit %q in %q", tc.wantUnit, got)
			}
		})
	}
}
