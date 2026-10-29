// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
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
// build tag, and a binary that compiled it is the Subscription Edition. The
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
//  2. every seam setter's error reaches a panic — the call's result bound in an
//     if-init named err, checked against nil and panicked on. A bare call, a
//     deferred or go one, `_ = …`, a result assigned and never checked, an if
//     whose body is empty or logs instead of panicking, and a setter taken as a
//     function value are each a registration that was refused and went on
//     anyway, or one the scan cannot follow at all.
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
			name:     "a setter whose error reaches an empty if body",
			body:     "package register\n\nimport \"github.com/dimipaun/fugaro/internal/cli\"\n\nfunc init() {\n\tif err := cli.SetEditionEE(); err != nil {\n\t}\n}\n",
			expected: []string{"SetEditionEE", "empty"},
		},
		{
			name:     "a setter whose error is assigned to a variable and never checked",
			body:     "package register\n\nimport \"github.com/dimipaun/fugaro/internal/cli\"\n\nfunc init() {\n\terr := cli.SetEditionEE()\n\t_ = err\n}\n",
			expected: []string{"SetEditionEE", "if-init"},
		},
		{
			name:     "a setter whose error is logged rather than panicked on",
			body:     "package register\n\nimport (\n\t\"log\"\n\n\t\"github.com/dimipaun/fugaro/internal/cli\"\n)\n\nfunc init() {\n\tif err := cli.SetEditionEE(); err != nil {\n\t\tlog.Print(err)\n\t}\n}\n",
			expected: []string{"SetEditionEE", "not a panic"},
		},
		{
			name:     "a setter taken as a function value",
			body:     "package register\n\nimport \"github.com/dimipaun/fugaro/internal/cli\"\n\nfunc init() {\n\tf := cli.SetEditionEE\n\t_ = f()\n}\n",
			expected: []string{"SetEditionEE", "function value"},
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

// panicShape is the one shape both rules accept, spelled out in every finding
// so a reader can act on one without the design doc.
const panicShape = "`if err := …; err != nil { panic(…) }` is the one shape a seam setter's call may take"

// setterCallsIn is one file's seam setter uses: the names it uses, from every
// position, and a finding for each use whose error does not reach a panic.
// Every *ast.CallExpr and every *ast.SelectorExpr in the file is read, the way
// internal/eeboundary's scan reads them, so a registration cannot hide from the
// pair rule inside a defer, a go statement or a call to something else.
//
// The one shape both rules accept is `if err := …; err != nil { panic(…) }`: the
// call bound in an if-init named err, whose body checks that err against nil and
// panics. Discarded is a call that is a statement of its own, the call of a defer
// or a go statement, an assignment whose left-hand side is the blank identifier,
// an assignment whose result is never checked that way, a result consumed
// somewhere else, or an if whose body is empty or does something other than
// panic. Taking a setter as a function value — a selector naming one where a call
// is not — is a finding too: the name flows into something the scan cannot
// follow, so nothing tells whether the error it answers reaches a panic.
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
		// last entry being the node's own parent: ast.Inspect answers nil when
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
		switch node := n.(type) {
		case *ast.CallExpr:
			if name, isSetter := setterName(node); isSetter {
				called = append(called, name)
				if why, unchecked := uncheckedResult(ancestry, node); unchecked {
					findings = append(findings, rel+": it calls "+name+why)
				}
			}
		case *ast.SelectorExpr:
			if slices.Contains(seamSetters, node.Sel.Name) && !callPosition(ancestry, node) {
				called = append(called, node.Sel.Name)
				findings = append(findings, rel+": it takes "+node.Sel.Name+" as a function value rather than calling it, so nothing tells whether the error it answers reaches a panic: "+panicShape)
			}
		}
		ancestry = append(ancestry, n)
		return true
	})
	return called, findings, nil
}

// callPosition answers whether expr is the Fun of the call it sits in: the
// setter's own call, the one shape both rules have something to say about,
// rather than a setter named where a value is wanted.
func callPosition(ancestry []ast.Node, expr ast.Expr) bool {
	if len(ancestry) == 0 {
		return false
	}
	call, ok := ancestry[len(ancestry)-1].(*ast.CallExpr)
	return ok && call.Fun == expr
}

