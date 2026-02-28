package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readAppJSSource(t *testing.T) string {
	t.Helper()

	paths := []string{
		filepath.Join("static", "app.js"),
	}

	entries, err := os.ReadDir(filepath.Join("static", "app"))
	if err != nil {
		t.Fatalf("read static/app: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".js" {
			continue
		}
		paths = append(paths, filepath.Join("static", "app", entry.Name()))
	}

	var builder strings.Builder
	for _, path := range paths {
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		builder.Write(source)
		builder.WriteByte('\n')
	}

	return builder.String()
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
		`const contentPanelFloatingClass = "is-content-panel-floating";`,
		"expected content panel floating class constant",
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
		"setContentPanelFloating(!isContentPanelFloating());",
		"expected floating-panel click handler toggle logic",
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
		"const deferFocusContentPanel = (remainingAttempts = 24) => {",
		"expected deferred content-panel focus helper for swap timing",
	)
	assertSourceContains(
		t,
		source,
		"deferFocusContentPanel(24);",
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

func TestAppJSFeedSelectionAutoLoadsItems(t *testing.T) {
	source := readAppJSSource(t)

	assertSourceContains(
		t,
		source,
		"const requestFeedItems = (feedButton, pendingPanelFocus) => {",
		"expected helper for requesting feed items from selected feed buttons",
	)
	assertSourceContains(
		t,
		source,
		"const getDisplayedFeedID = () => {",
		"expected helper for reading the feed currently shown in the item list",
	)
	assertSourceContains(
		t,
		source,
		"if (!getItemList()) {",
		"expected feed-panel focus logic to detect missing item list",
	)
	assertSourceContains(
		t,
		source,
		"requestFeedItems(selectedFeed, \"feed\");",
		"expected selected feed to auto-load items when main list is empty",
	)
	assertSourceContains(
		t,
		source,
		"const selectionChanged = next !== current;",
		"expected keyboard feed movement to detect feed selection changes",
	)
	assertSourceContains(
		t,
		source,
		"requestFeedItems(next, \"feed\");",
		"expected keyboard feed selection to auto-load items for the new feed",
	)
	assertSourceContains(
		t,
		source,
		"if (getItemList() && selectedFeedID && getDisplayedFeedID() === selectedFeedID) {",
		"expected open-selected-feed path to skip reload when selected feed is already displayed",
	)
	assertSourceContains(
		t,
		source,
		"focusItemList();",
		"expected same-feed open action to move focus to items while preserving selection",
	)
	assertSourceContains(
		t,
		source,
		"return requestFeedItems(selectedFeed, \"items\");",
		"expected explicit feed open action to continue loading selected feed items",
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
		"const nextUnreadRow = (row, options = {}) => {",
		"expected unread-row lookup helper for read shortcut advancement",
	)
	assertSourceContains(
		t,
		source,
		"const getReadingModalRow = () => {",
		"expected reading-modal row resolver for floating panel shortcuts",
	)
	assertSourceContains(
		t,
		source,
		"const handleReadingModalReadShortcut = () => {",
		"expected reading-modal read shortcut handler",
	)
	assertSourceContains(
		t,
		source,
		"const requestExpandRow = (row, options = {}) => {",
		"expected direct item-expand helper for keyboard modal navigation",
	)
	assertSourceContains(
		t,
		source,
		"htmx.ajax(\"GET\", `/items/${itemID}`",
		"expected modal advance to expand next item without synthetic click events",
	)
	assertSourceContains(
		t,
		source,
		`return openRowInReadingModal(next, { focusPanel: "content" });`,
		"expected pending read shortcut to open the next item via modal helper",
	)
	assertSourceContains(
		t,
		source,
		"const nextUnread = nextUnreadRow(current, { requireContent: true });",
		"expected reading-modal shortcut to target next unread item with content",
	)
	assertSourceContains(
		t,
		source,
		"keepFloating: Boolean(nextUnread && isContentPanelFloating()),",
		"expected modal read-advance state to preserve floating mode",
	)
	assertSourceContains(
		t,
		source,
		"setContentPanelFloating(true);",
		"expected pending read-advance to restore floating mode before opening next item",
	)
	assertSourceContains(
		t,
		source,
		"if (handleReadingModalReadShortcut()) {",
		"expected global read shortcut to delegate to reading-modal behavior first",
	)
	assertSourceContains(
		t,
		source,
		"toggleRead();",
		"expected read shortcut key binding to remain wired",
	)
}
