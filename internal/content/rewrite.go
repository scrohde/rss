package content

import (
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	ImageProxyPath          = "/image-proxy"
	MaxImageProxyURLLength  = 4096
	ImageProxyMaxBodyBytes  = 10 << 20
	ImageProxyTimeout       = 15 * time.Second
	ImageProxyCacheFallback = "public, max-age=86400"
	ImageProxyUserAgent     = "Mozilla/5.0 (compatible; PulseRSSImageProxy/1.0; +https://localhost)"
)

func RewriteSummaryHTML(text, baseURLRaw string) string {
	base := parseSummaryBaseURL(baseURLRaw)

	if !containsRewriteTargets(text) {
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

func containsRewriteTargets(text string) bool {
	return strings.Contains(text, "<img") || strings.Contains(text, "<source") || strings.Contains(text, "<a")
}

// parseSummaryBaseURL keeps rewriting deterministic by accepting only absolute
// http(s) URLs with a host.
func parseSummaryBaseURL(raw string) *url.URL {
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
