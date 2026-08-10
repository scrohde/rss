package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func readTextFile(t *testing.T, path string) string {
	t.Helper()

	// #nosec G304 -- Test paths come only from repository-controlled fixtures.
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(source)
}

func appScriptPaths(t *testing.T) []string {
	t.Helper()

	modules := filesWithExtensions(t, filepath.Join("static", "app"), ".js")
	paths := make([]string, 1, len(modules)+1)
	paths[0] = filepath.Join("static", "app.js")
	paths = append(paths, modules...)
	slices.Sort(paths)

	return paths
}

var (
	importFromPattern       = regexp.MustCompile(`from\s+["']([^"']+)["']`)
	importSideEffectPattern = regexp.MustCompile(`(?m)^\s*import\s+["']([^"']+)["']`)
	importCallPattern       = regexp.MustCompile(`\bimport\s*\(\s*["']([^"']+)["']\s*\)`)
)

func importSpecifiers(source string) []string {
	seen := map[string]struct{}{}
	for _, match := range importFromPattern.FindAllStringSubmatch(source, -1) {
		seen[match[1]] = struct{}{}
	}

	for _, match := range importSideEffectPattern.FindAllStringSubmatch(source, -1) {
		seen[match[1]] = struct{}{}
	}

	for _, match := range importCallPattern.FindAllStringSubmatch(source, -1) {
		seen[match[1]] = struct{}{}
	}

	specifiers := make([]string, 0, len(seen))
	for specifier := range seen {
		specifiers = append(specifiers, specifier)
	}

	slices.Sort(specifiers)

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

func TestFrontendJSEntrypointImportsMainModule(t *testing.T) {
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
		assertScriptImportsResolve(t, staticRoot, scriptPath)
	}
}

func assertScriptImportsResolve(t *testing.T, staticRoot, scriptPath string) {
	t.Helper()

	for _, specifier := range importSpecifiers(readTextFile(t, scriptPath)) {
		assertRelativeImportResolves(t, staticRoot, scriptPath, specifier)
	}
}

func assertRelativeImportResolves(t *testing.T, staticRoot, scriptPath, specifier string) {
	t.Helper()

	resolved, relative := relativeImportPath(scriptPath, specifier)
	if !relative {
		return
	}

	resolvedAbs, err := filepath.Abs(resolved)
	if err != nil {
		t.Fatalf("resolve %s import %s: %v", scriptPath, specifier, err)
	}

	if !pathWithinRoot(resolvedAbs, staticRoot) {
		t.Fatalf("import %q in %s escapes static directory (%s)", specifier, scriptPath, resolvedAbs)
	}

	info, err := os.Stat(resolvedAbs)
	if err != nil {
		t.Fatalf("import %q in %s does not resolve to a file: %v", specifier, scriptPath, err)
	}

	if info.IsDir() {
		t.Fatalf("import %q in %s resolved to directory %s", specifier, scriptPath, resolvedAbs)
	}
}

func relativeImportPath(scriptPath, specifier string) (string, bool) {
	if !strings.HasPrefix(specifier, ".") {
		return "", false
	}

	resolved := filepath.Clean(filepath.Join(filepath.Dir(scriptPath), filepath.FromSlash(specifier)))
	if filepath.Ext(resolved) == "" {
		resolved += ".js"
	}

	return resolved, true
}

func pathWithinRoot(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

func TestAppJSModulesAreReachableFromEntrypoint(t *testing.T) {
	t.Parallel()

	paths := appScriptPaths(t)
	entrypoint := filepath.Join("static", "app.js")
	known := scriptPathSet(paths)
	reachable := reachableAppScripts(t, known, entrypoint)
	unreachable := unreachableAppScripts(paths, reachable)

	if len(unreachable) > 0 {
		t.Fatalf("ES modules unreachable from static/app.js: %s", strings.Join(unreachable, ", "))
	}
}

func scriptPathSet(paths []string) map[string]struct{} {
	set := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		set[path] = struct{}{}
	}

	return set
}

func reachableAppScripts(t *testing.T, known map[string]struct{}, entrypoint string) map[string]struct{} {
	t.Helper()

	reachable := map[string]struct{}{entrypoint: {}}

	queue := []string{entrypoint}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]

		for _, imported := range knownRelativeImports(t, known, path) {
			if _, seen := reachable[imported]; seen {
				continue
			}

			reachable[imported] = struct{}{}
			queue = append(queue, imported)
		}
	}

	return reachable
}

func knownRelativeImports(t *testing.T, known map[string]struct{}, scriptPath string) []string {
	t.Helper()

	imports := make([]string, 0)

	for _, specifier := range importSpecifiers(readTextFile(t, scriptPath)) {
		resolved, relative := relativeImportPath(scriptPath, specifier)
		if !relative {
			continue
		}

		if _, exists := known[resolved]; exists {
			imports = append(imports, resolved)
		}
	}

	return imports
}

func unreachableAppScripts(paths []string, reachable map[string]struct{}) []string {
	unreachable := make([]string, 0)

	for _, path := range paths {
		if _, exists := reachable[path]; !exists {
			unreachable = append(unreachable, filepath.ToSlash(path))
		}
	}

	return unreachable
}
