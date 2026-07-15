package main

import (
	"os"
	"strings"
	"testing"
)

func TestResponsiveMobileBootstrapContracts(t *testing.T) {
	t.Parallel()

	domSource, err := os.ReadFile("static/app/dom.js")
	if err != nil {
		t.Fatalf("read DOM helpers: %v", err)
	}

	mobileSource, err := os.ReadFile("static/app/mobile.js")
	if err != nil {
		t.Fatalf("read mobile bootstrap: %v", err)
	}

	requiredDOMContracts := []string{
		`mobileLayoutQuery = "(max-width: 960px)"`,
		`window.matchMedia(mobileLayoutQuery)`,
	}
	requiredMobileContracts := []string{
		`mobileLayoutQuery } from "./dom.js"`,
		`const desktopReaderPath = "/"`,
		`document.getElementById("mobile-stream-feed-filter")`,
		`layoutMedia.addEventListener("change", syncResponsiveLayout)`,
		`pendingTransition`,
		`abortRequest(pendingTransition.mainContent)`,
		`document.body.addEventListener("htmx:beforeRequest", trackMainContentRequest)`,
		`document.body.addEventListener("htmx:historyRestore", syncResponsiveLayout)`,
	}

	assertSourceContracts(t, "DOM helpers", string(domSource), requiredDOMContracts)
	assertSourceContracts(t, "mobile bootstrap", string(mobileSource), requiredMobileContracts)
}

func assertSourceContracts(t *testing.T, label, source string, contracts []string) {
	t.Helper()

	for _, contract := range contracts {
		if !strings.Contains(source, contract) {
			t.Errorf("expected %s to contain %q", label, contract)
		}
	}
}
