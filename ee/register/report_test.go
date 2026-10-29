// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// The report's seam, at the one point that sets it (E2.2): the command the
// tagged tree carries under the name. The command's own tests live beside it
// in ee/report/cli; what is here is the wiring neither of those packages can
// see — that the name answers with the moved command rather than the stub an
// untagged build shows (D522, G3), and that the constructor handed to
// cli.RegisterCommand is ee/report/cli's own.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	reportcli "github.com/dimipaun/fugaro/ee/report/cli"
	"github.com/dimipaun/fugaro/internal/cli"
)

// executeReport runs the tagged tree with args and captures its stdout.
func executeReport(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := cli.NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(cli.NormalizeArgs(args))
	return out.String(), cmd.Execute()
}

// TestReportCommandRegistersTagged: a build that compiled this package carries
// the report's real command, not the stub an untagged build shows for the name
// (D522, G3). The command answering with its own refusal is what this holds: a
// call that cannot reach a project — an unknown --by is refused before any
// selection — fails with the report's message, where the stub would answer
// that this build does not include it.
func TestReportCommandRegistersTagged(t *testing.T) {
	root := cli.NewRootCmd()
	c, _, err := root.Find([]string{"report"})
	if err != nil || c == nil || c.Name() != "report" {
		t.Fatalf("the tagged tree has no report command: %v", err)
	}
	for _, flag := range []string{"by", "since", "until", "repo", "json", "csv"} {
		if c.Flags().Lookup(flag) == nil {
			t.Errorf("the tagged tree's report has no --%s flag", flag)
		}
	}

	out, err := executeReport(t, "report", "--by", "bogus")
	if err == nil {
		t.Fatal("report --by bogus was accepted")
	}
	if strings.Contains(err.Error(), "part of Fugaro Subscription Edition") {
		t.Fatalf("the tagged tree answers the untagged build's stub for the report (%q): ee/register registered nothing", err)
	}
	if !strings.Contains(err.Error(), `--by "bogus"`) {
		t.Errorf("report --by bogus = %v, want the report's own --by refusal", err)
	}
	if out != "" {
		t.Errorf("the refused report wrote %q to stdout, want nothing", out)
	}

	// The name is taken: a second registration of it is refused, which is the
	// set-once half of the seam (§5.5).
	if err := cli.RegisterCommand("report", reportcli.NewReportCmd); err == nil {
		t.Error("a second registration of report was accepted: the tagged tree's command could be replaced")
	}
}

// TestTheRegistrationHoldsTheMovedReport is the identity half of the same
// seam, and it is a source scan because the API must not gain a getter for it:
// a production reader that answered the registered constructor would be a new
// exported name in the core whose only caller is a test (design §10). What it
// pins is the one fact the runtime check above cannot see — that the function
// handed to cli.RegisterCommand for "report" is ee/report/cli.NewReportCmd and
// not some other constructor of the right shape.
//
// The repository is walked rather than ee/register alone, so a second call site
// — the shape a seam quietly halved comes back in — is a finding too.
func TestTheRegistrationHoldsTheMovedReport(t *testing.T) {
	const moved = "github.com/dimipaun/fugaro/ee/report/cli"
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
		found, err := reportRegistrationCalls(path, moved)
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
		t.Fatalf("the repository has %d calls of cli.RegisterCommand(\"report\", …) (%v), want exactly one: ee/register/report.go is the one place a tagged build's report command is registered", len(calls), calls)
	}
	if want := "ee/register/report.go"; calls[0] != want {
		t.Errorf("the report command is registered in %s, want %s", calls[0], want)
	}
}

// reportRegistrationCalls answers the files under path that register "report"
// with the moved package's NewReportCmd, as paths relative to the repository
// root. The scan reads the import that names the argument, so `reportcli.
// NewReportCmd` is the moved constructor and a same-named function of another
// package is not: a command constructor is a function of the right shape, and
// the shape is not the identity.
func reportRegistrationCalls(path, moved string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	aliases := map[string]bool{}
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) == moved {
			alias := filepath.Base(moved)
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			aliases[alias] = true
		}
	}
	if len(aliases) == 0 {
		return nil, nil
	}
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		fun, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || fun.Sel.Name != "RegisterCommand" {
			return true
		}
		name, ok := call.Args[0].(*ast.BasicLit)
		if !ok || name.Kind != token.STRING || strings.Trim(name.Value, `"`) != "report" {
			return true
		}
		arg, ok := call.Args[1].(*ast.SelectorExpr)
		if !ok || arg.Sel.Name != "NewReportCmd" {
			return true
		}
		pkg, ok := arg.X.(*ast.Ident)
		if !ok || !aliases[pkg.Name] {
			return true
		}
		rel, err := filepath.Rel("../..", path)
		if err != nil {
			return true
		}
		found = append(found, filepath.ToSlash(rel))
		return true
	})
	return found, nil
}