// uncheckedResult answers whether the setter call call, whose parent is the last
// node of its ancestry, lets its error go unchecked, and says how in words a
// reader can act on. It reads the one shape that decides it: `if err := …;
// err != nil { panic(…) }`. Everything else is a refused registration that went
// on anyway, whose error nothing panics on.
func uncheckedResult(ancestry []ast.Node, call ast.Expr) (string, bool) {
	if len(ancestry) == 0 {
		return "", false
	}
	switch parent := ancestry[len(ancestry)-1].(type) {
	case *ast.ExprStmt:
		return " as a statement of its own, so the error it answers is discarded and a refused registration panics nothing: " + panicShape, true
	case *ast.DeferStmt:
		return " in a defer statement, so the error it answers is discarded when it finally runs and a refused registration panics nothing: " + panicShape, true
	case *ast.GoStmt:
		return " in a go statement, so the error it answers is discarded in another goroutine and a refused registration panics nothing: " + panicShape, true
	case *ast.AssignStmt:
		if blankTarget(parent, call) {
			return " and assigns its error to the blank identifier, so the error it answers is discarded and a refused registration panics nothing: " + panicShape, true
		}
		if init := ifInit(ancestry); init != nil {
			if why := notPanicked(init, parent, call); why != "" {
				return why, true
			}
			return "", false
		}
		return " and its result is not the if-init the shape asks for, so the error it answers is never checked and a refused registration panics nothing: " + panicShape, true
	}
	return " where its result is used rather than checked, so the error it answers reaches no if that panics on it: " + panicShape, true
}

// ifInit answers the if statement whose init is the assignment holding the call
// whose ancestry this is, or nil when the assignment is not an if-init: the
// call's own parent is the last entry of the ancestry and the statement above it
// the one before.
func ifInit(ancestry []ast.Node) *ast.IfStmt {
	if len(ancestry) < 2 {
		return nil
	}
	stmt, ok := ancestry[len(ancestry)-2].(*ast.IfStmt)
	if !ok || stmt.Init != ancestry[len(ancestry)-1] {
		return nil
	}
	return stmt
}

// notPanicked answers why the if statement init leaves the setter's error
// unchecked, and "" when it is the shape both rules ask for: the init binds the
// call's result to err, the condition checks that err against nil, and the body
// is exactly one panic call.
func notPanicked(init *ast.IfStmt, assign *ast.AssignStmt, call ast.Expr) string {
	if assign.Tok != token.DEFINE || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 || assign.Rhs[0] != call {
		return " and binds its result outside the if-init the shape asks for, so the error it answers is never checked and a refused registration panics nothing: " + panicShape
	}
	if name, ok := assign.Lhs[0].(*ast.Ident); !ok || name.Name != "err" {
		return " and binds its result in the if-init to a name other than err, so what its nil check reads is not the error it answers: " + panicShape
	}
	if !checksNil(init.Cond, "err") {
		return " and its if-init does not check err against nil, so a refused registration panics nothing: " + panicShape
	}
	if len(init.Body.List) == 0 {
		return " and its if-init body is empty, so a refused registration panics nothing: " + panicShape
	}
	if !panicsOnIt(init.Body) {
		return " and its if-init body is not a panic call, so a refused registration panics nothing: " + panicShape
	}
	return ""
}

// checksNil answers whether cond is name compared against nil, in either order:
// the check that keeps the panic below it off a registration that was accepted.
func checksNil(cond ast.Expr, name string) bool {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.NEQ {
		return false
	}
	if ident, ok := bin.X.(*ast.Ident); ok && ident.Name == name {
		return isNilIdent(bin.Y)
	}
	if ident, ok := bin.Y.(*ast.Ident); ok && ident.Name == name {
		return isNilIdent(bin.X)
	}
	return false
}

// isNilIdent answers whether expr is the predeclared nil.
func isNilIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}

// panicsOnIt answers whether body is exactly one panic call — `panic(…)`, whose
// argument is not read: a panic that names the error, one that wraps it and one
// the compiler rejects all read the same here, and the rule is that the failure
// to register stops the build rather than being written down somewhere.
func panicsOnIt(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) != 1 {
		return false
	}
	stmt, ok := body.List[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := stmt.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	fun, ok := call.Fun.(*ast.Ident)
	return ok && fun.Name == "panic"
}

// blankTarget answers whether the assignment parent drops call's result: the
// right-hand side is the call and the left-hand side names it `_`.
func blankTarget(parent *ast.AssignStmt, call ast.Expr) bool {
	for i, rhs := range parent.Rhs {
		if rhs != call || i >= len(parent.Lhs) {
			continue
		}
		if id, ok := parent.Lhs[i].(*ast.Ident); ok && id.Name == "_" {
			return true
		}
	}
	return false
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
