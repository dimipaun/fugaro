package config

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestRoutingRefusesAClaudeIDTheMapDoesNotMap: the switch names a Claude ID the
// checked-in canonical-to-route map carries as explicitly unmapped (no measured
// route ID), and a route entry claims a spelling of it. The switch is refused
// loudly, with the map's own reason, and does not resolve: guessing the route's
// ID (or sending the canonical one on a route) is the failure this refuses.
//
// Hand mutation this fails on: dropping the map's unmapped check, so the switch
// resolves to a spelling nothing measured.
func TestRoutingRefusesAClaudeIDTheMapDoesNotMap(t *testing.T) {
	providers := map[string]ModelProvider{"openrouter": {
		Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Secret: "openrouter-api-key", Models: []string{"anthropic/claude-sonnet-5"}, AllowDataTo: []string{"*"},
	}}
	sw := &RoutingClaude{Via: RouteOpenRouter, Models: []string{"claude-sonnet-5"}}
	ps := CheckRouting(&Routing{Claude: sw}, providers, "routing")
	if len(ps) == 0 {
		t.Fatal("a Claude ID the map does not map was accepted by the switch")
	}
	got := problemsText(ps)
	if !strings.Contains(got, "no route's own ID has been measured") {
		t.Errorf("the refusal does not carry the map's reason: %s", got)
	}
	if route, sent, ok := RoutingClaudeFor(sw, "claude-sonnet-5", providers); ok {
		t.Errorf("RoutingClaudeFor resolved %s to (%q, %q): an unmapped ID must not resolve", "claude-sonnet-5", route, sent)
	}
}

// TestRoutingRefusesAClaimThatIsNotTheMapsPair: the route entry claims
// anthropic/claude-sonnet-5-5 — a spelling the old comparison folded into the
// canonical ID, which is exactly the guess Task 1b.2 removes. The switch names
// claude-sonnet-5-5 and the route claims the near miss, so there is no pair in
// the map and the switch is refused rather than resolved to a route entry that
// spells the model its own way.
//
// Hand mutation this fails on: resolving by the case/separator fold instead of
// the map (the pre-1b.2 sameClaudeID), which routes the call and prices it at
// whatever the wrong key is priced at.
func TestRoutingRefusesAClaimThatIsNotTheMapsPair(t *testing.T) {
	providers := map[string]ModelProvider{"openrouter": {
		Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Secret: "openrouter-api-key", Models: []string{"anthropic/claude-sonnet-5-5"}, AllowDataTo: []string{"*"},
	}}
	sw := &RoutingClaude{Via: RouteOpenRouter, Models: []string{"claude-sonnet-5-5"}}
	if ps := CheckRouting(&Routing{Claude: sw}, providers, "routing"); len(ps) == 0 {
		t.Error("a claim that is not the map's pair was accepted: the route's ID must be the one the map holds")
	}
	if route, sent, ok := RoutingClaudeFor(sw, "claude-sonnet-5-5", providers); ok {
		t.Errorf("RoutingClaudeFor resolved the near miss %q to (%q, %q), want no resolution", "anthropic/claude-sonnet-5-5", route, sent)
	}
}

