//nolint:testpackage // Content tests exercise package-internal helpers directly.
package content

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

const (
	substackURL424Prefix = "https://substackcdn.com/image/fetch/" +
		"$s_!sBbM!,w_424,c_limit,"
	substackURL848Prefix = "https://substackcdn.com/image/fetch/" +
		"$s_!sBbM!,w_848,c_limit,"
	substackURLSuffix = "f_auto,q_auto:good/https%3A%2F%2Fsubstack-post-" +
		"media.s3.amazonaws.com%2Fpublic%2Fimages%2Fa.png"
)

func proxied(raw string) string {
	return ImageProxyPath + "?url=" + url.QueryEscape(raw)
}

func containsAll(text, first, second string) bool {
	return strings.Contains(text, first) && strings.Contains(text, second)
}

func sanitizedSummaryString(input, baseURL string) string {
	return string(RewriteSummaryHTML(input, baseURL))
}

func TestRewriteSummaryHTMLImages(t *testing.T) {
	t.Parallel()

	input := `<p>Hello</p><img src="https://example.com/image.jpg" alt="x">`
	output := sanitizedSummaryString(input, "")

	expected := proxied("https://example.com/image.jpg")
	if !strings.Contains(output, expected) {
		t.Fatalf("expected proxied image url, got %q", output)
	}
}

func TestRewriteSummaryHTMLSrcset(t *testing.T) {
	t.Parallel()

	input := `<img srcset="https://example.com/a.jpg 1x, ` +
		`https://example.com/b.jpg 2x" src="https://example.com/a.jpg">`
	output := sanitizedSummaryString(input, "")
	expectedA := proxied("https://example.com/a.jpg")

	expectedB := proxied("https://example.com/b.jpg")
	if !containsAll(output, expectedA, expectedB) {
		t.Fatalf("expected proxied srcset urls, got %q", output)
	}
}

func TestRewriteSummaryHTMLForBaseRootRelativeImage(t *testing.T) {
	t.Parallel()

	input := `<img src="/assets/content/some-data-should-be-code/graph.png">`
	output := sanitizedSummaryString(
		input,
		"https://borretti.me/article/some-data-should-be-code",
	)

	expected := proxied(
		"https://borretti.me/assets/content/some-data-should-be-code/graph.png",
	)
	if !strings.Contains(output, expected) {
		t.Fatalf("expected proxied image url with base, got %q", output)
	}
}

func TestRewriteSummaryHTMLForBaseRelativeSrcset(t *testing.T) {
	t.Parallel()

	input := `<img srcset="images/a.jpg 1x, /images/b.jpg 2x">`
	output := sanitizedSummaryString(input, "https://example.com/posts/1")
	expectedA := proxied("https://example.com/posts/images/a.jpg")

	expectedB := proxied("https://example.com/images/b.jpg")
	if !containsAll(output, expectedA, expectedB) {
		t.Fatalf("expected proxied srcset urls with base, got %q", output)
	}
}

func TestRewriteSummaryHTMLSrcsetWithCommasInURL(t *testing.T) {
	t.Parallel()

	input := `<img srcset="` +
		substackURL424Prefix +
		substackURLSuffix +
		` 424w, ` +
		substackURL848Prefix +
		substackURLSuffix +
		` 848w" ` +
		`src="` +
		substackURL848Prefix +
		substackURLSuffix +
		`">`

	output := sanitizedSummaryString(input, "")
	if strings.Contains(output, ", w_424, c_limit") ||
		strings.Contains(output, ", w_848, c_limit") {
		t.Fatalf(
			"expected embedded-comma srcset URLs to remain intact, got %q",
			output,
		)
	}

	proxied424 := proxied(substackURL424Prefix + substackURLSuffix)

	proxied848 := proxied(substackURL848Prefix + substackURLSuffix)
	if !strings.Contains(output, proxied424+" 424w") ||
		!strings.Contains(output, proxied848+" 848w") {
		t.Fatalf("expected proxied srcset candidates, got %q", output)
	}
}

func TestRewriteSummaryHTMLAnchorTargetAndRel(t *testing.T) {
	t.Parallel()

	input := `<a href="https://example.com">Example</a>`

	output := sanitizedSummaryString(input, "")
	if !strings.Contains(output, `target="_blank"`) {
		t.Fatalf("expected target _blank, got %q", output)
	}

	if !strings.Contains(output, `rel="noopener noreferrer"`) {
		t.Fatalf("expected rel noopener noreferrer, got %q", output)
	}
}

