package eeboundary

// The seam pin (design ee-boundary-moves.md §5.5): every package-level seam the
// moved code fills is set once, and by `ee/register` alone. The core defines the
// setters -- cli.SetLaunchDeclarer and cli.RegisterCommand (E1.1),
// policy.SetGovernance, runner.SetStage, digest.SetSections (E1.2) and
// cli.SetEditionEE (E1.3) -- and a core caller that called one could set a seam
// to something the tagged build did not intend, or switch enforcement off in a
// binary that carries it. So no non-test file outside ee/register may call one,
// and this scan is what holds that.
//
// It is a source scan and not a build check for the reason the import boundary
// is one: the calls are legal Go wherever they sit, and a build would not
// notice. It matches each setter by the selector's own name -- `cli.SetStage`
// and a same-named method on some other type look alike to a scan without type
// information, which is deliberate: a call the scan does not recognise is a call
// it does not refuse. Declaring a setter is not calling one, so the core files
// that publish them are left alone, and a setter named as a value (`f :=
// runner.SetStage`, `cli.SetEditionEE` passed to a helper) is a use too, so the
// scan flags it: the name is what the scan reads, whether or not a call follows.
//
// The scan sees a setter only where its Go identifier is written down. Two ways
// reach one without naming it -- reflection (`reflect.ValueOf(...).MethodByName`)
// and a `//go:linkname` directive -- and this scan cannot see either; they are
// left to review.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// seamRule names the finding this scan produces.
const seamRule = "only ee/register sets a seam"

// seamSetterNames are the names only ee/register may use: the setters of the
// package-level seams (design §5.5). A name is matched on the selector's own
// name or on a bare identifier, so the package qualifier on the left of the dot
// is not read.
var seamSetterNames = []string{
	"SetLaunchDeclarer",
	"RegisterCommand",
	"SetSections",
	"SetGovernance",
	"SetStage",
	"SetEditionEE",
}

// eeRegisterDir is the one directory whose non-test files may call a setter.
const eeRegisterDir = "ee/register"

// seamSetterCalls walks root's non-test Go files and answers every call to a
// seam setter outside ee/register, with the number of Go files it parsed so a
// caller can tell a scan of the module from a scan of an empty directory.
func seamSetterCalls(root string) ([]finding, int, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, 0, err
	}
	var (
		findings []finding
		parsed   int
	)
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(abs, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == eeRegisterDir || strings.HasPrefix(rel, eeRegisterDir+"/") {
			return nil
		}
		parsed++
		findings = append(findings, setterCallsIn(p, rel)...)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return findings, parsed, nil
}

// setterCallsIn is the setter uses in one file, as findings: a call to a setter,
// or the setter named as a value (`f := runner.SetStage`, `cli.SetEditionEE`
// passed to a helper) — the name is what the scan reads, whether or not a call
// follows. A file the parser cannot read is one whose uses cannot be checked,
// and "cannot be checked" is not "is fine": every .go file in this module parses
// (`gofmt -l .` is a CI gate), so this fires only on a file added with a syntax
// error.
func setterCallsIn(path, rel string) []finding {
	fset := token.NewFileSet()
	f, perr := parser.ParseFile(fset, path, nil, 0)
	if perr != nil || f == nil {
		return []finding{{rule: seamRule, path: rel, detail: "it cannot be parsed, so its uses cannot be checked: " + errText(perr)}}
	}
	// A selector's Sel and a function's own declaration name are the two
	// positions that name a setter without using it. Every other occurrence —
	// the Fun of a call, a bare identifier, a selector used as a value — is a
	// use, and each is reported once: skipping the Sel keeps a call from being
	// counted twice.
	selOfSelector := map[*ast.Ident]bool{}
	declName := map[*ast.Ident]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			selOfSelector[x.Sel] = true
		case *ast.FuncDecl:
			declName[x.Name] = true
		}
		return true
	})
	var findings []finding
	ast.Inspect(f, func(n ast.Node) bool {
		name := ""
		switch x := n.(type) {
		case *ast.SelectorExpr:
			name = x.Sel.Name
		case *ast.Ident:
			if selOfSelector[x] || declName[x] {
				return true
			}
			// A core caller reaches its own package's setter unqualified:
			// cli.RegisterCommand inside package cli is RegisterCommand.
			name = x.Name
		default:
			return true
		}
		if !slices.Contains(seamSetterNames, name) {
			return true
		}
		findings = append(findings, finding{rule: seamRule, path: rel,
			detail: "it uses " + name + ", a seam setter only " + eeRegisterDir + " may call"})
		return true
	})
	return findings
}

