package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"html/template"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"text/template/parse"
)

func filesWithExtensions(t *testing.T, root string, extensions ...string) []string {
	t.Helper()

	wanted := make(map[string]struct{}, len(extensions))
	for _, extension := range extensions {
		wanted[extension] = struct{}{}
	}

	paths := make([]string, 0)

	err := filepath.WalkDir(root, matchingFileCollector(&paths, wanted))
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	slices.Sort(paths)

	return paths
}

func matchingFileCollector(paths *[]string, wanted map[string]struct{}) fs.WalkDirFunc {
	return func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if !entry.IsDir() && hasWantedExtension(path, wanted) {
			*paths = append(*paths, path)
		}

		return nil
	}
}

func hasWantedExtension(path string, wanted map[string]struct{}) bool {
	_, exists := wanted[filepath.Ext(path)]

	return exists
}

func TestTemplateDefinitionsAreReachable(t *testing.T) {
	t.Parallel()

	tmpl, err := template.ParseFS(templateFiles, "templates/*.html", "templates/partials/*.html")
	if err != nil {
		t.Fatalf("parse embedded templates: %v", err)
	}

	definitionFiles := templateDefinitionFiles(t, tmpl)
	reachable := reachableTemplateDefinitions(t, tmpl, renderedTemplateRoots(t))
	unreachable := unreachableTemplateDefinitions(definitionFiles, reachable)

	if len(unreachable) > 0 {
		t.Fatalf("template definitions unreachable from literal Go render roots: %s", strings.Join(unreachable, ", "))
	}
}

func templateDefinitionFiles(t *testing.T, tmpl *template.Template) map[string]string {
	t.Helper()

	implicitWrappers := implicitTemplateWrappers(t)
	definitions := make(map[string]string)

	for _, definition := range tmpl.Templates() {
		if isImplicitEmptyTemplate(definition, implicitWrappers) {
			continue
		}

		definitions[definition.Name()] = definition.Tree.ParseName
	}

	return definitions
}

func implicitTemplateWrappers(t *testing.T) map[string]struct{} {
	t.Helper()

	wrappers := make(map[string]struct{})
	for _, path := range filesWithExtensions(t, "templates", ".html") {
		wrappers[filepath.Base(path)] = struct{}{}
	}

	return wrappers
}

func isImplicitEmptyTemplate(definition *template.Template, wrappers map[string]struct{}) bool {
	if definition.Tree == nil || definition.Tree.Root == nil {
		return true
	}

	_, wrapper := wrappers[definition.Name()]

	return wrapper && strings.TrimSpace(definition.Tree.Root.String()) == ""
}

func reachableTemplateDefinitions(
	t *testing.T,
	tmpl *template.Template,
	roots []string,
) map[string]struct{} {
	t.Helper()

	reachable := make(map[string]struct{})

	queue := append([]string(nil), roots...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]

		references, visited := visitTemplateDefinition(t, tmpl, name, reachable)
		if !visited {
			continue
		}

		for reference := range references {
			queue = append(queue, reference)
		}
	}

	return reachable
}

func visitTemplateDefinition(
	t *testing.T,
	tmpl *template.Template,
	name string,
	reachable map[string]struct{},
) (map[string]struct{}, bool) {
	t.Helper()

	if _, seen := reachable[name]; seen {
		return nil, false
	}

	definition := tmpl.Lookup(name)
	if definition == nil || definition.Tree == nil {
		t.Fatalf("rendered or referenced template %q has no definition", name)
	}

	reachable[name] = struct{}{}

	references := make(map[string]struct{})
	collectTemplateReferences(definition.Tree.Root, references)

	return references, true
}

func unreachableTemplateDefinitions(
	definitionFiles map[string]string,
	reachable map[string]struct{},
) []string {
	unreachable := make([]string, 0)

	for name, path := range definitionFiles {
		if _, exists := reachable[name]; !exists {
			unreachable = append(unreachable, name+" ("+filepath.ToSlash(path)+")")
		}
	}

	slices.Sort(unreachable)

	return unreachable
}

func renderedTemplateRoots(t *testing.T) []string {
	t.Helper()

	seen := make(map[string]struct{})

	for _, path := range filesWithExtensions(t, filepath.Join("internal", "server"), ".go") {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}

		for _, name := range literalRenderedTemplates(t, path) {
			seen[name] = struct{}{}
		}
	}

	roots := make([]string, 0, len(seen))
	for name := range seen {
		roots = append(roots, name)
	}

	slices.Sort(roots)

	if len(roots) == 0 {
		t.Fatal("found no literal template render roots in internal/server")
	}

	return roots
}

func literalRenderedTemplates(t *testing.T, path string) []string {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	names := make([]string, 0)

	ast.Inspect(parsed, func(node ast.Node) bool {
		name, ok := literalTemplateRenderName(node)
		if ok {
			names = append(names, name)
		}

		return true
	})

	return names
}