// TestRoutingResolutionHasOneSite is the real half of 1b.1's
// TestRoutingHasOneResolver: the route a Claude call takes is answered in one
// place — internal/config's resolver, over internal/pricing's checked-in map —
// and every consumer asks it. The pins renderer, the price lookup and the run's
// own config do not read the map: the run rewrites its pins through the
// resolver once (internal/runner/routing.go), so a second site is a second
// answer to where a call goes and what it costs.
//
// The map's *inverse* is the one other reading, and it is narrower: an
// allow-list admits a pin the switch routed by the canonical ID of the same
// model (config.CheckAllowed, through the `allowed` helper). That question can
// only name a model the list already names — it cannot produce an ID to send —
// so it is allowed from pins.go alone, while the question "which route?" stays
// where it is.
//
// The call is resolved through the file's imports, not through the text
// "pricing.": an aliased import (pr "github.com/dimipaun/fugaro/internal/pricing")
// and a dot import are the same call, and the text search this test replaces
// stayed green under both (the review finding on this change).
//
// Hand mutation this fails on: reading the map from a second package (the
// runner's own stagePins, say) because it already knows the pins; or answering
// "which route" from pins.go as well.
func TestRoutingResolutionHasOneSite(t *testing.T) {
	const pricingPath = "github.com/dimipaun/fugaro/internal/pricing"
	// Which functions each file may call, and why. The route-answering pair is
	// the resolver's; CanonicalFor, which cannot answer a route, is also read
	// where the allow-list is judged.
	allowed := map[string]map[string]bool{
		"RouteIDFor":     {"internal/config/routing.go": true},
		"UnmappedReason": {"internal/config/routing.go": true},
		"CanonicalFor":   {"internal/config/routing.go": true, "internal/config/pins.go": true},
	}
	// The raw table is not a question a consumer asks, and it is unexported so
	// that it cannot be: this is the reminder for the day someone exports it
	// again, since a reader of the data can re-answer the question its own way.
	raw := map[string]bool{"ClaudeRoutes": true, "ClaudeRoute": true}
	found := map[string]bool{}
	for _, f := range moduleGoFiles(t) {
		for _, name := range pricingNames(f, pricingPath) {
			switch {
			case raw[name]:
				found[f.path+" reads pricing."+name+" (the raw map)"] = true
			case allowed[name] != nil && !allowed[name][f.path]:
				found[f.path+" calls pricing."+name] = true
			}
		}
	}
	if len(found) != 0 {
		t.Errorf("the canonical-to-route map is read where it should not be: %v — want internal/config/routing.go (the resolver) and the allow-list's inverse question in internal/config/pins.go", slices.Sorted(maps.Keys(found)))
	}
}

// goSource is one non-test Go file of the module, parsed: the file's syntax
// tree and the local names its imports are bound to, so a call is resolved by
// the import it came from rather than by the text a caller happens to have
// written it with.
type goSource struct {
	path    string // module-relative and cleaned: internal/config/routing.go
	file    *ast.File
	imports map[string]string // local name -> import path; "." for a dot import
}

// moduleGoFiles parses every non-test Go file of the module, in path order.
// Test files are excluded: a test spells a route's ID out on purpose — that is
// what makes an independent fixture independent — so a rule about where the
// *code* writes a spelling must not judge them.
func moduleGoFiles(t *testing.T) []goSource {
	t.Helper()
	root := filepath.Join("..", "..") // internal/config -> the module root
	var out []goSource
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "node_modules", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.ToSlash(rel), src, 0)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", rel, err)
		}
		out = append(out, goSource{path: filepath.ToSlash(rel), file: f, imports: importedNames(f)})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no Go file was read: the walk is looking in the wrong place")
	}
	slices.SortFunc(out, func(a, b goSource) int { return strings.Compare(a.path, b.path) })
	return out
}

// importedNames maps each local name in a file to the path it was imported
// from. A renamed import is the same import under another name, and a dot
// import is the same import spelled without a qualifier; "_" is dropped, since
// nothing can be called through it.
func importedNames(f *ast.File) map[string]string {
	m := map[string]string{}
	for _, is := range f.Imports {
		path, err := strconv.Unquote(is.Path.Value)
		if err != nil {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if is.Name != nil {
			name = is.Name.Name
		}
		if name == "_" {
			continue
		}
		m[name] = path
	}
	return m
}

// pricingNames returns the names of the package at pkgPath that a file refers
// to: pkg.Name for an import bound to whichever name, and a bare Name when the
// file dot-imports it. The caller's allow-list is by file, so the *name* is
// what is asked of it.
func pricingNames(f goSource, pkgPath string) []string {
	dot := f.imports["."] == pkgPath
	seen := map[string]bool{}
	ast.Inspect(f.file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := v.X.(*ast.Ident); ok && f.imports[id.Name] == pkgPath {
				seen[v.Sel.Name] = true
			}
		case *ast.Ident:
			if dot {
				seen[v.Name] = true
			}
		}
		return true
	})
	return slices.Sorted(maps.Keys(seen))
}
