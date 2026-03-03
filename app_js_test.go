package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func readTextFile(t *testing.T, path string) string {
	t.Helper()

	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(source)
}

func appScriptPaths(t *testing.T) []string {
	t.Helper()

	paths := []string{filepath.Join("static", "app.js")}
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
	sort.Strings(paths)
	return paths
}

func templatePaths(t *testing.T) []string {
	t.Helper()

	var paths []string
	err := filepath.WalkDir("templates", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".html" {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
	sort.Strings(paths)
	return paths
}

func joinFiles(t *testing.T, paths []string) string {
	t.Helper()

	var builder strings.Builder
	for _, path := range paths {
		builder.WriteString(readTextFile(t, path))
		builder.WriteByte('\n')
	}
	return builder.String()
}

var importFromPattern = regexp.MustCompile(`from\s+["']([^"']+)["']`)
var importSideEffectPattern = regexp.MustCompile(`(?m)^\s*import\s+["']([^"']+)["']`)

func importSpecifiers(source string) []string {
	seen := map[string]struct{}{}
	for _, match := range importFromPattern.FindAllStringSubmatch(source, -1) {
		seen[match[1]] = struct{}{}
	}
	for _, match := range importSideEffectPattern.FindAllStringSubmatch(source, -1) {
		seen[match[1]] = struct{}{}
	}

	specifiers := make([]string, 0, len(seen))
	for specifier := range seen {
		specifiers = append(specifiers, specifier)
	}
	sort.Strings(specifiers)
	return specifiers
}

func TestLayoutTemplateIncludesFrontendScriptEntrypoints(t *testing.T) {
	t.Parallel()

	source := readTextFile(t, filepath.Join("templates", "layout.html"))

	if strings.Count(source, `src="/static/vendor/htmx.min.js"`) != 1 {
		t.Fatal("expected layout template to include htmx script exactly once")
	}

	if strings.Count(source, `type="module" src="/static/app.js"`) != 1 {
		t.Fatal("expected layout template to include module /static/app.js exactly once")
	}
}

func TestAppJSEntrypointImportsMainModule(t *testing.T) {
	t.Parallel()

	specifiers := importSpecifiers(readTextFile(t, filepath.Join("static", "app.js")))
	if len(specifiers) != 1 || specifiers[0] != "./app/main.js" {
		t.Fatalf("expected static/app.js to import only ./app/main.js, got %v", specifiers)
	}
}

func TestAppJSRelativeImportsResolveToExistingFiles(t *testing.T) {
	t.Parallel()

	staticRoot, err := filepath.Abs("static")
	if err != nil {
		t.Fatalf("resolve static root: %v", err)
	}

	for _, scriptPath := range appScriptPaths(t) {
		source := readTextFile(t, scriptPath)
		for _, specifier := range importSpecifiers(source) {
			if !strings.HasPrefix(specifier, ".") {
				continue
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(scriptPath), filepath.FromSlash(specifier)))
			if filepath.Ext(resolved) == "" {
				resolved += ".js"
			}
			resolvedAbs, absErr := filepath.Abs(resolved)
			if absErr != nil {
				t.Fatalf("resolve %s import %s: %v", scriptPath, specifier, absErr)
			}
			if resolvedAbs != staticRoot &&
				!strings.HasPrefix(resolvedAbs, staticRoot+string(filepath.Separator)) {
				t.Fatalf("import %q in %s escapes static directory (%s)", specifier, scriptPath, resolvedAbs)
			}

			info, statErr := os.Stat(resolvedAbs)
			if statErr != nil {
				t.Fatalf("import %q in %s does not resolve to a file: %v", specifier, scriptPath, statErr)
			}
			if info.IsDir() {
				t.Fatalf("import %q in %s resolved to directory %s", specifier, scriptPath, resolvedAbs)
			}
		}
	}
}

func TestAppJSSelectorContractsMatchTemplates(t *testing.T) {
	t.Parallel()

	appSource := joinFiles(t, appScriptPaths(t))
	templateSource := joinFiles(t, templatePaths(t))

	contracts := []struct {
		name          string
		jsSelector    string
		templateToken string
	}{
		{
			name:          "subscribe form",
			jsSelector:    `form[data-subscribe-form='true']`,
			templateToken: `data-subscribe-form="true"`,
		},
		{
			name:          "opml import button",
			jsSelector:    `button[data-import-button='true']`,
			templateToken: `data-import-button="true"`,
		},
		{
			name:          "opml import file input",
			jsSelector:    `input[data-import-file-input='true']`,
			templateToken: `data-import-file-input="true"`,
		},
		{
			name:          "content panel float toggle",
			jsSelector:    `button[data-content-panel-full-toggle='true']`,
			templateToken: `data-content-panel-full-toggle="true"`,
		},
		{
			name:          "content panel close button",
			jsSelector:    `button[data-content-panel-close='true']`,
			templateToken: `data-content-panel-close="true"`,
		},
		{
			name:          "main content panel anchor",
			jsSelector:    "#main-content",
			templateToken: `id="main-content"`,
		},
		{
			name:          "feed list anchor",
			jsSelector:    "#feed-list",
			templateToken: `id="feed-list"`,
		},
	}

	for _, contract := range contracts {
		if !strings.Contains(appSource, contract.jsSelector) {
			t.Fatalf("expected app JS to keep %s selector %q", contract.name, contract.jsSelector)
		}
		if !strings.Contains(templateSource, contract.templateToken) {
			t.Fatalf("expected templates to keep %s token %q", contract.name, contract.templateToken)
		}
	}
}
