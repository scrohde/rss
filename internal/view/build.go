// Package view builds template-facing view models from store-layer values.
package view

import (
	"database/sql"
	"fmt"
	"html/template"
	"strings"
	"time"

	"rss/internal/content"
)

const (
	hoursPerDay = 24
	daysPerYear = 365
)

// BuildFeedView builds a FeedView from feed row values.
func BuildFeedView(
	id int64,
	sortOrder int,
	title string,
	originalTitle string,
	url string,
	itemCount int,
	unreadCount int,
	status FeedStatus,
) FeedView {
	refreshDisplay := "Never"
	if status.LastChecked.Valid {
		refreshDisplay = FormatRelativeShort(status.LastChecked.Time, time.Now())
	}

	errText := ""
	if status.LastError.Valid {
		errText = status.LastError.String
	}

	return FeedView{
		ID:                 id,
		SortOrder:          sortOrder,
		Title:              title,
		OriginalTitle:      originalTitle,
		URL:                url,
		ItemCount:          itemCount,
		UnreadCount:        unreadCount,
		LastRefreshDisplay: refreshDisplay,
		LastError:          errText,
	}
}

// BuildItemView builds an ItemView from item row values.
func BuildItemView(
	id int64,
	title string,
	link string,
	summary sql.NullString,
	contentText sql.NullString,
	published sql.NullTime,
	readAt sql.NullTime,
) ItemView {
	summaryHTML, hasSummary := renderOptionalHTML(summary, link)
	contentHTML, hasContent := renderOptionalHTML(contentText, link)
	publishedDisplay := "Unpublished"
	publishedCompact := "na"

	if published.Valid {
		publishedDisplay = FormatTime(published.Time)
		publishedCompact = FormatRelativeShort(published.Time, time.Now())
	}

	return ItemView{
		ID:               id,
		FeedID:           0,
		Title:            title,
		FeedTitle:        "",
		Link:             link,
		SummaryHTML:      summaryHTML,
		ContentHTML:      contentHTML,
		PublishedDisplay: publishedDisplay,
		PublishedCompact: publishedCompact,
		IsRead:           readAt.Valid,
		IsActive:         false,
		HasSummary:       hasSummary,
		HasContent:       hasContent,
		IsExpanded:       false,
	}
}

// FormatTime formats timestamps for expanded item display.
func FormatTime(t time.Time) string {
	return t.UTC().Format("Jan 2, 2006 - 3:04 PM")
}

// FormatRelativeShort formats age as a compact relative value.
func FormatRelativeShort(t, now time.Time) string {
	if t.IsZero() {
		return "na"
	}

	age := max(now.Sub(t), 0)

	switch {
	case age < time.Minute:
		return fmt.Sprintf("%ds", int(age.Seconds()))
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age.Minutes()))
	case age < hoursPerDay*time.Hour:
		return fmt.Sprintf("%dh", int(age.Hours()))
	case age < daysPerYear*hoursPerDay*time.Hour:
		return fmt.Sprintf("%dd", int(age.Hours()/hoursPerDay))
	default:
		return fmt.Sprintf("%dy", int(age.Hours()/(hoursPerDay*daysPerYear)))
	}
}

//nolint:gosec // HTML content is rewritten/sanitized before rendering in templates.
func renderOptionalHTML(raw sql.NullString, baseURL string) (template.HTML, bool) {
	if !raw.Valid {
		return "", false
	}

	text := strings.TrimSpace(raw.String)
	if text == "" {
		return "", false
	}

	text = content.RewriteSummaryHTML(text, baseURL)

	return template.HTML(text), true
}
