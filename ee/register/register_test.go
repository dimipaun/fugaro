// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See ee/LICENSE for the terms.

package register

// The rules R2 and §5.5 put on the one assembly point: a build that compiled
// this package reports the ee edition, the gating model of D522/D523 holds for
// a tagged `version`, and the tagged tree wires the governance pair together
// rather than one half of it.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/cli"
)

// TestTaggedBuildReportsEE is the headline: this package exists under the ee
// build tag, and a binary that compiled it is the enterprise edition. The
// edition is what the render step writes into the build record and what every
// launch-side warning judges a job image by, so a register whose init did not
// set it would leave a tagged binary reporting core while it carries ee/ code.
func TestTaggedBuildReportsEE(t *testing.T) {
	if got := cli.Edition(); got != cli.EditionEE {
		t.Fatalf("cli.Edition() = %q in a build that compiled ee/register, want %q: ee/register's init is the only thing that raises the edition, and cmd/fugaro/ee_on.go is the only thing that compiles it in", got, cli.EditionEE)
	}
}

// TestRegisterFollowsTheGatingModel is T27 (b): what a tagged build prints for
// a core command, and the set-once rule that keeps a seam from being halved.
//
// D523 is why the streams are read at all: no licence notice exists yet, so a
// tagged build's own output carries no licence, payment or Enterprise wording —
// the message a paid command answers with is a capability statement, and it is
// not reached here, because `version` is a core command. No key and no
// environment is set for the run: what the edition gates must not appear in
// output a user gets from a plain command.
func TestRegisterFollowsTheGatingModel(t *testing.T) {
	if err := cli.SetEditionEE(); err == nil {
		t.Error("a second cli.SetEditionEE was accepted: the set-once rule is what keeps a core caller from halving the enforcement a tagged build registered (design §5.5)")
	}

	cmd := cli.NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(cli.NormalizeArgs([]string{"version"}))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("fugaro version in a tagged build: %v", err)
	}
	if want := cli.Version + "\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q: a tagged build prints exactly what a core one does", out.String(), want)
	}
	for _, stream := range []struct {
		name string
		body string
	}{
		{"stdout", out.String()},
		{"stderr", errOut.String()},
	} {
		low := strings.ToLower(stream.body)
		for _, word := range []string{"licen", "enterprise", "subscription"} {
			if strings.Contains(low, word) {
				t.Errorf("%s = %q, and it carries %q: no licence notice exists yet (D523), and nothing a tagged build prints for a core command may read like one", stream.name, stream.body, word)
			}
		}
	}
}

// seamSetters mirrors internal/eeboundary/seam_test.go's seamSetterNames: the
// setters of the package-level seams, each of which answers an error rather
// than accepting a second call. The list is spelled out again because that file
// is a test of its own package and cannot be imported, and because this scan is
// about the calls this package makes rather than about who may make them.
var seamSetters = []string{
	"SetLaunchDeclarer",
	"RegisterCommand",
	"SetSections",
	"SetGovernance",
	"SetStage",
	"SetEditionEE",
}

// governancePair is the pair E2.3 moves together. Registering one half alone is
// worse than registering neither: the tagged binary would carry the policy and
// not the stage enforcement (or the reverse) while looking, to any check that
// asks only whether the hook is set, exactly like a fully wired one (G10).
var governancePair = []string{"SetGovernance", "SetStage"}

