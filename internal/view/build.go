// Package view builds template-facing view models from store-layer values.
package view

import (
	"database/sql"
	"fmt"
	"html/template"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	stdhtml "html"

	"rss/internal/content"
)

const (
	hoursPerDay          = 24
	daysPerYear          = 365
	compactPreviewMaxLen = 240
	compactPreviewSuffix = "..."
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

// BuildFeedContinuation finds the next feed with unread items after the current feed.
// The input order is the saved feed order returned by the store.
func BuildFeedContinuation(currentFeedID int64, feeds []FeedView) FeedContinuationData {
	var continuation FeedContinuationData

	continuation.CurrentFeedID = currentFeedID
	foundCurrent := false

	for _, feedView := range feeds {
		if !foundCurrent {
			foundCurrent = feedView.ID == currentFeedID

			continue
		}

		if feedView.UnreadCount <= 0 {
			continue
		}

		continuation.NextFeed = feedView
		continuation.HasNext = true

		return continuation
	}

	return continuation
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
	compactPreview := buildCompactPreview(summary)
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
		CompactPreview:   compactPreview,
		SummaryHTML:      summaryHTML,
		ContentHTML:      contentHTML,
		PublishedDisplay: publishedDisplay,
		PublishedCompact: publishedCompact,
		IsRead:           readAt.Valid,
		IsActive:         false,
		HasReaderContent: hasSummary || hasContent,
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

func renderOptionalHTML(raw sql.NullString, baseURL string) (template.HTML, bool) {
	if !raw.Valid {
		return "", false
	}

	text := strings.TrimSpace(raw.String)
	if text == "" {
		return "", false
	}

	safeHTML := content.RewriteSummaryHTML(text, baseURL)
	if strings.TrimSpace(string(safeHTML)) == "" {
		return "", false
	}

	return safeHTML, true
}

func buildCompactPreview(summary sql.NullString) string {
	if !summary.Valid {
		return ""
	}

	text := strings.TrimSpace(summary.String)
	if text == "" {
		return ""
	}

	nodes, ok := parseCompactPreviewFragment(text)
	if !ok {
		return ""
	}

	var b strings.Builder
	appendCompactPreviewText(&b, nodes)

	preview := normalizeCompactPreviewWhitespace(b.String())
	if preview == "" {
		return ""
	}

	return truncateCompactPreview(preview, compactPreviewMaxLen)
}

func parseCompactPreviewFragment(text string) ([]*html.Node, bool) {
	root := new(html.Node)
	root.Type = html.ElementNode
	root.DataAtom = atom.Div
	root.Data = "div"

	nodes, err := html.ParseFragment(strings.NewReader(text), root)
	if err != nil {
		return nil, false
	}

	return nodes, true
}

func appendCompactPreviewText(b *strings.Builder, nodes []*html.Node) {
	for _, node := range nodes {
		appendCompactPreviewNodeText(b, node)
	}
}

func appendCompactPreviewNodeText(b *strings.Builder, node *html.Node) {
	if node == nil {
		return
	}

	if shouldSkipCompactPreviewNode(node) {
		return
	}

	if node.Type == html.TextNode {
		mustWriteString(b, stdhtml.UnescapeString(node.Data))
		mustWriteByte(b, ' ')

		return
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		appendCompactPreviewNodeText(b, child)
	}
}

func shouldSkipCompactPreviewNode(node *html.Node) bool {
	if node.Type != html.ElementNode {
		return false
	}

	switch node.Data {
	case "script", "style", "object", "embed", "svg":
		return true
	default:
		return false
	}
}

func normalizeCompactPreviewWhitespace(text string) string {
	normalized := strings.Join(strings.Fields(text), " ")
	if normalized == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range normalized {
		if isCompactPreviewPunctuation(r) && b.Len() > 0 {
			trimTrailingSpace(&b)
		}

		mustWriteRune(&b, r)
	}

	return b.String()
}

func truncateCompactPreview(text string, limit int) string {
	if limit <= len(compactPreviewSuffix) || utf8.RuneCountInString(text) <= limit {
		return text
	}

	runes := []rune(text)

	cutoff := limit - len(compactPreviewSuffix)
	if cutoff <= 0 || cutoff >= len(runes) {
		return text
	}

	truncated := strings.TrimSpace(string(runes[:cutoff]))
	if lastSpace := strings.LastIndex(truncated, " "); lastSpace >= cutoff/2 {
		truncated = truncated[:lastSpace]
	}

	truncated = strings.TrimSpace(truncated)
	if truncated == "" {
		return strings.TrimSpace(string(runes[:cutoff])) + compactPreviewSuffix
	}

	return truncated + compactPreviewSuffix
}

func isCompactPreviewPunctuation(r rune) bool {
	switch r {
	case '.', ',', ';', ':', '!', '?':
		return true
	default:
		return false
	}
}

func trimTrailingSpace(b *strings.Builder) {
	text := b.String()
	if text == "" || text[len(text)-1] != ' ' {
		return
	}

	b.Reset()
	mustWriteString(b, text[:len(text)-1])
}

func mustWriteString(b *strings.Builder, text string) {
	_, err := b.WriteString(text)
	if err != nil {
		panic(err)
	}
}

func mustWriteByte(b *strings.Builder, value byte) {
	err := b.WriteByte(value)
	if err != nil {
		panic(err)
	}
}

func mustWriteRune(b *strings.Builder, value rune) {
	_, err := b.WriteRune(value)
	if err != nil {
		panic(err)
	}
}
