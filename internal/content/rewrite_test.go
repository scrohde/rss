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

func TestRewriteSummaryHTMLImages(t *testing.T) {
	t.Parallel()

	input := `<p>Hello</p><img src="https://example.com/image.jpg" alt="x">`
	output := RewriteSummaryHTML(input, "")

	expected := proxied("https://example.com/image.jpg")
	if !strings.Contains(output, expected) {
		t.Fatalf("expected proxied image url, got %q", output)
	}
}

func TestRewriteSummaryHTMLSrcset(t *testing.T) {
	t.Parallel()

	input := `<img srcset="https://example.com/a.jpg 1x, ` +
		`https://example.com/b.jpg 2x" src="https://example.com/a.jpg">`
	output := RewriteSummaryHTML(input, "")
	expectedA := proxied("https://example.com/a.jpg")

	expectedB := proxied("https://example.com/b.jpg")
	if !containsAll(output, expectedA, expectedB) {
		t.Fatalf("expected proxied srcset urls, got %q", output)
	}
}

func TestRewriteSummaryHTMLForBaseRootRelativeImage(t *testing.T) {
	t.Parallel()

	input := `<img src="/assets/content/some-data-should-be-code/graph.png">`
	output := RewriteSummaryHTML(
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
	output := RewriteSummaryHTML(input, "https://example.com/posts/1")
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

	output := RewriteSummaryHTML(input, "")
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

	output := RewriteSummaryHTML(input, "")
	if !strings.Contains(output, `target="_blank"`) {
		t.Fatalf("expected target _blank, got %q", output)
	}

	if !strings.Contains(output, `rel="noopener noreferrer"`) {
		t.Fatalf("expected rel noopener noreferrer, got %q", output)
	}
}

func TestRewriteSummaryHTMLAnchorRelPreservesExistingTokens(t *testing.T) {
	t.Parallel()

	input := `<a href="https://example.com" rel="author">Example</a>`

	output := RewriteSummaryHTML(input, "")
	if !strings.Contains(output, `rel="author noopener noreferrer"`) {
		t.Fatalf(
			"expected existing rel token plus noopener noreferrer, got %q",
			output,
		)
	}
}

func TestRewriteSummaryHTMLAnchorTargetOverwritesNonBlank(t *testing.T) {
	t.Parallel()

	input := `<a href="https://example.com" target="_self">Example</a>`

	output := RewriteSummaryHTML(input, "")
	if !strings.Contains(output, `target="_blank"`) {
		t.Fatalf("expected target _blank, got %q", output)
	}
}

func TestRewriteSummaryHTMLAnchorHrefResolvesAgainstBase(t *testing.T) {
	t.Parallel()

	input := `<a href="/r/u_hackrepair/comments/1r60b1p/` +
		`weve_built_this_before/">[link]</a>`

	output := RewriteSummaryHTML(
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

func TestRewriteSummaryHTMLStripsInlineEventAttrs(t *testing.T) {
	t.Parallel()

	input := `<p style="color:red" onclick="alert(1)">Hello <span style="display:none">world</span></p>`

	output := RewriteSummaryHTML(input, "")
	if !strings.Contains(output, `style="color:red"`) {
		t.Fatalf("expected style attributes preserved, got %q", output)
	}

	if strings.Contains(output, "onclick=") {
		t.Fatalf("expected inline event handlers stripped, got %q", output)
	}
}

func TestRewriteSummaryHTMLDropsSubstackImageOverlayControls(t *testing.T) {
	t.Parallel()

	input := `<figure><a class="image-link image2 is-viewable-img" href="https://example.com/full">` +
		`<div class="image2-inset"><picture><img src="https://example.com/image.jpg" alt="x"></picture></div>` +
		`<div class="image-link-expand"><button class="restack-image">Restack</button>` +
		`<button class="view-image">View image</button></div></a><figcaption>Caption with ` +
		`<a href="https://example.com/more">source</a></figcaption></figure>`

	output := RewriteSummaryHTML(input, "")

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
	output := RewriteSummaryHTML(input, "")

	if strings.Contains(output, "image-link-expand") || strings.Contains(output, "view-image") {
		t.Fatalf("expected Substack overlay controls removed, got %q", output)
	}

	if !strings.Contains(output, "<p>after</p>") {
		t.Fatalf("expected surrounding content preserved, got %q", output)
	}
}

func TestRewriteSummaryHTMLKeepsVideoEmbedsAndDropsScripts(t *testing.T) {
	t.Parallel()

	input := `<p>before</p><iframe src="https://tube.tchncs.de/" style="border:0" onclick="x()"></iframe>` +
		`<script>alert(1)</script><style>p{color:red;}</style><p>after</p>`

	output := RewriteSummaryHTML(input, "")
	if !strings.Contains(output, `<iframe src="https://tube.tchncs.de/"`) {
		t.Fatalf("expected iframe preserved, got %q", output)
	}

	if !strings.Contains(output, `style="border:0"`) {
		t.Fatalf("expected iframe style preserved, got %q", output)
	}

	if strings.Contains(output, "onclick=") {
		t.Fatalf("expected inline event handlers stripped, got %q", output)
	}

	if strings.Contains(output, "<script") {
		t.Fatalf("expected script removed, got %q", output)
	}

	if !strings.Contains(output, "<style>") {
		t.Fatalf("expected style tag preserved, got %q", output)
	}

	if !containsAll(output, "<p>before</p>", "<p>after</p>") {
		t.Fatalf("expected surrounding content preserved, got %q", output)
	}
}

func TestRewriteSummaryHTMLDropsUnsafeIframeSrc(t *testing.T) {
	t.Parallel()

	input := `<iframe src="javascript:alert(1)"></iframe><p>after</p>`
	output := RewriteSummaryHTML(input, "")

	if strings.Contains(output, "<iframe") {
		t.Fatalf("expected unsafe iframe dropped, got %q", output)
	}

	if !strings.Contains(output, "<p>after</p>") {
		t.Fatalf("expected safe content preserved, got %q", output)
	}
}

func TestRewriteSummaryHTMLRewritesYouTubeEmbedToNoCookie(t *testing.T) {
	t.Parallel()

	input := `<iframe src="https://www.youtube.com/embed/0YhJxJZOWBw?feature=oembed"></iframe>`
	output := RewriteSummaryHTML(input, "")

	if !strings.Contains(output, `src="https://www.youtube-nocookie.com/embed/0YhJxJZOWBw?feature=oembed"`) {
		t.Fatalf("expected youtube-nocookie embed src, got %q", output)
	}
}

func TestRewriteSummaryHTMLRewritesShortYouTubeURLToNoCookieEmbed(t *testing.T) {
	t.Parallel()

	input := `<iframe src="https://youtu.be/Jr2auYrBDA4"></iframe>`
	output := RewriteSummaryHTML(input, "")

	if !strings.Contains(output, `src="https://www.youtube-nocookie.com/embed/Jr2auYrBDA4"`) {
		t.Fatalf("expected youtube-nocookie embed src, got %q", output)
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
