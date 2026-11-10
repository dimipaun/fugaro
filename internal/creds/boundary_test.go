package creds

// The import boundary around the credential state (credentials-service plan
// Convention 11; O1's ruling that creds/ is denied to every run token).
//
// What keeps a run out of the cascade's state is the DATABASE, and this file is
// not that. A run token holds no permission on creds/ at all: the rules deny the
// whole subtree to every run identity, and the service writes it with its own
// admin account (O1, D136). That is the enforcement, it is pinned by C0.6/C1.3,
// and no test in this package can stand in for it.
//
// What this file pins is a second layer, and it is worth being exact about its
// limits, because the first version of this comment was not (review finding m8).
// It is an allow-list of the packages that may IMPORT internal/creds: the
// package itself, the service that owns the state (internal/credsvc, C1.10
// onward), and the operator's CLI (internal/cli, whose `fugaro accounts`
// commands show the marks and clear a broken one, C1.21). Every package on the
// run side is off the list — internal/runner and internal/gateway (the in-runner
// gateway), internal/agent, internal/managed, internal/backend, internal/task —
// so that a refactor cannot quietly move a write of the state into the code a
// run executes, where a later loosening of the rules would hand it to a
// contributor's process.
//
// It is not "nothing here is ever linked into the runner", and a reader should
// not take it that way: cmd/fugaro links internal/cli and the runner into ONE
// binary, so the binary a run's image carries does contain the operator commands
// and, from C1.21, the state above them. The plan puts them there deliberately
// (the operator has no admin endpoint to call, so the CLI writes creds/ as the
// operator's own admin identity) and what a run cannot do is hold that identity:
// the run's token is a different principal, and the rules are what refuse it. So
// this test says "no run-side package imports the state", and never "the state
// is not in the run's process".
//
// The scan reads import lines, parsed rather than grepped, so a comment or a
// string that merely names the package is not an import and is not this test's
// business. It walks the whole module, this file included: a test file is as
// much part of a package's build as any other, and there is no exemption. The
// allow-list itself is asserted to be exactly the three entries below, so
// widening it is an edit a reviewer sees rather than a new import that slips in.

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

// credsPackage is the import path this boundary is about.
const credsPackage = "github.com/dimipaun/fugaro/internal/creds"

// credsImporters is the ONE-LIST of directories (module-relative, slash
// separated) whose files may import credsPackage. Widening it is a deliberate
// edit here, made with the reason written next to the new entry, and never a
// quiet new import in some future package.
var credsImporters = []string{
	"internal/creds",   // the package itself, and its tests
	"internal/credsvc", // the service: the cascade's one caller (C1.10, C1.11)
	"internal/cli",     // the operator's `fugaro accounts` commands (C1.21)
}

// TestCredsIsNotReachableFromARun fails if any file outside the allow-list
// imports the credential state.
func TestCredsIsNotReachableFromARun(t *testing.T) {
	root := moduleRoot(t)

	var found []string
	err := eachGoFile(root, func(rel, path string) error {
		if allowlisted(rel) {
			return nil
		}
		imports, err := importPaths(path)
		if err != nil {
			return err
		}
		for _, imp := range imports {
			if imp == credsPackage {
				found = append(found, rel+" imports "+credsPackage)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Fatalf("a run-side file imports the credential state:\n  %s\n\nA run token holds no write on creds/ (O1), and the rules are what enforce it (C1.3); this list is what keeps a write of the state out of the code a run executes, so that a later loosening of the rules is not enough to hand the cascade's marks to a contributor's process. Move the call into the service, or add the directory to credsImporters with its reason if this is genuinely a new caller of the state.",
			strings.Join(found, "\n  "))
	}

	// The allow-list is exactly the three callers above: a fourth entry is a
	// deliberate edit here and not a quiet import somewhere (review finding m8),
	// and a name that is no longer a caller should leave the list rather than
	// widen it.
	want := []string{"internal/creds", "internal/credsvc", "internal/cli"}
	if len(credsImporters) != len(want) {
		t.Fatalf("the allow-list holds %d entries %v; want exactly %v", len(credsImporters), credsImporters, want)
	}
	for i, dir := range want {
		if credsImporters[i] != dir {
			t.Fatalf("the allow-list is %v; want exactly %v", credsImporters, want)
		}
	}
}

// allowlisted reports whether a module-relative file is one of the allowed
// importers: its directory is on the list, or is below one that is.
func allowlisted(rel string) bool {
	return under(credsImporters, rel)
}

// under reports whether a module-relative file is one of the directories on a
// one-list: its directory is on the list, or is below one that is.
func under(list []string, rel string) bool {
	for _, dir := range list {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	return false
}

// moduleRoot is this module's root, read from this package's own directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	// The tests of a package run with the package's directory as the working
	// directory, so the module root is two levels up.
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("the module root is not at %s: %v", root, err)
	}
	return root
}

// eachGoFile walks the module and calls fn with every .go file in it, as a
// module-relative slash path and as a path to read. Dot-directories,
// underscore-directories and testdata/ are skipped, as they are for the import
// rule: neither is a package the go tool builds.
func eachGoFile(root string, fn func(rel, path string) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel), path)
	})
}

