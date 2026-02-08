package view

import (
	"database/sql"
	"fmt"
	"html/template"
	"strings"
	"time"

	"rss/internal/content"
)

func BuildFeedView(id int64, title, url string, itemCount, unreadCount int, lastChecked sql.NullTime, lastError sql.NullString) FeedView {
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
		URL:                url,
		ItemCount:          itemCount,
		UnreadCount:        unreadCount,
		LastRefreshDisplay: refreshDisplay,
		LastError:          errText,
	}
}

func BuildItemView(id int64, title, link string, summary, contentText sql.NullString, published, readAt sql.NullTime) ItemView {
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
	}
}

func FormatTime(t time.Time) string {
	return t.Local().Format("Jan 2, 2006 - 3:04 PM")
}

func FormatRelativeShort(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "na"
	}
	age := now.Sub(t)
	if age < 0 {
		age = 0
	}
	switch {
	case age < time.Minute:
		return fmt.Sprintf("%ds", int(age.Seconds()))
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh", int(age.Hours()))
	case age < 365*24*time.Hour:
		return fmt.Sprintf("%dd", int(age.Hours()/24))
	default:
		return fmt.Sprintf("%dy", int(age.Hours()/(24*365)))
	}
}

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
