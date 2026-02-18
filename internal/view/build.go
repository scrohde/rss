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
	title string,
	originalTitle string,
	url string,
	itemCount int,
	unreadCount int,
	lastChecked sql.NullTime,
	lastError sql.NullString,
) FeedView {
	refreshDisplay := "Never"
	if lastChecked.Valid {
		refreshDisplay = FormatRelativeShort(lastChecked.Time, time.Now())
	}

	errText := ""
	if lastError.Valid {
		errText = lastError.String
	}

	return FeedView{
		ID:                 id,
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
	summaryHTML := pickSummaryHTML(summary, contentText, link)
	publishedDisplay := "Unpublished"
	publishedCompact := "na"

	if published.Valid {
		publishedDisplay = FormatTime(published.Time)
		publishedCompact = FormatRelativeShort(published.Time, time.Now())
	}

	return ItemView{
		ID:               id,
		Title:            title,
		Link:             link,
		SummaryHTML:      summaryHTML,
		PublishedDisplay: publishedDisplay,
		PublishedCompact: publishedCompact,
		IsRead:           readAt.Valid,
		IsActive:         false,
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

//nolint:gosec // Summary HTML is rewritten/sanitized before rendering in templates.
func pickSummaryHTML(summary, contentText sql.NullString, baseURL string) template.HTML {
	text := ""
	if contentText.Valid && strings.TrimSpace(contentText.String) != "" {
		text = contentText.String
	} else if summary.Valid && strings.TrimSpace(summary.String) != "" {
		text = summary.String
	}

	if text == "" {
		text = "<p>No summary available.</p>"
	}

	text = content.RewriteSummaryHTML(text, baseURL)

	return template.HTML(text)
}
