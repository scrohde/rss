//nolint:testpackage // OPML tests exercise package-internal helpers directly.
package opml

import (
	"bytes"
	"strings"
	"testing"
)

const (
	alphaFeedURL           = "https://example.com/alpha.xml"
	betaFeedURL            = "https://example.com/beta.xml"
	gammaFeedURL           = "https://example.com/gamma.xml"
	expectedNestedFeeds    = 3
	expectedRoundtripFeeds = 2
)

func TestParseCollectsNestedSubscriptions(t *testing.T) {
	t.Parallel()

	input := `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head>
    <title>Subscriptions</title>
  </head>
  <body>
    <outline text="Tech">
      <outline
        text="Alpha Feed"
        type="rss"
        xmlUrl="https://example.com/alpha.xml"
      />
      <outline title="Beta Feed" xmlurl="https://example.com/beta.xml" />
    </outline>
    <outline
      text="Gamma Feed"
      url="https://example.com/gamma.xml"
    />
  </body>
</opml>`

	got, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	expected := []Subscription{
		{Title: "Alpha Feed", URL: alphaFeedURL},
		{Title: "Beta Feed", URL: betaFeedURL},
		{Title: "Gamma Feed", URL: gammaFeedURL},
	}

	if len(got) != expectedNestedFeeds {
		t.Fatalf(
			"expected %d subscriptions, got %d",
			expectedNestedFeeds,
			len(got),
		)
	}

	for index := range expected {
		assertSubscription(t, got[index], expected[index], index)
	}
}

func TestWriteRoundTrip(t *testing.T) {
	t.Parallel()

	input := []Subscription{
		{Title: "Alpha", URL: "https://example.com/alpha.xml"},
		{Title: "", URL: betaFeedURL},
		{Title: "Ignored", URL: ""},
	}

	var buf bytes.Buffer

	err := Write(&buf, "My Subscriptions", input)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Parse(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("Parse roundtrip: %v", err)
	}

	expected := []Subscription{
		{Title: "Alpha", URL: alphaFeedURL},
		{Title: betaFeedURL, URL: betaFeedURL},
	}

	if len(got) != expectedRoundtripFeeds {
		t.Fatalf(
			"expected %d subscriptions after roundtrip, got %d",
			expectedRoundtripFeeds,
			len(got),
		)
	}

	for index := range expected {
		assertSubscription(t, got[index], expected[index], index)
	}
}

func assertSubscription(t *testing.T, got, want Subscription, index int) {
	t.Helper()

	if got != want {
		t.Fatalf(
			"subscription %d mismatch: got %+v want %+v",
			index,
			got,
			want,
		)
	}
}