// TestRegisterWiresTheGovernancePair holds this directory to two shape rules,
// over the real tree and over planted ones:
//
//  1. the governance pair is called both or neither;
//  2. every seam setter's error reaches a panic — a bare call, a deferred one
//     or `_ = …` is a registration that was refused and went on anyway.
//
// It is a source scan for the reason internal/eeboundary's is one: the calls
// are legal Go wherever they sit, and a build would not notice. The name is
// deliberately not TestTaggedBinaryWiresTheGovernance: the plan gives that name
// to E2.3's identity test, which lands in this same package beside
// ee/register/governance.go — the hook holding ee/governance's functions rather
// than merely non-nil ones. That half is E2.3's because until the move the
// policy and runner hooks default to today's non-nil core implementation, and a
// test that asked only "is it set" would pass while enforcement was silently
// dropped (G10).
func TestRegisterWiresTheGovernancePair(t *testing.T) {
	findings, parsed, err := registerFindings(".")
	if err != nil {
		t.Fatal(err)
	}
	if parsed < 1 {
		t.Fatalf("the scan parsed %d non-test Go files in ee/register, and this package holds one: a scan of nothing would pass every rule below by looking at nothing", parsed)
	}
	if len(findings) > 0 {
		t.Errorf("ee/register does not hold the tagged tree's seams together:\n  %s", strings.Join(findings, "\n  "))
	}

	for _, c := range []struct {
		name     string
		body     string
		expected []string
	}{
		{
			name: "the governance pair, each error reaching a panic",
			body: "package register\n\nimport (\n\t\"github.com/dimipaun/fugaro/internal/policy\"\n\t\"github.com/dimipaun/fugaro/internal/runner\"\n)\n\nfunc init() {\n\tif err := policy.SetGovernance(policy.GovernanceFuncs{}); err != nil {\n\t\tpanic(err)\n\t}\n\tif err := runner.SetStage(runner.StageFuncs{}); err != nil {\n\t\tpanic(err)\n\t}\n}\n",
		},
		{
			name:     "only the policy half of the governance pair",
			body:     "package register\n\nimport \"github.com/dimipaun/fugaro/internal/policy\"\n\nfunc init() {\n\tif err := policy.SetGovernance(policy.GovernanceFuncs{}); err != nil {\n\t\tpanic(err)\n\t}\n}\n",
			expected: []string{"SetGovernance", "without"},
		},
		{
			name:     "only the runner half of the governance pair",
			body:     "package register\n\nimport \"github.com/dimipaun/fugaro/internal/runner\"\n\nfunc init() {\n\tif err := runner.SetStage(runner.StageFuncs{}); err != nil {\n\t\tpanic(err)\n\t}\n}\n",
			expected: []string{"SetStage", "without"},
		},
		{
			name:     "a setter whose error is assigned to the blank identifier",
			body:     "package register\n\nimport \"github.com/dimipaun/fugaro/internal/cli\"\n\nfunc init() { _ = cli.SetEditionEE() }\n",
			expected: []string{"SetEditionEE", "discard"},
		},
		{
			name:     "a setter called as a bare statement",
			body:     "package register\n\nimport \"github.com/dimipaun/fugaro/internal/cli\"\n\nfunc init() { cli.SetEditionEE() }\n",
			expected: []string{"SetEditionEE", "discard"},
		},
		{
			name:     "both halves of the pair registered in defer statements",
			body:     "package register\n\nimport (\n\t\"github.com/dimipaun/fugaro/internal/policy\"\n\t\"github.com/dimipaun/fugaro/internal/runner\"\n)\n\nfunc init() {\n\tdefer policy.SetGovernance(policy.GovernanceFuncs{})\n\tdefer runner.SetStage(runner.StageFuncs{})\n}\n",
			expected: []string{"SetGovernance", "SetStage", "defer"},
		},
		{
			name:     "only the runner half, inside a defer statement",
			body:     "package register\n\nimport \"github.com/dimipaun/fugaro/internal/runner\"\n\nfunc init() { defer runner.SetStage(runner.StageFuncs{}) }\n",
			expected: []string{"SetStage", "defer"},
		},
		{
			name: "neither half of the pair, the edition beside it",
			body: "package register\n\nimport \"github.com/dimipaun/fugaro/internal/cli\"\n\nfunc init() {\n\tif err := cli.SetEditionEE(); err != nil {\n\t\tpanic(err)\n\t}\n}\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "register.go"), []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			findings, parsed, err := registerFindings(dir)
			if err != nil {
				t.Fatal(err)
			}
			if parsed != 1 {
				t.Fatalf("the scan parsed %d files of the planted tree, want 1", parsed)
			}
			if len(c.expected) == 0 {
				if len(findings) > 0 {
					t.Errorf("the planted tree is clean and the scan answered:\n  %s", strings.Join(findings, "\n  "))
				}
				return
			}
			joined := strings.Join(findings, "\n")
			for _, want := range c.expected {
				if !strings.Contains(joined, want) {
					t.Errorf("the scan answered:\n  %s\nand it holds no finding naming %q", joined, want)
				}
			}
		})
	}
}

