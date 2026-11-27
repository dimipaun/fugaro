package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestRewriteBudgetCallSites: managed.RewriteBudget is a library function with
// no fence of its own (R9 of docs/managed-flag-ergonomics) — it replaces the
// amount a managed project's creation fact records and asks nobody on the way.
// The fence is at its callers, so the set of callers is the whole of what keeps
// it honest: `fugaro update`'s rewrite refuses a coding agent's session and a
// Fugaro run's own process before it writes (T3), and `fugaro managed flag` is
// refused in both the same way (T4). A third caller would be a way to change a
// recorded amount that neither refusal covers, and nothing else in the tree
// would notice it.
//
// The scan is over every non-test file in internal/, and it pins the files
// rather than the lines: a call site that moves down its own file is not a
// finding, and a new one anywhere else is.
func TestRewriteBudgetCallSites(t *testing.T) {
	fset := token.NewFileSet()
	found := map[string]int{}
	err := filepath.WalkDir(filepath.Join("..", "..", "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "RewriteBudget" {
				return true
			}
			// The path is module-relative, as the want below spells it.
			file := filepath.ToSlash(path)
			if i := strings.Index(file, "internal/"); i >= 0 {
				file = file[i:]
			}
			found[file]++
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("the scan of internal/: %v", err)
	}
	var got []string
	for file, n := range found {
		got = append(got, fmt.Sprintf("%s (%d)", file, n))
	}
	sort.Strings(got)
	want := []string{"internal/cli/managed_flag.go (1)", "internal/cli/update_selfflag.go (1)"}
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Errorf("managed.RewriteBudget is called from %v, want %v: the two callers that are fenced themselves are the only way a recorded amount may change", got, want)
	}
}
