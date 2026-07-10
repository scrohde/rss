package view_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"rss/internal/view"
)

func buildItem(summary, content sql.NullString) view.ItemView {
	return view.BuildItemView(
		42,
		"Title",
		"http://example.com/item",
		summary,
		content,
		sql.NullTime{Time: time.Time{}, Valid: false},
		sql.NullTime{Time: time.Time{}, Valid: false},
	)
}

func TestBuildItemViewSummaryOnly(t *testing.T) {
	t.Parallel()

	item := buildItem(
		sql.NullString{String: "<p>Summary</p>", Valid: true},
		sql.NullString{String: "", Valid: false},
	)

	if !item.HasSummary {
		t.Fatal("expected HasSummary true")
	}

	if !item.HasReaderContent {
		t.Fatal("expected HasReaderContent true")
	}

	if item.HasContent {
		t.Fatal("expected HasContent false")
	}

	if item.CompactPreview != "Summary" {
		t.Fatalf("expected CompactPreview to use summary text, got %q", item.CompactPreview)
	}

	if !strings.Contains(string(item.SummaryHTML), "Summary") {
		t.Fatalf("expected SummaryHTML to include summary text, got %q", item.SummaryHTML)
	}

	if string(item.ContentHTML) != "" {
		t.Fatalf("expected empty ContentHTML, got %q", item.ContentHTML)
	}
}

func TestBuildItemViewContentOnly(t *testing.T) {
	t.Parallel()

	item := buildItem(
		sql.NullString{String: "", Valid: false},
		sql.NullString{String: "<p>Content</p>", Valid: true},
	)

	if item.HasSummary {
		t.Fatal("expected HasSummary false")
	}

	if !item.HasReaderContent {
		t.Fatal("expected HasReaderContent true")
	}

	if !item.HasContent {
		t.Fatal("expected HasContent true")
	}

	if item.CompactPreview != "" {
		t.Fatalf("expected empty CompactPreview, got %q", item.CompactPreview)
	}

	if string(item.SummaryHTML) != "" {
		t.Fatalf("expected empty SummaryHTML, got %q", item.SummaryHTML)
	}

	if !strings.Contains(string(item.ContentHTML), "Content") {
		t.Fatalf("expected ContentHTML to include content text, got %q", item.ContentHTML)
	}
}

func TestBuildItemViewSummaryAndContent(t *testing.T) {
	t.Parallel()

	item := buildItem(
		sql.NullString{String: "<p>Summary</p>", Valid: true},
		sql.NullString{String: "<p>Content</p>", Valid: true},
	)

	if !item.HasSummary {
		t.Fatal("expected HasSummary true")
	}

	if !item.HasReaderContent {
		t.Fatal("expected HasReaderContent true")
	}

	if !item.HasContent {
		t.Fatal("expected HasContent true")
	}

	if item.CompactPreview != "Summary" {
		t.Fatalf("expected CompactPreview to prefer summary text, got %q", item.CompactPreview)
	}

	if !strings.Contains(string(item.SummaryHTML), "Summary") {
		t.Fatalf("expected SummaryHTML to include summary text, got %q", item.SummaryHTML)
	}

	if !strings.Contains(string(item.ContentHTML), "Content") {
		t.Fatalf("expected ContentHTML to include content text, got %q", item.ContentHTML)
	}
}

func TestBuildItemViewNoSummaryOrContent(t *testing.T) {
	t.Parallel()

	item := buildItem(
		sql.NullString{String: "", Valid: false},
		sql.NullString{String: "", Valid: false},
	)

	if item.HasSummary {
		t.Fatal("expected HasSummary false")
	}

	if item.HasReaderContent {
		t.Fatal("expected HasReaderContent false")
	}

	if item.HasContent {
		t.Fatal("expected HasContent false")
	}

	if item.CompactPreview != "" {
		t.Fatalf("expected empty CompactPreview, got %q", item.CompactPreview)
	}

	if string(item.SummaryHTML) != "" {
		t.Fatalf("expected empty SummaryHTML, got %q", item.SummaryHTML)
	}

	if string(item.ContentHTML) != "" {
		t.Fatalf("expected empty ContentHTML, got %q", item.ContentHTML)
	}
}

func TestBuildItemViewTreatsActiveOnlyMarkupAsNoReaderContent(t *testing.T) {
	t.Parallel()

	item := buildItem(
		sql.NullString{String: `<script>attack()</script><iframe src="/attack"></iframe>`, Valid: true},
		sql.NullString{String: `<style>body{display:none}</style><form action="/attack"></form>`, Valid: true},
	)

	if item.HasSummary || item.HasContent || item.HasReaderContent {
		t.Fatalf("expected active-only markup to produce no reader content, got %+v", item)
	}

	if item.SummaryHTML != "" || item.ContentHTML != "" {
		t.Fatalf("expected active-only markup to produce no template HTML, got summary=%q content=%q",
			item.SummaryHTML, item.ContentHTML)
	}
}

func TestBuildItemViewCompactPreviewSanitizesAndTruncatesSummaryHTML(t *testing.T) {
	t.Parallel()

	item := buildItem(
		sql.NullString{
			String: `<div>
				<h2>Big heading</h2>
				<p>Alpha <strong>beta</strong> <a href="/story">gamma</a>.</p>
				<img src="/hero.jpg" alt="hero image">
				<p>` + strings.Repeat("word ", 80) + `tail</p>
			</div>`,
			Valid: true,
		},
		sql.NullString{String: "", Valid: false},
	)

	if strings.Contains(item.CompactPreview, "<") || strings.Contains(item.CompactPreview, ">") {
		t.Fatalf("expected CompactPreview without raw HTML tags, got %q", item.CompactPreview)
	}

	if strings.Contains(item.CompactPreview, "hero image") {
		t.Fatalf("expected CompactPreview to omit media markup text, got %q", item.CompactPreview)
	}

	if !strings.Contains(item.CompactPreview, "Big heading Alpha beta gamma.") {
		t.Fatalf("expected CompactPreview to keep normalized text content, got %q", item.CompactPreview)
	}

	if len(item.CompactPreview) > 240 {
		t.Fatalf("expected CompactPreview to stay bounded, got length %d", len(item.CompactPreview))
	}

	if !strings.HasSuffix(item.CompactPreview, "...") {
		t.Fatalf("expected truncated CompactPreview to end with ellipsis, got %q", item.CompactPreview)
	}
}