// importPaths parses one Go file and answers the import paths it names. Parsing
// rather than grepping is what keeps a comment or a string that mentions the
// package out of the result.
func importPaths(path string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(f.Imports))
	for _, imp := range f.Imports {
		out = append(out, strings.Trim(imp.Path.Value, `"`))
	}
	return out, nil
}

// The slip half of the boundary, which is narrower than the import list above.
//
// internal/cli is a caller of this package because `fugaro accounts` reads the
// marks and clears a broken one (C1.21) — an operator's own act, on the
// operator's own machine, over the state the cascade keeps. A slip is not that.
// It is the service's grant to ONE run: the grant is what a launcher asks for,
// the claim is what a run's job presents, and the node names a run's credentials
// and holds the hash of the secret that proves the claim (D1932, D1941). No
// operator command grants, claims or ends one, and G10 keeps the slip routes to
// the grant and the claim so that no admin surface grows beside them; the
// retirement of a slip is the service's, when the run's outcome appears or its
// max runtime passes. So the packages that may name a slip are the state's own
// and the service's, and the scan below is what says so.
//
// It reads selectors rather than text: a file that imports this package and
// writes creds.Slip, or a slip's sentinel error, is a finding, while a comment
// or a string that merely spells one is not. A dot import of the state is a
// finding of its own — it binds every name unqualified, so the selector walk
// would not see a slip named at all — and so is a file that cannot be parsed,
// because "cannot be checked" is not "is fine".

// slipImporters is the ONE-LIST of directories whose files may reach the slip
// store. It is deliberately not credsImporters: a fourth entry, or an entry
// here that is not one of these two, is an edit a reviewer reads.
var slipImporters = []string{
	"internal/creds",   // the package itself, and its tests
	"internal/credsvc", // the service: the grant and the claim (DS1.3, DS1.6)
}

// slipNames are the identifiers of the slip half of this package: naming one of
// them from another package is reaching the slip store. The list is closed
// rather than a prefix match, so a new name is an edit here.
var slipNames = []string{
	"Slip",            // the record
	"SlipStore",       // the store's interface
	"Claim",           // the claim a slip carries, and the act of claiming
	"ClaimSecretHash", // the hash a claim secret is kept as
	"PathSlip",        // the node's path
	"ErrNoSlip",       // the refusals
	"ErrSlipExists",
	"ErrSlipMalformed",
	"ErrSlipClaimed",
	"ErrSlipExpired",
	"ErrSlipIdentity",
}

