package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const mobileReaderActionsPaddingToken = "padding-top: calc(\n" +
	"      var(--mobile-reader-fab-gap) + var(--mobile-reader-fab-size) + var(--mobile-reader-content-gap)\n" +
	"    );"

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
		!strings.Contains(source, "background: var(--scrollbar-thumb);") {
		t.Fatal("expected visible scrollbar thumb styling")
	}
}

func TestStylesThemeContractsFollowServerMarkers(t *testing.T) {
	t.Parallel()

	source := readStylesCSS(t)

	if !cssRuleContains(source, "html[data-theme=\"dark\"]", "color-scheme: dark;") {
		t.Fatal("expected explicit dark theme selector to opt into dark color-scheme")
	}

	if !strings.Contains(source, "html[data-theme=\"system\"],") {
		t.Fatal("expected explicit system theme selector in dark-mode media override")
	}

	if !strings.Contains(source, "html:not([data-theme])") {
		t.Fatal("expected anonymous pages without a theme marker to follow system theme")
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

	checks := []struct {
		msg string
		ok  bool
	}{
		{
			ok:  strings.Contains(source, "@media (max-width: 960px)"),
			msg: "expected mobile media query block",
		},
		{
			ok:  strings.Contains(source, ".mobile-stream,") && strings.Contains(source, "display: flex;"),
			msg: "expected mobile stream styles to be defined",
		},
		{
			ok:  cssRuleContains(source, ".mobile-reader", "display: flex;"),
			msg: "expected mobile reader styles to be defined",
		},
		{
			ok: cssRuleContains(source, ".mobile-reader-article", "background: transparent;") &&
				!cssRuleContains(source, ".mobile-reader-article", "border-top: 1px solid var(--panel-divider);"),
			msg: "expected mobile reader article to use flat separators instead of a card shell",
		},
		{
			ok:  cssRuleContains(source, ".feed-panel", "display: none;"),
			msg: "expected mobile layout to hide feed panel",
		},
		{
			ok:  cssRuleContains(source, ".main-panel", "overflow-x: clip;"),
			msg: "expected mobile main panel to clip horizontal overflow",
		},
		{
			ok:  cssRuleContains(source, ".mobile-card-source", "overflow-wrap: anywhere;"),
			msg: "expected mobile card feed titles to wrap instead of widening the viewport",
		},
		{
			ok:  cssRuleContains(source, ".mobile-card-title", "overflow-wrap: anywhere;"),
			msg: "expected mobile card titles to wrap instead of widening the viewport",
		},
		{
			ok: cssRuleContains(source, ".mobile-reader-fab", "width: 34px;") &&
				cssRuleContains(source, ".mobile-reader-actions", "position: fixed;") &&
				cssRuleContains(source, ".mobile-reader-actions", "z-index: 5;") &&
				cssRuleContains(
					source,
					".mobile-reader",
					mobileReaderActionsPaddingToken,
				),
			msg: "expected mobile reader actions to float at the top with reserved content space",
		},
	}

	for _, check := range checks {
		if !check.ok {
			t.Fatal(check.msg)
		}
	}
}

func TestStylesPulseStatusContracts(t *testing.T) {
	t.Parallel()

	source := readStylesCSS(t)

	checks := []struct {
		msg string
		ok  bool
	}{
		{
			ok:  cssRuleContains(source, ".feed-link", "grid-template-columns: 10px minmax(0, 1fr) auto;"),
			msg: "expected desktop feed rows to reserve pulse status space",
		},
		{
			ok:  cssRuleContains(source, ".feed-pulse-indicator.none", "opacity: 0;"),
			msg: "expected empty desktop pulse status slots to stay invisible",
		},
		{
			ok:  cssRuleContains(source, ".feed-link:focus-visible", "outline: 2px solid var(--accent);"),
			msg: "expected desktop feed rows to have a visible keyboard focus state",
		},
		{
			ok: cssRuleContains(source, ".brand-subtitle-pending", "display: none;") &&
				cssRuleContains(source, ".brand-pulse.htmx-request .brand-subtitle-default", "display: none;") &&
				cssRuleContains(source, ".brand-pulse.htmx-request .brand-subtitle-pending", "display: inline;"),
			msg: "expected brand pulse button to show temporary refreshing feedback during htmx requests",
		},
		{
			ok:  cssRuleContains(source, ".mobile-stream-list", "border-top: 1px solid var(--panel-divider);"),
			msg: "expected mobile stream list to start with a thin divider",
		},
		{
			ok: cssRuleContains(source, ".mobile-card", "border-bottom: 1px solid var(--panel-divider);") &&
				cssRuleContains(source, ".mobile-card", "background: transparent;"),
			msg: "expected mobile items to render as divided rows instead of cards",
		},
		{
			ok: cssRuleContains(source, ".mobile-card-mark-read", "width: 34px;") &&
				cssRuleContains(source, ".mobile-card-mark-read", "height: 34px;"),
			msg: "expected mobile mark-read icon button to have a stable compact touch target",
		},
		{
			ok: strings.Contains(source, ".mobile-card-mark-read:focus-visible") &&
				strings.Contains(source, "outline: 2px solid var(--accent);"),
			msg: "expected mobile mark-read icon button to have a visible keyboard focus state",
		},
		{
			ok:  strings.Contains(source, "@media (prefers-reduced-motion: reduce)"),
			msg: "expected pending pulse animation to respect reduced motion",
		},
	}

	for _, check := range checks {
		if !check.ok {
			t.Fatal(check.msg)
		}
	}
}
