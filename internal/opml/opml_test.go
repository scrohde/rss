package opml

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseCollectsNestedSubscriptions(t *testing.T) {
	input := `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head>
    <title>Subscriptions</title>
  </head>
  <body>
    <outline text="Tech">
      <outline text="Alpha Feed" type="rss" xmlUrl="https://example.com/alpha.xml" />
      <outline title="Beta Feed" xmlurl="https://example.com/beta.xml" />
    </outline>
    <outline text="Gamma Feed" url="https://example.com/gamma.xml" />
  </body>
</opml>`

	got, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 subscriptions, got %d", len(got))
	}

	if got[0].Title != "Alpha Feed" || got[0].URL != "https://example.com/alpha.xml" {
		t.Fatalf("unexpected first subscription: %+v", got[0])
	}
	if got[1].Title != "Beta Feed" || got[1].URL != "https://example.com/beta.xml" {
		t.Fatalf("unexpected second subscription: %+v", got[1])
	}
	if got[2].Title != "Gamma Feed" || got[2].URL != "https://example.com/gamma.xml" {
		t.Fatalf("unexpected third subscription: %+v", got[2])
	}
}

func TestWriteRoundTrip(t *testing.T) {
	input := []Subscription{
		{Title: "Alpha", URL: "https://example.com/alpha.xml"},
		{Title: "", URL: "https://example.com/beta.xml"},
		{Title: "Ignored", URL: ""},
	}

	var buf bytes.Buffer
	if err := Write(&buf, "My Subscriptions", input); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Parse(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("Parse roundtrip: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 subscriptions after roundtrip, got %d", len(got))
	}

	if got[0].Title != "Alpha" || got[0].URL != "https://example.com/alpha.xml" {
		t.Fatalf("unexpected first roundtrip subscription: %+v", got[0])
	}
	if got[1].Title != "https://example.com/beta.xml" || got[1].URL != "https://example.com/beta.xml" {
		t.Fatalf("unexpected second roundtrip subscription: %+v", got[1])
	}
}
