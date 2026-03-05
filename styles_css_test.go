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

func TestStylesReaderPanelsAvoidPersistentSeparators(t *testing.T) {
	t.Parallel()

	source := readStylesCSS(t)

	if cssRuleContains(source, ".topbar", "border-bottom:") {
		t.Fatal("expected topbar to avoid a bottom border separator")
	}

	if cssRuleContains(source, ".feed-panel", "border-right:") {
		t.Fatal("expected feed panel to avoid a right border separator")
	}

	if !cssRuleContains(source, ".feed-panel-resizer::before", "opacity: 0;") {
		t.Fatal("expected feed resizer line to be hidden until interaction")
	}

	if !cssRuleContains(source, ".content-panel-resizer::before", "opacity: 0;") {
		t.Fatal("expected content resizer line to be hidden until interaction")
	}

	feedResizerActivePattern := `(?s)\.feed-panel-resizer:hover::before,\s*` +
		`body\.is-resizing-feed-panel \.feed-panel-resizer::before\s*\{[^}]*opacity:\s*1;`
	if !regexp.MustCompile(feedResizerActivePattern).MatchString(source) {
		t.Fatal("expected feed resizer line to appear on hover")
	}

	contentResizerActivePattern := `(?s)\.content-panel-resizer:hover::before,\s*` +
		`body\.is-resizing-content-panel \.content-panel-resizer::before\s*\{[^}]*opacity:\s*1;`
	if !regexp.MustCompile(contentResizerActivePattern).MatchString(source) {
		t.Fatal("expected content resizer line to appear on hover")
	}
}

func TestStylesItemKeyboardOutlineContract(t *testing.T) {
	t.Parallel()

	source := readStylesCSS(t)
	selector := "#item-list.is-keyboard-nav:focus-within .item-entry.is-active"

	if !cssRuleContains(source, selector, "box-shadow:") {
		t.Fatalf("expected %s rule to declare box-shadow", selector)
	}

	if !cssRuleContains(source, selector, "#2563EB") {
		t.Fatalf("expected %s rule to include #2563EB outline token", selector)
	}
}

func TestStylesMobileReaderContracts(t *testing.T) {
	t.Parallel()

	source := readStylesCSS(t)

	if !strings.Contains(source, "@media (max-width: 960px)") {
		t.Fatal("expected mobile media query block")
	}

	if !strings.Contains(source, ".mobile-stream,") || !strings.Contains(source, "display: flex;") {
		t.Fatal("expected mobile stream styles to be defined")
	}

	if !cssRuleContains(source, ".mobile-reader", "display: flex;") {
		t.Fatal("expected mobile reader styles to be defined")
	}

	if !cssRuleContains(source, ".feed-panel", "display: none;") {
		t.Fatal("expected mobile layout to hide feed panel")
	}
}
