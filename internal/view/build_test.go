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

	if item.HasContent {
		t.Fatal("expected HasContent false")
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

	if !item.HasContent {
		t.Fatal("expected HasContent true")
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

	if !item.HasContent {
		t.Fatal("expected HasContent true")
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

	if item.HasContent {
		t.Fatal("expected HasContent false")
	}

	if string(item.SummaryHTML) != "" {
		t.Fatalf("expected empty SummaryHTML, got %q", item.SummaryHTML)
	}

	if string(item.ContentHTML) != "" {
		t.Fatalf("expected empty ContentHTML, got %q", item.ContentHTML)
	}
}