func TestOnlyEERegisterSetsTheSeams(t *testing.T) {
	findings, parsed, err := seamSetterCalls(moduleRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if parsed < 100 {
		t.Fatalf("the scan parsed %d non-test Go files: it did not see the module, so an empty result would prove nothing", parsed)
	}
	assertNoFindings(t, &report{findings: findings}, seamRule,
		"Every seam is a package-level value whose default is the core implementation, and ee/register sets it once (design ee-boundary-moves.md §5.5). A core caller that set cli.SetLaunchDeclarer, cli.RegisterCommand, policy.SetGovernance, runner.SetStage, digest.SetSections or cli.SetEditionEE could halve the enforcement a tagged binary registered or switch a seam off. Move the call into ee/register, or reach the seam's value instead of setting it.")
}

func TestSeamSetterScanCatchesPlantedCalls(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "go.mod", []byte(goModFixture))
	for name, body := range map[string]string{
		// A core file calling a setter, once per package the seam lives in.
		"internal/cli/x.go":  "package cli\n\nfunc init() { _ = RegisterCommand(\"x\", nil) }\n",
		"internal/web/y.go":  "package web\n\nimport \"github.com/dimipaun/fugaro/internal/digest\"\n\nfunc init() { _ = digest.SetSections(nil) }\n",
		"ee/governance/z.go": "package governance\n\nimport \"github.com/dimipaun/fugaro/internal/policy\"\n\nfunc init() { _ = policy.SetGovernance(policy.GovernanceFuncs{}) }\n",
		// The one allowed file: ee/register may call any of them.
		"ee/register/register.go": "package register\n\nimport \"github.com/dimipaun/fugaro/internal/cli\"\n\nfunc init() { _ = cli.SetEditionEE() }\n",
		// A test file may exercise a setter.
		"internal/cli/extend_test.go": "package cli\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) { _ = SetLaunchDeclarer(nil) }\n",
		// A setter named as a value, not called: an assignment and an argument,
		// one a bare identifier and one a selector, both flagged.
		"internal/runner/assign.go": "package runner\n\nvar stage = SetStage\n",
		"internal/web/pass.go":      "package web\n\nimport \"github.com/dimipaun/fugaro/internal/cli\"\n\nfunc helper(f func() error) {}\n\nfunc init() { helper(cli.SetEditionEE) }\n",
		// Declaring a same-named function is not calling one.
		"internal/other/w.go": "package other\n\nfunc SetStage() {}\n",
	} {
		writeFixture(t, root, name, []byte(body))
	}

	findings, parsed, err := seamSetterCalls(root)
	if err != nil {
		t.Fatal(err)
	}
	if parsed != 6 {
		t.Fatalf("the scan parsed %d non-test files, want 6 (every one but ee/register's and the _test one)", parsed)
	}
	got := make([]string, 0, len(findings))
	for _, f := range findings {
		got = append(got, f.path+": "+f.detail)
	}
	slices.Sort(got)
	want := []string{
		"internal/cli/x.go: it uses RegisterCommand, a seam setter only ee/register may call",
		"internal/web/y.go: it uses SetSections, a seam setter only ee/register may call",
		"ee/governance/z.go: it uses SetGovernance, a seam setter only ee/register may call",
		"internal/runner/assign.go: it uses SetStage, a seam setter only ee/register may call",
		"internal/web/pass.go: it uses SetEditionEE, a seam setter only ee/register may call",
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the scan answered\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestSeamSetterScanDoesNotFlagTheDefinitions keeps the pin from refusing the
// very setters the core is meant to publish: the files that declare them are
// non-test files outside ee/register, and they are not calls.
func TestSeamSetterScanDoesNotFlagTheDefinitions(t *testing.T) {
	core := filepath.Join(moduleRoot(t), "internal", "cli", "extend.go")
	if _, err := os.Stat(core); err != nil {
		t.Fatal(err)
	}
	if got := setterCallsIn(core, "internal/cli/extend.go"); len(got) != 0 {
		t.Errorf("the scan flagged the setter definitions in extend.go: %s", joinFindings(got))
	}
}
