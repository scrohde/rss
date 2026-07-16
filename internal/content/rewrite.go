package content

import (
	"html/template"
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

	youtubeVideoIDMaxLength = 64
	youtubeVideoIDChars     = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	youtubeEmbedSandbox     = "allow-scripts allow-same-origin allow-presentation allow-popups"
)

// summaryAllowedElements is intentionally small: stored feed markup may format reader content, but it may not create
// controls, executable content, or independent network clients. Iframes pass a separate YouTube-only URL check and
// receive enforced sandbox attributes below.
//
//nolint:gochecknoglobals // A package-level literal keeps the security allowlist centralized and auditable.
var summaryAllowedElements = map[string]bool{
	"a":          true,
	"abbr":       true,
	"address":    true,
	"article":    true,
	"aside":      true,
	"b":          true,
	"bdi":        true,
	"bdo":        true,
	"blockquote": true,
	"br":         true,
	"caption":    true,
	"cite":       true,
	"code":       true,
	"col":        true,
	"colgroup":   true,
	"dd":         true,
	"del":        true,
	"div":        true,
	"dl":         true,
	"dt":         true,
	"em":         true,
	"figcaption": true,
	"figure":     true,
	"footer":     true,
	"h1":         true,
	"h2":         true,
	"h3":         true,
	"h4":         true,
	"h5":         true,
	"h6":         true,
	"header":     true,
	"hr":         true,
	"i":          true,
	"img":        true,
	"iframe":     true,
	"ins":        true,
	"kbd":        true,
	"li":         true,
	"main":       true,
	"mark":       true,
	"nav":        true,
	"ol":         true,
	"p":          true,
	"picture":    true,
	"pre":        true,
	"q":          true,
	"rp":         true,
	"rt":         true,
	"ruby":       true,
	"s":          true,
	"samp":       true,
	"section":    true,
	"small":      true,
	"source":     true,
	"span":       true,
	"strong":     true,
	"sub":        true,
	"sup":        true,
	"table":      true,
	"tbody":      true,
	"td":         true,
	"tfoot":      true,
	"th":         true,
	"thead":      true,
	"time":       true,
	"tr":         true,
	"u":          true,
	"ul":         true,
	"var":        true,
	"wbr":        true,
}

// summaryDroppedSubtrees contain content whose descendants are also unsafe or exist only as active UI fallback.
//
//nolint:gochecknoglobals // A package-level literal keeps dangerous subtree handling centralized and auditable.
var summaryDroppedSubtrees = map[string]bool{
	"audio":    true,
	"button":   true,
	"canvas":   true,
	"datalist": true,
	"embed":    true,
	"fieldset": true,
	"input":    true,
	"legend":   true,
	"math":     true,
	"meter":    true,
	"noscript": true,
	"object":   true,
	"optgroup": true,
	"option":   true,
	"output":   true,
	"progress": true,
	"script":   true,
	"select":   true,
	"style":    true,
	"svg":      true,
	"template": true,
	"textarea": true,
	"video":    true,
}

//nolint:gochecknoglobals // A package-level literal makes every feed-derived attribute explicit for review.
var summaryAllowedAttrs = map[string]map[string]bool{
	"a": {
		"href": true,
	},
	"col": {
		"span": true,
	},
	"colgroup": {
		"span": true,
	},
	"img": {
		"alt":    true,
		"height": true,
		"src":    true,
		"srcset": true,
		"width":  true,
	},
	"iframe": {
		"height": true,
		"src":    true,
		"width":  true,
	},
	"li": {
		"value": true,
	},
	"ol": {
		"reversed": true,
		"start":    true,
		"type":     true,
	},
	"source": {
		"media":  true,
		"sizes":  true,
		"srcset": true,
		"type":   true,
	},
	"td": {
		"colspan": true,
		"rowspan": true,
	},
	"th": {
		"abbr":    true,
		"colspan": true,
		"rowspan": true,
		"scope":   true,
	},
	"time": {
		"datetime": true,
	},
}

//nolint:gochecknoglobals // A package-level literal makes the small shared attribute set explicit for review.
var summaryAllowedCommonAttrs = map[string]bool{
	"dir":   true,
	"lang":  true,
	"title": true,
}

