package content

import (
	"net/url"
	"strings"
	"testing"
)

func TestRewriteSummaryHTMLImages(t *testing.T) {
	input := `<p>Hello</p><img src="https://example.com/image.jpg" alt="x">`
	output := RewriteSummaryHTML(input, "")
	expected := ImageProxyPath + "?url=" + url.QueryEscape("https://example.com/image.jpg")
	if !strings.Contains(output, expected) {
		t.Fatalf("expected proxied image url, got %q", output)
	}
}

func TestRewriteSummaryHTMLSrcset(t *testing.T) {
	input := `<img srcset="https://example.com/a.jpg 1x, https://example.com/b.jpg 2x" src="https://example.com/a.jpg">`
	output := RewriteSummaryHTML(input, "")
	expectedA := ImageProxyPath + "?url=" + url.QueryEscape("https://example.com/a.jpg")
	expectedB := ImageProxyPath + "?url=" + url.QueryEscape("https://example.com/b.jpg")
	if !strings.Contains(output, expectedA) || !strings.Contains(output, expectedB) {
		t.Fatalf("expected proxied srcset urls, got %q", output)
	}
}

func TestRewriteSummaryHTMLForBaseRootRelativeImage(t *testing.T) {
	input := `<img src="/assets/content/some-data-should-be-code/graph.png">`
	output := RewriteSummaryHTML(input, "https://borretti.me/article/some-data-should-be-code")
	expected := ImageProxyPath + "?url=" + url.QueryEscape("https://borretti.me/assets/content/some-data-should-be-code/graph.png")
	if !strings.Contains(output, expected) {
		t.Fatalf("expected proxied image url with base, got %q", output)
	}
}

func TestRewriteSummaryHTMLForBaseRelativeSrcset(t *testing.T) {
	input := `<img srcset="images/a.jpg 1x, /images/b.jpg 2x">`
	output := RewriteSummaryHTML(input, "https://example.com/posts/1")
	expectedA := ImageProxyPath + "?url=" + url.QueryEscape("https://example.com/posts/images/a.jpg")
	expectedB := ImageProxyPath + "?url=" + url.QueryEscape("https://example.com/images/b.jpg")
	if !strings.Contains(output, expectedA) || !strings.Contains(output, expectedB) {
		t.Fatalf("expected proxied srcset urls with base, got %q", output)
	}
}

func TestRewriteSummaryHTMLSrcsetWithCommasInURL(t *testing.T) {
	input := `<img srcset="https://substackcdn.com/image/fetch/$s_!sBbM!,w_424,c_limit,f_auto,q_auto:good/https%3A%2F%2Fsubstack-post-media.s3.amazonaws.com%2Fpublic%2Fimages%2Fa.png 424w, https://substackcdn.com/image/fetch/$s_!sBbM!,w_848,c_limit,f_auto,q_auto:good/https%3A%2F%2Fsubstack-post-media.s3.amazonaws.com%2Fpublic%2Fimages%2Fa.png 848w" src="https://substackcdn.com/image/fetch/$s_!sBbM!,w_848,c_limit,f_auto,q_auto:good/https%3A%2F%2Fsubstack-post-media.s3.amazonaws.com%2Fpublic%2Fimages%2Fa.png">`
	output := RewriteSummaryHTML(input, "")
	if strings.Contains(output, ", w_424, c_limit") || strings.Contains(output, ", w_848, c_limit") {
		t.Fatalf("expected srcset URLs with embedded commas to remain intact, got %q", output)
	}
	proxied424 := ImageProxyPath + "?url=" + url.QueryEscape("https://substackcdn.com/image/fetch/$s_!sBbM!,w_424,c_limit,f_auto,q_auto:good/https%3A%2F%2Fsubstack-post-media.s3.amazonaws.com%2Fpublic%2Fimages%2Fa.png")
	proxied848 := ImageProxyPath + "?url=" + url.QueryEscape("https://substackcdn.com/image/fetch/$s_!sBbM!,w_848,c_limit,f_auto,q_auto:good/https%3A%2F%2Fsubstack-post-media.s3.amazonaws.com%2Fpublic%2Fimages%2Fa.png")
	if !strings.Contains(output, proxied424+" 424w") || !strings.Contains(output, proxied848+" 848w") {
		t.Fatalf("expected proxied srcset candidates, got %q", output)
	}
}

func TestRewriteSummaryHTMLAnchorTargetAndRel(t *testing.T) {
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
	input := `<a href="https://example.com" rel="author">Example</a>`
	output := RewriteSummaryHTML(input, "")
	if !strings.Contains(output, `rel="author noopener noreferrer"`) {
		t.Fatalf("expected existing rel token plus noopener noreferrer, got %q", output)
	}
}

func TestRewriteSummaryHTMLAnchorTargetOverwritesNonBlank(t *testing.T) {
	input := `<a href="https://example.com" target="_self">Example</a>`
	output := RewriteSummaryHTML(input, "")
	if !strings.Contains(output, `target="_blank"`) {
		t.Fatalf("expected target _blank, got %q", output)
	}
}

func TestRewriteSummaryHTMLAnchorHrefResolvesAgainstBase(t *testing.T) {
	input := `<a href="/r/u_hackrepair/comments/1r60b1p/weve_built_this_before/">[link]</a>`
	output := RewriteSummaryHTML(input, "https://www.reddit.com/r/accelerate/comments/1r60h2p/discussion_weve_built_this_before/")
	if !strings.Contains(output, `href="https://www.reddit.com/r/u_hackrepair/comments/1r60b1p/weve_built_this_before/"`) {
		t.Fatalf("expected absolute href, got %q", output)
	}
}

func TestBuildImageProxyRequestHeaders(t *testing.T) {
	target, err := url.Parse("https://cdn-images-1.medium.com/max/1024/1*svqMSkVB3MnkjOetkxoLCQ.png")
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}

	req, err := BuildImageProxyRequest(target)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if got := req.Header.Get("User-Agent"); got != ImageProxyUserAgent {
		t.Fatalf("expected image proxy user-agent %q, got %q", ImageProxyUserAgent, got)
	}
	if got := req.Header.Get("Accept"); got == "" || !strings.Contains(got, "image/webp") {
		t.Fatalf("expected image-focused accept header, got %q", got)
	}
	if got := req.Header.Get("Referer"); got != "" {
		t.Fatalf("expected no referer header, got %q", got)
	}
}
