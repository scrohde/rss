package main

import (
	"os"
	"path/filepath"
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

func cssBlockContains(source, selector, token string) bool {
	selectorIndex := strings.Index(source, selector)
	if selectorIndex == -1 {
		return false
	}

	blockStart := strings.Index(source[selectorIndex:], "{")
	if blockStart == -1 {
		return false
	}

	blockStart += selectorIndex

	blockEnd := strings.Index(source[blockStart:], "}")
	if blockEnd == -1 {
		return false
	}

	blockEnd += blockStart

	return strings.Contains(source[blockStart:blockEnd], token)
}

func TestStylesTopbarMatchesFeedPanelBackground(t *testing.T) {
	t.Parallel()

	source := readStylesCSS(t)

	if !cssBlockContains(source, ".topbar {", "background: var(--sidebar);") {
		t.Fatal("expected topbar background to use --sidebar")
	}

	if !cssBlockContains(source, ".feed-panel {", "background: var(--sidebar);") {
		t.Fatal("expected feed panel background to use --sidebar")
	}
}