func TestRewriteSummaryHTMLAnchorRelDiscardsCallerTokens(t *testing.T) {
	t.Parallel()

	input := `<a href="https://example.com" rel="author">Example</a>`

	output := sanitizedSummaryString(input, "")
	if !strings.Contains(output, `rel="noopener noreferrer"`) || strings.Contains(output, "author") {
		t.Fatalf("expected only enforced rel tokens, got %q", output)
	}
}

func TestRewriteSummaryHTMLAnchorTargetOverwritesNonBlank(t *testing.T) {
	t.Parallel()

	input := `<a href="https://example.com" target="_self">Example</a>`

	output := sanitizedSummaryString(input, "")
	if !strings.Contains(output, `target="_blank"`) {
		t.Fatalf("expected target _blank, got %q", output)
	}
}

func TestRewriteSummaryHTMLAnchorHrefResolvesAgainstBase(t *testing.T) {
	t.Parallel()

	input := `<a href="/r/u_hackrepair/comments/1r60b1p/` +
		`weve_built_this_before/">[link]</a>`

	output := sanitizedSummaryString(
		input,
		"https://www.reddit.com/r/accelerate/comments/1r60h2p/"+
			"discussion_weve_built_this_before/",
	)
	if !strings.Contains(
		output,
		`href="https://www.reddit.com/r/u_hackrepair/comments/`+
			`1r60b1p/weve_built_this_before/"`,
	) {
		t.Fatalf("expected absolute href, got %q", output)
	}
}

func TestRewriteSummaryHTMLStripsActiveAndPresentationAttrs(t *testing.T) {
	t.Parallel()

	input := `<p style="color:red" onclick="alert(1)">Hello <span style="display:none">world</span></p>`

	output := sanitizedSummaryString(input, "")
	if strings.Contains(output, "style=") || strings.Contains(output, "onclick=") {
		t.Fatalf("expected active and presentation attributes stripped, got %q", output)
	}
}

func TestRewriteSummaryHTMLDropsSubstackImageOverlayControls(t *testing.T) {
	t.Parallel()

	input := `<figure><a class="image-link image2 is-viewable-img" href="https://example.com/full">` +
		`<div class="image2-inset"><picture><img src="https://example.com/image.jpg" alt="x"></picture></div>` +
		`<div class="image-link-expand"><button class="restack-image">Restack</button>` +
		`<button class="view-image">View image</button></div></a><figcaption>Caption with ` +
		`<a href="https://example.com/more">source</a></figcaption></figure>`

	output := sanitizedSummaryString(input, "")

	if strings.Contains(output, "image-link-expand") ||
		strings.Contains(output, "restack-image") ||
		strings.Contains(output, "view-image") {
		t.Fatalf("expected Substack overlay controls removed, got %q", output)
	}

	if !strings.Contains(output, proxied("https://example.com/image.jpg")) {
		t.Fatalf("expected image preserved and proxied, got %q", output)
	}

	if !strings.Contains(output, `href="https://example.com/full"`) {
		t.Fatalf("expected outer image link preserved, got %q", output)
	}

	if !strings.Contains(output, "<figcaption>Caption with") {
		t.Fatalf("expected figcaption preserved, got %q", output)
	}
}

func TestRewriteSummaryHTMLDropsSubstackImageOverlayWithoutImages(t *testing.T) {
	t.Parallel()

	input := `<div class="image-link-expand"><button class="view-image">View image</button></div><p>after</p>`
	output := sanitizedSummaryString(input, "")

	if strings.Contains(output, "image-link-expand") || strings.Contains(output, "view-image") {
		t.Fatalf("expected Substack overlay controls removed, got %q", output)
	}

	if !strings.Contains(output, "<p>after</p>") {
		t.Fatalf("expected surrounding content preserved, got %q", output)
	}
}

func TestRewriteSummaryHTMLDropsActiveEmbeddedContent(t *testing.T) {
	t.Parallel()

	input := `<p>before</p><iframe src="https://tube.tchncs.de/" style="border:0" onclick="x()"></iframe>` +
		`<script>alert(1)</script><style>p{color:red;}</style><p>after</p>`

	output := sanitizedSummaryString(input, "")
	if strings.Contains(output, "iframe") || strings.Contains(output, "script") || strings.Contains(output, "style") {
		t.Fatalf("expected active embedded content removed, got %q", output)
	}

	if !containsAll(output, "<p>before</p>", "<p>after</p>") {
		t.Fatalf("expected surrounding content preserved, got %q", output)
	}
}

