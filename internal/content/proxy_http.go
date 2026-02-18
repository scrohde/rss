// Package content provides HTML/content rewrite and image-proxy helpers.
package content

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

const maxProxyRedirects = 10

var (
	errMaxProxyRedirects = errors.New("stopped after 10 redirects")
	errProxyRedirect     = errors.New("redirect blocked")
)

// NewHTTPClient returns the HTTP client used for image proxy fetches.
func NewHTTPClient() *http.Client {
	client := new(http.Client)
	client.Timeout = ImageProxyTimeout
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxProxyRedirects {
			return errMaxProxyRedirects
		}

		if !IsAllowedProxyURL(req.URL) {
			return errProxyRedirect
		}

		return nil
	}

	return client
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
