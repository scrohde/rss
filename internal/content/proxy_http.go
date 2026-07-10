// Package content provides HTML/content rewrite and image-proxy helpers.
package content

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"rss/internal/outbound"
)

const maxProxyRedirects = 5

// NewHTTPClient returns the HTTP client used for image proxy fetches.
func NewHTTPClient() *http.Client {
	return outbound.NewClient(outbound.ClientOptions{
		Resolver:         nil,
		BaseTransport:    nil,
		DialContext:      nil,
		Timeout:          ImageProxyTimeout,
		MaxResponseBytes: ImageProxyMaxBodyBytes,
		MaxRedirects:     maxProxyRedirects,
	})
}

// BuildImageProxyRequest builds an image-proxy request for a target URL.
func BuildImageProxyRequest(
	ctx context.Context,
	target *url.URL,
) (*http.Request, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		target.String(),
		http.NoBody,
	)
	if err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	req.Header.Set("User-Agent", ImageProxyUserAgent)
	req.Header.Set(
		"Accept",
		"image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8",
	)

	return req, nil
}
