//nolint:testpackage // URL normalization errors are intentionally package-internal.
package feed

import "testing"

func TestNormalizeURLAppliesOutboundAuthorityPolicy(t *testing.T) {
	t.Parallel()

	blocked := []string{
		"ftp://example.com/feed.xml",
		"https://user:pass@example.com/feed.xml",
		"http://127.0.0.1/feed.xml",
		"http://10.0.0.1/feed.xml",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/feed.xml",
		"http://[::ffff:127.0.0.1]/feed.xml",
		"http://example.com:8080/feed.xml",
		"https://example.com:80/feed.xml",
	}

	for _, raw := range blocked {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			normalized, err := NormalizeURL(raw)
			if err == nil {
				t.Fatalf("NormalizeURL(%q) = %q, want error", raw, normalized)
			}
		})
	}

	normalized, err := NormalizeURL("example.com/feed.xml")
	if err != nil {
		t.Fatalf("normalize public shorthand URL: %v", err)
	}

	if normalized != "https://example.com/feed.xml" {
		t.Fatalf("normalized URL = %q", normalized)
	}
}
