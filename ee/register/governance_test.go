// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// The governance seam, at the one point that sets it (E2.3): the policy the
// run's layers are merged under and the stage tripwire that reads it. Both
// halves are one registration, and what is here is what neither ee/governance's
// own tests nor the core's can see — that the functions handed to
// policy.SetGovernance and runner.SetStage are ee/governance's own and not
// some other pair of the right shape.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/ee/governance"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/runner/runnertest"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestTaggedBinaryWiresTheGovernance: a build that compiled this package
// enforces the governance ee/governance carries, and it does so through the
// pair of seams and not one of them (D521, G10).
//
// The source scan is the identity half, and it is a scan because the API must
// not gain a getter for it: a production reader that answered the installed
// pair would be a new exported name in the core whose only caller is a test
// (design §10). What it pins is the fact the runtime check below cannot see —
// that the functions handed to the two setters are ee/governance's own. The
// repository is walked rather than this package alone, so a second call site —
// the shape a seam quietly halved comes back in — is a finding too.
//
// The runtime half then reads the installed pair back: a merge in this test
// binary resolves the shipped defaults, and the stage the pair is asked about
// leaves the stage_budget halt by time.
func TestTaggedBinaryWiresTheGovernance(t *testing.T) {
	const moved = "github.com/dimipaun/fugaro/ee/governance"
	for _, seam := range []struct {
		call     string
		fields   map[string]string
		corePkg  string
		structNu string
	}{
		{"SetGovernance", map[string]string{"Defaults": "Defaults", "Tighten": "Tighten"}, policyPkg, "GovernanceFuncs"},
		{"SetStage", map[string]string{"Timeout": "Timeout", "TimedOut": "TimedOut"}, runnerPkg, "StageFuncs"},
	} {
		t.Run(seam.call, func(t *testing.T) {
			var calls []string
			err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "testdata" {
						return fs.SkipDir
					}
					return nil
				}
				if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
					return nil
				}
				found, err := seamCalls(path, seam.corePkg, seam.structNu, seam.call, moved, seam.fields)
				if err != nil {
					return err
				}
				calls = append(calls, found...)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != 1 {
				t.Fatalf("the repository has %d calls of %s.%s(…) (%v), want exactly one: ee/register/governance.go is the one place a tagged build's governance is registered",
					len(calls), seam.corePkg, seam.call, calls)
			}
			if want := "ee/register/governance.go"; calls[0] != want {
				t.Errorf("%s is called in %s, want %s", seam.call, calls[0], want)
			}
		})
	}

	// The merge in this binary resolves the shipped values, which is what the
	// policy half is for: an untagged build resolves none of them (D522).
	if got := policy.Merge(policy.Layer{Governance: policy.Governance{}.WithDefaults()}).Governance; got != governance.Defaults() {
		t.Errorf("the tagged binary resolved %+v, want the shipped defaults %+v", got, governance.Defaults())
	}
	// And the stage half hangs a stage on the bound the block asked for: a run
	// whose repository writes rules a stage timeout stops as the stage_budget
	// halt by time, which is what an untagged build does not do (D522).
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"],
		"timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }",
		"timeouts: { total: 1m, stage: 10s, verify: 5s, finalize_reserve: 5s }", 1) +
		"governance:\n  stage_timeout: { implement: 200ms }\n"
	h := runnertest.New(t, cfg, nil)
	rec, err := h.Run(t, runnertest.Blocked(1))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Halt == nil || rec.Halt.Reason != runstore.HaltStageBudget || rec.Halt.Detail != "time" {
		t.Fatalf("the tagged run's halt = %+v, want the stage_budget halt by time", rec.Halt)
	}
}

// The core packages the two setters live in, as the scan reads their import
// paths.
const (
	policyPkg = "github.com/dimipaun/fugaro/internal/policy"
	runnerPkg = "github.com/dimipaun/fugaro/internal/runner"
)

// seamCalls answers the files under path that call corePkg's setter with a
// literal whose named fields are movedPkg's own functions, as paths relative
// to the repository root. The scan reads the imports that name both sides, so
// a same-named function of another package is not the moved one: a seam's
// functions are values of the right shape, and the shape is not the identity.
func seamCalls(path, corePkg, structName, setter, movedPkg string, fields map[string]string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	coreAliases, movedAliases := map[string]bool{}, map[string]bool{}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		alias := filepath.Base(p)
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		switch p {
		case corePkg:
			coreAliases[alias] = true
		case movedPkg:
			movedAliases[alias] = true
		}
	}
	if len(coreAliases) == 0 || len(movedAliases) == 0 {
		return nil, nil
	}
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		fun, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || fun.Sel.Name != setter {
			return true
		}
		if pkg, ok := fun.X.(*ast.Ident); !ok || !coreAliases[pkg.Name] {
			return true
		}
		lit, ok := call.Args[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		typ, ok := lit.Type.(*ast.SelectorExpr)
		if !ok || typ.Sel.Name != structName {
			return true
		}
		if pkg, ok := typ.X.(*ast.Ident); !ok || !coreAliases[pkg.Name] {
			return true
		}
		named := map[string]bool{}
		for _, el := range lit.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				return true
			}
			want, ok := fields[key.Name]
			if !ok {
				return true
			}
			val, ok := kv.Value.(*ast.SelectorExpr)
			if !ok || val.Sel.Name != want {
				return true
			}
			pkg, ok := val.X.(*ast.Ident)
			if !ok || !movedAliases[pkg.Name] {
				return true
			}
			named[key.Name] = true
		}
		for field := range fields {
			if !named[field] {
				return true
			}
		}
		rel, err := filepath.Rel("../..", path)
		if err != nil {
			return true
		}
		found = append(found, rel)
		return true
	})
	return found, nil
}
