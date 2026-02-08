package content

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	ImageProxyPath          = "/image-proxy"
	MaxImageProxyURLLength  = 4096
	ImageProxyTimeout       = 15 * time.Second
	ImageProxyCacheFallback = "public, max-age=86400"
)

func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: ImageProxyTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if !IsAllowedProxyURL(req.URL) {
				return errors.New("redirect blocked")
			}
			return nil
		},
	}
}

func RewriteSummaryHTML(text, baseRaw string) string {
	base := parseRewriteBaseURL(baseRaw)

	if !strings.Contains(text, "<img") && !strings.Contains(text, "<source") && !strings.Contains(text, "<a") {
		return text
	}
	root := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	nodes, err := html.ParseFragment(strings.NewReader(text), root)
	if err != nil {
		return text
	}
	changed := false
	for _, node := range nodes {
		if rewriteSummaryNode(node, base) {
			changed = true
		}
	}
	if !changed {
		return text
	}
	var b strings.Builder
	for _, node := range nodes {
		_ = html.Render(&b, node)
	}
	return b.String()
}

func rewriteSummaryNode(node *html.Node, base *url.URL) bool {
	changed := false
	if node.Type == html.ElementNode {
		switch node.Data {
		case "img":
			if rewriteAttr(node, "src", func(value string) (string, bool) {
				return ProxyImageURL(value, base)
			}) {
				changed = true
			}
			if rewriteAttr(node, "srcset", func(value string) (string, bool) {
				return rewriteSrcset(value, base)
			}) {
				changed = true
			}
		case "source":
			if rewriteAttr(node, "srcset", func(value string) (string, bool) {
				return rewriteSrcset(value, base)
			}) {
				changed = true
			}
		case "a":
			if upsertAttr(node, "target", "_blank") {
				changed = true
			}
			if ensureRelTokens(node, "noopener", "noreferrer") {
				changed = true
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if rewriteSummaryNode(child, base) {
			changed = true
		}
	}
	return changed
}

func rewriteAttr(node *html.Node, key string, rewrite func(string) (string, bool)) bool {
	for i, attr := range node.Attr {
		if attr.Key != key {
			continue
		}
		if updated, ok := rewrite(attr.Val); ok {
			node.Attr[i].Val = updated
			return true
		}
		return false
	}
	return false
}

func upsertAttr(node *html.Node, key, value string) bool {
	for i, attr := range node.Attr {
		if attr.Key != key {
			continue
		}
		if attr.Val == value {
			return false
		}
		node.Attr[i].Val = value
		return true
	}
	node.Attr = append(node.Attr, html.Attribute{Key: key, Val: value})
	return true
}

func ensureRelTokens(node *html.Node, required ...string) bool {
	index := -1
	tokens := []string{}
	existing := map[string]bool{}
	for i, attr := range node.Attr {
		if attr.Key != "rel" {
			continue
		}
		index = i
		for _, token := range strings.Fields(attr.Val) {
			tokens = append(tokens, token)
			existing[strings.ToLower(token)] = true
		}
		break
	}

	changed := false
	for _, token := range required {
		normalized := strings.ToLower(token)
		if existing[normalized] {
			continue
		}
		tokens = append(tokens, token)
		existing[normalized] = true
		changed = true
	}

	if index >= 0 {
		if !changed {
			return false
		}
		node.Attr[index].Val = strings.Join(tokens, " ")
		return true
	}

	node.Attr = append(node.Attr, html.Attribute{Key: "rel", Val: strings.Join(required, " ")})
	return true
}

func rewriteSrcset(value string, base *url.URL) (string, bool) {
	parts := strings.Split(value, ",")
	changed := false
	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		if updated, ok := ProxyImageURL(fields[0], base); ok {
			fields[0] = updated
			changed = true
		}
		parts[i] = strings.Join(fields, " ")
	}
	if !changed {
		return value, false
	}
	return strings.Join(parts, ", "), true
}

func ProxyImageURL(raw string, base *url.URL) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return raw, false
	}
	if strings.HasPrefix(trimmed, ImageProxyPath+"?") {
		return raw, false
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "data:") {
		return raw, false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return raw, false
	}
	if parsed.Host == "" {
		if base == nil {
			return raw, false
		}
		parsed = base.ResolveReference(parsed)
	} else if parsed.Scheme == "" && base != nil {
		parsed.Scheme = base.Scheme
	}
	if parsed.Host == "" {
		return raw, false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return raw, false
	}
	if !IsAllowedProxyURL(parsed) {
		return raw, false
	}
	return ImageProxyPath + "?url=" + url.QueryEscape(parsed.String()), true
}

func parseRewriteBaseURL(raw string) *url.URL {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil
	}
	return parsed
}

func IsAllowedProxyURL(target *url.URL) bool {
	if target == nil {
		return false
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return false
	}
	if target.Hostname() == "" {
		return false
	}
	return !isDisallowedHost(target.Hostname())
}

func isDisallowedHost(host string) bool {
	hostname := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if hostname == "" || hostname == "localhost" {
		return true
	}
	if ip := net.ParseIP(hostname); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return true
		}
	}
	return false
}

func BuildImageProxyRequest(target *url.URL) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	req.Header.Set("User-Agent", "PulseRSS/1.0")
	req.Header.Set("Accept", "image/*,*/*;q=0.8")
	req.Header.Set("Referer", fmt.Sprintf("%s://%s/", target.Scheme, target.Host))
	return req, nil
}