// registerFindings scans every non-test .go file under root — a directory of
// this module — and answers what it found, with the number of files it parsed
// so a caller can tell a scan of the package from a scan of nothing.
func registerFindings(root string) ([]string, int, error) {
	var (
		findings []string
		parsed   int
		called   []string
	)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		parsed++
		names, found, err := setterCallsIn(p, rel)
		if err != nil {
			return err
		}
		called = append(called, names...)
		findings = append(findings, found...)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	var halves []string
	for _, name := range governancePair {
		if slices.Contains(called, name) {
			halves = append(halves, name)
		}
	}
	if len(halves) == 1 {
		findings = append(findings, fmt.Sprintf("ee/register calls %s without %s: one half of the governance pair alone is a tagged binary that enforces half of the governance it registered", halves[0], other(halves[0])))
	}
	return findings, parsed, nil
}

// other answers the half of the governance pair a name is not.
func other(name string) string {
	for _, candidate := range governancePair {
		if candidate != name {
			return candidate
		}
	}
	return ""
}

// setterCallsIn is one file's seam setter calls: the names it calls, from every
// position, and a finding for each call whose error it discards. Every
// *ast.CallExpr in the file is read, the way internal/eeboundary's scan reads
// them, so a registration cannot hide from the pair rule inside a defer, a go
// statement or a call to something else.
//
// A call in an if-init — `if err := …; err != nil { panic(err) }` — is the
// shape both rules ask for and is not a finding. Discarded is a call that is a
// statement of its own, the call of a defer or a go statement, or an
// assignment's right-hand side whose left-hand side is the blank identifier.
//
// A file the parser cannot read is one whose calls cannot be checked, and
// "cannot be checked" is not "is fine": `gofmt -l .` is a gate, so this fires
// only on a file added with a syntax error.
func setterCallsIn(path, rel string) ([]string, []string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("%s cannot be parsed, so its setter calls cannot be checked: %w", rel, err)
	}
	var (
		called   []string
		findings []string
		// ancestry is the nodes currently open above the one being read, its
		// last entry being the call's own parent: ast.Inspect answers nil when
		// it leaves a node, which is where one is dropped.
		ancestry []ast.Node
	)
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			if len(ancestry) > 0 {
				ancestry = ancestry[:len(ancestry)-1]
			}
			return false
		}
		if call, ok := n.(ast.Expr); ok {
			if name, isSetter := setterName(call); isSetter {
				called = append(called, name)
				if why, discarded := discardedResult(ancestry, call); discarded {
					findings = append(findings, rel+": it calls "+name+why)
				}
			}
		}
		ancestry = append(ancestry, n)
		return true
	})
	return called, findings, nil
}

// discardedResult answers whether the setter call call, whose parent is the last
// node of its ancestry, throws its error away, and says how in words a reader
// can act on. It reads the one position that decides it: an expression
// statement runs for its effect alone, a defer or a go statement cannot carry a
// result anywhere, and `_ = …` names the result and drops it.
func discardedResult(ancestry []ast.Node, call ast.Expr) (string, bool) {
	if len(ancestry) == 0 {
		return "", false
	}
	switch parent := ancestry[len(ancestry)-1].(type) {
	case *ast.ExprStmt:
		return " as a statement of its own, so the error it answers is discarded", true
	case *ast.DeferStmt:
		return " in a defer statement, so the error it answers is discarded when it finally runs", true
	case *ast.GoStmt:
		return " in a go statement, so the error it answers is discarded in another goroutine", true
	case *ast.AssignStmt:
		for i, rhs := range parent.Rhs {
			if rhs != call || i >= len(parent.Lhs) {
				continue
			}
			if id, ok := parent.Lhs[i].(*ast.Ident); ok && id.Name == "_" {
				return " and assigns its error to the blank identifier, so the error it answers is discarded and a refused registration panics nothing", true
			}
		}
	}
	return "", false
}

// setterName answers the seam setter an expression calls, by the selector's own
// name: the package qualifier on the left of the dot is not read, so `cli.SetStage`
// and a same-named method elsewhere look alike, which is deliberate — a call the
// scan does not recognise is one it does not check.
func setterName(expr ast.Expr) (string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	var name string
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		name = fun.Sel.Name
	case *ast.Ident:
		name = fun.Name
	}
	if slices.Contains(seamSetters, name) {
		return name, true
	}
	return "", false
}
