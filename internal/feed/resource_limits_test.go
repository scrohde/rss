//nolint:testpackage // Feed tests exercise package-internal helpers directly.
package feed

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"testing"
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