func literalTemplateRenderName(node ast.Node) (string, bool) {
	call, ok := node.(*ast.CallExpr)
	if !ok || len(call.Args) < 2 || !isTemplateRenderCall(call.Fun) {
		return "", false
	}

	nameLiteral, ok := call.Args[1].(*ast.BasicLit)
	if !ok || nameLiteral.Kind != token.STRING {
		return "", false
	}

	name, err := strconv.Unquote(nameLiteral.Value)

	return name, err == nil
}

func isTemplateRenderCall(function ast.Expr) bool {
	switch current := function.(type) {
	case *ast.Ident:
		return current.Name == "renderTemplate" || current.Name == "ExecuteTemplate"
	case *ast.SelectorExpr:
		return current.Sel.Name == "renderTemplate" || current.Sel.Name == "ExecuteTemplate"
	default:
		return false
	}
}

func collectTemplateReferences(node parse.Node, references map[string]struct{}) {
	switch current := node.(type) {
	case *parse.ListNode:
		collectTemplateListReferences(current, references)
	case *parse.TemplateNode:
		collectTemplateNodeReference(current, references)
	case *parse.IfNode:
		collectIfTemplateReferences(current, references)
	case *parse.RangeNode:
		collectRangeTemplateReferences(current, references)
	case *parse.WithNode:
		collectWithTemplateReferences(current, references)
	default:
		return
	}
}

func collectTemplateListReferences(node *parse.ListNode, references map[string]struct{}) {
	if node == nil {
		return
	}

	for _, child := range node.Nodes {
		collectTemplateReferences(child, references)
	}
}

func collectTemplateNodeReference(node *parse.TemplateNode, references map[string]struct{}) {
	if node != nil {
		references[node.Name] = struct{}{}
	}
}

func collectIfTemplateReferences(node *parse.IfNode, references map[string]struct{}) {
	if node != nil {
		collectTemplateBranches(node.List, node.ElseList, references)
	}
}

func collectRangeTemplateReferences(node *parse.RangeNode, references map[string]struct{}) {
	if node != nil {
		collectTemplateBranches(node.List, node.ElseList, references)
	}
}

func collectWithTemplateReferences(node *parse.WithNode, references map[string]struct{}) {
	if node != nil {
		collectTemplateBranches(node.List, node.ElseList, references)
	}
}

func collectTemplateBranches(
	list *parse.ListNode,
	elseList *parse.ListNode,
	references map[string]struct{},
) {
	collectTemplateReferences(list, references)
	collectTemplateReferences(elseList, references)
}

func TestFrontendImageAssetsAreReferenced(t *testing.T) {
	t.Parallel()

	sources := applicationAssetReferenceSources(t)
	unreferenced := make([]string, 0)

	for _, path := range frontendImageAssetPaths(t) {
		if !sourcesReferenceAsset(sources, path) {
			unreferenced = append(unreferenced, filepath.ToSlash(path))
		}
	}

	if len(unreferenced) > 0 {
		t.Fatalf("image/icon assets with no application reference: %s", strings.Join(unreferenced, ", "))
	}
}

func applicationAssetReferenceSources(t *testing.T) []string {
	t.Helper()

	paths := append(
		filesWithExtensions(t, "templates", ".html"),
		filesWithExtensions(t, "static", ".css", ".js")...,
	)
	paths = append(paths, productionGoFiles(t)...)

	sources := make([]string, 0, len(paths))
	for _, path := range paths {
		sources = append(sources, readTextFile(t, path))
	}

	return sources
}

func frontendImageAssetPaths(t *testing.T) []string {
	t.Helper()

	return filesWithExtensions(t, "static", ".avif", ".gif", ".ico", ".png", ".svg", ".webp")
}

func sourcesReferenceAsset(sources []string, path string) bool {
	patterns := assetReferencePatterns(path)
	for _, source := range sources {
		for _, pattern := range patterns {
			if pattern.MatchString(source) {
				return true
			}
		}
	}

	return false
}

func assetReferencePatterns(path string) []*regexp.Regexp {
	slashPath := filepath.ToSlash(path)
	candidates := []string{"/" + slashPath, strings.TrimPrefix(slashPath, "static/")}

	patterns := make([]*regexp.Regexp, 0, len(candidates))
	for _, candidate := range candidates {
		bounded := "(?m)(^|[\\\"'\\x60(=])" + regexp.QuoteMeta(candidate) + "($|[\\\"'\\x60)\\s?#>])"
		patterns = append(patterns, regexp.MustCompile(bounded))
	}

	return patterns
}

func productionGoFiles(t *testing.T) []string {
	t.Helper()

	paths := filesWithExtensions(t, "internal", ".go")
	paths = append(paths, "main.go")

	production := paths[:0]
	for _, path := range paths {
		if !strings.HasSuffix(path, "_test.go") {
			production = append(production, path)
		}
	}

	return production
}
