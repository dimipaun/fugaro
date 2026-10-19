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
// The property is therefore a shape of the source, and it is read as one: the
// call sites are counted over the package's non-test files through the parser,
// so a second call added anywhere — in startBudgetBackend, in the session, in
// a helper — fails this test rather than leaving TestTokenIsTakenOnce green
// (which is what the 2026-10-18 review found an extra take in
// startBudgetBackend did). Comments do not count: the scan is over call
// expressions, so prose that names the function is not a call site.
func TestTheBudgetTokenIsTakenAtOneCallSite(t *testing.T) {
	const want = "projectpolicy.go in budgetClient"
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var sites []string
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
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// The function the call sits in, not the one it is nested in by
			// hand: a call inside a func literal is still that function's.
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "takeBudgetToken" {
					return true
				}
				sites = append(sites, name+" in "+fn.Name.Name)
				return true
			})
		}
	}
	if len(sites) != 1 || sites[0] != want {
		t.Errorf("takeBudgetToken is called from %v, want exactly [%s]: the take is read-only, so a second call site reads the object a second time without any other test being able to see it, and the identity it unseals is then not the one the policy read and budget.Open share (finding 2)", sites, want)
	}
}
