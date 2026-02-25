package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readStylesCSS(t *testing.T) string {
	t.Helper()

	source, err := os.ReadFile(filepath.Join("static", "styles.css"))
	if err != nil {
		t.Fatalf("read styles.css: %v", err)
	}

	return string(source)
}

func cssRuleContains(source, selector, token string) bool {
	pattern := "(?s)" +
		regexp.QuoteMeta(selector) +
		`\s*\{[^}]*` +
		regexp.QuoteMeta(token)

	return regexp.MustCompile(pattern).MatchString(source)
}

func TestStylesTopbarMatchesFeedPanelBackground(t *testing.T) {
	t.Parallel()

	source := readStylesCSS(t)

	if !cssRuleContains(source, ".topbar", "background: var(--sidebar);") {
		t.Fatal("expected topbar background to use --sidebar")
	}

	if !cssRuleContains(source, ".feed-panel", "background: var(--sidebar);") {
		t.Fatal("expected feed panel background to use --sidebar")
	}
}

func TestStylesContentPanelScrollbarSwitching(t *testing.T) {
	t.Parallel()

	source := readStylesCSS(t)

	if !cssRuleContains(source, ".main-panel", "overflow-y: auto;") {
		t.Fatal("expected main panel to handle item-list scrolling by default")
	}

	if !cssRuleContains(source, "#content-panel", "overflow-y: hidden;") {
		t.Fatal("expected content panel overflow to be hidden by default")
	}

	if !cssRuleContains(source, "#content-panel.is-open", "overflow-y: auto;") {
		t.Fatal("expected expanded content panel to enable vertical scrolling")
	}

	if !cssRuleContains(
		source,
		".app:has(#content-panel.is-open) .main-panel",
		"overflow-y: hidden;",
	) {
		t.Fatal("expected main panel scrolling to be disabled when content panel is open")
	}
}

func TestStylesReadingSurfaceScrollbarsAreVisible(t *testing.T) {
	t.Parallel()

	source := readStylesCSS(t)

	if !strings.Contains(source, "scrollbar-width: thin;") {
		t.Fatal("expected visible thin scrollbar on reading surfaces")
	}

	if !strings.Contains(source, ".main-panel::-webkit-scrollbar") ||
		!strings.Contains(source, "width: 10px;") {
		t.Fatal("expected non-zero webkit scrollbar width")
	}

	if !strings.Contains(source, ".main-panel::-webkit-scrollbar-thumb") ||
		!strings.Contains(source, "background: rgba(31, 41, 55, 0.38);") {
		t.Fatal("expected visible scrollbar thumb styling")
	}
}