func TestRewriteSummaryHTMLDropsUnsafeIframeSrc(t *testing.T) {
	t.Parallel()

	input := `<iframe src="javascript:alert(1)"></iframe><p>after</p>`
	output := sanitizedSummaryString(input, "")

	if strings.Contains(output, "<iframe") {
		t.Fatalf("expected unsafe iframe dropped, got %q", output)
	}

	if !strings.Contains(output, "<p>after</p>") {
		t.Fatalf("expected safe content preserved, got %q", output)
	}
}

func TestRewriteSummaryHTMLAllowsYouTubeEmbed(t *testing.T) {
	t.Parallel()

	input := `<iframe width="200" height="113" src="https://www.youtube.com/embed/0YhJxJZOWBw?feature=oembed" ` +
		`frameborder="0" allow="autoplay" onclick="attack()" title="An interview"></iframe>`
	output := sanitizedSummaryString(input, "")

	wantAttrs := []string{
		`<iframe width="200" height="113" src="https://www.youtube.com/embed/0YhJxJZOWBw?feature=oembed"`,
		`title="An interview"`,
		`loading="lazy"`,
		`referrerpolicy="strict-origin-when-cross-origin"`,
		`sandbox="allow-scripts allow-same-origin allow-presentation allow-popups"`,
		`allow="encrypted-media; picture-in-picture; fullscreen"`,
		`allowfullscreen=""`,
	}
	for _, want := range wantAttrs {
		if !strings.Contains(output, want) {
			t.Fatalf("expected hardened YouTube iframe attribute %q, got %q", want, output)
		}
	}

	if strings.Contains(output, "onclick") || strings.Contains(output, "frameborder") ||
		strings.Contains(output, `allow="autoplay"`) {
		t.Fatalf("expected feed-provided active attributes removed, got %q", output)
	}
}

func TestRewriteSummaryHTMLDropsNonEmbedYouTubeURL(t *testing.T) {
	t.Parallel()

	input := `<iframe src="https://youtu.be/Jr2auYrBDA4"></iframe>`
	output := sanitizedSummaryString(input, "")

	if output != "" {
		t.Fatalf("expected iframe removed, got %q", output)
	}
}

func TestRewriteSummaryHTMLAllowsYouTubeNoCookieEmbed(t *testing.T) {
	t.Parallel()

	input := `<iframe src="https://www.youtube-nocookie.com/embed/Jr2auYrBDA4"></iframe>`
	output := sanitizedSummaryString(input, "")

	if !strings.Contains(output, `src="https://www.youtube-nocookie.com/embed/Jr2auYrBDA4"`) {
		t.Fatalf("expected youtube-nocookie embed preserved, got %q", output)
	}

	if !strings.Contains(output, `title="YouTube video"`) {
		t.Fatalf("expected fallback iframe title, got %q", output)
	}
}

func TestRewriteSummaryHTMLDropsUntrustedIframeHostsAndPaths(t *testing.T) {
	t.Parallel()

	inputs := []string{
		`<iframe src="https://example.com/embed/Jr2auYrBDA4"></iframe>`,
		`<iframe src="https://www.youtube.com/watch?v=Jr2auYrBDA4"></iframe>`,
		`<iframe src="https://www.youtube.com.evil.test/embed/Jr2auYrBDA4"></iframe>`,
		`<iframe src="http://www.youtube.com/embed/Jr2auYrBDA4"></iframe>`,
		`<iframe src="https://www.youtube.com/embed/a%2Fb"></iframe>`,
	}

	for _, input := range inputs {
		if output := sanitizedSummaryString(input, ""); output != "" {
			t.Fatalf("expected unsafe iframe removed for %q, got %q", input, output)
		}
	}
}

