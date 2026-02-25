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

func TestAppJSArrowKeysSupportExpandedPanelScrolling(t *testing.T) {
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
		"const isExpandedContextTarget = (target) => {",
		"expected expanded context target helper",
	)
	assertSourceContains(
		t,
		source,
		"hasExpandedItem() &&",
		"expected expanded-item keyboard branch guard",
	)
	assertSourceContains(
		t,
		source,
		"scrollExpandedPanel(expandedPanelScrollStep);",
		"expected ArrowDown expanded panel scrolling branch",
	)
	assertSourceContains(
		t,
		source,
		"scrollExpandedPanel(-expandedPanelScrollStep);",
		"expected ArrowUp expanded panel scrolling branch",
	)
}
