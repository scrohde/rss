package content

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

const maxProxyRedirects = 10

func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: ImageProxyTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxProxyRedirects {
				return errors.New("stopped after 10 redirects")
			}
			if !IsAllowedProxyURL(req.URL) {
				return errors.New("redirect blocked")
			}
			return nil
		},
	}
}

func BuildImageProxyRequest(target *url.URL) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	req.Header.Set("User-Agent", ImageProxyUserAgent)
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
	return req, nil
}
