package runner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The run takes its budget token once, and this is what holds it there
// (finding 2): the take is read-only since the seal — the job's grant on
// tokens/ has no delete (D16) — so a second read of the object would find it
// again and succeed, and no assertion over the bucket, the exchange count or
// the run's record can see it. What makes the read one is that takeBudgetToken
// has a single call site, budgetClient, which memoizes the identity it builds
// (its c.done guard) and hands the unsealed token to the policy read and to
// budget.Open both.
//
// The property is therefore a shape of the source, and it is read as one: every
// mention of takeBudgetToken in the package's non-test files is counted through
// the parser. A call expression is a call site; a selector that is not one is a
// method value — `f := r.takeBudgetToken`, or the method handed to something
// else — which takes the same take, so it is counted too and there must be none
// (the 2026-10-19 review found this scan missed exactly that). A second call
// anywhere — in startBudgetBackend, in the session, in a helper — therefore
// fails this test rather than leaving TestTokenIsTakenOnce green, which is what
// the 2026-10-18 review found an extra take in startBudgetBackend did. What the
// scan cannot see is a value reaching the method through an interface or a
// variable the parser cannot follow; the package's only mention of the name is
// the one below, so nothing here does that. Comments do not count: the scan is
// over expressions, so prose that names the function is not a site.
func TestTheBudgetTokenIsTakenAtOneCallSite(t *testing.T) {
	const want = "projectpolicy.go in budgetClient"
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var sites, values []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				if fn.Body == nil {
					continue
				}
				// The function the site sits in, not the one it is nested in
				// by hand: one inside a func literal is still that function's.
				scanTakeSites(name+" in "+fn.Name.Name, fn.Body, &sites, &values)
				continue
			}
			// A package-level declaration can name the method too
			// (`var take = (*run).takeBudgetToken`), so it is scanned as well.
			scanTakeSites(name+" at package level", decl, &sites, &values)
		}
	}
	if len(sites) != 1 || sites[0] != want || len(values) != 0 {
		t.Errorf("takeBudgetToken is called from %v, and taken as a method value at %v, want exactly [%s] and no method value: the take is read-only, so a second one reads the object a second time without any other test being able to see it, and a method value takes it just as a call does — the identity it unseals is then not the one the policy read and budget.Open share (finding 2)", sites, values, want)
	}
}

// scanTakeSites sorts every mention of takeBudgetToken under node into the calls
// that are call sites and the selectors that are method values; both take the
// token, and the two are told apart by whether the selector is the callee.
func scanTakeSites(where string, node ast.Node, sites, values *[]string) {
	callee := map[*ast.SelectorExpr]bool{}
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "takeBudgetToken" {
			callee[sel] = true
		}
		return true
	})
	ast.Inspect(node, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "takeBudgetToken" {
			return true
		}
		if callee[sel] {
			*sites = append(*sites, where)
			return true
		}
		*values = append(*values, where)
		return true
	})
}
