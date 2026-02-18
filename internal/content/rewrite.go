package content

import (
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	// ImageProxyPath is the route that fetches remote images through the server-side proxy.
	ImageProxyPath = "/image-proxy"
	// MaxImageProxyURLLength bounds the encoded `url` query value length.
	MaxImageProxyURLLength = 4096
	// ImageProxyMaxBodyBytes caps proxied image downloads.
	ImageProxyMaxBodyBytes = 10 << 20
	// ImageProxyTimeout is the timeout used by image proxy upstream requests.
	ImageProxyTimeout = 15 * time.Second
	// ImageProxyCacheFallback is used when upstream omits cache directives.
	ImageProxyCacheFallback = "public, max-age=86400"
	// ImageProxyUserAgent identifies proxy requests to upstream servers.
	ImageProxyUserAgent = "Mozilla/5.0 (compatible; PulseRSSImageProxy/1.0; https://localhost)"
)

const (
	attrIndexNotFound = -1
	relAttrKey        = "rel"
)

type relTokens struct {
	existing map[string]bool
	tokens   []string
}

type relAttrLookup struct {
	existing map[string]bool
	tokens   []string
	index    int
}

// RewriteSummaryHTML rewrites summary HTML image and anchor URLs when possible.
func RewriteSummaryHTML(text, baseURLRaw string) string {
	base := parseSummaryBaseURL(baseURLRaw)

	if !containsRewriteTargets(text) {
		return text
	}

	nodes, ok := parseSummaryFragment(text)
	if !ok {
		return text
	}

	if !rewriteSummaryNodes(nodes, base) {
		return text
	}

	rewritten, ok := renderSummaryNodes(nodes)
	if !ok {
		return text
	}

	return rewritten
}

func parseSummaryFragment(text string) ([]*html.Node, bool) {
	root := new(html.Node)
	root.Type = html.ElementNode
	root.DataAtom = atom.Div
	root.Data = "div"

	nodes, err := html.ParseFragment(strings.NewReader(text), root)
	if err != nil {
		return nil, false
	}

	return nodes, true
}

func rewriteSummaryNodes(nodes []*html.Node, base *url.URL) bool {
	changed := false

	for _, node := range nodes {
		if rewriteSummaryNode(node, base) {
			changed = true
		}
	}

	return changed
}

func renderSummaryNodes(nodes []*html.Node) (string, bool) {
	var b strings.Builder
	for _, node := range nodes {
		renderErr := html.Render(&b, node)
		if renderErr != nil {
			return "", false
		}
	}

	return b.String(), true
}

func rewriteSummaryNode(node *html.Node, base *url.URL) bool {
	changed := false
	if node.Type == html.ElementNode {
		changed = rewriteSummaryElement(node, base)
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if rewriteSummaryNode(child, base) {
			changed = true
		}
	}

	return changed
}

func rewriteSummaryElement(node *html.Node, base *url.URL) bool {
	switch node.Data {
	case "img":
		return rewriteSummaryImageNode(node, base)
	case "source":
		return rewriteAttr(node, "srcset", func(value string) (string, bool) {
			return rewriteSrcset(value, base)
		})
	case "a":
		return rewriteSummaryAnchorNode(node, base)
	default:
		return false
	}
}

func rewriteSummaryImageNode(node *html.Node, base *url.URL) bool {
	changed := rewriteAttr(node, "src", func(value string) (string, bool) {
		return ProxyImageURL(value, base)
	})

	if rewriteAttr(node, "srcset", func(value string) (string, bool) {
		return rewriteSrcset(value, base)
	}) {
		changed = true
	}

	return changed
}

func rewriteSummaryAnchorNode(node *html.Node, base *url.URL) bool {
	changed := rewriteAttr(node, "href", func(value string) (string, bool) {
		return rewriteAnchorURL(value, base)
	})

	if upsertAttr(node, "target", "_blank") {
		changed = true
	}

	if ensureRelTokens(node, "noopener", "noreferrer") {
		changed = true
	}

	return changed
}

func rewriteAttr(
	node *html.Node,
	key string,
	rewrite func(string) (string, bool),
) bool {
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

	node.Attr = append(node.Attr, html.Attribute{
		Namespace: "",
		Key:       key,
		Val:       value,
	})

	return true
}

func ensureRelTokens(node *html.Node, required ...string) bool {
	lookup := findRelAttr(node)

	merged, changed := mergeRelTokens(
		lookup.tokens,
		lookup.existing,
		required,
	)
	if lookup.index != attrIndexNotFound {
		if !changed {
			return false
		}

		node.Attr[lookup.index].Val = strings.Join(merged, " ")

		return true
	}

	node.Attr = append(node.Attr, html.Attribute{
		Namespace: "",
		Key:       relAttrKey,
		Val:       strings.Join(required, " "),
	})

	return true
}

func findRelAttr(node *html.Node) relAttrLookup {
	for i, attr := range node.Attr {
		if attr.Key != relAttrKey {
			continue
		}

		tokenData := collectRelTokens(attr.Val)

		return relAttrLookup{
			existing: tokenData.existing,
			tokens:   tokenData.tokens,
			index:    i,
		}
	}

	return relAttrLookup{
		existing: map[string]bool{},
		tokens:   nil,
		index:    attrIndexNotFound,
	}
}

func collectRelTokens(raw string) relTokens {
	fields := strings.Fields(raw)
	tokens := append([]string(nil), fields...)

	existing := make(map[string]bool, len(fields))

	for _, token := range fields {
		existing[strings.ToLower(token)] = true
	}

	return relTokens{
		tokens:   tokens,
		existing: existing,
	}
}

func mergeRelTokens(tokens []string, existing map[string]bool, required []string) ([]string, bool) {
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

	return tokens, changed
}

func containsRewriteTargets(text string) bool {
	return strings.Contains(text, "<img") ||
		strings.Contains(text, "<source") ||
		strings.Contains(text, "<a")
}

func rewriteAnchorURL(rawURL string, base *url.URL) (string, bool) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return rawURL, false
	}

	parsed, ok := parseAnchorURL(trimmed)
	if !ok {
		return rawURL, false
	}

	resolved, ok := resolveAnchorURL(parsed, base)
	if !ok {
		return rawURL, false
	}

	rewritten := resolved.String()
	if rewritten == rawURL {
		return rawURL, false
	}

	return rewritten, true
}

func parseAnchorURL(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}

	return parsed, true
}

func resolveAnchorURL(parsed, base *url.URL) (*url.URL, bool) {
	resolved := parsed
	if resolved.Host == "" {
		if base == nil {
			return nil, false
		}

		resolved = base.ResolveReference(resolved)
	} else if resolved.Scheme == "" && base != nil {
		resolved.Scheme = base.Scheme
	}

	if resolved.Host == "" {
		return nil, false
	}

	if !isHTTPScheme(resolved.Scheme) {
		return nil, false
	}

	return resolved, true
}

func isHTTPScheme(scheme string) bool {
	return scheme == "http" || scheme == "https"
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

	if !isHTTPScheme(parsed.Scheme) {
		return nil
	}

	return parsed
}