// RewriteSummaryHTML parses stored feed markup, copies only inactive allowlisted HTML, rewrites image requests through
// the proxy, and returns the reviewed result as template-safe HTML. Invalid input fails closed.
func RewriteSummaryHTML(text, baseURLRaw string) template.HTML {
	nodes, ok := parseSummaryFragment(text)
	if !ok {
		return ""
	}

	sanitized := sanitizeSummaryNodes(nodes, parseSummaryBaseURL(baseURLRaw))

	rewritten, ok := renderSummaryNodes(sanitized)
	if !ok {
		return ""
	}

	// #nosec G203 -- every emitted node and attribute was created by the strict allowlist above.
	return template.HTML(rewritten)
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

func sanitizeSummaryNodes(nodes []*html.Node, base *url.URL) []*html.Node {
	sanitized := make([]*html.Node, 0, len(nodes))
	for _, node := range nodes {
		sanitized = append(sanitized, sanitizeSummaryNode(node, "", base)...)
	}

	return sanitized
}

func sanitizeSummaryNode(node *html.Node, parentElement string, base *url.URL) []*html.Node {
	if node == nil {
		return nil
	}

	switch node.Type {
	case html.TextNode:
		return []*html.Node{{Type: html.TextNode, Data: node.Data}}
	case html.ElementNode:
		return sanitizeSummaryElement(node, parentElement, base)
	case html.ErrorNode, html.DocumentNode, html.CommentNode, html.DoctypeNode, html.RawNode:
		return nil
	}

	return nil
}

func sanitizeSummaryElement(node *html.Node, parentElement string, base *url.URL) []*html.Node {
	name := strings.ToLower(strings.TrimSpace(node.Data))
	if node.Namespace != "" || summaryDroppedSubtrees[name] {
		return nil
	}

	if !summaryAllowedElements[name] {
		return sanitizeSummaryChildren(node, parentElement, base)
	}

	cloned := newSanitizedSummaryElement(node, name, parentElement, base)
	if cloned == nil {
		return nil
	}

	for _, child := range sanitizeSummaryChildren(node, name, base) {
		cloned.AppendChild(child)
	}

	return []*html.Node{cloned}
}

func newSanitizedSummaryElement(
	node *html.Node,
	name string,
	parentElement string,
	base *url.URL,
) *html.Node {
	if name == "source" && parentElement != "picture" {
		return nil
	}

	cloned := new(html.Node)
	cloned.Type = html.ElementNode
	cloned.DataAtom = atom.Lookup([]byte(name))
	cloned.Data = name

	cloned.Attr = sanitizeSummaryAttrs(node, name, base)
	if (name == "img" || name == "source") && !hasSummaryImageSource(cloned) {
		return nil
	}

	if name == "iframe" && !hasSummaryAttr(cloned, "src") {
		return nil
	}

	addEnforcedSummaryAttrs(cloned, name)

	return cloned
}

func addEnforcedSummaryAttrs(node *html.Node, name string) {
	if name == "a" {
		node.Attr = append(node.Attr,
			html.Attribute{Namespace: "", Key: "target", Val: "_blank"},
			html.Attribute{Namespace: "", Key: "rel", Val: "noopener noreferrer"},
		)
	}

	if name == "img" {
		node.Attr = append(node.Attr,
			html.Attribute{Namespace: "", Key: "loading", Val: "lazy"},
			html.Attribute{Namespace: "", Key: "decoding", Val: "async"},
			html.Attribute{Namespace: "", Key: "referrerpolicy", Val: "no-referrer"},
		)
	}

	if name == "iframe" {
		if !hasSummaryAttr(node, "title") {
			node.Attr = append(node.Attr, html.Attribute{Namespace: "", Key: "title", Val: "YouTube video"})
		}

		node.Attr = append(node.Attr,
			html.Attribute{Namespace: "", Key: "loading", Val: "lazy"},
			html.Attribute{Namespace: "", Key: "referrerpolicy", Val: "strict-origin-when-cross-origin"},
			html.Attribute{Namespace: "", Key: "sandbox", Val: youtubeEmbedSandbox},
			html.Attribute{Namespace: "", Key: "allow", Val: "encrypted-media; picture-in-picture; fullscreen"},
			html.Attribute{Namespace: "", Key: "allowfullscreen", Val: ""},
		)
	}
}

func sanitizeSummaryChildren(node *html.Node, parentElement string, base *url.URL) []*html.Node {
	var children []*html.Node
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		children = append(children, sanitizeSummaryNode(child, parentElement, base)...)
	}

	return children
}

