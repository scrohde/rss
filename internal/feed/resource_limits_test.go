//nolint:testpackage // Feed tests exercise package-internal helpers directly.
package feed

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"

	"rss/internal/store"
)

func TestParseFetchResponseRejectsOversizedDecodedBody(t *testing.T) {
	t.Parallel()

	var compressed bytes.Buffer

	writer := gzip.NewWriter(&compressed)

	_, err := writer.Write(bytes.Repeat([]byte("x"), int(maxFeedBodyBytes)+1))
	if err != nil {
		t.Fatalf("write compressed body: %v", err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatalf("close compressed body: %v", err)
	}

	reader, err := gzip.NewReader(bytes.NewReader(compressed.Bytes()))
	if err != nil {
		t.Fatalf("open compressed body: %v", err)
	}

	t.Cleanup(func() {
		closeErr := reader.Close()
		if closeErr != nil {
			t.Errorf("close compressed reader: %v", closeErr)
		}
	})

	//nolint:exhaustruct // parseFetchResponse only needs the status and body.
	_, err = parseFetchResponse(&http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(reader)})
	if !errors.Is(err, errFeedBodyTooLarge) {
		t.Fatalf("expected oversized feed error, got %v", err)
	}
}

func TestParseFetchResponseRetainsNewestItemsFromOversizedFeed(t *testing.T) {
	t.Parallel()

	const itemCount = 1036

	//nolint:exhaustruct // parseFetchResponse only needs the status and body.
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(buildRSSFeed(t, itemCount))),
	}

	result, err := parseFetchResponse(response)
	if err != nil {
		t.Fatalf("parseFetchResponse: %v", err)
	}

	if len(result.Feed.Items) != store.MaxFeedItems {
		t.Fatalf("expected %d retained items, got %d", store.MaxFeedItems, len(result.Feed.Items))
	}

	if result.Feed.Items[0].GUID != "item-1035" {
		t.Fatalf("expected newest item first, got %q", result.Feed.Items[0].GUID)
	}

	if result.Feed.Items[store.MaxFeedItems-1].GUID != "item-36" {
		t.Fatalf("expected oldest retained item-36, got %q", result.Feed.Items[store.MaxFeedItems-1].GUID)
	}
}

func TestLimitFeedItemsPreservesSourceOrderWhenDatesAreMissing(t *testing.T) {
	t.Parallel()

	items := make([]*gofeed.Item, store.MaxFeedItems+1)
	for index := range items {
		//nolint:exhaustruct // The ordering helper only needs a GUID and absent dates.
		items[index] = &gofeed.Item{GUID: fmt.Sprintf("item-%d", index)}
	}

	limited := limitFeedItems(items)
	if limited[0].GUID != "item-0" {
		t.Fatalf("expected source order to be preserved, got %q first", limited[0].GUID)
	}

	if limited[len(limited)-1].GUID != "item-999" {
		t.Fatalf("expected source order to retain item-999 last, got %q", limited[len(limited)-1].GUID)
	}
}

func buildRSSFeed(t *testing.T, itemCount int) string {
	t.Helper()

	base := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)

	var body strings.Builder

	_, err := body.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel><title>Large feed</title>`)
	if err != nil {
		t.Fatalf("write feed header: %v", err)
	}

	for index := range itemCount {
		published := base.Add(time.Duration(index) * time.Hour).Format(time.RFC1123Z)

		_, err = fmt.Fprintf(
			&body,
			"<item><title>Item %d</title><guid>item-%d</guid><pubDate>%s</pubDate></item>",
			index,
			index,
			published,
		)
		if err != nil {
			t.Fatalf("write feed item: %v", err)
		}
	}

	_, err = body.WriteString(`</channel></rss>`)
	if err != nil {
		t.Fatalf("write feed footer: %v", err)
	}

	return body.String()
}
