package content

import (
	"net/url"
	"strings"
	"testing"
)

func TestRewriteSummaryHTMLImages(t *testing.T) {
	input := `<p>Hello</p><img src="https://example.com/image.jpg" alt="x">`
	output := RewriteSummaryHTML(input)
	expected := ImageProxyPath + "?url=" + url.QueryEscape("https://example.com/image.jpg")
	if !strings.Contains(output, expected) {
		t.Fatalf("expected proxied image url, got %q", output)
	}
}

func TestRewriteSummaryHTMLSrcset(t *testing.T) {
	input := `<img srcset="https://example.com/a.jpg 1x, https://example.com/b.jpg 2x" src="https://example.com/a.jpg">`
	output := RewriteSummaryHTML(input)
	expectedA := ImageProxyPath + "?url=" + url.QueryEscape("https://example.com/a.jpg")
	expectedB := ImageProxyPath + "?url=" + url.QueryEscape("https://example.com/b.jpg")
	if !strings.Contains(output, expectedA) || !strings.Contains(output, expectedB) {
		t.Fatalf("expected proxied srcset urls, got %q", output)
	}
}

func TestRewriteSummaryHTMLAnchorTargetAndRel(t *testing.T) {
	input := `<a href="https://example.com">Example</a>`
	output := RewriteSummaryHTML(input)
	if !strings.Contains(output, `target="_blank"`) {
		t.Fatalf("expected target _blank, got %q", output)
	}
	if !strings.Contains(output, `rel="noopener noreferrer"`) {
		t.Fatalf("expected rel noopener noreferrer, got %q", output)
	}
}

func TestRewriteSummaryHTMLAnchorRelPreservesExistingTokens(t *testing.T) {
	input := `<a href="https://example.com" rel="author">Example</a>`
	output := RewriteSummaryHTML(input)
	if !strings.Contains(output, `rel="author noopener noreferrer"`) {
		t.Fatalf("expected existing rel token plus noopener noreferrer, got %q", output)
	}
}

func TestRewriteSummaryHTMLAnchorTargetOverwritesNonBlank(t *testing.T) {
	input := `<a href="https://example.com" target="_self">Example</a>`
	output := RewriteSummaryHTML(input)
	if !strings.Contains(output, `target="_blank"`) {
		t.Fatalf("expected target _blank, got %q", output)
	}
}