func sanitizeSummaryAttrs(node *html.Node, element string, base *url.URL) []html.Attribute {
	allowedForElement := summaryAllowedAttrs[element]
	seen := make(map[string]bool, len(node.Attr))
	attrs := make([]html.Attribute, 0, len(node.Attr))

	for _, attr := range node.Attr {
		key := strings.ToLower(strings.TrimSpace(attr.Key))
		if attr.Namespace != "" || key == "" || seen[key] ||
			(!summaryAllowedCommonAttrs[key] && !allowedForElement[key]) {
			continue
		}

		value, ok := sanitizeSummaryAttrValue(element, key, attr.Val, base)
		if !ok {
			continue
		}

		seen[key] = true
		attrs = append(attrs, html.Attribute{Namespace: "", Key: key, Val: value})
	}

	return attrs
}

func sanitizeSummaryAttrValue(element, key, value string, base *url.URL) (string, bool) {
	switch key {
	case "href":
		return sanitizeSummaryLink(value, base)
	case "srcset":
		return rewriteSrcset(value, base)
	case "src":
		if element == "iframe" {
			return sanitizeYouTubeEmbedURL(value)
		}

		return ProxyImageURL(value, base)
	case "dir":
		direction := strings.ToLower(strings.TrimSpace(value))

		return direction, direction == "ltr" || direction == "rtl" || direction == "auto"
	default:
		return value, true
	}
}

func sanitizeYouTubeEmbedURL(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !isAllowedYouTubeEmbedURL(parsed) {
		return "", false
	}

	host := strings.ToLower(parsed.Hostname())
	parsed.Scheme = "https"
	parsed.Host = host

	return parsed.String(), true
}

func isAllowedYouTubeEmbedURL(parsed *url.URL) bool {
	if parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.Fragment != "" {
		return false
	}

	host := strings.ToLower(parsed.Hostname())
	if host != "www.youtube.com" && host != "www.youtube-nocookie.com" {
		return false
	}

	videoID, ok := strings.CutPrefix(parsed.EscapedPath(), "/embed/")

	return ok && videoID != "" && !strings.Contains(videoID, "/") && isYouTubeVideoID(videoID)
}

func isYouTubeVideoID(value string) bool {
	if len(value) > youtubeVideoIDMaxLength {
		return false
	}

	return strings.IndexFunc(value, func(char rune) bool {
		return !strings.ContainsRune(youtubeVideoIDChars, char)
	}) == -1
}

func sanitizeSummaryLink(raw string, base *url.URL) (string, bool) {
	parsed, ok := parseAnchorURL(strings.TrimSpace(raw))
	if !ok {
		return "", false
	}

	resolved, ok := resolveAnchorURL(parsed, base)
	if !ok {
		return "", false
	}

	return resolved.String(), true
}

func hasSummaryImageSource(node *html.Node) bool {
	for _, attr := range node.Attr {
		if attr.Key == "src" || attr.Key == "srcset" {
			return true
		}
	}

	return false
}

func hasSummaryAttr(node *html.Node, key string) bool {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return true
		}
	}

	return false
}

func renderSummaryNodes(nodes []*html.Node) (string, bool) {
	var builder strings.Builder
	for _, node := range nodes {
		err := html.Render(&builder, node)
		if err != nil {
			return "", false
		}
	}

	return builder.String(), true
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

	if resolved.Host == "" || !isHTTPScheme(resolved.Scheme) {
		return nil, false
	}

	return resolved, true
}

func isHTTPScheme(scheme string) bool {
	return scheme == "http" || scheme == "https"
}

// parseSummaryBaseURL keeps rewriting deterministic by accepting only absolute http(s) URLs with a host.
func parseSummaryBaseURL(raw string) *url.URL {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || !isHTTPScheme(parsed.Scheme) {
		return nil
	}

	return parsed
}