// TestBoundaryAllowsSlipsOnlyViaService fails if a file outside slipImporters
// reaches the slip store, or if the state is dot-imported anywhere.
func TestBoundaryAllowsSlipsOnlyViaService(t *testing.T) {
	// The scan is proved first, on sources this test states, so that an empty
	// result over the module below is a result and not a walk that read nothing.
	for _, c := range []struct {
		name string
		src  string
		want bool
	}{
		{
			name: "a package holding a slip",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nvar s creds.Slip\n",
			want: true,
		},
		{
			name: "a package matching a slip's refusal",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nvar err = creds.ErrSlipClaimed\n",
			want: true,
		},
		{
			name: "a package renaming the state's import",
			src:  "package x\n\nimport c \"github.com/dimipaun/fugaro/internal/creds\"\n\nvar s c.SlipStore\n",
			want: true,
		},
		{
			name: "a package dot-importing the state",
			src:  "package x\n\nimport . \"github.com/dimipaun/fugaro/internal/creds\"\n\nvar s Slip\n",
			want: true,
		},
		{
			name: "a package naming a mark, which is not a slip",
			src:  "package x\n\nimport \"github.com/dimipaun/fugaro/internal/creds\"\n\nvar m creds.Mark\n",
			want: false,
		},
		{
			name: "a comment that merely spells a slip",
			src:  "package x\n\n// creds.Slip is the service's grant to one run.\nvar s = 1\n",
			want: false,
		},
		{
			name: "a blank import of the state",
			src:  "package x\n\nimport _ \"github.com/dimipaun/fugaro/internal/creds\"\n",
			want: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			found := slipReferences("planted.go", "planted.go", []byte(c.src))
			if c.want && len(found) == 0 {
				t.Fatal("the scan read a source that reaches the slip store as reaching nothing: a scan that cannot fail is not a scan")
			}
			if !c.want && len(found) > 0 {
				t.Fatalf("the scan refused a source that does not reach the slip store: %v", found)
			}
		})
	}

	var found []string
	err := eachGoFile(moduleRoot(t), func(rel, path string) error {
		if under(slipImporters, rel) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found = append(found, slipReferences(path, rel, src)...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Fatalf("a file outside the service reaches the slip store:\n  %s\n\nA slip is the service's grant to ONE run (D1932): the grant is what a launcher asks for, the claim is what the run's job presents, and no operator command grants, claims or ends one (G10). Read or write slips from internal/credsvc, or add the directory to slipImporters with its reason if this is genuinely a new caller.", strings.Join(found, "\n  "))
	}

	// The one-list is exactly the two callers above, for the reason
	// credsImporters is held to its own: a third entry is an edit here and not a
	// quiet import somewhere.
	want := []string{"internal/creds", "internal/credsvc"}
	if !slices.Equal(slipImporters, want) {
		t.Fatalf("slipImporters is %v; want exactly %v", slipImporters, want)
	}
}

// slipReferences answers the slip identifiers one Go file names through this
// package. src is passed in rather than read from path, so the plants above run
// through the same walk the module does.
func slipReferences(path, rel string, src []byte) []string {
	f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
	if err != nil || f == nil {
		return []string{rel + " cannot be parsed, so its slip references cannot be checked"}
	}
	name, dot, ok := credsImport(f)
	if dot {
		return []string{rel + " dot-imports " + credsPackage + ", so every name it holds — a slip's included — is bound unqualified and unreadable by this scan"}
	}
	if !ok {
		return nil
	}
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok || x.Name != name {
			return true
		}
		if slices.Contains(slipNames, sel.Sel.Name) {
			found = append(found, rel+" names "+name+"."+sel.Sel.Name)
		}
		return true
	})
	return found
}

// credsImport answers how a file reaches this package: the local name it binds
// (the package's own name unless the import renames it) and whether it is a dot
// import. A blank import binds nothing and is not a caller.
func credsImport(f *ast.File) (name string, dot bool, ok bool) {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) != credsPackage {
			continue
		}
		switch {
		case imp.Name == nil:
			return "creds", false, true
		case imp.Name.Name == ".":
			return "", true, true
		case imp.Name.Name == "_":
			continue
		default:
			return imp.Name.Name, false, true
		}
	}
	return "", false, false
}
