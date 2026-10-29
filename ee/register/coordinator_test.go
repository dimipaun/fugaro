// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// The coordinator's two seams, at the one point that sets them (E2.1): the
// command the tagged tree carries under the name, and the launch declarer the
// tagged build's launches reach. The command's own tests live beside it in
// ee/coordinator/cli; what is here is the wiring neither of those packages can
// see.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	coordcli "github.com/dimipaun/fugaro/ee/coordinator/cli"
	"github.com/dimipaun/fugaro/internal/cli"
)

// execute runs the tagged tree with args and captures its output.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := cli.NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(cli.NormalizeArgs(args))
	return out.String(), cmd.Execute()
}

// TestCoordinatorCommandRegistersTagged: a build that compiled this package
// carries the coordinator's real command, with the three subcommands the
// console and the sweep job spell, and not the stub an untagged build shows for
// the name (D522, G3). The command answering at all is what this holds: what it
// prints for a call with no project is the command's own refusal, and the stub
// would instead answer that this build does not include it.
func TestCoordinatorCommandRegistersTagged(t *testing.T) {
	root := cli.NewRootCmd()
	c, _, err := root.Find([]string{"coordinator"})
	if err != nil || c == nil || c.Name() != "coordinator" {
		t.Fatalf("the tagged tree has no coordinator command: %v", err)
	}
	var subs []string
	for _, sub := range c.Commands() {
		subs = append(subs, sub.Name())
	}
	for _, want := range []string{"report", "sweep", "status"} {
		if !slices.Contains(subs, want) {
			t.Errorf("the tagged tree's coordinator has no %s subcommand: %v", want, subs)
		}
	}

	out, err := execute(t, "coordinator", "status")
	if err == nil {
		t.Fatal("coordinator status ran with no project selected")
	}
	if strings.Contains(err.Error(), "part of Fugaro Subscription Edition") {
		t.Fatalf("the tagged tree answers the untagged build's stub for the coordinator (%q): ee/register registered nothing", err)
	}
	if out != "" {
		t.Errorf("the refused coordinator status wrote %q to stdout, want nothing", out)
	}

	// The name is taken: a second registration of it is refused, which is the
	// set-once half of the seam (§5.5).
	if err := cli.RegisterCommand("coordinator", coordcli.NewCoordinatorCmd); err == nil {
		t.Error("a second registration of coordinator was accepted: the tagged tree's command could be replaced")
	}
	// The declarer likewise: a refused second call is what keeps a tagged build
	// from quietly dropping the one it installed.
	if err := cli.SetLaunchDeclarer(coordcli.Declare); err == nil {
		t.Error("a second cli.SetLaunchDeclarer was accepted: the declarer a tagged build installed could be replaced")
	}
}

// TestTheHookHoldsTheMovedDeclare is the identity half of the same seam, and it
// is a source scan because the API must not gain a getter for it: a production
// reader that answered the declarer would be a new exported name in the core
// whose only caller is a test (design §10). What it pins is the one fact the
// runtime check above cannot see — that the function handed to
// cli.SetLaunchDeclarer is ee/coordinator/cli.Declare and not some other
// function of the right shape.
//
// The repository is walked rather than ee/register alone, so a second call site
// — the shape a seam quietly halved comes back in — is a finding too.
func TestTheHookHoldsTheMovedDeclare(t *testing.T) {
	const moved = "github.com/dimipaun/fugaro/ee/coordinator/cli"
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
		found, err := declarerCalls(path, moved)
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
		t.Fatalf("the repository has %d calls of cli.SetLaunchDeclarer (%v), want exactly one: ee/register/coordinator.go is the one place a tagged build's declarer is set", len(calls), calls)
	}
	if want := "ee/register/coordinator.go"; calls[0] != want {
		t.Errorf("the declarer is set in %s, want %s", calls[0], want)
	}
}

// declarerCalls answers the files under path that call cli.SetLaunchDeclarer
// with the moved coordinator's Declare, as paths relative to the repository
// root. The scan reads the import that names the argument, so `coordcli.Declare`
// is the moved function and a same-named function of another package is not: a
// launch declarer is a function of the right shape, and the shape is not the
// identity.
func declarerCalls(path, moved string) ([]string, error) {
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
		if !ok {
			return true
		}
		fun, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || fun.Sel.Name != "SetLaunchDeclarer" || len(call.Args) != 1 {
			return true
		}
		arg, ok := call.Args[0].(*ast.SelectorExpr)
		if !ok || arg.Sel.Name != "Declare" {
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