func TestRewriteSummaryHTMLStrictInactiveAllowlist(t *testing.T) {
	t.Parallel()

	input := `<div id="app" class="item-entry" style="position:fixed" ` +
		`HX-POST = "/feeds/1/delete" DATA-HX-TRIGGER="every 1ms" onclick="attack()">` +
		`<p title="kept">Safe <strong data-hx-post="/attack">formatting</strong></p>` +
		`<form action="/feeds/1/delete"><label>Visible text<input autofocus></label>` +
		`<button formaction="/feeds/1/delete">Dangerous control</button></form>` +
		`<link rel="stylesheet" href="https://example.com/attack.css">` +
		`<meta http-equiv="refresh" content="0;url=/feeds/1/delete">` +
		`<script>script payload</script><style>style payload</style>` +
		`<object data="/attack">object payload</object><embed src="/attack">` +
		`<iframe src="/attack">iframe payload</iframe>` +
		`<svg><foreignObject><div hx-post="/attack">svg payload</div></foreignObject></svg>` +
		`<math><mtext>math payload</mtext></math><video src="/attack">video payload</video>` +
		`<template><div hx-post="/attack">template payload</div></template></div>`

	output := sanitizedSummaryString(input, "https://example.com/posts/1")
	if !strings.Contains(output, `<p title="kept">Safe <strong>formatting</strong></p>`) ||
		!strings.Contains(output, "Visible text") {
		t.Fatalf("expected safe formatting and form text preserved, got %q", output)
	}

	blocked := []string{
		"hx-", "data-hx", "style=", "onclick", "id=", "class=", "<form", "<input", "<button",
		"<link", "<meta", "<script", "<style", "<object", "<embed", "<iframe", "<svg", "<math",
		"<video", "<template", "Dangerous control", "script payload", "style payload", "object payload",
		"iframe payload", "svg payload", "math payload", "video payload", "template payload",
	}
	for _, value := range blocked {
		if strings.Contains(strings.ToLower(output), strings.ToLower(value)) {
			t.Fatalf("expected %q removed from sanitized output %q", value, output)
		}
	}
}

func TestRewriteSummaryHTMLHandlesDuplicateAndMalformedAttrs(t *testing.T) {
	t.Parallel()

	input := "<DIV\n HX-POST \t ='/feeds/1/delete' data-HX-post='/attack' " +
		"title='first' title='second'><p><x:thing x:hx-post='/attack'>safe"
	output := sanitizedSummaryString(input, "https://example.com/posts/1")

	if strings.Contains(strings.ToLower(output), "hx-") {
		t.Fatalf("expected malformed active attributes removed, got %q", output)
	}

	if strings.Count(output, "title=") != 1 || !strings.Contains(output, `title="first"`) {
		t.Fatalf("expected duplicate attributes collapsed to the first value, got %q", output)
	}

	if !strings.Contains(output, "safe") {
		t.Fatalf("expected safe malformed-fragment text preserved, got %q", output)
	}
}

func TestRewriteSummaryHTMLDropsUnsafeImageCandidates(t *testing.T) {
	t.Parallel()

	input := `<picture>` +
		`<source srcset="javascript:alert(1) 1x, https://example.com/source.jpg 2x">` +
		`<img src="javascript:alert(1)" ` +
		`srcset="https://example.com/image.jpg 1x, data:image/png;base64,AAAA 2x" alt="safe">` +
		`</picture>`
	output := sanitizedSummaryString(input, "https://example.com/posts/1")

	if strings.Contains(strings.ToLower(output), "javascript:") || strings.Contains(output, "data:image") {
		t.Fatalf("expected unsafe image candidates removed, got %q", output)
	}

	if !strings.Contains(output, proxied("https://example.com/source.jpg")) ||
		!strings.Contains(output, proxied("https://example.com/image.jpg")) {
		t.Fatalf("expected safe image candidates proxied, got %q", output)
	}

	if !strings.Contains(output, `loading="lazy"`) || !strings.Contains(output, `referrerpolicy="no-referrer"`) {
		t.Fatalf("expected enforced image attributes, got %q", output)
	}
}

func TestBuildImageProxyRequestHeaders(t *testing.T) {
	t.Parallel()

	target, err := url.Parse(
		"https://cdn-images-1.medium.com/max/1024/1*svqMSkVB3MnkjOetkxoLCQ.png",
	)
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}

	req, err := BuildImageProxyRequest(context.Background(), target)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if got := req.Header.Get("User-Agent"); got != ImageProxyUserAgent {
		t.Fatalf(
			"expected image proxy user-agent %q, got %q",
			ImageProxyUserAgent,
			got,
		)
	}

	if got := req.Header.Get("Accept"); got == "" ||
		!strings.Contains(got, "image/webp") {
		t.Fatalf("expected image-focused accept header, got %q", got)
	}

	if got := req.Header.Get("Referer"); got != "" {
		t.Fatalf("expected no referer header, got %q", got)
	}
}
