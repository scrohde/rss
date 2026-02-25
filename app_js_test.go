package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readAppJSSource(t *testing.T) string {
	t.Helper()

	source, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}

	return string(source)
}

func assertSourceContains(t *testing.T, source, token, message string) {
	t.Helper()

	if !strings.Contains(source, token) {
		t.Fatal(message)
	}
}

func TestAppJSIncludesContentPanelControlHandlers(t *testing.T) {
	source := readAppJSSource(t)

	assertSourceContains(
		t,
		source,
		`const contentPanelFullPageClass = "is-content-panel-fullpage";`,
		"expected content panel full-page class constant",
	)
	assertSourceContains(
		t,
		source,
		`button[data-content-panel-full-toggle='true']`,
		"expected full-page toggle selector binding",
	)
	assertSourceContains(
		t,
		source,
		`button[data-content-panel-close='true']`,
		"expected close button selector binding",
	)
	assertSourceContains(
		t,
		source,
		"setContentPanelFullPage(!isContentPanelFullPage());",
		"expected full-page click handler toggle logic",
	)
	assertSourceContains(
		t,
		source,
		"syncContentPanelMode();",
		"expected content panel mode sync on render lifecycle",
	)
}

func TestAppJSKeyboardNavigationSupportsPanelFocusModel(t *testing.T) {
	source := readAppJSSource(t)

	assertSourceContains(
		t,
		source,
		`panelFocus: "items",`,
		"expected panel focus state default",
	)
	assertSourceContains(
		t,
		source,
		"const moveWithinFocusedPanel = (delta, panel) => {",
		"expected panel-aware vertical keyboard helper",
	)
	assertSourceContains(
		t,
		source,
		`const desktopPanelNavigationEnabled = isDesktopLayout() && !isFeedEditMode();`,
		"expected desktop-only panel navigation guard",
	)
	assertSourceContains(
		t,
		source,
		"openSelectedFeed();",
		"expected right-key feed-panel activation branch",
	)
	assertSourceContains(
		t,
		source,
		"expandActiveToContentPanel();",
		"expected right-key item-panel expansion branch",
	)
	assertSourceContains(
		t,
		source,
		"collapseContentPanelToItems();",
		"expected left-key content-panel collapse branch",
	)
	assertSourceContains(
		t,
		source,
		"focusFeedPanel();",
		"expected left-key item-panel focus-to-feed behavior",
	)
	assertSourceContains(
		t,
		source,
		"state.pendingPanelFocus = \"items\";",
		"expected pending item-panel focus tracking after feed activation",
	)
	assertSourceContains(
		t,
		source,
		"state.pendingPanelFocus = \"content\";",
		"expected pending content-panel focus tracking after expansion",
	)
	assertSourceContains(
		t,
		source,
		"const deferFocusContentPanel = (remainingAttempts = 3) => {",
		"expected deferred content-panel focus helper for swap timing",
	)
	assertSourceContains(
		t,
		source,
		"deferFocusContentPanel();",
		"expected focus handoff retry when content panel is not ready on first swap",
	)
	assertSourceContains(
		t,
		source,
		"} else if (getFeedLinks({ visibleOnly: true }).length) {",
		"expected feed-focus fallback when item list is absent on startup",
	)
	assertSourceContains(
		t,
		source,
		"focusFeedPanel();",
		"expected feed panel focus handoff when item list is absent after swaps",
	)
}

func TestAppJSKeyboardShortcutsPreserveReadOpenAndContentScroll(t *testing.T) {
	source := readAppJSSource(t)

	assertSourceContains(
		t,
		source,
		"const expandedPanelScrollStep = 72;",
		"expected expanded panel scroll step constant",
	)
	assertSourceContains(
		t,
		source,
		"if (panel === \"content\") {",
		"expected content-panel branch in panel-aware vertical movement",
	)
	assertSourceContains(
		t,
		source,
		"return scrollExpandedPanel(delta * expandedPanelScrollStep);",
		"expected content-panel vertical keys to scroll expanded panel",
	)
	assertSourceContains(
		t,
		source,
		"openActiveLink();",
		"expected open-article shortcut to remain available",
	)
	assertSourceContains(
		t,
		source,
		"toggleRead();",
		"expected read-toggle shortcut to remain available",
	)
}
