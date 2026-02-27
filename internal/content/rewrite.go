package content

import (
	"net/url"
	"slices"
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

type summarySanitizeResult struct {
	changed bool
	removed bool
}

type summaryRewriteResult struct {
	changed bool
	stop    bool
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
	if node.Type != html.ElementNode {
		return rewriteSummaryChildren(node, base)
	}

	result := rewriteSummaryElementNode(node, base)
	if result.stop {
		return result.changed
	}

	childrenChanged := rewriteSummaryChildren(node, base)

	return result.changed || childrenChanged
}

func rewriteSummaryChildren(node *html.Node, base *url.URL) bool {
	changed := false

	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		if rewriteSummaryNode(child, base) {
			changed = true
		}

		child = next
	}

	return changed
}

func rewriteSummaryElementNode(node *html.Node, base *url.URL) summaryRewriteResult {
	sanitized := sanitizeSummaryElement(node)
	if sanitized.changed {
		if sanitized.removed {
			return summaryRewriteResult{
				changed: true,
				stop:    true,
			}
		}

		if rewriteSummaryElement(node, base) {
			return summaryRewriteResult{
				changed: true,
				stop:    false,
			}
		}

		return summaryRewriteResult{
			changed: true,
			stop:    false,
		}
	}

	return summaryRewriteResult{
		changed: rewriteSummaryElement(node, base),
		stop:    false,
	}
}

func sanitizeSummaryElement(node *html.Node) summarySanitizeResult {
	if shouldDropSummaryElement(node) {
		dropSummaryNode(node)

		return summarySanitizeResult{
			changed: true,
			removed: true,
		}
	}

	return summarySanitizeResult{
		changed: dropEventHandlerAttrs(node),
		removed: false,
	}
}

func shouldDropSummaryElement(node *html.Node) bool {
	if hasClassToken(node, "image-link-expand") {
		return true
	}

	switch node.Data {
	case "script", "object", "embed":
		return true
	default:
		return false
	}
}

func hasClassToken(node *html.Node, token string) bool {
	if node == nil || token == "" {
		return false
	}

	classes, found := attrValue(node, "class")
	if !found {
		return false
	}

	return slices.Contains(strings.Fields(classes), token)
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
	case "iframe":
		return rewriteSummaryIFrameNode(node, base)
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

func rewriteSummaryIFrameNode(node *html.Node, base *url.URL) bool {
	src, found := attrValue(node, "src")
	if !found {
		dropSummaryNode(node)

		return true
	}

	parsed, ok := parseAnchorURL(strings.TrimSpace(src))
	if !ok {
		dropSummaryNode(node)

		return true
	}

	resolved, ok := resolveAnchorURL(parsed, base)
	if !ok {
		dropSummaryNode(node)

		return true
	}

	resolved = rewritePrivacyEmbedURL(resolved)

	changed := keepOnlyAttrs(node, map[string]bool{
		"allow":          true,
		"frameborder":    true,
		"height":         true,
		"loading":        true,
		"referrerpolicy": true,
		"sandbox":        true,
		"src":            true,
		"style":          true,
		"title":          true,
		"width":          true,
	})

	rewritten := resolved.String()
	if upsertAttr(node, "src", rewritten) {
		changed = true
	}

	if upsertAttr(node, "loading", "lazy") {
		changed = true
	}

	if upsertAttr(node, "allow", "autoplay; encrypted-media; fullscreen; picture-in-picture") {
		changed = true
	}

	return changed
}

func rewritePrivacyEmbedURL(target *url.URL) *url.URL {
	if target == nil {
		return nil
	}

	host := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
	switch host {
	case "youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com":
		if !strings.HasPrefix(target.EscapedPath(), "/embed/") {
			return target
		}

		cloned := cloneURL(target)
		cloned.Host = "www.youtube-nocookie.com"

		return cloned
	case "youtu.be":
		videoID := strings.Trim(target.EscapedPath(), "/")
		if videoID == "" {
			return target
		}

		cloned := cloneURL(target)
		cloned.Host = "www.youtube-nocookie.com"
		cloned.Path = "/embed/" + videoID
		cloned.RawPath = ""

		return cloned
	default:
		return target
	}
}

func cloneURL(src *url.URL) *url.URL {
	if src == nil {
		return nil
	}

	dst := new(url.URL)
	*dst = *src

	return dst
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

func keepOnlyAttrs(node *html.Node, allowed map[string]bool) bool {
	if len(node.Attr) == 0 {
		return false
	}

	changed := false

	attrs := node.Attr[:0]
	for _, attr := range node.Attr {
		if !allowed[attr.Key] {
			changed = true

			continue
		}

		attrs = append(attrs, attr)
	}

	node.Attr = attrs

	return changed
}

func attrValue(node *html.Node, key string) (string, bool) {
	for _, attr := range node.Attr {
		if attr.Key != key {
			continue
		}

		return attr.Val, true
	}

	return "", false
}

func dropEventHandlerAttrs(node *html.Node) bool {
	if len(node.Attr) == 0 {
		return false
	}

	changed := false

	attrs := node.Attr[:0]
	for _, attr := range node.Attr {
		if strings.HasPrefix(attr.Key, "on") {
			changed = true

			continue
		}

		attrs = append(attrs, attr)
	}

	node.Attr = attrs

	return changed
}

func dropSummaryNode(node *html.Node) {
	if node.Parent != nil {
		node.Parent.RemoveChild(node)

		return
	}

	node.Type = html.TextNode
	node.DataAtom = 0
	node.Data = ""
	node.Attr = nil
	node.FirstChild = nil
	node.LastChild = nil
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
	lower := strings.ToLower(text)

	targets := [...]string{
		"<img",
		"<source",
		"<a",
		"<iframe",
		"<script",
		"<style",
		"<object",
		"<embed",
		"image-link-expand",
		"style=",
		" on",
	}

	return slices.ContainsFunc(targets[:], func(target string) bool {
		return strings.Contains(lower, target)
	})
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
